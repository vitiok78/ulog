package ulog

import (
	"context"
	"log/slog"
	"reflect"
	"testing"
	"time"
)

func TestWithAttrsContext(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		attrs    []slog.Attr
		expected []slog.Attr
	}{
		{
			name:     "add attrs to empty context",
			ctx:      context.Background(),
			attrs:    []slog.Attr{slog.String("key1", "value1"), slog.Int("key2", 42)},
			expected: []slog.Attr{slog.String("key1", "value1"), slog.Int("key2", 42)},
		},
		{
			name:     "add attrs to context with existing attrs",
			ctx:      context.WithValue(context.Background(), ctxKey, []slog.Attr{slog.String("existing", "value")}),
			attrs:    []slog.Attr{slog.String("new", "attr")},
			expected: []slog.Attr{slog.String("existing", "value"), slog.String("new", "attr")},
		},
		{
			name:     "add empty attrs slice",
			ctx:      context.Background(),
			attrs:    []slog.Attr{},
			expected: nil,
		},
		{
			name:     "add nil attrs",
			ctx:      context.Background(),
			attrs:    nil,
			expected: nil,
		},
		{
			name:     "add single attr",
			ctx:      context.Background(),
			attrs:    []slog.Attr{slog.String("single", "attr")},
			expected: []slog.Attr{slog.String("single", "attr")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resultCtx := WithAttrsContext(tt.ctx, tt.attrs...)

			// If no attrs were added, context should be unchanged
			if len(tt.attrs) == 0 {
				if resultCtx != tt.ctx {
					t.Error("Expected context to be unchanged when no attrs are added")
				}
				return
			}

			// Extract attrs from result context
			resultAttrs := extractContextAttrs(resultCtx)

			if !reflect.DeepEqual(resultAttrs, tt.expected) {
				t.Errorf("Expected attrs %+v, got %+v", tt.expected, resultAttrs)
			}
		})
	}
}

func TestExtractContextAttrs(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		expected []slog.Attr
	}{
		{
			name:     "extract from context with attrs",
			ctx:      context.WithValue(context.Background(), ctxKey, []slog.Attr{slog.String("key", "value")}),
			expected: []slog.Attr{slog.String("key", "value")},
		},
		{
			name:     "extract from empty context",
			ctx:      context.Background(),
			expected: nil,
		},
		{
			name:     "extract from nil context",
			ctx:      nil,
			expected: nil,
		},
		{
			name:     "extract from context with wrong value type",
			ctx:      context.WithValue(context.Background(), ctxKey, "not-attrs"),
			expected: nil,
		},
		{
			name:     "extract from context with empty attrs slice",
			ctx:      context.WithValue(context.Background(), ctxKey, []slog.Attr{}),
			expected: []slog.Attr{},
		},
		{
			name:     "extract from context with multiple attrs",
			ctx:      context.WithValue(context.Background(), ctxKey, []slog.Attr{slog.String("key1", "value1"), slog.Int("key2", 42), slog.Bool("key3", true)}),
			expected: []slog.Attr{slog.String("key1", "value1"), slog.Int("key2", 42), slog.Bool("key3", true)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractContextAttrs(tt.ctx)

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("Expected %+v, got %+v", tt.expected, result)
			}
		})
	}
}

func TestWithAttrsContextChaining(t *testing.T) {
	// Test chaining multiple WithAttrsContext calls
	ctx := context.Background()

	// Add first set of attrs
	ctx1 := WithAttrsContext(ctx, slog.String("first", "attr"))
	attrs1 := extractContextAttrs(ctx1)
	if len(attrs1) != 1 || attrs1[0].Key != "first" {
		t.Errorf("Expected first attr, got %+v", attrs1)
	}

	// Add second set of attrs
	ctx2 := WithAttrsContext(ctx1, slog.String("second", "attr"))
	attrs2 := extractContextAttrs(ctx2)
	if len(attrs2) != 2 {
		t.Errorf("Expected 2 attrs, got %d", len(attrs2))
	}

	// Check order is preserved
	if attrs2[0].Key != "first" || attrs2[1].Key != "second" {
		t.Errorf("Expected attrs in order [first, second], got %+v", attrs2)
	}

	// Add third set with multiple attrs
	ctx3 := WithAttrsContext(ctx2, slog.Int("third", 3), slog.Bool("fourth", true))
	attrs3 := extractContextAttrs(ctx3)
	if len(attrs3) != 4 {
		t.Errorf("Expected 4 attrs, got %d", len(attrs3))
	}

	// Check all attrs are present in correct order
	expectedKeys := []string{"first", "second", "third", "fourth"}
	for i, attr := range attrs3 {
		if attr.Key != expectedKeys[i] {
			t.Errorf("Expected attr key %s at position %d, got %s", expectedKeys[i], i, attr.Key)
		}
	}
}

func TestContextKeyType(t *testing.T) {
	// Test that ctxKey is of the correct type
	var expectedType ctxKeyType
	if ctxKey != expectedType {
		t.Error("Expected ctxKey to be of type ctxKeyType")
	}

	// Test that different instances of ctxKeyType are equal (empty struct)
	var anotherKey ctxKeyType
	if ctxKey != anotherKey {
		t.Error("Expected ctxKeyType instances to be equal")
	}

	// Test that ctxKey can be used as a context key
	ctx := context.WithValue(context.Background(), ctxKey, "test")
	if ctx.Value(ctxKey) != "test" {
		t.Error("Expected ctxKey to work as context key")
	}
}

func TestWithAttrsContextPreservesOriginal(t *testing.T) {
	// Test that WithAttrsContext doesn't modify the original context's attrs
	originalAttrs := []slog.Attr{slog.String("original", "value")}
	ctx := context.WithValue(context.Background(), ctxKey, originalAttrs)

	// Add new attrs
	newCtx := WithAttrsContext(ctx, slog.String("new", "value"))

	// Original context should be unchanged
	originalExtracted := extractContextAttrs(ctx)
	if len(originalExtracted) != 1 || originalExtracted[0].Key != "original" {
		t.Error("Original context was modified")
	}

	// New context should have both attrs
	newExtracted := extractContextAttrs(newCtx)
	if len(newExtracted) != 2 {
		t.Errorf("Expected 2 attrs in new context, got %d", len(newExtracted))
	}
}

func TestAttrTypes(t *testing.T) {
	// Test various slog.Attr types work correctly
	ctx := context.Background()

	attrs := []slog.Attr{
		slog.String("string", "value"),
		slog.Int("int", 42),
		slog.Int64("int64", 123456789),
		slog.Float64("float", 3.14),
		slog.Bool("bool", true),
		slog.Time("time", testTime),
		slog.Duration("duration", testDuration),
		slog.Any("any", map[string]string{"key": "value"}),
	}

	newCtx := WithAttrsContext(ctx, attrs...)
	extracted := extractContextAttrs(newCtx)

	if len(extracted) != len(attrs) {
		t.Errorf("Expected %d attrs, got %d", len(attrs), len(extracted))
	}

	for i, attr := range extracted {
		if attr.Key != attrs[i].Key {
			t.Errorf("Expected key %s at position %d, got %s", attrs[i].Key, i, attr.Key)
		}
		// Skip value comparison for uncomparable types like maps
		if attr.Key != "any" {
			if !attr.Value.Equal(attrs[i].Value) {
				t.Errorf("Expected value %v at position %d, got %v", attrs[i].Value, i, attr.Value)
			}
		} else {
			// For "any" type, just check that both values are non-nil
			if attr.Value.Any() == nil || attrs[i].Value.Any() == nil {
				t.Errorf("Expected non-nil values for 'any' type at position %d", i)
			}
		}
	}
}

func TestContextIntegrationWithHandler(t *testing.T) {
	// Test that context attrs work correctly with the handler
	ctx := context.Background()
	ctx = WithAttrsContext(ctx, slog.String("ctx_key", "ctx_value"))

	// Create a mock writer to capture output
	writer := &mockWriter{}
	config := Config{
		Formatter:  &mockFormatter{},
		Writer:     writer,
		Channel:    "test",
		CtxKeyName: "context",
		AddSource:  false,
	}

	handler := NewHandler(config)
	record := slog.NewRecord(testTime, slog.LevelInfo, "test message", 0)

	err := handler.Handle(ctx, record)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// The output should contain the context information
	output := writer.String()
	if output == "" {
		t.Error("Expected non-empty output")
	}
}

// Test constants for consistent testing
var (
	testTime     = mustParseTime("2023-01-01T12:00:00Z")
	testDuration = mustParseDuration("5m30s")
)

func mustParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func mustParseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		panic(err)
	}
	return d
}
