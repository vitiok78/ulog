package ulog

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
		{"branching", branchingGroup{}, attrLimitMarker},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The runaway value comes first: everything after it, in the
			// record, on the logger and in the request context, must stay.
			var buf bytes.Buffer
			h := NewHandler(Config{Formatter: NewJSONFormatter(), Writer: &buf, Channel: "test", CtxKeyName: "ctx", Level: slog.LevelInfo})
			logger := slog.New(h).With("service", "api")
			reqCtx := WithAttrsContext(context.Background(), slog.String("request_id", "req-1"))
			logger.InfoContext(reqCtx, "m", "v", tt.value, "ok", "kept")

			var parsed struct {
				Context map[string]interface{} `json:"context"`
			}
			if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
				t.Fatalf("output is not one JSON record: %v", err)
			}
			got := parsed.Context

			found := false
			for _, v := range got {
				if v == tt.marker {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("no %q among %d context entries", tt.marker, len(got))
			}
			if got["ok"] != "kept" {
				t.Errorf("ok = %#v, want kept", got["ok"])
			}
			if got["service"] != "api" {
				t.Errorf("service = %#v, want api", got["service"])
			}
			if reqAttrs, _ := got["ctx"].(map[string]interface{}); reqAttrs["request_id"] != "req-1" {
				t.Errorf("ctx = %#v, want request_id req-1", got["ctx"])
			}
		})
	}
}

// wideSelf returns one prebuilt group of 100 000 references to itself.
type wideSelf struct{ calls *atomic.Int64 }

var wideSelfGroup slog.Value

func (s wideSelf) LogValue() slog.Value {
	s.calls.Add(1)
	return wideSelfGroup
}

// TestHandlerStopsAtExhaustedBudget: once an attribute's budget is spent,
// the remaining siblings at every level are not visited.
func TestHandlerStopsAtExhaustedBudget(t *testing.T) {
	calls := &atomic.Int64{}
	self := wideSelf{calls: calls}
	attrs := make([]slog.Attr, 100_000)
	for i := range attrs {
		attrs[i] = slog.Any("x", self)
	}
	wideSelfGroup = slog.GroupValue(attrs...)
	t.Cleanup(func() { wideSelfGroup = slog.Value{} })

	// Visits after exhaustion each built a key: millions of strings.
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	ctx := logOne(t, func(l *slog.Logger) { l.Info("m", "v", self, "ok", "kept") })
	runtime.ReadMemStats(&after)

	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 64<<20 {
		t.Errorf("logging allocated %d MiB", allocated>>20)
	}
	if n := calls.Load(); n > maxAttrExpansion {
		t.Errorf("LogValue called %d times, want at most the budget %d", n, maxAttrExpansion)
	}
	if ctx["ok"] != "kept" {
		t.Errorf("ok = %#v, want kept", ctx["ok"])
	}
}

// longKeyCycle references itself twice under 256-byte names: under the
// depth and attribute limits, but the flattened keys alone grow to about
// a gigabyte.
type longKeyCycle struct{}

var (
	longKeyA = strings.Repeat("a", 256)
	longKeyB = strings.Repeat("b", 256)
)

func (s longKeyCycle) LogValue() slog.Value {
	return slog.GroupValue(slog.Any(longKeyA, s), slog.Any(longKeyB, s))
}

func TestHandlerBoundsKeyBytes(t *testing.T) {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	ctx := logOne(t, func(l *slog.Logger) { l.Info("m", "v", longKeyCycle{}, "ok", "kept") })
	runtime.ReadMemStats(&after)

	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 128<<20 {
		t.Errorf("logging allocated %d MiB", allocated>>20)
	}
	found := false
	for k, v := range ctx {
		if v == attrLimitMarker {
			found = true
			if len(k) > maxMarkerKeyBytes {
				t.Errorf("marker key is %d bytes, want at most %d", len(k), maxMarkerKeyBytes)
			}
		}
	}
	if !found {
		t.Errorf("no limit marker among %d entries", len(ctx))
	}
	if ctx["ok"] != "kept" {
		t.Errorf("ok = %#v, want kept", ctx["ok"])
	}
}

// sharedLeafTree branches into two children for level levels; every leaf
// is the same 64 KiB string. 2^15 leaves: 2 GiB of values from 64 KiB.
type sharedLeafTree struct{ level int }

var sharedLeaf = strings.Repeat("x", 64<<10)

func (s sharedLeafTree) LogValue() slog.Value {
	if s.level == 0 {
		return slog.StringValue(sharedLeaf)
	}
	return slog.GroupValue(slog.Any("l", sharedLeafTree{s.level - 1}), slog.Any("r", sharedLeafTree{s.level - 1}))
}

func TestHandlerBoundsSharedLeafPayload(t *testing.T) {
	for name, f := range map[string]Formatter{"json": NewJSONFormatter(), "text": NewTextFormatter()} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			l := slog.New(NewHandler(Config{Formatter: f, Writer: &buf, Channel: "test", Level: slog.LevelInfo}))

			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			l.Info("m", "v", sharedLeafTree{15}, "ok", "kept")
			runtime.ReadMemStats(&after)

			if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 256<<20 {
				t.Errorf("logging allocated %d MiB", allocated>>20)
			}
			out := buf.String()
			if !strings.Contains(out, attrLimitMarker) {
				t.Errorf("no limit marker in %d bytes of output", len(out))
			}
			if !strings.Contains(out, `"ok":"kept"`) {
				t.Error("ok attribute lost")
			}
		})
	}
}

// badTimeTree is sharedLeafTree with a time that JSON rejects (year 10000)
// in a zone with a 64 KiB name: its fallback text is long.
type badTimeTree struct{ level int }

var badTime = time.Date(10000, 1, 1, 0, 0, 0, 0, time.FixedZone(strings.Repeat("z", 64<<10), 0))

func (s badTimeTree) LogValue() slog.Value {
	if s.level == 0 {
		return slog.TimeValue(badTime)
	}
	return slog.GroupValue(slog.Any("l", badTimeTree{s.level - 1}), slog.Any("r", badTimeTree{s.level - 1}))
}

// TestHandlerMeasuresLeavesLikeFormatters: payload measurement must not
// undercount (invalid times) or expand what the formatter bounds (shared
// maps).
func TestHandlerMeasuresLeavesLikeFormatters(t *testing.T) {
	// 30 maps, each referencing the one below twice: 2^30 entries if
	// encoded naively.
	dag := map[string]interface{}{"leaf": 1}
	for i := 0; i < 30; i++ {
		dag = map[string]interface{}{"a": dag, "b": dag}
	}

	cases := map[string]func(*slog.Logger){
		"bad times": func(l *slog.Logger) { l.Info("m", "v", badTimeTree{15}, "ok", "kept") },
		"map dag":   func(l *slog.Logger) { l.Info("m", slog.Group("g", "dag", dag), "ok", "kept") },
	}
	for name, logIt := range cases {
		for fname, f := range map[string]Formatter{"json": NewJSONFormatter(), "text": NewTextFormatter()} {
			t.Run(name+"/"+fname, func(t *testing.T) {
				var buf bytes.Buffer
				l := slog.New(NewHandler(Config{Formatter: f, Writer: &buf, Channel: "test", Level: slog.LevelInfo}))

				var before, after runtime.MemStats
				runtime.GC()
				runtime.ReadMemStats(&before)
				logIt(l)
				runtime.ReadMemStats(&after)

				// Bounded by the 16 MiB payload budget times the per-leaf
				// work (measure, then format); unbounded it is gigabytes.
				if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 512<<20 {
					t.Errorf("logging allocated %d MiB", allocated>>20)
				}
				if !strings.Contains(buf.String(), `"ok":"kept"`) {
					t.Error("ok attribute lost")
				}
			})
		}
	}
}
