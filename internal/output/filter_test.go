package output

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// TestFilterEmission pins the shape of what a filter writes: strings raw
// (jq's -r, which is what makes `--jq '.a[].key' | xargs` work), one
// result per line, everything else JSON-encoded.
func TestFilterEmission(t *testing.T) {
	t.Parallel()
	doc := map[string]any{
		"name":  "PROJ-1",
		"tags":  []any{"a", "b"},
		"count": 2,
		"nested": map[string]any{
			"ok": true,
		},
	}
	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "string emits raw, unquoted", expr: ".name", want: "PROJ-1\n"},
		{name: "several results are newline-delimited", expr: ".tags[]", want: "a\nb\n"},
		{name: "an array stays one json line", expr: ".tags", want: "[\"a\",\"b\"]\n"},
		{name: "number encodes as json", expr: ".count", want: "2\n"},
		{name: "object encodes as json", expr: ".nested", want: "{\"ok\":true}\n"},
		{name: "a missing field is null, not a failure", expr: ".nope", want: "null\n"},
		{name: "empty writes nothing at all", expr: "empty", want: ""},
		{name: "bare halt stops without failing", expr: "halt", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, err := CompileFilter(tt.expr)
			if err != nil {
				t.Fatalf("compile %q: %v", tt.expr, err)
			}
			var buf bytes.Buffer
			if err := f.apply(t.Context(), &buf, doc); err != nil {
				t.Fatalf("apply: %v", err)
			}
			if buf.String() != tt.want {
				t.Errorf("output = %q, want %q", buf.String(), tt.want)
			}
		})
	}
}

// TestFilterByteFidelity pins that a filter does not undo the escaping
// discipline the JSON renderer works to keep. gojq.Marshal is used
// precisely because it leaves these bytes alone.
func TestFilterByteFidelity(t *testing.T) {
	t.Parallel()
	doc := map[string]any{"s": "a<b>c&d e"}
	f, err := CompileFilter(".s")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := f.apply(t.Context(), &buf, doc); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "a<b>c&d e\n" {
		t.Errorf("output = %q, want the bytes unescaped", got)
	}
}

// TestFilterFaultsAreDistinguishable pins the split the taxonomy depends
// on: a syntax fault and a shape fault get different codes because they
// have different fixes, so the output layer must mark them differently.
func TestFilterFaultsAreDistinguishable(t *testing.T) {
	t.Parallel()
	t.Run("unparseable expression", func(t *testing.T) {
		t.Parallel()
		if _, err := CompileFilter(".data.["); !errors.Is(err, ErrFilterInvalid) {
			t.Errorf("err = %v, want ErrFilterInvalid", err)
		}
	})
	t.Run("undefined function is also a compile fault", func(t *testing.T) {
		t.Parallel()
		if _, err := CompileFilter("nosuchfn(.)"); !errors.Is(err, ErrFilterInvalid) {
			t.Errorf("err = %v, want ErrFilterInvalid", err)
		}
	})
	t.Run("valid expression, wrong shape", func(t *testing.T) {
		t.Parallel()
		f, err := CompileFilter(".nope[]")
		if err != nil {
			t.Fatal(err)
		}
		err = f.apply(t.Context(), &bytes.Buffer{}, map[string]any{})
		if !errors.Is(err, ErrFilterFailed) {
			t.Errorf("err = %v, want ErrFilterFailed", err)
		}
	})
	t.Run("explicit error() is a shape fault", func(t *testing.T) {
		t.Parallel()
		f, err := CompileFilter(`error("boom")`)
		if err != nil {
			t.Fatal(err)
		}
		err = f.apply(t.Context(), &bytes.Buffer{}, map[string]any{})
		if !errors.Is(err, ErrFilterFailed) {
			t.Errorf("err = %v, want ErrFilterFailed", err)
		}
	})
}

// TestFilterWritesNothingWhenItFailsPartway is the reason results are
// buffered. An expression can emit several values and THEN fail; writing
// as it goes would leave valid lines on stdout ahead of an error frame,
// producing a document no consumer can parse.
func TestFilterWritesNothingWhenItFailsPartway(t *testing.T) {
	t.Parallel()
	f, err := CompileFilter(`.[] | if . == 3 then error("stop") else . end`)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := f.apply(t.Context(), &buf, []any{1, 2, 3}); !errors.Is(err, ErrFilterFailed) {
		t.Fatalf("err = %v, want ErrFilterFailed", err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %q before failing; a partial list ahead of an error frame is unparseable", buf.String())
	}
}

// TestFilterHonorsContextCancellation pins that a runaway expression is
// answered as a cancellation rather than as a bad filter — the taxonomy
// maps those to different codes, and blaming the expression for a
// deadline would send the reader to fix the wrong thing.
func TestFilterHonorsContextCancellation(t *testing.T) {
	t.Parallel()
	f, err := CompileFilter("range(1e9)")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = f.apply(ctx, &bytes.Buffer{}, map[string]any{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// TestFilterIsolationHoldsPerBuiltin pins each isolation property to the
// mechanism that actually provides it, rather than to a string absent
// from the output.
//
// The first version asserted only that successful output did not contain
// "PATH". That passes when isolation is genuinely broken: an environment
// loader exposing one unrelated variable, or an input iterator yielding
// a secret, leaves it green. Each case below states the mechanism —
// compile-time rejection, or an empty result — so a regression changes
// the observable behavior the test names.
func TestFilterIsolationHoldsPerBuiltin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		expr string
		// compileFails means gojq must REFUSE the program outright.
		compileFails bool
		// want is the exact output when it does compile.
		want string
	}{
		{name: "env is an empty object, not the process environment", expr: "env", want: "{}\n"},
		{name: "$ENV likewise", expr: "$ENV", want: "{}\n"},
		{name: "no named variable resolves out of $ENV", expr: "$ENV.PATH", want: "null\n"},
		{name: "env has no keys at all", expr: "env | keys | length", want: "0\n"},
		{name: "input is refused at compile time", expr: "input", compileFails: true},
		{name: "inputs is refused at compile time", expr: "inputs", compileFails: true},
		{name: "import is refused without a module loader", expr: `import "m" as m; .`, compileFails: true},
		{name: "include is refused without a module loader", expr: `include "m"; .`, compileFails: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, err := CompileFilter(tt.expr)
			if tt.compileFails {
				if err == nil {
					t.Fatalf("%q compiled; it must be refused without the matching gojq option", tt.expr)
				}
				if !errors.Is(err, ErrFilterInvalid) {
					t.Errorf("err = %v, want ErrFilterInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("compile %q: %v", tt.expr, err)
			}
			var buf bytes.Buffer
			if err := f.apply(t.Context(), &buf, map[string]any{}); err != nil {
				t.Fatalf("apply: %v", err)
			}
			if buf.String() != tt.want {
				t.Errorf("output = %q, want %q", buf.String(), tt.want)
			}
		})
	}
}

// TestFilterIsolationLimitIsDocumented pins the HONEST limit rather than
// an isolation claim the code cannot make. gojq's time builtins are
// unconditional, so a filter does observe the host clock and timezone.
// Nothing there reaches Jira data or credentials, but the comment on
// Filter says so explicitly and this keeps that admission true.
func TestFilterIsolationLimitIsDocumented(t *testing.T) {
	t.Parallel()
	f, err := CompileFilter("now | type")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := f.apply(t.Context(), &buf, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "number\n" {
		t.Errorf("now = %q; if this stopped working the Filter comment's stated limit is stale", buf.String())
	}
}

// TestFilterPreservesLargeIntegers pins numeric fidelity. Decoding into
// `any` with plain json.Unmarshal widens every number to float64, so
// 9007199254740993 silently became ...992 — in bound variables AND in
// every document a filter ran over. Jira issues large numeric ids, and a
// reporting tool that quietly alters an id is worse than one that
// refuses.
func TestFilterPreservesLargeIntegers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "beyond float64's exact integer range", raw: `{"n":9007199254740993}`, want: "9007199254740993\n"},
		{name: "far beyond int64", raw: `{"n":123456789012345678901234567890}`, want: "123456789012345678901234567890\n"},
		{name: "negative large", raw: `{"n":-9007199254740993}`, want: "-9007199254740993\n"},
		{name: "ordinary float is untouched", raw: `{"n":1.5}`, want: "1.5\n"},
		{name: "zero", raw: `{"n":0}`, want: "0\n"},
		{name: "nested inside a list", raw: `{"n":[9007199254740993]}`, want: "[9007199254740993]\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc, err := DecodeJSONNumbers([]byte(tt.raw))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			f, err := CompileFilter(".n")
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if err := f.apply(t.Context(), &buf, doc); err != nil {
				t.Fatalf("apply: %v", err)
			}
			if buf.String() != tt.want {
				t.Errorf("output = %q, want %q", buf.String(), tt.want)
			}
		})
	}
}

// TestFilterHaltErrorIsNotSuccess pins the difference between a bare
// `halt` (a clean stop) and `halt_error` (a deliberate failure). Both
// carry a nil value when the input is null, so checking only the value
// turned an explicit error into success at exit 0 with empty output —
// the fabricated-success shape, produced by the caller's own expression.
func TestFilterHaltErrorIsNotSuccess(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		expr    string
		wantErr bool
	}{
		{name: "bare halt stops cleanly", expr: "halt", wantErr: false},
		{name: "halt_error on null is still a failure", expr: "null | halt_error(7)", wantErr: true},
		{name: "halt_error with a message is a failure", expr: `"boom" | halt_error`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, err := CompileFilter(tt.expr)
			if err != nil {
				t.Fatal(err)
			}
			err = f.apply(t.Context(), &bytes.Buffer{}, map[string]any{})
			if tt.wantErr && !errors.Is(err, ErrFilterFailed) {
				t.Errorf("err = %v, want ErrFilterFailed — a deliberate halt_error must not read as success", err)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("err = %v, want nil", err)
			}
		})
	}
}

// TestFilterDeadlineBoundsSilentSpinners pins the bound the byte cap
// cannot provide. The expressions that cost most emit NOTHING to count:
// `repeat(.) | empty` spins forever, and `[repeat(.)]` grows inside gojq
// before a single value is yielded. Only a deadline reaches into gojq's
// evaluation loop.
func TestFilterDeadlineBoundsSilentSpinners(t *testing.T) {
	orig := maxFilterRuntime
	t.Cleanup(func() { maxFilterRuntime = orig })
	maxFilterRuntime = 150 * time.Millisecond

	for _, expr := range []string{"repeat(.) | empty", "[repeat(.)]"} {
		t.Run(expr, func(t *testing.T) {
			f, err := CompileFilter(expr)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- f.apply(t.Context(), &bytes.Buffer{}, map[string]any{"x": 1}) }()
			select {
			case err := <-done:
				if err == nil {
					t.Errorf("%q returned nil; an unbounded expression must fail, not succeed", expr)
				}
			case <-time.After(15 * time.Second):
				t.Fatalf("%q was not bounded by the deadline", expr)
			}
		})
	}
}

// shortWriter reports one byte fewer than it took, with a nil error —
// the exact shape that made a truncated result look successful.
type shortWriter struct{ n int }

func (w *shortWriter) Write(p []byte) (int, error) { w.n = len(p) - 1; return w.n, nil }

// TestFilterRejectsAShortWrite pins that a partial write is a failure.
func TestFilterRejectsAShortWrite(t *testing.T) {
	t.Parallel()
	f, err := CompileFilter(".a")
	if err != nil {
		t.Fatal(err)
	}
	err = f.apply(t.Context(), &shortWriter{}, map[string]any{"a": "value"})
	var werr *WriteError
	if !errors.As(err, &werr) {
		t.Fatalf("err = %v, want a *WriteError — a truncated result must not report success", err)
	}
	if !errors.Is(err, io.ErrShortWrite) {
		t.Errorf("err = %v, want it to wrap io.ErrShortWrite", err)
	}
}
