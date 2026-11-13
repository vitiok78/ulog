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
// Error text, or its %+v dump. The dump includes fields JSON would hide
// (json:"-", unexported): keeping the log line wins over hiding them.
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

// encodeFields returns m with every value made safe for json.Marshal, so a
// value JSON rejects becomes a string and the rest stay as they are. A nil
// map stays nil and encodes as null, as before.
//
// Each value is encoded once here to test it, and the result is copied once
// more by the final Marshal. Nested maps are rebuilt, not re-encoded, and a
// map referenced several times is rebuilt once, so the work stays linear in
// the number of distinct values.
func encodeFields(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	s := &safeState{path: map[uintptr]bool{}, done: map[uintptr]safeResult{}}
	out, _ := s.safeMap(m)
	return out
}

// maxRepeatedBytes bounds the output spent on extra copies of a nested map
// that is referenced more than once. Repeats past it become a marker. A few
// maps referencing each other twice per level would otherwise print
// exponentially large; the first copy of every map is always written.
const maxRepeatedBytes = 4 << 20

// safeState carries one encodeFields call.
type safeState struct {
	path     map[uintptr]bool       // maps on the current path, for cycles
	done     map[uintptr]safeResult // maps already rebuilt
	repeated int                    // approximate bytes spent on repeats
}

// safeResult is a rebuilt map and the approximate size of its JSON.
type safeResult struct {
	out  map[string]interface{}
	size int
}

// safeMap rebuilds m with every value made safe and returns it with its
// approximate encoded size.
func (s *safeState) safeMap(m map[string]interface{}) (map[string]interface{}, int) {
	ptr := reflect.ValueOf(m).Pointer()
	s.path[ptr] = true
	defer delete(s.path, ptr)

	out := make(map[string]interface{}, len(m))
	size := 2 // {}
	for k, v := range m {
		var n int
		out[k], n = s.safeValue(v)
		size += len(k) + 4 + n // "k":v,
	}
	s.done[ptr] = safeResult{out: out, size: size}
	return out, size
}

// safeValue returns v as a value json.Marshal accepts, with its approximate
// encoded size: its encoding as json.RawMessage, or its description when v
// cannot be encoded. Nested map[string]interface{} values (context groups)
// are made safe entry by entry.
func (s *safeState) safeValue(v interface{}) (interface{}, int) {
	if nested, ok := v.(map[string]interface{}); ok && nested != nil {
		ptr := reflect.ValueOf(nested).Pointer()
		if s.path[ptr] {
			marker := fmt.Sprintf("[cyclic %T]", v)
			return marker, len(marker) + 2
		}
		if r, ok := s.done[ptr]; ok {
			if s.repeated+r.size > maxRepeatedBytes {
				marker := fmt.Sprintf("[repeated %T omitted: written in full elsewhere in this record]", v)
				return marker, len(marker) + 2
			}
			s.repeated += r.size
			return r.out, r.size
		}
		return s.safeMap(nested)
	}

	if data, err := marshalValue(v); err == nil {
		return json.RawMessage(data), len(data)
	}
	desc := describe(v)
	return desc, len(desc) + 2
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

// describe returns a string for a value JSON cannot encode. It never panics.
//
// Losing the value is worse than exposing it, so describe shows as much as
// it safely can: the value's String or Error text if it has one, otherwise
// fmt's %+v. Note that %+v prints every field, including json:"-" and
// unexported ones, so secrets JSON would hide can appear here.
//
// fmt has no cycle detection and would recurse forever on a value that
// refers to itself through a map or slice. Such a value, or one too deep or
// too large to walk (see maxDescribeDepth, maxDescribeNodes), is named by
// its type instead.
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

	budget := maxDescribeNodes
	if !printable(reflect.ValueOf(v), 0, map[visit]bool{}, &budget) {
		return fmt.Sprintf("[cyclic, too deep or too large %T]", v)
	}
	return fmt.Sprintf("%+v", v)
}

const (
	// maxDescribeDepth bounds how deep describe looks into a value.
	maxDescribeDepth = 64
	// maxDescribeNodes bounds how many values describe visits. A structure
	// that shares maps or slices a lot can be exponentially larger printed
	// than in memory.
	maxDescribeNodes = 100_000
)

// visit identifies a map or slice on the current path of printable.
type visit struct {
	ptr uintptr
	len int
	typ reflect.Type
}

// printable reports whether fmt can print v in bounded time. It follows what
// fmt follows: maps, slices, arrays, structs and interfaces at any depth, and
// pointers only at the top level (deeper pointers print as addresses). It is
// conservative: it ignores String and Error methods that would stop fmt early.
func printable(v reflect.Value, depth int, path map[visit]bool, budget *int) bool {
	*budget--
	if depth > maxDescribeDepth || *budget < 0 {
		return false
	}

	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return true
		}
		return printable(v.Elem(), depth+1, path, budget)

	case reflect.Pointer:
		if depth > 0 || v.IsNil() {
			return true
		}
		return printable(v.Elem(), depth+1, path, budget)

	case reflect.Map:
		if v.IsNil() {
			return true
		}
		key := visit{ptr: v.Pointer(), len: v.Len(), typ: v.Type()}
		if path[key] {
			return false
		}
		path[key] = true
		defer delete(path, key)

		iter := v.MapRange()
		for iter.Next() {
			if !printable(iter.Key(), depth+1, path, budget) || !printable(iter.Value(), depth+1, path, budget) {
				return false
			}
		}
		return true

	case reflect.Slice:
		if v.IsNil() || !canNest(v.Type().Elem()) {
			return true
		}
		key := visit{ptr: v.Pointer(), len: v.Len(), typ: v.Type()}
		if path[key] {
			return false
		}
		path[key] = true
		defer delete(path, key)
		return elementsPrintable(v, depth, path, budget)

	case reflect.Array:
		if !canNest(v.Type().Elem()) {
			return true
		}
		return elementsPrintable(v, depth, path, budget)

	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !printable(v.Field(i), depth+1, path, budget) {
				return false
			}
		}
		return true
	}

	return true
}

// elementsPrintable runs printable over the elements of a slice or array.
func elementsPrintable(v reflect.Value, depth int, path map[visit]bool, budget *int) bool {
	for i := 0; i < v.Len(); i++ {
		if !printable(v.Index(i), depth+1, path, budget) {
			return false
		}
	}
	return true
}

// canNest reports whether values of type t can contain other values that
// fmt would print recursively.
func canNest(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Interface, reflect.Pointer, reflect.Map, reflect.Slice, reflect.Array, reflect.Struct:
		return true
	}
	return false
}
