package ulog

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"
)

const defaultTimeFormat = time.RFC3339Nano

// Formatter defines the interface for log formatters
type Formatter interface {
	Format(record LogRecord) ([]byte, error)
}

// JSONFormatter formats logs as JSON following Symfony monolog format.
//
// Context and Extra are encoded value by value. A value JSON cannot encode
// (a channel, a func, a NaN, a cycle, a panicking MarshalJSON) is written as
// a string instead, so one bad value never drops the record: its String or
// Error text, %+v for a scalar, or just its type for a struct, map, slice,
// array or pointer.
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
		"context":    encodeFields(record.Context),
		"level":      record.Level,
		"level_name": record.LevelName,
		"channel":    record.Channel,
		"datetime":   record.DateTime.Format(f.TimeFormat),
		"extra":      encodeFields(record.Extra),
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(logMap)
	if err != nil {
		return nil, err
	}

	// Add newline
	return append(jsonData, '\n'), nil
}

// TextFormatter formats logs as human-readable text.
//
// Context and Extra are written as JSON with the same per-value fallback as
// JSONFormatter.
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

	logLine += " " + textFields(record.Context)
	logLine += " " + textFields(record.Extra)

	// Add newline
	return []byte(logLine + "\n"), nil
}

// textFields renders m for TextFormatter: "[]" when empty, JSON otherwise.
func textFields(m map[string]interface{}) string {
	if len(m) == 0 {
		return "[]"
	}
	data, err := json.Marshal(encodeFields(m))
	if err != nil {
		// Not reachable: every entry is already valid JSON.
		return "[]"
	}
	return string(data)
}

// encodeFields encodes each value of m on its own, so a value JSON rejects
// becomes a string and the rest stay as they are. A nil map stays nil and
// encodes as null, as before.
func encodeFields(m map[string]interface{}) map[string]json.RawMessage {
	return encodeMap(m, map[uintptr]bool{})
}

// encodeMap is encodeFields with the maps on the current path in seen, so a
// map that contains itself ends in a string instead of recursing forever.
func encodeMap(m map[string]interface{}, seen map[uintptr]bool) map[string]json.RawMessage {
	if m == nil {
		return nil
	}

	ptr := reflect.ValueOf(m).Pointer()
	seen[ptr] = true
	defer delete(seen, ptr)

	out := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		out[k] = encodeValue(v, seen)
	}
	return out
}

// encodeValue returns v as JSON, or its description as a JSON string when v
// cannot be encoded. Nested map[string]interface{} values (context groups)
// are encoded entry by entry too.
func encodeValue(v interface{}, seen map[uintptr]bool) json.RawMessage {
	if nested, ok := v.(map[string]interface{}); ok && nested != nil {
		if seen[reflect.ValueOf(nested).Pointer()] {
			return jsonString(fmt.Sprintf("[cyclic %T]", v))
		}
		if data, err := json.Marshal(encodeMap(nested, seen)); err == nil {
			return data
		}
	}

	if data, err := marshalValue(v); err == nil {
		return data
	}
	return jsonString(describe(v))
}

// marshalValue is json.Marshal that turns a panic into an error.
func marshalValue(v interface{}) (data []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			data, err = nil, errors.New("ulog: panic while encoding value")
		}
	}()
	return json.Marshal(v)
}

// jsonString encodes s, which never fails.
func jsonString(s string) json.RawMessage {
	data, _ := json.Marshal(s)
	return data
}

// describe returns a string for a value JSON cannot encode. It never panics.
//
// A Stringer or error gives its own text, as fmt would print it. Other
// scalars (a channel, a func, a NaN) are printed with %+v. Anything that can
// hold other values (struct, map, slice, array, pointer) is named by type
// only: %+v would print the fields JSON leaves out, json:"-" and unexported
// ones, which may hold secrets.
func describe(v interface{}) (s string) {
	defer func() {
		if r := recover(); r != nil {
			s = fmt.Sprintf("[unprintable %T]", v)
		}
	}()

	switch x := v.(type) {
	case error:
		return x.Error()
	case fmt.Stringer:
		return x.String()
	}

	switch reflect.ValueOf(v).Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128,
		reflect.String, reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return fmt.Sprintf("%+v", v)
	}
	return fmt.Sprintf("[unencodable %T]", v)
}
