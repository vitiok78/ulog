package ulog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// Mock formatter for testing
type mockFormatter struct {
	shouldError bool
}

func (m *mockFormatter) Format(record LogRecord) ([]byte, error) {
	if m.shouldError {
		return nil, errors.New("formatter error")
	}
	return []byte(fmt.Sprintf("MOCK: %s [%s] %s\n", record.DateTime.Format(time.RFC3339), record.LevelName, record.Message)), nil
}

// Mock writer for testing
type mockWriter struct {
	buf         bytes.Buffer
	shouldError bool
}

func (m *mockWriter) Write(p []byte) (n int, err error) {
	if m.shouldError {
		return 0, errors.New("writer error")
	}
	return m.buf.Write(p)
}

func (m *mockWriter) String() string {
	return m.buf.String()
}

func TestNewHandler(t *testing.T) {
	config := Config{
		Formatter:  &mockFormatter{},
		Writer:     &mockWriter{},
		Channel:    "test",
		CtxKeyName: "ctx",
		AddSource:  false,
	}

	handler := NewHandler(config)
	if handler == nil {
		t.Error("Expected NewHandler to return a non-nil handler")
	}

	if handler.config.Channel != "test" {
		t.Errorf("Expected channel 'test', got '%s'", handler.config.Channel)
	}

	if len(handler.attrs) != 0 {
		t.Errorf("Expected empty attrs slice, got length %d", len(handler.attrs))
	}

	if len(handler.groups) != 0 {
		t.Errorf("Expected empty groups slice, got length %d", len(handler.groups))
	}
}

func TestHandlerEnabled(t *testing.T) {
	config := Config{
		Formatter:  &mockFormatter{},
		Writer:     &mockWriter{},
		Channel:    "test",
		CtxKeyName: "ctx",
		AddSource:  false,
	}

	handler := NewHandler(config)
	ctx := context.Background()

	// Test that Enabled always returns true (current implementation)
	levels := []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError}
	for _, level := range levels {
		if !handler.Enabled(ctx, level) {
			t.Errorf("Expected Enabled to return true for level %v", level)
		}
	}
}

func TestHandlerHandle(t *testing.T) {
	writer := &mockWriter{}
	config := Config{
		Formatter:  &mockFormatter{},
		Writer:     writer,
		Channel:    "test-channel",
		CtxKeyName: "ctx",
		AddSource:  false,
	}

	handler := NewHandler(config)
	ctx := context.Background()

	// Create a test record
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "test message", 0)
	record.Add("key1", "value1")
	record.Add("key2", 42)

	err := handler.Handle(ctx, record)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	output := writer.String()
	if !strings.Contains(output, "test message") {
		t.Errorf("Expected output to contain 'test message', got: %s", output)
	}

	if !strings.Contains(output, "INFO") {
		t.Errorf("Expected output to contain 'INFO', got: %s", output)
	}
}

func TestHandlerHandleWithFormatterError(t *testing.T) {
	writer := &mockWriter{}
	config := Config{
		Formatter:  &mockFormatter{shouldError: true},
		Writer:     writer,
		Channel:    "test",
		CtxKeyName: "ctx",
		AddSource:  false,
	}

	handler := NewHandler(config)
	ctx := context.Background()

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "test message", 0)
	err := handler.Handle(ctx, record)

	if err == nil {
		t.Error("Expected error from formatter but got none")
	}

	if !strings.Contains(err.Error(), "formatter error") {
		t.Errorf("Expected formatter error, got: %v", err)
	}
}

func TestHandlerHandleWithWriterError(t *testing.T) {
	writer := &mockWriter{shouldError: true}
	config := Config{
		Formatter:  &mockFormatter{},
		Writer:     writer,
		Channel:    "test",
		CtxKeyName: "ctx",
		AddSource:  false,
	}

	handler := NewHandler(config)
	ctx := context.Background()

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "test message", 0)
	err := handler.Handle(ctx, record)

	if err == nil {
		t.Error("Expected error from writer but got none")
	}

	if !strings.Contains(err.Error(), "writer error") {
		t.Errorf("Expected writer error, got: %v", err)
	}
}

func TestHandlerWithAttrs(t *testing.T) {
	config := Config{
		Formatter:  &mockFormatter{},
		Writer:     &mockWriter{},
		Channel:    "test",
		CtxKeyName: "ctx",
		AddSource:  false,
	}

	handler := NewHandler(config)
	attrs := []slog.Attr{
		slog.String("attr1", "value1"),
		slog.Int("attr2", 42),
	}

	newHandler := handler.WithAttrs(attrs)
	if newHandler == nil {
		t.Error("Expected WithAttrs to return a non-nil handler")
	}

	// Should be a different handler instance
	if newHandler == handler {
		t.Error("Expected WithAttrs to return a new handler instance")
	}

	// Original handler should be unchanged
	if len(handler.attrs) != 0 {
		t.Errorf("Expected original handler to have 0 attrs, got %d", len(handler.attrs))
	}

	// New handler should have the attributes
	if h, ok := newHandler.(*Handler); ok {
		if len(h.attrs) != 2 {
			t.Errorf("Expected new handler to have 2 attrs, got %d", len(h.attrs))
		}
	} else {
		t.Error("Expected WithAttrs to return a *Handler")
	}
}

func TestHandlerWithGroup(t *testing.T) {
	config := Config{
		Formatter:  &mockFormatter{},
		Writer:     &mockWriter{},
		Channel:    "test",
		CtxKeyName: "ctx",
		AddSource:  false,
	}

	handler := NewHandler(config)
	groupName := "testgroup"

	newHandler := handler.WithGroup(groupName)
	if newHandler == nil {
		t.Error("Expected WithGroup to return a non-nil handler")
	}

	// Should be a different handler instance
	if newHandler == handler {
		t.Error("Expected WithGroup to return a new handler instance")
	}

	// Original handler should be unchanged
	if len(handler.groups) != 0 {
		t.Errorf("Expected original handler to have 0 groups, got %d", len(handler.groups))
	}

	// New handler should have the group
	if h, ok := newHandler.(*Handler); ok {
		if len(h.groups) != 1 {
			t.Errorf("Expected new handler to have 1 group, got %d", len(h.groups))
		}
		if h.groups[0] != groupName {
			t.Errorf("Expected group name '%s', got '%s'", groupName, h.groups[0])
		}
	} else {
		t.Error("Expected WithGroup to return a *Handler")
	}
}

func TestHandlerAppendAttr(t *testing.T) {
	config := Config{
		Formatter:  &mockFormatter{},
		Writer:     &mockWriter{},
		Channel:    "test",
		CtxKeyName: "ctx",
		AddSource:  false,
	}

	handler := NewHandler(config)

	tests := []struct {
		name     string
		attr     slog.Attr
		groups   []string
		expected map[string]interface{}
	}{
		{
			name:     "simple string attribute",
			attr:     slog.String("key", "value"),
			groups:   nil,
			expected: map[string]interface{}{"key": "value"},
		},
		{
			name:     "int attribute",
			attr:     slog.Int("number", 42),
			groups:   nil,
			expected: map[string]interface{}{"number": int64(42)},
		},
		{
			name:     "attribute with group",
			attr:     slog.String("key", "value"),
			groups:   []string{"group1"},
			expected: map[string]interface{}{"group1.key": "value"},
		},
		{
			name:     "attribute with nested groups",
			attr:     slog.String("key", "value"),
			groups:   []string{"group1", "group2"},
			expected: map[string]interface{}{"group1.group2.key": "value"},
		},
		{
			name:     "empty attribute",
			attr:     slog.Attr{},
			groups:   nil,
			expected: map[string]interface{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := make(map[string]interface{})
			handler.appendAttr(m, tt.attr, tt.groups)

			if len(m) != len(tt.expected) {
				t.Errorf("Expected map length %d, got %d", len(tt.expected), len(m))
			}

			for k, v := range tt.expected {
				if m[k] != v {
					t.Errorf("Expected m[%s] = %v, got %v", k, v, m[k])
				}
			}
		})
	}
}

func TestHandlerExtractValue(t *testing.T) {
	config := Config{
		Formatter:  &mockFormatter{},
		Writer:     &mockWriter{},
		Channel:    "test",
		CtxKeyName: "ctx",
		AddSource:  false,
	}

	handler := NewHandler(config)

	tests := []struct {
		name     string
		value    slog.Value
		expected interface{}
	}{
		{
			name:     "string value",
			value:    slog.StringValue("test"),
			expected: "test",
		},
		{
			name:     "int value",
			value:    slog.IntValue(42),
			expected: int64(42),
		},
		{
			name:     "error value",
			value:    slog.AnyValue(errors.New("test error")),
			expected: "test error",
		},
		{
			name:     "stringer value",
			value:    slog.AnyValue(time.Duration(5 * time.Second)),
			expected: time.Duration(5 * time.Second).String(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.extractValue(tt.value)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestNewMultiHandler(t *testing.T) {
	handler1 := NewHandler(Config{
		Formatter:  &mockFormatter{},
		Writer:     &mockWriter{},
		Channel:    "test1",
		CtxKeyName: "ctx",
		AddSource:  false,
	})

	handler2 := NewHandler(Config{
		Formatter:  &mockFormatter{},
		Writer:     &mockWriter{},
		Channel:    "test2",
		CtxKeyName: "ctx",
		AddSource:  false,
	})

	multiHandler := NewMultiHandler(handler1, handler2)
	if multiHandler == nil {
		t.Error("Expected NewMultiHandler to return a non-nil handler")
	}

	if len(multiHandler.handlers) != 2 {
		t.Errorf("Expected 2 handlers, got %d", len(multiHandler.handlers))
	}
}

func TestMultiHandlerEnabled(t *testing.T) {
	// Test with only enabled handlers (our Handler always returns true for Enabled)
	enabledHandler1 := NewHandler(Config{
		Formatter:  &mockFormatter{},
		Writer:     &mockWriter{},
		Channel:    "test1",
		CtxKeyName: "ctx",
		AddSource:  false,
	})

	enabledHandler2 := NewHandler(Config{
		Formatter:  &mockFormatter{},
		Writer:     &mockWriter{},
		Channel:    "test2",
		CtxKeyName: "ctx",
		AddSource:  false,
	})

	multiHandler := NewMultiHandler(enabledHandler1, enabledHandler2)
	ctx := context.Background()

	// Should return true when at least one handler is enabled
	enabled := multiHandler.Enabled(ctx, slog.LevelInfo)
	if !enabled {
		t.Error("Expected Enabled to return true when handlers are enabled")
	}

	// Test with empty MultiHandler
	emptyMultiHandler := NewMultiHandler()
	emptyEnabled := emptyMultiHandler.Enabled(ctx, slog.LevelInfo)
	if emptyEnabled {
		t.Error("Expected Enabled to return false when no handlers are present")
	}
}

func TestMultiHandlerHandle(t *testing.T) {
	writer1 := &mockWriter{}
	writer2 := &mockWriter{}

	handler1 := NewHandler(Config{
		Formatter:  &mockFormatter{},
		Writer:     writer1,
		Channel:    "test1",
		CtxKeyName: "ctx",
		AddSource:  false,
	})

	handler2 := NewHandler(Config{
		Formatter:  &mockFormatter{},
		Writer:     writer2,
		Channel:    "test2",
		CtxKeyName: "ctx",
		AddSource:  false,
	})

	multiHandler := NewMultiHandler(handler1, handler2)
	ctx := context.Background()

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "test message", 0)
	err := multiHandler.Handle(ctx, record)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Both writers should have received the log
	if !strings.Contains(writer1.String(), "test message") {
		t.Error("Expected first writer to contain 'test message'")
	}

	if !strings.Contains(writer2.String(), "test message") {
		t.Error("Expected second writer to contain 'test message'")
	}
}

func TestMultiHandlerHandleWithError(t *testing.T) {
	writer1 := &mockWriter{shouldError: true}
	writer2 := &mockWriter{}

	handler1 := NewHandler(Config{
		Formatter:  &mockFormatter{},
		Writer:     writer1,
		Channel:    "test1",
		CtxKeyName: "ctx",
		AddSource:  false,
	})

	handler2 := NewHandler(Config{
		Formatter:  &mockFormatter{},
		Writer:     writer2,
		Channel:    "test2",
		CtxKeyName: "ctx",
		AddSource:  false,
	})

	multiHandler := NewMultiHandler(handler1, handler2)
	ctx := context.Background()

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "test message", 0)
	err := multiHandler.Handle(ctx, record)

	// Should return error from first handler
	if err == nil {
		t.Error("Expected error from first handler")
	}

	// Second writer should not have received the log due to early return
	if strings.Contains(writer2.String(), "test message") {
		t.Error("Expected second writer to not contain 'test message' due to early return")
	}
}

func TestMultiHandlerWithAttrs(t *testing.T) {
	handler1 := NewHandler(Config{
		Formatter:  &mockFormatter{},
		Writer:     &mockWriter{},
		Channel:    "test1",
		CtxKeyName: "ctx",
		AddSource:  false,
	})

	handler2 := NewHandler(Config{
		Formatter:  &mockFormatter{},
		Writer:     &mockWriter{},
		Channel:    "test2",
		CtxKeyName: "ctx",
		AddSource:  false,
	})

	multiHandler := NewMultiHandler(handler1, handler2)
	attrs := []slog.Attr{slog.String("key", "value")}

	newMultiHandler := multiHandler.WithAttrs(attrs)
	if newMultiHandler == nil {
		t.Error("Expected WithAttrs to return a non-nil handler")
	}

	if mh, ok := newMultiHandler.(*MultiHandler); ok {
		if len(mh.handlers) != 2 {
			t.Errorf("Expected 2 handlers, got %d", len(mh.handlers))
		}
	} else {
		t.Error("Expected WithAttrs to return a *MultiHandler")
	}
}

func TestMultiHandlerWithGroup(t *testing.T) {
	handler1 := NewHandler(Config{
		Formatter:  &mockFormatter{},
		Writer:     &mockWriter{},
		Channel:    "test1",
		CtxKeyName: "ctx",
		AddSource:  false,
	})

	handler2 := NewHandler(Config{
		Formatter:  &mockFormatter{},
		Writer:     &mockWriter{},
		Channel:    "test2",
		CtxKeyName: "ctx",
		AddSource:  false,
	})

	multiHandler := NewMultiHandler(handler1, handler2)
	groupName := "testgroup"

	newMultiHandler := multiHandler.WithGroup(groupName)
	if newMultiHandler == nil {
		t.Error("Expected WithGroup to return a non-nil handler")
	}

	if mh, ok := newMultiHandler.(*MultiHandler); ok {
		if len(mh.handlers) != 2 {
			t.Errorf("Expected 2 handlers, got %d", len(mh.handlers))
		}
	} else {
		t.Error("Expected WithGroup to return a *MultiHandler")
	}
}
