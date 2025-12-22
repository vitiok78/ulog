// Package ulog provides a configurable logging system based on Go's slog package.
// It follows the log structure from PHP Symfony monolog package.
package ulog

import (
	"io"
	"log/slog"
	"os"
	"time"
)

// LogRecord represents a log entry following the Symfony monolog structure
type LogRecord struct {
	Message   string                 `json:"message"`
	Channel   string                 `json:"channel"`
	Level     int                    `json:"level"`
	LevelName string                 `json:"level_name"`
	DateTime  time.Time              `json:"datetime"`
	Context   map[string]interface{} `json:"context"`
	Extra     map[string]interface{} `json:"extra"`
}

// Config holds the configuration for the ulog handler
type Config struct {
	Formatter  Formatter
	Writer     io.Writer
	Channel    string
	CtxKeyName string
	AddSource  bool
	Level      slog.Level
}

// DefaultConfig returns a default configuration with text formatter and os.Stderr output
func DefaultConfig() Config {
	return Config{
		Formatter:  NewTextFormatter(),
		Writer:     os.Stderr,
		Channel:    "app",
		AddSource:  false,
		CtxKeyName: "ctx",
		Level:      slog.LevelInfo,
	}
}

// New creates a new slog.Handler using the provided configuration
func New(config Config) slog.Handler {
	handler := NewHandler(config)

	// Create a new slog.Handler
	var slogHandler slog.Handler = handler

	return slogHandler
}

// Helper functions to create common configurations

// NewJSONLogger creates a new logger with JSON formatting
func NewJSONLogger(w io.Writer, channel string) slog.Handler {
	config := DefaultConfig()
	config.Formatter = NewJSONFormatter()
	config.Writer = w
	config.Channel = channel
	return New(config)
}

// NewTextLogger creates a new logger with text formatting
func NewTextLogger(w io.Writer, channel string) slog.Handler {
	config := DefaultConfig()
	config.Formatter = NewTextFormatter()
	config.Writer = w
	config.Channel = channel
	return New(config)
}

// NewFileLogger creates a new logger that writes to a file
func NewFileLogger(path string, format string, channel string, maxSize int64, maxBackups int, maxAge int) (slog.Handler, error) {
	var formatter Formatter
	if format == "json" {
		formatter = NewJSONFormatter()
	} else {
		formatter = NewTextFormatter()
	}

	fileWriter, err := NewFileWriter(path, maxSize, maxBackups, maxAge)
	if err != nil {
		return nil, err
	}

	config := DefaultConfig()
	config.Formatter = formatter
	config.Writer = fileWriter
	config.Channel = channel

	return New(config), nil
}

// Map slog levels to Symfony monolog levels
func mapSlogLevelToMonologLevel(level slog.Level) (int, string) {
	switch {
	case level <= slog.LevelDebug:
		return 100, "DEBUG"
	case level <= slog.LevelInfo:
		return 200, "INFO"
	case level <= slog.LevelWarn:
		return 300, "WARNING"
	case level <= slog.LevelError:
		return 400, "ERROR"
	default:
		return 500, "CRITICAL"
	}
}
