package ulog

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Handler implements slog.Handler interface
type Handler struct {
	config Config
	attrs  []slog.Attr
	groups []string
	mu     sync.RWMutex
}

// NewHandler creates a new ulog Handler
func NewHandler(config Config) *Handler {
	return &Handler{
		config: config,
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
	// Process attributes from the record
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

	_, err = h.config.Writer.Write(formattedLog)
	return err
}

// WithAttrs implements slog.Handler interface
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Create a new handler with the same config
	newHandler := NewHandler(h.config)
	newHandler.attrs = append(h.attrs, attrs...)
	return newHandler
}

// WithGroup implements slog.Handler interface
func (h *Handler) WithGroup(name string) slog.Handler {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Create a new handler with the same config
	newHandler := NewHandler(h.config)
	newHandler.groups = append(h.groups, name)
	return newHandler
}

// appendAttr adds an attribute to the log map
func (h *Handler) appendAttr(m map[string]interface{}, attr slog.Attr, groups []string) {
	if attr.Equal(slog.Attr{}) {
		return
	}

	// Apply groups to the key
	key := attr.Key
	if len(groups) > 0 {
		key = strings.Join(groups, ".") + "." + key
	}

	switch attr.Value.Kind() {
	case slog.KindGroup:
		// For group values, recursively add each attribute in the group
		groupAttrs := attr.Value.Group()
		// If the group key is not empty, create a new level of nesting
		if key != "" {
			subGroups := append(append([]string{}, groups...), key)
			for _, groupAttr := range groupAttrs {
				h.appendAttr(m, groupAttr, subGroups)
			}
		} else {
			// If the group has no key, add attributes at the current level
			for _, groupAttr := range groupAttrs {
				h.appendAttr(m, groupAttr, groups)
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

		// Handle errors
		if err, ok := anyVal.(error); ok {
			return err.Error()
		}

		// Handle fmt.Stringer
		if stringer, ok := anyVal.(fmt.Stringer); ok {
			return stringer.String()
		}

		return anyVal
	case slog.KindLogValuer:
		return h.extractValue(v.LogValuer().LogValue())
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
