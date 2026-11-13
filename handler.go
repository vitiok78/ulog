package ulog

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

// Handler implements slog.Handler interface.
//
// Writes to Config.Writer are serialized across the handler and every handler
// derived from it with WithAttrs or WithGroup, so the writer itself need not
// be safe for concurrent use. Separate NewHandler calls on one writer do not
// share that lock; give them a writer that is safe on its own (FileWriter,
// MultiWriter, *os.File).
type Handler struct {
	config  Config
	attrs   []slog.Attr
	groups  []string
	mu      sync.RWMutex
	writeMu *sync.Mutex // shared with derived handlers
}

// NewHandler creates a new ulog Handler
func NewHandler(config Config) *Handler {
	return &Handler{
		config:  config,
		writeMu: &sync.Mutex{},
	}
}

// Enabled implements slog.Handler interface
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.config.Level
}

// Handle implements slog.Handler interface
func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	// Map slog level to monolog level
	level, levelName := mapSlogLevelToMonologLevel(record.Level)

	// Convert slog.Record to our LogRecord format (Symfony monolog format)
	logRecord := LogRecord{
		Message:   record.Message,
		Level:     level,
		LevelName: levelName,
		Channel:   h.config.Channel,
		DateTime:  time.Now(),
		Context:   make(map[string]interface{}),
		Extra:     make(map[string]interface{}),
	}

	h.mu.RLock()
	// One budget for the whole record, shared by all its attributes.
	budget := newAttrBudget()

	// Process attributes from the record
	record.Attrs(func(attr slog.Attr) bool {
		h.appendAttrBudget(logRecord.Context, attr, h.groups, 0, budget)
		return true
	})

	// Process pre-defined attributes
	for _, attr := range h.attrs {
		h.appendAttrBudget(logRecord.Context, attr, h.groups, 0, budget)
	}

	// Extract attributes from context if available
	if ctxAttrs := extractContextAttrs(ctx); len(ctxAttrs) > 0 {
		ctxMap := make(map[string]interface{})
		for _, attr := range ctxAttrs {
			h.appendAttrBudget(ctxMap, attr, nil, 0, budget)
		}
		logRecord.Context[h.config.CtxKeyName] = ctxMap
	}
	h.mu.RUnlock()

	// Format and write the log record
	formattedLog, err := h.config.Formatter.Format(logRecord)
	if err != nil {
		return err
	}

	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	_, err = h.config.Writer.Write(formattedLog)
	return err
}

// WithAttrs implements slog.Handler interface
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Create a new handler with the same config
	newHandler := NewHandler(h.config)
	newHandler.writeMu = h.writeMu
	// Clip: without it, two handlers derived from h could append into the
	// same spare capacity and overwrite each other's attributes.
	newHandler.attrs = append(slices.Clip(h.attrs), attrs...)
	return newHandler
}

// WithGroup implements slog.Handler interface
func (h *Handler) WithGroup(name string) slog.Handler {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Create a new handler with the same config
	newHandler := NewHandler(h.config)
	newHandler.writeMu = h.writeMu
	newHandler.groups = append(slices.Clip(h.groups), name)
	return newHandler
}

// appendAttr adds an attribute to the log map
func (h *Handler) appendAttr(m map[string]interface{}, attr slog.Attr, groups []string) {
	h.appendAttrBudget(m, attr, groups, 0, newAttrBudget())
}

const (
	// maxAttrDepth bounds group nesting within one attribute.
	maxAttrDepth = 64
	// maxAttrsPerRecord bounds how many attributes one record expands to.
	maxAttrsPerRecord = 100_000
)

// attrBudget is what one record may still expand. A LogValuer can return a
// group that contains itself, or branches to itself twice; Resolve stops
// only at the group, so without a shared budget expansion never ends.
type attrBudget struct{ left int }

func newAttrBudget() *attrBudget { return &attrBudget{left: maxAttrsPerRecord} }

// appendAttrBudget is appendAttr with the record's budget and the depth of
// attr inside groups.
func (h *Handler) appendAttrBudget(m map[string]interface{}, attr slog.Attr, groups []string, depth int, b *attrBudget) {
	b.left--
	if b.left < 0 {
		if b.left == -1 {
			m["ulog.truncated"] = fmt.Sprintf("[attribute limit reached: %d per record]", maxAttrsPerRecord)
		}
		return
	}

	// Resolve LogValuers first, as slog asks handlers to: a LogValuer may
	// resolve to a group. Resolve also turns a panicking LogValue into an
	// error value and stops endless LogValue chains.
	attr.Value = attr.Value.Resolve()

	if attr.Equal(slog.Attr{}) {
		return
	}

	// Apply groups to the key
	key := attr.Key
	if len(groups) > 0 {
		key = strings.Join(groups, ".") + "." + key
	}

	if depth > maxAttrDepth {
		m[key] = fmt.Sprintf("[max group depth %d]", maxAttrDepth)
		return
	}

	switch attr.Value.Kind() {
	case slog.KindGroup:
		// For group values, recursively add each attribute in the group
		groupAttrs := attr.Value.Group()
		// If the group key is not empty, create a new level of nesting
		if attr.Key != "" {
			subGroups := append(slices.Clip(groups), attr.Key)
			for _, groupAttr := range groupAttrs {
				h.appendAttrBudget(m, groupAttr, subGroups, depth+1, b)
			}
		} else {
			// If the group has no key, add attributes at the current level
			for _, groupAttr := range groupAttrs {
				h.appendAttrBudget(m, groupAttr, groups, depth+1, b)
			}
		}
	default:
		// For non-group values, extract the value properly
		m[key] = h.extractValue(attr.Value)
	}
}

// extractValue properly extracts values from slog.Value, handling errors and stringers
func (h *Handler) extractValue(v slog.Value) interface{} {
	switch v.Kind() {
	case slog.KindAny:
		// Handle special types
		anyVal := v.Any()

		// Errors and Stringers become their text. The method may panic (a
		// typed nil such as (*os.PathError)(nil) is the usual case); a log
		// call must not, so fall back to describe, which prints what fmt
		// would ("<nil>" for a nil receiver, else a PANIC note).
		if err, ok := anyVal.(error); ok {
			return methodText(anyVal, err.Error)
		}
		if stringer, ok := anyVal.(fmt.Stringer); ok {
			return methodText(anyVal, stringer.String)
		}

		return anyVal
	case slog.KindLogValuer:
		return h.extractValue(v.Resolve())
	case slog.KindDuration:
		// Handle duration values by converting to string
		return v.Duration().String()
	case slog.KindInt64:
		return v.Int64()
	case slog.KindUint64:
		return v.Uint64()
	case slog.KindFloat64:
		return v.Float64()
	case slog.KindString:
		return v.String()
	case slog.KindBool:
		return v.Bool()
	case slog.KindTime:
		return v.Time()
	default:
		return v.Any()
	}
}

// methodText returns text(), or describe(v) if text panics.
func methodText(v interface{}, text func() string) (s string) {
	defer func() {
		if r := recover(); r != nil {
			s = describe(v)
		}
	}()
	return text()
}

// MultiHandler sends log entries to multiple handlers
type MultiHandler struct {
	handlers []slog.Handler
}

func NewMultiHandler(handlers ...slog.Handler) *MultiHandler {
	return &MultiHandler{handlers: handlers}
}

// Enabled reports whether the handler handles records at the given level.
func (h *MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle dispatches the record to all handlers.
func (h *MultiHandler) Handle(ctx context.Context, record slog.Record) error {
	for _, handler := range h.handlers {
		if err := handler.Handle(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

// WithAttrs returns a new Handler whose attributes consist of
// both the receiver's attributes and the arguments.
func (h *MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		handlers[i] = handler.WithAttrs(attrs)
	}
	return &MultiHandler{handlers: handlers}
}

// WithGroup returns a new Handler with the given group name.
func (h *MultiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		handlers[i] = handler.WithGroup(name)
	}
	return &MultiHandler{handlers: handlers}
}
