package ulog

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// TestHandlerSerializesWrites logs from many goroutines through a handler
// and handlers derived from it, all onto one bytes.Buffer, which is not safe
// for concurrent use. Run with -race; every line must come out whole.
func TestHandlerSerializesWrites(t *testing.T) {
	var buf bytes.Buffer
	base := NewHandler(Config{Formatter: NewJSONFormatter(), Writer: &buf, Channel: "test", Level: slog.LevelInfo})
	loggers := []*slog.Logger{
		slog.New(base),
		slog.New(base.WithAttrs([]slog.Attr{slog.String("a", "1")})),
		slog.New(base.WithGroup("g")),
		slog.New(base.WithGroup("g").WithAttrs([]slog.Attr{slog.Int("b", 2)})),
	}

	const perLogger = 200
	var wg sync.WaitGroup
	for _, logger := range loggers {
		for g := 0; g < 2; g++ {
			wg.Add(1)
			go func(logger *slog.Logger) {
				defer wg.Done()
				for i := 0; i < perLogger; i++ {
					logger.Info("concurrent", "i", i)
				}
			}(logger)
		}
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if want := len(loggers) * 2 * perLogger; len(lines) != want {
		t.Fatalf("got %d lines, want %d", len(lines), want)
	}
	for _, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Fatalf("interleaved or broken line: %q", line)
		}
	}
}

// TestMultiWriterAddWriterDuringWrites adds writers while others write.
func TestMultiWriterAddWriterDuringWrites(t *testing.T) {
	var first bytes.Buffer
	mw := NewMultiWriter(&first)

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if _, err := mw.Write([]byte("x\n")); err != nil {
					t.Errorf("Write: %v", err)
					return
				}
			}
		}()
	}
	added := make([]*bytes.Buffer, 10)
	for i := range added {
		added[i] = &bytes.Buffer{}
		mw.AddWriter(added[i])
	}
	wg.Wait()

	if got := strings.Count(first.String(), "x\n"); got != 400 {
		t.Errorf("first writer got %d lines, want 400", got)
	}
}

// TestSiblingHandlersKeepOwnAttrs: two handlers derived from one parent must
// not share attribute or group storage.
func TestSiblingHandlersKeepOwnAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := NewHandler(Config{Formatter: NewJSONFormatter(), Writer: &buf, Channel: "test", Level: slog.LevelInfo})

	// Two derivations leave spare capacity in the parent's attrs.
	parent := h.WithAttrs([]slog.Attr{slog.Int("a", 1), slog.Int("b", 2), slog.Int("c", 3)}).
		WithAttrs([]slog.Attr{slog.Int("d", 4)})
	y := slog.New(parent.WithAttrs([]slog.Attr{slog.String("child", "y")}))
	_ = parent.WithAttrs([]slog.Attr{slog.String("child", "z")})

	groupParent := h.WithGroup("g1").WithGroup("g2").WithGroup("g3")
	gy := slog.New(groupParent.WithGroup("y"))
	_ = groupParent.WithGroup("z")

	y.Info("attrs")
	gy.Info("groups", "k", "v")

	out := buf.String()
	if !strings.Contains(out, `"child":"y"`) || strings.Contains(out, `"child":"z"`) {
		t.Errorf("sibling attrs leaked: %s", out)
	}
	if !strings.Contains(out, `"g1.g2.g3.y.k":"v"`) {
		t.Errorf("sibling groups leaked: %s", out)
	}
}
