package ulog

import (
	"context"
	"log/slog"
)

// Context key type for storing attributes in context
type ctxKeyType struct{}

var ctxKey = ctxKeyType{}

// WithAttrsContext adds slog.Attr values to the context
func WithAttrsContext(ctx context.Context, attrs ...slog.Attr) context.Context {
	if len(attrs) == 0 {
		return ctx
	}

	existingAttrs := extractContextAttrs(ctx)
	return context.WithValue(ctx, ctxKey, append(existingAttrs, attrs...))
}

// extractContextAttrs extracts slog.Attr values from context
func extractContextAttrs(ctx context.Context) []slog.Attr {
	if ctx == nil {
		return nil
	}

	attrs, ok := ctx.Value(ctxKey).([]slog.Attr)
	if !ok {
		return nil
	}

	return attrs
}
