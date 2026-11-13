package ulog

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
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

	// Build the line in one buffer: context and extra can be megabytes, and
	// joining strings would copy them several times.
	line := []byte(logLine)
	line = appendTextFields(append(line, ' '), record.Context)
	line = appendTextFields(append(line, ' '), record.Extra)
	return append(line, '\n'), nil
}

// textFields renders m for TextFormatter: "[]" when empty, JSON otherwise.
func appendTextFields(dst []byte, m map[string]interface{}) []byte {
	if len(m) == 0 {
		return append(dst, "[]"...)
	}
	data, err := json.Marshal(encodeFields(m))
	if err != nil {
		// Not reachable: every entry is already valid JSON.
		return append(dst, "[]"...)
	}
	return append(dst, data...)
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
	s := &safeState{path: map[uintptr]bool{}, done: map[uintptr]safeResult{}, valueBytes: maxFieldsValueBytes}
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
	path       map[uintptr]bool       // maps on the current path, for cycles
	done       map[uintptr]safeResult // maps already rebuilt
	repeated   int                    // approximate bytes spent on repeats
	valueBytes int                    // encoded value bytes still allowed
}

const (
	// maxFieldsValueBytes caps the encoded values of one record section
	// (context or extra), counted from the bytes actually produced. A value
	// that starts while budget is left is written in full, so a single
	// large value is never cut. Past the cap, only small values are written
	// (see smallValue); others become valueLimitMarker without being
	// encoded. The handler can expand one compact value into thousands of
	// leaves that all repeat one shared payload; this is what bounds them.
	maxFieldsValueBytes = 16 << 20
	// maxSmallStringBytes: strings up to this length still count as small.
	maxSmallStringBytes = 1024
)

// valueLimitMarker replaces values past maxFieldsValueBytes.
var valueLimitMarker = fmt.Sprintf("[value omitted: %d bytes of values already written in this record]", maxFieldsValueBytes)

// smallValue encodes v if it is cheap to encode and small: nil, a bool, a
// number, a short string or a time that encodes. These are still written
// past the cap, so IDs, counts and timestamps logged next to a runaway
// value survive.
func smallValue(v interface{}) (json.RawMessage, bool) {
	switch x := v.(type) {
	case nil, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
	case string:
		if len(x) > maxSmallStringBytes {
			return nil, false
		}
	case time.Time:
		data, err := x.MarshalJSON()
		if err != nil {
			return nil, false
		}
		return data, true
	default:
		return nil, false
	}
	data, err := marshalValue(v)
	if err != nil {
		return nil, false
	}
	return data, true
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

	// Past the cap: small values only, the rest is not encoded at all.
	if s.valueBytes <= 0 {
		if data, ok := smallValue(v); ok {
			return data, len(data)
		}
		return valueLimitMarker, len(valueLimitMarker) + 2
	}

	if data, err := marshalValue(v); err == nil {
		s.valueBytes -= len(data)
		return json.RawMessage(data), len(data)
	}
	desc := describe(v)
	s.valueBytes -= len(desc)
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
// Losing the value is worse than exposing it, so describe prints what fmt's
// %+v would: the value's Format, Error or String output if it has one,
// otherwise all of its fields, including json:"-" and unexported ones, so
// secrets JSON would hide can appear here.
//
// It does not call fmt on the value itself, because fmt has no limits: it
// recurses forever on a value that refers to itself and can print gigabytes
// from a small value that repeats one long string. describe prints through
// dumper instead, which matches %+v for ordinary values, writes "[cycle]"
// where fmt would loop, and stops at maxDescribeBytes with a marker.
func describe(v interface{}) (s string) {
	defer func() {
		if r := recover(); r != nil {
			s = fmt.Sprintf("[unprintable %T]", v)
		}
	}()
	return dump(v)
}

const (
	// maxDescribeBytes caps the output of describe.
	maxDescribeBytes = 4 << 20
	// maxDescribeDepth caps nesting; deeper values print as "[max depth]".
	maxDescribeDepth = 256
	// maxSortedKeys: maps with more entries print in iteration order rather
	// than sorted, to avoid collecting and sorting all their keys.
	maxSortedKeys = 100_000
)

// dumpFull unwinds a dumper when its output reaches the cap.
type dumpFull struct{}

// dumper prints values like fmt's %+v into a buffer of bounded size.
type dumper struct {
	buf   []byte
	limit int
	full  bool           // a method's output reached the limit
	path  map[visit]bool // maps and slices on the current path
}

// visit identifies a map or slice on the current path of a dumper.
type visit struct {
	ptr uintptr
	len int
	typ reflect.Type
}

// dump returns v printed like %+v, cut at maxDescribeBytes.
func dump(v interface{}) (s string) {
	d := &dumper{limit: maxDescribeBytes, path: map[visit]bool{}}
	defer func() {
		if r := recover(); r != nil {
			if _, full := r.(dumpFull); !full {
				panic(r)
			}
			d.buf = fmt.Appendf(d.buf, "...[truncated at %d bytes]", maxDescribeBytes)
			s = string(d.buf)
		}
	}()
	d.value(reflect.ValueOf(v), 0)
	return string(d.buf)
}

// write appends s, or as much of it as fits and then stops the dump.
func (d *dumper) write(s string) {
	if room := d.limit - len(d.buf); len(s) > room {
		d.buf = append(d.buf, s[:room]...)
		panic(dumpFull{})
	}
	d.buf = append(d.buf, s...)
}

// value prints v following fmt's printValue for the %+v verb.
func (d *dumper) value(v reflect.Value, depth int) {
	if !v.IsValid() {
		d.write("<nil>")
		return
	}
	if depth > maxDescribeDepth {
		d.write("[max depth]")
		return
	}

	if v.Kind() == reflect.Interface && v.IsNil() {
		d.write("<nil>")
		return
	}

	// fmt prints a value through its Format, Error or String method when
	// it can reach the value (exported fields only).
	if v.CanInterface() && hasPrintMethod(v.Type()) {
		d.method(v.Interface())
		return
	}

	switch v.Kind() {
	case reflect.Bool:
		d.write(strconv.FormatBool(v.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		d.write(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		d.write(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32:
		d.write(strconv.FormatFloat(v.Float(), 'g', -1, 32))
	case reflect.Float64:
		d.write(strconv.FormatFloat(v.Float(), 'g', -1, 64))
	case reflect.Complex64:
		d.write(fmt.Sprint(complex64(v.Complex())))
	case reflect.Complex128:
		d.write(fmt.Sprint(v.Complex()))
	case reflect.String:
		d.write(v.String())
	case reflect.Interface:
		d.value(v.Elem(), depth+1)
	case reflect.Pointer:
		// fmt follows a pointer only at the top level.
		if depth == 0 && !v.IsNil() {
			switch v.Elem().Kind() {
			case reflect.Array, reflect.Slice, reflect.Struct, reflect.Map:
				d.write("&")
				d.value(v.Elem(), depth+1)
				return
			}
		}
		d.pointer(v)
	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		d.pointer(v)
	case reflect.Map:
		d.mapValue(v, depth)
	case reflect.Slice, reflect.Array:
		d.list(v, depth)
	case reflect.Struct:
		d.structValue(v, depth)
	default:
		d.write("?")
	}
}

func (d *dumper) pointer(v reflect.Value) {
	if p := v.Pointer(); p != 0 {
		d.write("0x" + strconv.FormatUint(uint64(p), 16))
		return
	}
	d.write("<nil>")
}

// enter marks a map or slice as on the current path. It reports false, and
// prints "[cycle]", when it already is: fmt would recurse forever there.
func (d *dumper) enter(v reflect.Value) (visit, bool) {
	key := visit{ptr: v.Pointer(), len: v.Len(), typ: v.Type()}
	if d.path[key] {
		d.write("[cycle]")
		return key, false
	}
	d.path[key] = true
	return key, true
}

func (d *dumper) mapValue(v reflect.Value, depth int) {
	if v.IsNil() {
		d.write("map[]")
		return
	}
	key, ok := d.enter(v)
	if !ok {
		return
	}
	defer delete(d.path, key)

	d.write("map[")
	entry := func(i int, k, val reflect.Value) {
		if i > 0 {
			d.write(" ")
		}
		d.value(k, depth+1)
		d.write(":")
		d.value(val, depth+1)
	}
	if v.Len() > maxSortedKeys {
		iter := v.MapRange()
		for i := 0; iter.Next(); i++ {
			entry(i, iter.Key(), iter.Value())
		}
	} else {
		// Take keys and values together: looking a value up again by its
		// key fails for keys that are not equal to themselves (NaN).
		type pair struct{ k, v reflect.Value }
		pairs := make([]pair, 0, v.Len())
		for iter := v.MapRange(); iter.Next(); {
			pairs = append(pairs, pair{iter.Key(), iter.Value()})
		}
		slices.SortStableFunc(pairs, func(a, b pair) int { return compareKeys(a.k, b.k) })
		for i, p := range pairs {
			entry(i, p.k, p.v)
		}
	}
	d.write("]")
}

func (d *dumper) list(v reflect.Value, depth int) {
	if v.Kind() == reflect.Slice {
		if v.IsNil() {
			d.write("[]")
			return
		}
		key, ok := d.enter(v)
		if !ok {
			return
		}
		defer delete(d.path, key)
	}

	// fmt prints byte slices and arrays as decimals without methods.
	bytes := v.Type().Elem().Kind() == reflect.Uint8
	d.write("[")
	for i := 0; i < v.Len(); i++ {
		if i > 0 {
			d.write(" ")
		}
		if bytes {
			d.write(strconv.FormatUint(v.Index(i).Uint(), 10))
		} else {
			d.value(v.Index(i), depth+1)
		}
	}
	d.write("]")
}

func (d *dumper) structValue(v reflect.Value, depth int) {
	t := v.Type()
	d.write("{")
	for i := 0; i < v.NumField(); i++ {
		if i > 0 {
			d.write(" ")
		}
		d.write(t.Field(i).Name)
		d.write(":")
		field := v.Field(i)
		if field.Kind() == reflect.Interface && !field.IsNil() {
			field = field.Elem()
		}
		d.value(field, depth+1)
	}
	d.write("}")
}

// compareKeys orders map keys like fmt for the common key kinds. Other
// kinds compare equal and keep map order.
func compareKeys(a, b reflect.Value) int {
	if a.Kind() == reflect.Interface && b.Kind() == reflect.Interface {
		switch {
		case a.IsNil() || b.IsNil():
			return cmp.Compare(boolInt(!a.IsNil()), boolInt(!b.IsNil()))
		case a.Elem().Type() != b.Elem().Type():
			return cmp.Compare(a.Elem().Type().String(), b.Elem().Type().String())
		}
		return compareKeys(a.Elem(), b.Elem())
	}

	switch a.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return cmp.Compare(a.Int(), b.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return cmp.Compare(a.Uint(), b.Uint())
	case reflect.String:
		return cmp.Compare(a.String(), b.String())
	case reflect.Float32, reflect.Float64:
		return cmp.Compare(a.Float(), b.Float())
	case reflect.Bool:
		return cmp.Compare(boolInt(a.Bool()), boolInt(b.Bool()))
	case reflect.Pointer, reflect.Chan, reflect.UnsafePointer:
		return cmp.Compare(a.Pointer(), b.Pointer())
	}
	return 0
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

var (
	errorType     = reflect.TypeOf((*error)(nil)).Elem()
	stringerType  = reflect.TypeOf((*fmt.Stringer)(nil)).Elem()
	formatterType = reflect.TypeOf((*fmt.Formatter)(nil)).Elem()
)

// hasPrintMethod reports whether %+v prints a value of type t through its
// Format, Error or String method.
func hasPrintMethod(t reflect.Type) bool {
	return t.Implements(formatterType) || t.Implements(errorType) || t.Implements(stringerType)
}

// method prints x through its Format, Error or String method, as fmt's
// handleMethods does for %+v, but without fmt: fmt formats a panic value
// with no limits (a panic value that contains itself overflows the stack),
// and Format would write into an unbounded buffer. Here Format writes into
// the capped buffer, and a panic becomes a fixed note.
func (d *dumper) method(x interface{}) {
	// fmt prints "<nil>" when the receiver is a nil pointer and the method
	// panics; do the same without calling it.
	if rv := reflect.ValueOf(x); rv.Kind() == reflect.Pointer && rv.IsNil() {
		if !safeCall(func() { d.callMethod(x) }) {
			d.write("<nil>")
		}
		d.stopIfFull()
		return
	}

	kind := "String"
	switch x.(type) {
	case fmt.Formatter:
		kind = "Format"
	case error:
		kind = "Error"
	}

	var payload interface{}
	if !safeCallPayload(func() { d.callMethod(x) }, &payload) {
		// Written piece by piece through the cap: the payload may be large.
		d.stopIfFull()
		d.write("%!v(PANIC=" + kind + " method: ")
		if s, isString := payload.(string); isString {
			d.write(s)
		} else {
			d.write(fmt.Sprintf("%T", payload))
		}
		d.write(")")
	}
	d.stopIfFull()
}

// callMethod runs the print method of x, writing its output.
func (d *dumper) callMethod(x interface{}) {
	switch m := x.(type) {
	case fmt.Formatter:
		m.Format(dumpState{d}, 'v')
	case error:
		d.writeQuiet(m.Error())
	case fmt.Stringer:
		d.writeQuiet(m.String())
	}
}

// writeQuiet appends what fits of s without unwinding, so it is safe to call
// from user code that might recover panics. stopIfFull unwinds afterwards.
func (d *dumper) writeQuiet(s string) {
	if d.full {
		return
	}
	if room := d.limit - len(d.buf); len(s) > room {
		d.buf = append(d.buf, s[:room]...)
		d.full = true
		return
	}
	d.buf = append(d.buf, s...)
}

// writeQuietBytes is writeQuiet for a byte slice, without copying it to a
// string first.
func (d *dumper) writeQuietBytes(b []byte) {
	if d.full {
		return
	}
	if room := d.limit - len(d.buf); len(b) > room {
		d.buf = append(d.buf, b[:room]...)
		d.full = true
		return
	}
	d.buf = append(d.buf, b...)
}

// stopIfFull unwinds the dump once a method has filled the buffer.
func (d *dumper) stopIfFull() {
	if d.full {
		panic(dumpFull{})
	}
}

// dumpState is the fmt.State a Format method writes to, as for %+v.
type dumpState struct{ d *dumper }

func (s dumpState) Write(b []byte) (int, error) {
	s.d.writeQuietBytes(b)
	return len(b), nil
}

func (s dumpState) Width() (int, bool)     { return 0, false }
func (s dumpState) Precision() (int, bool) { return 0, false }
func (s dumpState) Flag(c int) bool        { return c == '+' }

// safeCall runs f and reports whether it returned without panicking.
func safeCall(f func()) bool {
	var payload interface{}
	return safeCallPayload(f, &payload)
}

// safeCallPayload runs f; on a panic it stores the panic value in payload
// and returns false. The value is not formatted here.
func safeCallPayload(f func(), payload *interface{}) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			*payload = r
			ok = false
		}
	}()
	f()
	return true
}
