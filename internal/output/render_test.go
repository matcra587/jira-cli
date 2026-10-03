package output_test

// No t.Parallel here: RegisterHuman writes to an unsynchronized package
// global (the same single-threaded-startup posture as envelope's
// registration map), so these tests must not run concurrently with each
// other or with any other test that registers a human renderer.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

type renderData struct {
	Text string `json:"text"`
}

func assembleOK(op string, data any) output.Result {
	return output.Assemble(output.Inputs{
		Op:   op,
		Data: data,
		Now:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
}

func assembleFailure(op string, exitCode int) output.Result {
	return output.Assemble(output.Inputs{
		Op:       op,
		Data:     renderData{Text: "unused on failure"},
		Errors:   []envelope.Error{{Code: "boom", Message: "m", Hint: "h"}},
		ExitCode: exitCode,
		Now:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
}

func TestRenderUnknownFormatIsLoudNotSilentFallback(t *testing.T) {
	var buf bytes.Buffer
	err := output.Render(t.Context(), &buf, output.Format("ndjson-not-registered-yet"), assembleOK("rendertest.unknown", renderData{Text: "x"}))
	if err == nil {
		t.Fatal("Render accepted an unregistered format silently")
	}
	if !strings.Contains(err.Error(), "ndjson-not-registered-yet") {
		t.Errorf("error = %v, want it to name the unregistered format", err)
	}
	if buf.Len() != 0 {
		t.Errorf("buffer = %q, want nothing written for an unknown format", buf.String())
	}
}

func TestRenderJSONEmitsFullEnvelope(t *testing.T) {
	res := assembleOK("rendertest.json", renderData{Text: "hello"})
	var buf bytes.Buffer
	if err := output.Render(t.Context(), &buf, output.FormatJSON, res); err != nil {
		t.Fatalf("Render: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	for _, key := range []string{"ok", "meta", "data", "errors", "warnings"} {
		if _, ok := got[key]; !ok {
			t.Errorf("json render missing top-level key %q, full frame expected: %s", key, buf.String())
		}
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Errorf("json render not newline-terminated: %q", buf.String())
	}
}

// TestRenderCompactAsymmetry pins design R2 exactly as documented in
// render.go: compact strips to bare data on success (no envelope
// wrapper, no null-stripping within that data), but keeps the full frame
// on failure so an error is never invisible in the lean mode.
func TestRenderCompactAsymmetry(t *testing.T) {
	t.Run("success renders bare data, no envelope wrapper", func(t *testing.T) {
		res := assembleOK("rendertest.compact.ok", renderData{Text: "hello"})
		var buf bytes.Buffer
		if err := output.Render(t.Context(), &buf, output.FormatCompact, res); err != nil {
			t.Fatalf("Render: %v", err)
		}

		var got map[string]any
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("output not valid JSON: %v", err)
		}
		for _, envelopeKey := range []string{"ok", "meta", "errors", "warnings"} {
			if _, ok := got[envelopeKey]; ok {
				t.Errorf("compact success render leaked envelope key %q, want bare data only: %s", envelopeKey, buf.String())
			}
		}
		if got["text"] != "hello" {
			t.Errorf("compact success render = %s, want bare {\"text\":\"hello\"}", buf.String())
		}
	})

	t.Run("failure keeps the full frame, same as json", func(t *testing.T) {
		res := assembleFailure("rendertest.compact.fail", 3)
		var buf bytes.Buffer
		if err := output.Render(t.Context(), &buf, output.FormatCompact, res); err != nil {
			t.Fatalf("Render: %v", err)
		}

		var got map[string]any
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("output not valid JSON: %v", err)
		}
		for _, key := range []string{"ok", "meta", "data", "errors", "warnings"} {
			if _, ok := got[key]; !ok {
				t.Errorf("compact failure render missing top-level key %q, full frame expected: %s", key, buf.String())
			}
		}
		if got["ok"] != false {
			t.Errorf("compact failure render ok = %v, want false", got["ok"])
		}
	})
}

func TestRenderHumanRefusesFailureEnvelopes(t *testing.T) {
	// Registration panics on duplicates by design; reset so a
	// repeated in-process run (-count=N) starts clean.
	output.ResetHumanRenderersForTest()
	// Registered so a missing-renderer error can't be mistaken for this
	// (distinct) refusal.
	output.RegisterHuman("rendertest.human.refuse-failure", func(v *output.View, env envelope.Envelope) error {
		t.Fatal("human renderer invoked for a failure envelope; renderHuman must refuse before dispatch")
		return nil
	})

	res := assembleFailure("rendertest.human.refuse-failure", 1)
	var buf bytes.Buffer
	err := output.Render(t.Context(), &buf, output.FormatHuman, res)
	if err == nil {
		t.Fatal("renderHuman accepted a failure envelope; that belongs to the root error boundary, not Render")
	}
	if !strings.Contains(err.Error(), "root error boundary") {
		t.Errorf("error = %v, want it to name the root-error-boundary ownership", err)
	}
}

func TestExplicitFailureReceiptKeepsFailureAndTracksDestination(t *testing.T) {
	res := assembleFailure("rendertest.failure.receipt", 3)
	res.Human = func(v *output.View, env envelope.Envelope) error {
		if env.OK || env.Meta.ExitCode == nil || *env.Meta.ExitCode != 3 || len(env.Errors) != 1 {
			t.Fatal("receipt renderer received a fabricated success")
		}
		v.Line("retained receipt")
		return nil
	}
	var buf bytes.Buffer
	if err := output.RenderFailureReceipt(t.Context(), &buf, res); err != nil || buf.String() != "retained receipt\n" {
		t.Fatalf("explicit failure receipt: output=%q err=%v", buf.String(), err)
	}
	err := output.RenderFailureReceipt(t.Context(), alwaysErrWriter{}, res)
	var writeErr *output.WriteError
	if !errors.As(err, &writeErr) || !errors.Is(err, errWriteBoom) {
		t.Fatal("failure receipt rendering swallowed its destination failure")
	}
}

func TestRenderHumanNoRegisteredRendererIsLoudNotJSONFallback(t *testing.T) {
	res := assembleOK("rendertest.human.unregistered", renderData{Text: "x"})
	var buf bytes.Buffer
	err := output.Render(t.Context(), &buf, output.FormatHuman, res)
	if err == nil {
		t.Fatal("renderHuman silently succeeded for an operation with no registered human renderer")
	}
	if !strings.Contains(err.Error(), "rendertest.human.unregistered") {
		t.Errorf("error = %v, want it to name the operation", err)
	}
	if buf.Len() != 0 {
		t.Errorf("buffer = %q, want nothing written (no silent JSON fallback)", buf.String())
	}
}

func TestRenderHumanDispatchesToRegisteredRenderer(t *testing.T) {
	// Registration panics on duplicates by design; reset so a
	// repeated in-process run (-count=N) starts clean.
	output.ResetHumanRenderersForTest()
	const op = "rendertest.human.dispatch"
	var gotCommand string
	output.RegisterHuman(op, func(v *output.View, env envelope.Envelope) error {
		gotCommand = env.Meta.Command
		v.Line("rendered for a human")
		return nil
	})

	res := assembleOK(op, renderData{Text: "x"})
	var buf bytes.Buffer
	if err := output.Render(t.Context(), &buf, output.FormatHuman, res); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if buf.String() != "rendered for a human\n" {
		t.Errorf("buffer = %q, want the registered renderer's exact output", buf.String())
	}
	if gotCommand != op {
		t.Errorf("renderer saw Meta.Command = %q, want %q", gotCommand, op)
	}
}

func TestRegisterHumanPanicsOnDuplicateOp(t *testing.T) {
	// Registration panics on duplicates by design; reset so a
	// repeated in-process run (-count=N) starts clean.
	output.ResetHumanRenderersForTest()
	const op = "rendertest.human.dup"
	noop := func(v *output.View, env envelope.Envelope) error { return nil }
	output.RegisterHuman(op, noop)

	defer func() {
		if recover() == nil {
			t.Fatal("RegisterHuman accepted a duplicate registration for the same op")
		}
	}()
	output.RegisterHuman(op, noop)
}

// TestEncodeJSONByteFidelity pins the fidelity contract on the one
// printer: literal <, >, & survive SetEscapeHTML(false), and U+2028 /
// U+2029 — which encoding/json escapes UNCONDITIONALLY, HTML flag or
// not — are restored to raw bytes before the destination sees them.
// The counter-case rides along: a value containing the six-character
// TEXT "\\u2028" (backslash, u, digits) must keep its escaped form
// `\\\\u2028`, or the restore pass would be inventing characters the
// value never held.
func TestEncodeJSONByteFidelity(t *testing.T) {
	raw := "<tag>&amp;end\u2028mid\u2029close backslash text: \\u2028 stays text"
	res := assembleOK("rendertest.escaping", renderData{Text: raw})

	var buf bytes.Buffer
	if err := output.Render(t.Context(), &buf, output.FormatJSON, res); err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := buf.String()

	for _, want := range []string{"<tag>", "&amp;", "end\u2028mid", "mid\u2029close", `stays text`} {
		if !strings.Contains(got, want) {
			t.Errorf("output = %q, want literal %q preserved", got, want)
		}
	}
	// The literal-backslash text must still read as escaped text, not a
	// restored separator: two backslashes then u2028.
	if !strings.Contains(got, `\\u2028 stays`) {
		t.Errorf("output = %q, want the textual \\u2028 kept escaped as \\\\u2028", got)
	}

	// The line survives as ONE decodable frame whose value round-trips
	// byte-identically — fidelity, not just absence of escapes.
	var frame struct {
		Data renderData `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &frame); err != nil {
		t.Fatalf("restored output no longer parses as JSON: %v\noutput: %q", err, got)
	}
	if frame.Data.Text != raw {
		t.Errorf("round-trip = %q, want %q", frame.Data.Text, raw)
	}
}

func TestRenderPreservesRawJSONSpelling(t *testing.T) {
	const raw = `{"nativeText":"\u2028\u2029\u003c\/","literalText":"<>&` + "\u2028\u2029" + `","large":9007199254740993}`
	for _, mode := range []output.Format{output.FormatJSON, output.FormatCompact} {
		var buf bytes.Buffer
		res := assembleOK("rendertest.raw-spelling", struct {
			Native json.RawMessage `json:"native"`
		}{json.RawMessage(raw)})
		if err := output.Render(t.Context(), &buf, mode, res); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(buf.Bytes(), []byte(raw)) || buf.Bytes()[buf.Len()-1] != '\n' {
			t.Fatalf("raw native JSON changed in %s: %s", mode, buf.Bytes())
		}
	}
}

type incompleteFrameWriter struct{}

func (incompleteFrameWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestRenderRejectsIncompleteFrameDelivery(t *testing.T) {
	for _, mode := range []output.Format{output.FormatJSON, output.FormatCompact} {
		err := output.Render(t.Context(), incompleteFrameWriter{}, mode, assembleOK("rendertest.short-frame", renderData{Text: "receipt"}))
		var failure *output.WriteError
		if !errors.As(err, &failure) || !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("incomplete receipt delivery was accepted: %v", err)
		}
	}
}

// alwaysErrWriter fails on every Write, simulating a broken pipe or full
// disk destination.
type alwaysErrWriter struct{}

var errWriteBoom = errors.New("boom: destination refused the bytes")

func (alwaysErrWriter) Write(p []byte) (int, error) {
	return 0, errWriteBoom
}

func TestRenderWrapsDestinationFailureInWriteError(t *testing.T) {
	res := assembleOK("rendertest.writeerr", renderData{Text: "x"})
	err := output.Render(t.Context(), alwaysErrWriter{}, output.FormatJSON, res)
	if err == nil {
		t.Fatal("Render returned no error for a writer that always fails")
	}

	var werr *output.WriteError
	if !errors.As(err, &werr) {
		t.Fatalf("error = %v (%T), want *output.WriteError", err, err)
	}
	if !strings.Contains(werr.Error(), "write output:") {
		t.Errorf("WriteError.Error() = %q, want it prefixed with %q", werr.Error(), "write output:")
	}
	if !errors.Is(err, errWriteBoom) {
		t.Error("errors.Is(err, errWriteBoom) = false, want true (WriteError.Unwrap exposes the cause)")
	}
}

// TestResultRendererBeatsTheRegistry pins the seam the command
// construction path needs.
//
// The op-keyed registry panics on a duplicate and lives for the life of
// the process, so a path that registered at command construction would
// die the second time a tree is built — and this codebase builds many
// trees per test process. Carrying the renderer on the Result removes
// that shared state rather than weakening the duplicate check.
func TestResultRendererBeatsTheRegistry(t *testing.T) {
	const op = "rendertest.human.result-wins"
	output.RegisterHuman(op, func(v *output.View, _ envelope.Envelope) error {
		_, err := io.WriteString(v.Output().Writer(), "from the registry")
		return err
	})

	res := output.Assemble(output.Inputs{
		Op: op, Data: map[string]any{}, Now: time.Now(), RequestID: output.NewRequestID(),
		Human: func(v *output.View, _ envelope.Envelope) error {
			_, err := io.WriteString(v.Output().Writer(), "from the result")
			return err
		},
	})

	var buf bytes.Buffer
	if err := output.Render(context.Background(), &buf, output.FormatHuman, res); err != nil {
		t.Fatalf("render: %v", err)
	}
	if got := buf.String(); got != "from the result" {
		t.Errorf("rendered %q, want the result's own renderer to win", got)
	}
}

// TestRenderHumanReportsDestinationFailureThroughVoidFinalisers pins the
// fix for the regression the migration introduced: View draws every
// Row/Line/Block through clog.Output.WriteLine, which returns nothing,
// so a broken human-output pipe (or a full disk) used to vanish the
// moment the registered renderer returned nil — an exit 0 where
// json/compact would report output_write_failed. renderHuman must wrap
// the destination in a tracking writer and surface what it recorded.
func TestRenderHumanReportsDestinationFailureThroughVoidFinalisers(t *testing.T) {
	// Registration panics on duplicates by design; reset so a
	// repeated in-process run (-count=N) starts clean.
	output.ResetHumanRenderersForTest()
	const op = "rendertest.human.writeerr"
	output.RegisterHuman(op, func(v *output.View, _ envelope.Envelope) error {
		// Drawn through View.Line -> clog.Output.WriteLine, which
		// swallows its own write error — exactly the void finaliser
		// this test exists to see past. The renderer itself returns
		// nil, same as any real renderer would with nothing else to
		// report.
		v.Line("this line never reaches a broken pipe intact")
		return nil
	})

	res := assembleOK(op, renderData{Text: "x"})
	err := output.Render(t.Context(), alwaysErrWriter{}, output.FormatHuman, res)
	if err == nil {
		t.Fatal("renderHuman returned nil against a writer that always fails — the write failure was swallowed by clog's void WriteLine")
	}

	var werr *output.WriteError
	if !errors.As(err, &werr) {
		t.Fatalf("error = %v (%T), want *output.WriteError", err, err)
	}
	if !errors.Is(err, errWriteBoom) {
		t.Error("errors.Is(err, errWriteBoom) = false, want true (WriteError.Unwrap exposes the cause)")
	}
}

// TestRenderHumanHealthyWriterReturnsNil is
// TestRenderHumanReportsDestinationFailureThroughVoidFinalisers's
// counterpart: the same renderer, drawing through the same void
// WriteLine path, against a writer that never fails must still return
// nil — the tracking wrapper must not itself invent a failure.
func TestRenderHumanHealthyWriterReturnsNil(t *testing.T) {
	output.ResetHumanRenderersForTest()
	const op = "rendertest.human.writeok"
	output.RegisterHuman(op, func(v *output.View, _ envelope.Envelope) error {
		v.Line("a healthy line")
		return nil
	})

	res := assembleOK(op, renderData{Text: "x"})
	var buf bytes.Buffer
	if err := output.Render(t.Context(), &buf, output.FormatHuman, res); err != nil {
		t.Fatalf("Render against a healthy writer = %v, want nil", err)
	}
	if got := buf.String(); got != "a healthy line\n" {
		t.Errorf("buffer = %q, want the rendered line unharmed by the tracking wrapper", got)
	}
}

// TestRegistryStillServesUnconvertedCommands pins the fallback, so a
// command that has not moved to the construction path keeps working.
func TestRegistryStillServesUnconvertedCommands(t *testing.T) {
	const op = "rendertest.human.registry-fallback"
	output.RegisterHuman(op, func(v *output.View, _ envelope.Envelope) error {
		_, err := io.WriteString(v.Output().Writer(), "from the registry")
		return err
	})

	res := output.Assemble(output.Inputs{
		Op: op, Data: map[string]any{}, Now: time.Now(), RequestID: output.NewRequestID(),
	})

	var buf bytes.Buffer
	if err := output.Render(context.Background(), &buf, output.FormatHuman, res); err != nil {
		t.Fatalf("render: %v", err)
	}
	if got := buf.String(); got != "from the registry" {
		t.Errorf("rendered %q, want the registry fallback", got)
	}
}
