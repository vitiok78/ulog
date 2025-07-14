package ulog

import (
	"encoding/json"
	"fmt"
	"time"
)

const defaultTimeFormat = time.RFC3339Nano

// Formatter defines the interface for log formatters
type Formatter interface {
	Format(record LogRecord) ([]byte, error)
}

// JSONFormatter formats logs as JSON following Symfony monolog format
type JSONFormatter struct {
	TimeFormat string
}

// NewJSONFormatter creates a new JSONFormatter
func NewJSONFormatter() *JSONFormatter {
	return &JSONFormatter{
		TimeFormat: defaultTimeFormat,
	}
}

// Format implements the Formatter interface
func (f *JSONFormatter) Format(record LogRecord) ([]byte, error) {
	// Create a map to represent the JSON structure
	logMap := map[string]interface{}{
		"message":    record.Message,
		"context":    record.Context,
		"level":      record.Level,
		"level_name": record.LevelName,
		"channel":    record.Channel,
		"datetime":   record.DateTime.Format(f.TimeFormat),
		"extra":      record.Extra,
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(logMap)
	if err != nil {
		return nil, err
	}

	// Add newline
	return append(jsonData, '\n'), nil
}

// TextFormatter formats logs as human-readable text
type TextFormatter struct {
	TimeFormat string
}

// NewTextFormatter creates a new TextFormatter
func NewTextFormatter() *TextFormatter {
	return &TextFormatter{
		TimeFormat: defaultTimeFormat,
	}
}

// Format implements the Formatter interface
func (f *TextFormatter) Format(record LogRecord) ([]byte, error) {
	// Format the basic log line
	logLine := fmt.Sprintf("[%s] %s.%s: %s",
		record.DateTime.Format(f.TimeFormat),
		record.Channel,
		record.LevelName,
		record.Message)

	// Add context if present
	if len(record.Context) > 0 {
		contextJSON, err := json.Marshal(record.Context)
		if err == nil {
			logLine += fmt.Sprintf(" %s", string(contextJSON))
		}
	} else {
		logLine += " []"
	}

	// Add extra if present
	if len(record.Extra) > 0 {
		extraJSON, err := json.Marshal(record.Extra)
		if err == nil {
			logLine += fmt.Sprintf(" %s", string(extraJSON))
		}
	} else {
		logLine += " []"
	}

	// Add newline
	return []byte(logLine + "\n"), nil
}
