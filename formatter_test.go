package ulog

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestNewJSONFormatter(t *testing.T) {
	formatter := NewJSONFormatter()
	if formatter == nil {
		t.Error("Expected NewJSONFormatter to return a non-nil formatter")
	}

	if formatter.TimeFormat != defaultTimeFormat {
		t.Errorf("Expected default time format %s, got %s", defaultTimeFormat, formatter.TimeFormat)
	}
}

func TestNewTextFormatter(t *testing.T) {
	formatter := NewTextFormatter()
	if formatter == nil {
		t.Error("Expected NewTextFormatter to return a non-nil formatter")
	}

	if formatter.TimeFormat != defaultTimeFormat {
		t.Errorf("Expected default time format %s, got %s", defaultTimeFormat, formatter.TimeFormat)
	}
}

func TestJSONFormatterFormat(t *testing.T) {
	formatter := NewJSONFormatter()
	now := time.Now()

	tests := []struct {
		name     string
		record   LogRecord
		validate func(t *testing.T, output []byte)
	}{
		{
			name: "basic log record",
			record: LogRecord{
				Message:   "test message",
				Channel:   "test",
				Level:     200,
				LevelName: "INFO",
				DateTime:  now,
				Context:   map[string]interface{}{"key": "value"},
				Extra:     map[string]interface{}{"extra": "data"},
			},
			validate: func(t *testing.T, output []byte) {
				// Should be valid JSON
				var parsed map[string]interface{}
				if err := json.Unmarshal(output, &parsed); err != nil {
					t.Errorf("Output is not valid JSON: %v", err)
				}

				// Check required fields
				if parsed["message"] != "test message" {
					t.Errorf("Expected message 'test message', got %v", parsed["message"])
				}

				if parsed["channel"] != "test" {
					t.Errorf("Expected channel 'test', got %v", parsed["channel"])
				}

				if parsed["level"] != float64(200) {
					t.Errorf("Expected level 200, got %v", parsed["level"])
				}

				if parsed["level_name"] != "INFO" {
					t.Errorf("Expected level_name 'INFO', got %v", parsed["level_name"])
				}

				// Check datetime format
				if _, ok := parsed["datetime"].(string); !ok {
					t.Error("Expected datetime to be a string")
				}

				// Check context
				if context, ok := parsed["context"].(map[string]interface{}); ok {
					if context["key"] != "value" {
						t.Errorf("Expected context key 'value', got %v", context["key"])
					}
				} else {
					t.Error("Expected context to be a map")
				}

				// Check extra
				if extra, ok := parsed["extra"].(map[string]interface{}); ok {
					if extra["extra"] != "data" {
						t.Errorf("Expected extra 'data', got %v", extra["extra"])
					}
				} else {
					t.Error("Expected extra to be a map")
				}

				// Should end with newline
				if !strings.HasSuffix(string(output), "\n") {
					t.Error("Expected output to end with newline")
				}
			},
		},
		{
			name: "empty context and extra",
			record: LogRecord{
				Message:   "test message",
				Channel:   "test",
				Level:     200,
				LevelName: "INFO",
				DateTime:  now,
				Context:   map[string]interface{}{},
				Extra:     map[string]interface{}{},
			},
			validate: func(t *testing.T, output []byte) {
				var parsed map[string]interface{}
				if err := json.Unmarshal(output, &parsed); err != nil {
					t.Errorf("Output is not valid JSON: %v", err)
				}

				// Empty maps should still be present
				if _, ok := parsed["context"]; !ok {
					t.Error("Expected context field to be present")
				}

				if _, ok := parsed["extra"]; !ok {
					t.Error("Expected extra field to be present")
				}
			},
		},
		{
			name: "nil context and extra",
			record: LogRecord{
				Message:   "test message",
				Channel:   "test",
				Level:     200,
				LevelName: "INFO",
				DateTime:  now,
				Context:   nil,
				Extra:     nil,
			},
			validate: func(t *testing.T, output []byte) {
				var parsed map[string]interface{}
				if err := json.Unmarshal(output, &parsed); err != nil {
					t.Errorf("Output is not valid JSON: %v", err)
				}

				// Nil maps should be present as null
				if parsed["context"] != nil {
					t.Errorf("Expected context to be null, got %v", parsed["context"])
				}

				if parsed["extra"] != nil {
					t.Errorf("Expected extra to be null, got %v", parsed["extra"])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := formatter.Format(tt.record)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			tt.validate(t, output)
		})
	}
}

func TestJSONFormatterCustomTimeFormat(t *testing.T) {
	formatter := &JSONFormatter{
		TimeFormat: time.RFC822,
	}

	now := time.Now()
	record := LogRecord{
		Message:   "test message",
		Channel:   "test",
		Level:     200,
		LevelName: "INFO",
		DateTime:  now,
		Context:   map[string]interface{}{},
		Extra:     map[string]interface{}{},
	}

	output, err := formatter.Format(record)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(output, &parsed); err != nil {
		t.Errorf("Output is not valid JSON: %v", err)
	}

	expectedTime := now.Format(time.RFC822)
	if parsed["datetime"] != expectedTime {
		t.Errorf("Expected datetime %s, got %v", expectedTime, parsed["datetime"])
	}
}

func TestTextFormatterFormat(t *testing.T) {
	formatter := NewTextFormatter()
	now := time.Now()

	tests := []struct {
		name     string
		record   LogRecord
		validate func(t *testing.T, output []byte)
	}{
		{
			name: "basic log record",
			record: LogRecord{
				Message:   "test message",
				Channel:   "test",
				Level:     200,
				LevelName: "INFO",
				DateTime:  now,
				Context:   map[string]interface{}{"key": "value"},
				Extra:     map[string]interface{}{"extra": "data"},
			},
			validate: func(t *testing.T, output []byte) {
				outputStr := string(output)

				// Should contain basic elements
				if !strings.Contains(outputStr, "test message") {
					t.Error("Expected output to contain 'test message'")
				}

				if !strings.Contains(outputStr, "test.INFO") {
					t.Error("Expected output to contain 'test.INFO'")
				}

				if !strings.Contains(outputStr, now.Format(defaultTimeFormat)) {
					t.Error("Expected output to contain formatted datetime")
				}

				// Should contain context JSON
				if !strings.Contains(outputStr, `"key":"value"`) {
					t.Error("Expected output to contain context JSON")
				}

				// Should contain extra JSON
				if !strings.Contains(outputStr, `"extra":"data"`) {
					t.Error("Expected output to contain extra JSON")
				}

				// Should end with newline
				if !strings.HasSuffix(outputStr, "\n") {
					t.Error("Expected output to end with newline")
				}
			},
		},
		{
			name: "empty context and extra",
			record: LogRecord{
				Message:   "test message",
				Channel:   "test",
				Level:     200,
				LevelName: "INFO",
				DateTime:  now,
				Context:   map[string]interface{}{},
				Extra:     map[string]interface{}{},
			},
			validate: func(t *testing.T, output []byte) {
				outputStr := string(output)

				// Should contain empty brackets for empty maps
				if !strings.Contains(outputStr, " [] []") {
					t.Error("Expected output to contain ' [] []' for empty context and extra")
				}
			},
		},
		{
			name: "nil context and extra",
			record: LogRecord{
				Message:   "test message",
				Channel:   "test",
				Level:     200,
				LevelName: "INFO",
				DateTime:  now,
				Context:   nil,
				Extra:     nil,
			},
			validate: func(t *testing.T, output []byte) {
				outputStr := string(output)

				// Should contain empty brackets for nil maps
				if !strings.Contains(outputStr, " [] []") {
					t.Error("Expected output to contain ' [] []' for nil context and extra")
				}
			},
		},
		{
			name: "context with marshal error (circular reference)",
			record: LogRecord{
				Message:   "test message",
				Channel:   "test",
				Level:     200,
				LevelName: "INFO",
				DateTime:  now,
				Context:   createCircularReference(),
				Extra:     map[string]interface{}{},
			},
			validate: func(t *testing.T, output []byte) {
				outputStr := string(output)

				// Should still contain the message and basic info
				if !strings.Contains(outputStr, "test message") {
					t.Error("Expected output to contain 'test message'")
				}

				// When JSON marshal fails, the formatter should still produce output
				// The exact format may vary, but it should contain the basic message
				if len(outputStr) == 0 {
					t.Error("Expected non-empty output even when context marshal fails")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := formatter.Format(tt.record)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			tt.validate(t, output)
		})
	}
}

func TestTextFormatterCustomTimeFormat(t *testing.T) {
	formatter := &TextFormatter{
		TimeFormat: time.RFC822,
	}

	now := time.Now()
	record := LogRecord{
		Message:   "test message",
		Channel:   "test",
		Level:     200,
		LevelName: "INFO",
		DateTime:  now,
		Context:   map[string]interface{}{},
		Extra:     map[string]interface{}{},
	}

	output, err := formatter.Format(record)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	outputStr := string(output)
	expectedTime := now.Format(time.RFC822)
	if !strings.Contains(outputStr, expectedTime) {
		t.Errorf("Expected output to contain time %s, got: %s", expectedTime, outputStr)
	}
}

func TestDefaultTimeFormat(t *testing.T) {
	if defaultTimeFormat != time.RFC3339Nano {
		t.Errorf("Expected default time format to be RFC3339Nano, got %s", defaultTimeFormat)
	}
}

func TestFormatterInterface(t *testing.T) {
	// Test that both formatters implement the Formatter interface
	var _ Formatter = &JSONFormatter{}
	var _ Formatter = &TextFormatter{}
}

// Helper function to create a circular reference that will cause JSON marshal to fail
func createCircularReference() map[string]interface{} {
	m := make(map[string]interface{})
	m["self"] = m // This creates a circular reference
	return m
}

// panicky fails both JSON encoding and printing by panicking.
type panicky struct{}

func (panicky) MarshalJSON() ([]byte, error) { panic("MarshalJSON boom") }
func (panicky) String() string               { panic("String boom") }

// cyclicStruct refers to itself through a map, which fmt would print forever.
type cyclicStruct struct {
	Ch   chan int
	Next map[string]interface{}
}

// badRecord holds values JSON cannot encode next to normal ones.
func badRecord() LogRecord {
	cyclic := cyclicStruct{Ch: make(chan int), Next: map[string]interface{}{}}
	cyclic.Next["back"] = cyclic

	selfSlice := make([]interface{}, 1)
	selfSlice[0] = selfSlice

	return LogRecord{
		Message:   "bad values",
		Channel:   "test",
		Level:     200,
		LevelName: "INFO",
		DateTime:  time.Now(),
		Context: map[string]interface{}{
			"ok":        "value",
			"n":         42,
			"ch":        make(chan int),
			"fn":        func() {},
			"nan":       math.NaN(),
			"cycle":     createCircularReference(),
			"panicky":   panicky{},
			"cyclic":    cyclic,
			"selfSlice": selfSlice,
			"group":     map[string]interface{}{"inner": "kept", "bad": func() {}},
		},
		Extra: map[string]interface{}{
			"e":   "x",
			"bad": make(chan string),
		},
	}
}

// checkBadValues asserts the encoded context and extra of badRecord.
func checkBadValues(t *testing.T, context, extra map[string]interface{}) {
	t.Helper()

	if context["ok"] != "value" {
		t.Errorf("context.ok = %v, want value", context["ok"])
	}
	if context["n"] != float64(42) {
		t.Errorf("context.n = %v, want 42", context["n"])
	}
	if context["nan"] != "NaN" {
		t.Errorf("context.nan = %v, want \"NaN\"", context["nan"])
	}
	for _, key := range []string{"ch", "fn", "panicky", "cyclic", "selfSlice"} {
		if s, ok := context[key].(string); !ok || s == "" {
			t.Errorf("context.%s = %#v, want a non-empty string", key, context[key])
		}
	}

	if cycle, ok := context["cycle"].(map[string]interface{}); !ok {
		t.Errorf("context.cycle = %#v, want an object", context["cycle"])
	} else if s, ok := cycle["self"].(string); !ok || !strings.Contains(s, "cyclic") {
		t.Errorf("context.cycle.self = %#v, want a cyclic marker", cycle["self"])
	}

	if group, ok := context["group"].(map[string]interface{}); !ok {
		t.Errorf("context.group = %#v, want an object", context["group"])
	} else {
		if group["inner"] != "kept" {
			t.Errorf("context.group.inner = %v, want kept", group["inner"])
		}
		if _, ok := group["bad"].(string); !ok {
			t.Errorf("context.group.bad = %#v, want a string", group["bad"])
		}
	}

	if extra["e"] != "x" {
		t.Errorf("extra.e = %v, want x", extra["e"])
	}
	if _, ok := extra["bad"].(string); !ok {
		t.Errorf("extra.bad = %#v, want a string", extra["bad"])
	}
}

func TestJSONFormatterDegradesPerValue(t *testing.T) {
	output, err := NewJSONFormatter().Format(badRecord())
	if err != nil {
		t.Fatalf("Format: %v", err)
	}

	var parsed struct {
		Message string                 `json:"message"`
		Context map[string]interface{} `json:"context"`
		Extra   map[string]interface{} `json:"extra"`
	}
	if err := json.Unmarshal(output, &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, output)
	}
	if parsed.Message != "bad values" {
		t.Errorf("message = %q, want %q", parsed.Message, "bad values")
	}
	checkBadValues(t, parsed.Context, parsed.Extra)
}

func TestTextFormatterDegradesPerValue(t *testing.T) {
	output, err := NewTextFormatter().Format(badRecord())
	if err != nil {
		t.Fatalf("Format: %v", err)
	}

	line := strings.TrimSuffix(string(output), "\n")
	_, rest, found := strings.Cut(line, ": bad values ")
	if !found {
		t.Fatalf("line lacks the message: %s", line)
	}

	// rest is "<context JSON> <extra JSON>"; decode both in sequence.
	dec := json.NewDecoder(strings.NewReader(rest))
	var context, extra map[string]interface{}
	if err := dec.Decode(&context); err != nil {
		t.Fatalf("context block is not JSON: %v\n%s", err, line)
	}
	if err := dec.Decode(&extra); err != nil {
		t.Fatalf("extra block is not JSON: %v\n%s", err, line)
	}
	checkBadValues(t, context, extra)
}

func TestDescribeIsSafe(t *testing.T) {
	deep := map[string]interface{}{}
	m := deep
	for i := 0; i < 100; i++ {
		next := map[string]interface{}{}
		m["next"] = next
		m = next
	}

	shared := map[string]interface{}{"a": 1}
	type node struct {
		Name   string
		Parent *node
	}
	parent := &node{Name: "parent"}
	parent.Parent = parent

	tests := []struct {
		name  string
		value interface{}
		want  string // substring; "" = any non-empty string
	}{
		{"chan", make(chan int), ""},
		{"panicky", panicky{}, "PANIC"},
		{"self map", createCircularReference(), "cyclic"},
		{"too deep", deep, "too deep"},
		{"shared, not cyclic", []interface{}{shared, shared}, "map[a:1]"},
		{"pointer cycle", parent, "parent"},
		{"bytes", []byte("hi"), "[104 105]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := describe(tt.value)
			if got == "" || !strings.Contains(got, tt.want) {
				t.Errorf("describe = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}
