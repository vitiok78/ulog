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
	// Process attributes from the record. appendAttr gives each one its own
	// expansion budget, so one runaway value cannot crowd out the others.
	record.Attrs(func(attr slog.Attr) bool {
		h.appendAttr(logRecord.Context, attr, h.groups)
		return true
	})

	// Process pre-defined attributes
	for _, attr := range h.attrs {
		h.appendAttr(logRecord.Context, attr, h.groups)
	}

	// Extract attributes from context if available
	if ctxAttrs := extractContextAttrs(ctx); len(ctxAttrs) > 0 {
		ctxMap := make(map[string]interface{})
		for _, attr := range ctxAttrs {
			h.appendAttr(ctxMap, attr, nil)
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
	_ = h.appendAttrBudget(m, attr, groups, 0, newAttrBudget())
}

const (
	// maxAttrDepth bounds group nesting within one attribute.
	maxAttrDepth = 64
	// maxAttrExpansion bounds how many attributes one top-level attribute
	// expands to.
	maxAttrExpansion = 100_000
	// maxAttrKeyBytes bounds the total length of the flattened keys one
	// top-level attribute expands to. Keys repeat every group name above
	// them, so long names in a cycle grow fast even under the other limits.
	maxAttrKeyBytes = 16 << 20
	// maxAttrMethodBytes bounds the Error() and String() text one top-level
	// attribute's values produce. That text is built per occurrence, so a
	// group tree repeating one Stringer builds it thousands of times. (The
	// size of all other values is bounded where they are written, in the
	// formatter.)
	maxAttrMethodBytes = 16 << 20
	// maxMarkerKeyBytes caps the key the limit marker is written under.
	maxMarkerKeyBytes = 1024
)

// attrLimitMarker is written where an attribute's expansion was cut.
var attrLimitMarker = fmt.Sprintf("[attribute limit reached: %d attributes, %d key bytes or %d method-text bytes per value]",
	maxAttrExpansion, maxAttrKeyBytes, maxAttrMethodBytes)

// attrBudget is what one top-level attribute may still expand. A LogValuer
// can return a group that contains itself, or branches to itself twice;
// Resolve stops only at the group, so without a budget expansion never ends.
// Each top-level attribute gets its own, so one runaway value cannot crowd
// out its siblings (a request ID logged after it, say).
type attrBudget struct {
	left        int // attributes
	keyBytes    int // flattened key bytes
	methodBytes int // Error() and String() text
}

func newAttrBudget() *attrBudget {
	return &attrBudget{left: maxAttrExpansion, keyBytes: maxAttrKeyBytes, methodBytes: maxAttrMethodBytes}
}

// hasTextMethod reports whether extractValue turns v into the text of its
// Error or String method.
func hasTextMethod(v slog.Value) bool {
	if v.Kind() != slog.KindAny {
		return false
	}
	switch v.Any().(type) {
	case error, fmt.Stringer:
		return true
	}
	return false
}

// boundedKey joins groups and key with ".", stopping at limit bytes.
func boundedKey(groups []string, key string, limit int) string {
	var sb strings.Builder
	add := func(s string) bool {
		if room := limit - sb.Len(); len(s) > room {
			sb.WriteString(s[:room])
			return false
		}
		sb.WriteString(s)
		return true
	}
	for _, g := range groups {
		if !add(g) || !add(".") {
			return sb.String()
		}
	}
	add(key)
	return sb.String()
}

// appendAttrBudget is appendAttr with the attribute's budget and its depth
// inside groups.
func (h *Handler) appendAttrBudget(m map[string]interface{}, attr slog.Attr, groups []string, depth int, b *attrBudget) bool {
	// Exhausted: the marker is already written. Return before any work, and
	// tell the caller to stop visiting the remaining siblings.
	if b.left <= 0 {
		return false
	}
	b.left--

	// Check the key's length before building it.
	keyLen := len(attr.Key)
	for _, g := range groups {
		keyLen += len(g) + 1
	}
	if b.left == 0 || keyLen > b.keyBytes {
		b.left = 0
		m[boundedKey(groups, attr.Key, maxMarkerKeyBytes)] = attrLimitMarker
		return false
	}
	b.keyBytes -= keyLen

	// Apply groups to the key
	key := attr.Key
	if len(groups) > 0 {
		key = strings.Join(groups, ".") + "." + key
	}

	// Resolve LogValuers first, as slog asks handlers to: a LogValuer may
	// resolve to a group. Resolve also turns a panicking LogValue into an
	// error value and stops endless LogValue chains.
	attr.Value = attr.Value.Resolve()

	if attr.Equal(slog.Attr{}) {
		return true
	}

	if depth > maxAttrDepth {
		m[key] = fmt.Sprintf("[max group depth %d]", maxAttrDepth)
		return true
	}

	switch attr.Value.Kind() {
	case slog.KindGroup:
		// For group values, recursively add each attribute in the group.
		// A key-less group adds its attributes at the current level.
		subGroups := groups
		if attr.Key != "" {
			subGroups = append(slices.Clip(groups), attr.Key)
		}
		for _, groupAttr := range attr.Value.Group() {
			if !h.appendAttrBudget(m, groupAttr, subGroups, depth+1, b) {
				return false
			}
		}
	default:
		// Error() and String() build their text on every call: once the
		// budget is spent, call no more. Checked before the call, charged
		// after, so a single value is always written in full.
		method := hasTextMethod(attr.Value)
		if method && b.methodBytes <= 0 {
			b.left = 0
			m[key] = attrLimitMarker
			return false
		}

		// For non-group values, extract the value properly
		val := h.extractValue(attr.Value)
		if s, ok := val.(string); ok && method {
			b.methodBytes -= len(s)
		}
		m[key] = val
	}
	return true
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
