package ulog

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// logOne logs one record through a JSON handler and returns its context.
func logOne(t *testing.T, log func(*slog.Logger)) map[string]interface{} {
	t.Helper()
	var buf bytes.Buffer
	log(slog.New(NewHandler(Config{Formatter: NewJSONFormatter(), Writer: &buf, Channel: "test", Level: slog.LevelInfo})))

	var parsed struct {
		Context map[string]interface{} `json:"context"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("output is not one JSON record: %v\n%s", err, buf.String())
	}
	return parsed.Context
}

type panickingStringer struct{}

func (panickingStringer) String() string { panic("String boom") }

type panickingValuer struct{}

func (panickingValuer) LogValue() slog.Value { panic("LogValue boom") }

type groupValuer struct{}

func (groupValuer) LogValue() slog.Value {
	return slog.GroupValue(slog.String("id", "1"), slog.Int("age", 30))
}

// TestHandlerSurvivesPanickingMethods: a value whose Error, String or
// LogValue panics must not make the log call panic, and the record keeps
// its other attributes.
func TestHandlerSurvivesPanickingMethods(t *testing.T) {
	ctx := logOne(t, func(l *slog.Logger) {
		l.Info("m",
			"typedNil", (*os.PathError)(nil),
			"stringer", panickingStringer{},
			"valuer", panickingValuer{},
			"ok", "kept",
		)
	})

	if ctx["typedNil"] != "<nil>" {
		t.Errorf("typedNil = %#v, want <nil>", ctx["typedNil"])
	}
	if want := "%!v(PANIC=String method: String boom)"; ctx["stringer"] != want {
		t.Errorf("stringer = %#v, want %q", ctx["stringer"], want)
	}
	if s, _ := ctx["valuer"].(string); !strings.Contains(s, "LogValue panicked") {
		t.Errorf("valuer = %#v, want slog's LogValue panicked error", ctx["valuer"])
	}
	if ctx["ok"] != "kept" {
		t.Errorf("ok = %#v, want kept", ctx["ok"])
	}
}

// TestHandlerResolvesGroupValuers: a LogValuer that returns a group is
// written as that group, not as a list of raw slog.Attr structs.
func TestHandlerResolvesGroupValuers(t *testing.T) {
	ctx := logOne(t, func(l *slog.Logger) { l.Info("m", "user", groupValuer{}) })

	if ctx["user.id"] != "1" || ctx["user.age"] != float64(30) {
		t.Errorf("context = %v, want user.id=1 and user.age=30", ctx)
	}
}

// TestHandlerNestedGroupKeys: a group attribute inside WithGroup gets each
// group name once.
func TestHandlerNestedGroupKeys(t *testing.T) {
	ctx := logOne(t, func(l *slog.Logger) {
		l.WithGroup("g").Info("m", slog.Group("req", "id", 1, slog.Group("inner", "x", 2)))
	})

	if ctx["g.req.id"] != float64(1) || ctx["g.req.inner.x"] != float64(2) {
		t.Errorf("context = %v, want g.req.id=1 and g.req.inner.x=2", ctx)
	}
}

// selfGroup returns a group holding itself: endless if expanded naively.
type selfGroup struct{}

func (s selfGroup) LogValue() slog.Value { return slog.GroupValue(slog.Any("next", s)) }

// inlineSelfGroup does the same through a group with no key, which adds no
// key prefix.
type inlineSelfGroup struct{}

func (s inlineSelfGroup) LogValue() slog.Value {
	return slog.GroupValue(slog.Int("n", 1), slog.Any("", s))
}

// branchingGroup references itself twice: 2^depth attributes if expanded.
type branchingGroup struct{}

func (s branchingGroup) LogValue() slog.Value {
	return slog.GroupValue(slog.Any("a", s), slog.Any("b", s))
}

// TestHandlerBoundsSelfReferencingValuers: values whose LogValue returns a
// group containing themselves must finish, with a marker, and keep the
// record's other attributes.
func TestHandlerBoundsSelfReferencingValuers(t *testing.T) {
	tests := []struct {
		name   string
		value  slog.LogValuer
		marker string
	}{
		{"self", selfGroup{}, "[max group depth 64]"},
		{"inline self", inlineSelfGroup{}, "[max group depth 64]"},
		{"branching", branchingGroup{}, "[attribute limit reached: 100000 per record]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := logOne(t, func(l *slog.Logger) { l.Info("m", "ok", "kept", "v", tt.value) })

			found := false
			for _, v := range ctx {
				if v == tt.marker {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("no %q among %d context entries", tt.marker, len(ctx))
			}
			if ctx["ok"] != "kept" {
				t.Errorf("ok = %#v, want kept", ctx["ok"])
			}
		})
	}
}
