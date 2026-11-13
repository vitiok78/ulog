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

	b := describeBudget{nodes: maxDescribeNodes, bytes: maxDescribeBytes}
	if !printable(reflect.ValueOf(v), 0, map[visit]bool{}, &b) {
		if b.bytes < 0 && b.nodes >= 0 {
			return fmt.Sprintf("[%T too large to print: over %d bytes]", v, maxDescribeBytes)
		}
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
	// maxDescribeBytes bounds the estimated size of the printed value. A
	// slice holding one long string many times is small in memory and huge
	// printed.
	maxDescribeBytes = 4 << 20
)

// describeBudget is what printable may still spend.
type describeBudget struct {
	nodes int
	bytes int
}

// spend takes n bytes and reports whether the budget still holds.
func (b *describeBudget) spend(n int) bool {
	b.bytes -= n
	return b.bytes >= 0
}

// visit identifies a map or slice on the current path of printable.
type visit struct {
	ptr uintptr
	len int
	typ reflect.Type
}

// printable reports whether fmt can print v in bounded time and space. It
// follows what fmt follows: maps, slices, arrays, structs and interfaces at
// any depth, and pointers only at the top level (deeper pointers print as
// addresses), and estimates the printed size on the way. It is conservative:
// it ignores String and Error methods that would stop fmt early.
func printable(v reflect.Value, depth int, path map[visit]bool, b *describeBudget) bool {
	b.nodes--
	if depth > maxDescribeDepth || b.nodes < 0 {
		return false
	}

	// Where fmt would call Format, Error or String, it prints the method's
	// result instead of the value: charge that result. describe has already
	// handled Error and String on the top-level value.
	if v.IsValid() && v.CanInterface() && hasPrintMethod(v.Type(), depth) {
		return b.spend(methodOutputLen(v.Interface()))
	}

	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return b.spend(len("<nil>"))
		}
		return printable(v.Elem(), depth+1, path, b)

	case reflect.Pointer:
		if depth > 0 || v.IsNil() {
			return b.spend(len("0xc000000000"))
		}
		return b.spend(1) && printable(v.Elem(), depth+1, path, b)

	case reflect.Map:
		if v.IsNil() {
			return b.spend(len("map[]"))
		}
		key := visit{ptr: v.Pointer(), len: v.Len(), typ: v.Type()}
		if path[key] {
			return false
		}
		path[key] = true
		defer delete(path, key)

		if !b.spend(len("map[]")) {
			return false
		}
		iter := v.MapRange()
		for iter.Next() {
			if !b.spend(2) || !printable(iter.Key(), depth+1, path, b) || !printable(iter.Value(), depth+1, path, b) {
				return false
			}
		}
		return true

	case reflect.Slice:
		if v.IsNil() {
			return b.spend(len("[]"))
		}
		if !canNest(v.Type().Elem()) {
			return scalarsFit(v, b)
		}
		key := visit{ptr: v.Pointer(), len: v.Len(), typ: v.Type()}
		if path[key] {
			return false
		}
		path[key] = true
		defer delete(path, key)
		return elementsPrintable(v, depth, path, b)

	case reflect.Array:
		if !canNest(v.Type().Elem()) {
			return scalarsFit(v, b)
		}
		return elementsPrintable(v, depth, path, b)

	case reflect.Struct:
		if !b.spend(len("{}")) {
			return false
		}
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			if !b.spend(len(t.Field(i).Name)+2) || !printable(v.Field(i), depth+1, path, b) {
				return false
			}
		}
		return true

	case reflect.String:
		return b.spend(v.Len() + 1)
	}

	return b.spend(scalarSize(v.Kind()))
}

var (
	errorType     = reflect.TypeOf((*error)(nil)).Elem()
	stringerType  = reflect.TypeOf((*fmt.Stringer)(nil)).Elem()
	formatterType = reflect.TypeOf((*fmt.Formatter)(nil)).Elem()
)

// hasPrintMethod reports whether fmt's %+v prints a value of type t through
// one of its methods. fmt calls Format at any depth, and Error and String
// below the top level (printArg handles the top level, describe mirrors it).
func hasPrintMethod(t reflect.Type, depth int) bool {
	if t.Implements(formatterType) {
		return true
	}
	return depth > 0 && (t.Implements(errorType) || t.Implements(stringerType))
}

// methodOutputLen is the length of what fmt prints for x through its method.
func methodOutputLen(x interface{}) (n int) {
	defer func() {
		if r := recover(); r != nil {
			n = len("%!v(PANIC=)")
		}
	}()
	return len(fmt.Sprintf("%+v", x))
}

// elementsPrintable runs printable over the elements of a slice or array.
func elementsPrintable(v reflect.Value, depth int, path map[visit]bool, b *describeBudget) bool {
	if !b.spend(len("[]")) {
		return false
	}
	for i := 0; i < v.Len(); i++ {
		if !b.spend(1) || !printable(v.Index(i), depth+1, path, b) {
			return false
		}
	}
	return true
}

// scalarsFit spends the printed size of a slice or array of scalars. Strings
// are summed one by one (they may all share one long string); other elements
// are estimated from the count, without looking at each.
func scalarsFit(v reflect.Value, b *describeBudget) bool {
	if !b.spend(len("[]")) {
		return false
	}
	if v.Type().Elem().Kind() != reflect.String {
		n := v.Len()
		size := scalarSize(v.Type().Elem().Kind()) + 1
		if n > b.bytes/size {
			b.bytes = -1
			return false
		}
		return b.spend(n * size)
	}
	for i := 0; i < v.Len(); i++ {
		if !b.spend(v.Index(i).Len() + 1) {
			return false
		}
	}
	return true
}

// scalarSize is the most fmt prints for one scalar of kind k, roughly.
func scalarSize(k reflect.Kind) int {
	switch k {
	case reflect.Bool:
		return len("false")
	case reflect.Int8, reflect.Uint8:
		return len("-128")
	case reflect.Complex64, reflect.Complex128:
		return 2*len("-1.7976931348623157e+308") + 3
	}
	return len("-1.7976931348623157e+308")
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
