package ulog

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	// Test default values
	if config.Channel != "app" {
		t.Errorf("Expected default channel 'app', got '%s'", config.Channel)
	}

	if config.AddSource != false {
		t.Errorf("Expected default AddSource false, got %v", config.AddSource)
	}

	if config.CtxKeyName != "ctx" {
		t.Errorf("Expected default CtxKeyName 'ctx', got '%s'", config.CtxKeyName)
	}

	if config.Writer != os.Stderr {
		t.Errorf("Expected default Writer to be os.Stderr")
	}

	// Test that formatter is not nil and is TextFormatter
	if config.Formatter == nil {
		t.Error("Expected default formatter to not be nil")
	}

	if _, ok := config.Formatter.(*TextFormatter); !ok {
		t.Error("Expected default formatter to be TextFormatter")
	}
}

func TestNew(t *testing.T) {
	var buf bytes.Buffer
	config := Config{
		Formatter:  NewTextFormatter(),
		Writer:     &buf,
		Channel:    "test",
		CtxKeyName: "context",
		AddSource:  false,
	}

	handler := New(config)
	if handler == nil {
		t.Error("Expected New to return a non-nil handler")
	}

	// Test that the handler implements slog.Handler interface
	var _ slog.Handler = handler
}

func TestNewJSONLogger(t *testing.T) {
	var buf bytes.Buffer
	channel := "test-json"

	handler := NewJSONLogger(&buf, channel)
	if handler == nil {
		t.Error("Expected NewJSONLogger to return a non-nil handler")
	}

	// Test that it creates a logger that can handle log entries
	logger := slog.New(handler)
	logger.Info("test message")

	output := buf.String()
	if !strings.Contains(output, "test message") {
		t.Errorf("Expected output to contain 'test message', got: %s", output)
	}

	if !strings.Contains(output, `"channel":"test-json"`) {
		t.Errorf("Expected output to contain channel name, got: %s", output)
	}

	// Should be valid JSON format
	if !strings.Contains(output, "{") || !strings.Contains(output, "}") {
		t.Errorf("Expected JSON format output, got: %s", output)
	}
}

func TestNewTextLogger(t *testing.T) {
	var buf bytes.Buffer
	channel := "test-text"

	handler := NewTextLogger(&buf, channel)
	if handler == nil {
		t.Error("Expected NewTextLogger to return a non-nil handler")
	}

	// Test that it creates a logger that can handle log entries
	logger := slog.New(handler)
	logger.Info("test message")

	output := buf.String()
	if !strings.Contains(output, "test message") {
		t.Errorf("Expected output to contain 'test message', got: %s", output)
	}

	if !strings.Contains(output, "test-text") {
		t.Errorf("Expected output to contain channel name, got: %s", output)
	}

	if !strings.Contains(output, "INFO") {
		t.Errorf("Expected output to contain log level, got: %s", output)
	}
}

func TestNewFileLogger(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	logPath := tempDir + "/test.log"

	tests := []struct {
		name       string
		path       string
		format     string
		channel    string
		maxSize    int64
		maxBackups int
		maxAge     int
		wantError  bool
	}{
		{
			name:       "valid json logger",
			path:       logPath,
			format:     "json",
			channel:    "test",
			maxSize:    1024,
			maxBackups: 5,
			maxAge:     7,
			wantError:  false,
		},
		{
			name:       "valid text logger",
			path:       logPath + "2",
			format:     "text",
			channel:    "test",
			maxSize:    1024,
			maxBackups: 5,
			maxAge:     7,
			wantError:  false,
		},
		{
			name:       "invalid path",
			path:       "/invalid/path/that/does/not/exist/test.log",
			format:     "json",
			channel:    "test",
			maxSize:    1024,
			maxBackups: 5,
			maxAge:     7,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, err := NewFileLogger(tt.path, tt.format, tt.channel, tt.maxSize, tt.maxBackups, tt.maxAge)

			if tt.wantError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if handler == nil {
				t.Error("Expected non-nil handler")
				return
			}

			// Test that the logger works
			logger := slog.New(handler)
			logger.Info("test message")

			// Clean up - close the file writer if possible
			if h, ok := handler.(*Handler); ok {
				if fw, ok := h.config.Writer.(*FileWriter); ok {
					fw.Close()
				}
			}
		})
	}
}

func TestMapSlogLevelToMonologLevel(t *testing.T) {
	tests := []struct {
		slogLevel    slog.Level
		expectedCode int
		expectedName string
	}{
		{slog.LevelDebug, 100, "DEBUG"},
		{slog.LevelInfo, 200, "INFO"},
		{slog.LevelWarn, 300, "WARNING"},
		{slog.LevelError, 400, "ERROR"},
		{slog.Level(12), 500, "CRITICAL"}, // Higher than ERROR
	}

	for _, tt := range tests {
		t.Run(tt.expectedName, func(t *testing.T) {
			code, name := mapSlogLevelToMonologLevel(tt.slogLevel)

			if code != tt.expectedCode {
				t.Errorf("Expected code %d, got %d", tt.expectedCode, code)
			}

			if name != tt.expectedName {
				t.Errorf("Expected name %s, got %s", tt.expectedName, name)
			}
		})
	}
}

func TestLogRecord(t *testing.T) {
	// Test LogRecord struct creation and JSON marshaling
	now := time.Now()
	record := LogRecord{
		Message:   "test message",
		Channel:   "test",
		Level:     200,
		LevelName: "INFO",
		DateTime:  now,
		Context:   map[string]interface{}{"key": "value"},
		Extra:     map[string]interface{}{"extra": "data"},
	}

	if record.Message != "test message" {
		t.Errorf("Expected message 'test message', got '%s'", record.Message)
	}

	if record.Channel != "test" {
		t.Errorf("Expected channel 'test', got '%s'", record.Channel)
	}

	if record.Level != 200 {
		t.Errorf("Expected level 200, got %d", record.Level)
	}

	if record.LevelName != "INFO" {
		t.Errorf("Expected level name 'INFO', got '%s'", record.LevelName)
	}

	if !record.DateTime.Equal(now) {
		t.Errorf("Expected datetime %v, got %v", now, record.DateTime)
	}
}

func TestConfig(t *testing.T) {
	// Test Config struct creation
	var buf bytes.Buffer
	formatter := NewJSONFormatter()

	config := Config{
		Formatter:  formatter,
		Writer:     &buf,
		Channel:    "test-config",
		CtxKeyName: "custom-ctx",
		AddSource:  true,
	}

	if config.Formatter != formatter {
		t.Error("Expected formatter to match")
	}

	if config.Writer != &buf {
		t.Error("Expected writer to match")
	}

	if config.Channel != "test-config" {
		t.Errorf("Expected channel 'test-config', got '%s'", config.Channel)
	}

	if config.CtxKeyName != "custom-ctx" {
		t.Errorf("Expected CtxKeyName 'custom-ctx', got '%s'", config.CtxKeyName)
	}

	if config.AddSource != true {
		t.Errorf("Expected AddSource true, got %v", config.AddSource)
	}
}
