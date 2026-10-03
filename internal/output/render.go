package output

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// Format is a concrete rendering the output core can produce — the
// resolved value after --output and auto-detection are folded together.
type Format string

const (
	// FormatJSON emits the full envelope frame, one line, byte-clean.
	FormatJSON Format = "json"
	// FormatCompact emits bare data on success — the token-lean agent
	// default — and the full frame on failure (design R2), so an error
	// is never invisible in the lean mode.
	FormatCompact Format = "compact"
	// FormatHuman is the gated TTY surface: bespoke per-operation
	// renderers, registered via RegisterHuman as commands land.
	FormatHuman Format = "human"
)

// renderFunc renders one assembled result to w. Every renderer returns
// destination failures — a broken pipe is a real outcome, not noise.
// renderJSON and renderCompact get this for free from encodeJSON's own
// *WriteError; renderHuman earns it back by hand, wrapping w in a
// trackingWriter, because the View it draws through discards clog's
// per-line write errors (see renderHuman and trackingWriter).
type renderFunc func(ctx context.Context, w io.Writer, res Result) error

// formats is the render registry. A map rather than a switch because
// the design settles format dispatch as registry-shaped: a later format
// (ndjson, csv) is one entry plus its renderer, never an edit to a
// dispatch site. It stays unexported — every renderer is output-core
// code, so a registration API would be machinery with no caller.
var formats = map[Format]renderFunc{
	FormatJSON:    renderJSON,
	FormatCompact: renderCompact,
	FormatHuman:   renderHuman,
}

// Render dispatches the assembled result to the renderer for f. An
// unknown format is a programmer error surfaced loudly, never a silent
// fallback to some default shape.
func Render(ctx context.Context, w io.Writer, f Format, res Result) error {
	render, ok := formats[f]
	if !ok {
		return fmt.Errorf("no renderer registered for output format %q", f)
	}
	return render(ctx, w, res)
}

// RenderHelp runs native framework help through the same destination tracking
// used for human views. Cobra and clib's help callback discard write errors;
// keeping the writer here restores the failure contract without replacing
// their renderer or hiding a terminal's file descriptor.
func RenderHelp(ctx context.Context, w io.Writer, render func(io.Writer) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tracked, destination := newTrackingWriter(w)
	err := render(destination)
	if tracked.err != nil {
		return &WriteError{Err: errors.Join(err, tracked.err)}
	}
	return err
}

// HumanRenderFunc renders one operation's typed data for a person, including
// explicitly retained failure receipts dispatched by the root error boundary.
// It draws through a *View built for this call — never a bare io.Writer
// — so a renderer reaches only Row/Line/Logger to lay itself out, and
// cannot fall back to fmt.Fprintf-ing its own shape. Status and
// diagnostics belong on stderr through clog, owned by the command.
type HumanRenderFunc func(v *View, env envelope.Envelope) error

// humanRenderers maps operation name to its bespoke human renderer.
// Commands register during construction, before any Render call; the
// map is not synchronized because registration is single-threaded
// startup wiring.
// humanRenderers is startup-written and render-read; the mutex protects
// the readers, not a permission to register concurrently.
var (
	humanMu        sync.RWMutex
	humanRenderers = map[string]HumanRenderFunc{}
)

// RegisterHuman registers op's human renderer. A duplicate registration
// is a wiring bug two commands would otherwise fight over silently, so
// it panics at startup rather than letting last-write win.
// HasHuman reports whether op has a bespoke human renderer.
//
// Exported for a guardrail, not for rendering: human mode is not part of
// the audited machine matrix, so without a test that walks this registry
// an envelope-bearing command can ship whose only failure mode is that
// a person running it gets an error instead of output. That has now
// happened twice.
func HasHuman(op string) bool {
	humanMu.RLock()
	defer humanMu.RUnlock()
	_, ok := humanRenderers[op]
	return ok
}

// RegisterHuman records op's bespoke human renderer. Registration is
// startup-only; a duplicate is a programming error, not a runtime one.
func RegisterHuman(op string, fn HumanRenderFunc) {
	humanMu.Lock()
	defer humanMu.Unlock()
	if _, dup := humanRenderers[op]; dup {
		panic(fmt.Sprintf("output: human renderer for %q registered twice", op))
	}
	humanRenderers[op] = fn
}

func renderJSON(ctx context.Context, w io.Writer, res Result) error {
	// Assemble already drops the filter on a failure, but Result is
	// exported: a caller constructing one directly could hand this an
	// error envelope AND a filter. renderCompact has always checked; JSON
	// checking too means the never-filter-an-error-frame rule holds at
	// the renderer, not only at the assembler.
	if res.Filter != nil && res.Envelope.OK {
		return renderFiltered(ctx, w, res, res.Envelope)
	}
	return encodeJSON(w, res.Envelope)
}

func renderCompact(ctx context.Context, w io.Writer, res Result) error {
	if !res.Envelope.OK {
		// R2: errors keep the full frame in every machine mode. Bare
		// data on failure would strip the very fields (ok, errors[],
		// exit_code) an agent needs to notice something went wrong.
		return encodeJSON(w, res.Envelope)
	}
	// A filter sees exactly what this mode would have printed, so an
	// expression is written against the shape --output advertises rather
	// than against a document the caller never asked for.
	if res.Filter != nil {
		return renderFiltered(ctx, w, res, res.Envelope.Data)
	}
	// Bare data, no null-stripping: null-vs-absent is a per-field
	// decision made in each Output struct's tags (design §6.1), so the
	// renderer must not erase a null a field chose to emit.
	return encodeJSON(w, res.Envelope.Data)
}

// Evaluation is buffered, so these failures leave stdout untouched and the
// root can safely emit a complete error frame containing the original result.
func renderFiltered(ctx context.Context, w io.Writer, res Result, doc any) error {
	err := res.Filter.apply(ctx, w, doc)
	var writeErr *WriteError
	if errors.As(err, &writeErr) {
		return err
	}
	if errors.Is(err, ErrFilterFailed) || errors.Is(err, ErrFilterTooLarge) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &FilterError{Err: err, Envelope: res.Envelope}
	}
	return err
}

func renderHuman(_ context.Context, w io.Writer, res Result) error {
	if !res.Envelope.OK {
		// Human failure rendering is owned by the root error boundary
		// (message + hint on stderr through clog); routing a failure
		// envelope here means a caller skipped that boundary.
		return fmt.Errorf("human rendering of failure envelopes is owned by the root error boundary, not Render (op %q)", res.Envelope.Meta.Command)
	}
	// The result's own renderer wins. A command built through the
	// construction path carries it, which is what lets that path own
	// human output without registering into a process-global map it
	// would panic on the second time a tree is built. The registry
	// remains for every command not yet converted.
	fn := res.Human
	ok := fn != nil
	if !ok {
		humanMu.RLock()
		fn, ok = humanRenderers[res.Envelope.Meta.Command]
		humanMu.RUnlock()
	}
	if !ok {
		// Loud, not a JSON fallback: a command shipping without its
		// human surface is a gap the gauntlet must see, and 0-magic
		// forbids quietly showing an agent format to a person.
		return fmt.Errorf("no human renderer registered for operation %q", res.Envelope.Meta.Command)
	}
	return renderHumanData(w, res, fn)
}

// RenderFailureReceipt renders retained typed data on stdout while the root
// reports the failure on stderr. It never relabels the failure as a success or
// looks up a success renderer implicitly: the construction path supplies the
// renderer together with the receipt. Ordinary failure envelopes still cannot
// pass through Render's human branch.
func RenderFailureReceipt(_ context.Context, w io.Writer, res Result) error {
	if res.Envelope.OK || len(res.Envelope.Errors) == 0 || res.Envelope.Data == nil || res.Human == nil {
		return fmt.Errorf("failure receipt rendering requires failed typed data and its explicit human renderer")
	}
	return renderHumanData(w, res, res.Human)
}

func renderHumanData(w io.Writer, res Result, fn HumanRenderFunc) error {
	// res.ColorMode, not a fresh resolution: internal/output cannot import
	// internal/cli (cli already imports output — the cycle the design's
	// CYCLE CONSTRAINT rules out), so the mode cli.ResolvedColorMode
	// published for this invocation travels here as data instead —
	// writeEnvelope reads it onto Inputs.ColorMode, Assemble carries it
	// onto Result. Building the View with it here, rather than trusting a
	// renderer to pass the right mode itself, is what keeps the View's own
	// color/TTY decision agreeing with what the pure value sinks
	// (cli.Status, cli.Entity, ...) already decided for the same writer.
	//
	// w is wrapped in a tracking writer first: View draws through
	// clog.Output.WriteLine, which returns nothing (clog does not surface
	// a per-line write error to its caller), so a broken human-output
	// pipe or a full disk would otherwise vanish the moment fn returns
	// nil — an exit 0 where json/compact would report output_write_failed.
	// The tracker recovers that failure from underneath clog's void
	// finaliser: fn draws through it exactly as before, and afterward we
	// check what actually reached the destination.
	tracked, dest := newTrackingWriter(w)
	if err := fn(NewView(dest, res.ColorMode), res.Envelope); err != nil {
		return err
	}
	if tracked.err != nil {
		return &WriteError{Err: tracked.err}
	}
	return nil
}

// encodeJSON writes one newline-terminated frame through the wire encoder.
// Raw JSON keeps its authored escape spelling; ordinary string values leave
// HTML and line separators literal. Encoding or delivery failures retain the
// shared output-failure classification.
func encodeJSON(w io.Writer, v any) error {
	raw, err := jira.MarshalNoEscape(v)
	if err != nil {
		return &WriteError{Err: err}
	}
	raw = append(raw, '\n')
	n, err := w.Write(raw)
	if err != nil {
		return &WriteError{Err: err}
	}
	if n != len(raw) {
		return &WriteError{Err: io.ErrShortWrite}
	}
	return nil
}

// WriteError wraps a failure to produce or deliver machine output bytes
// — a broken pipe, a full disk, an unmarshalable payload. It is typed so
// internal/cli maps it to output_write_failed (exit 8), and so callers
// can recognize "the work may have happened; only reporting it failed"
// and never retry a mutation solely because its result could not be
// written.
type WriteError struct {
	Err error
}

func (e *WriteError) Error() string {
	return fmt.Sprintf("write output: %v", e.Err)
}

func (e *WriteError) Unwrap() error {
	return e.Err
}

// trackingWriter wraps an io.Writer and records the FIRST non-nil write
// error or short write it observes — the destination-failure signal
// renderHuman needs but cannot get back from clog directly, since
// clog.Output.WriteLine (what View draws every Row/Line/Block through)
// returns nothing: a broken pipe or a full disk during human rendering
// would otherwise be indistinguishable, from renderHuman's own return
// value, from a renderer that simply had nothing left to write.
//
// First error only: once the destination has failed once, later writes
// failing differently (or "succeeding" against an already-broken pipe)
// add no information renderHuman needs — the first failure is the one
// that actually explains why bytes stopped arriving.
type trackingWriter struct {
	w   io.Writer
	err error
}

func (t *trackingWriter) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if t.err == nil {
		switch {
		case err != nil:
			t.err = err
		case n < len(p):
			t.err = io.ErrShortWrite
		}
	}
	return n, err
}

// fdTrackingWriter adds Fd()/Read()/Close() pass-through on top of
// trackingWriter's Write tracking, so the wrapper still satisfies
// colorprofile.Detect's term.File shape (io.ReadWriteCloser + Fd()) and
// clog.NewOutput's own `interface{ Fd() uintptr }` probe when the
// wrapped writer does. Without this, wrapping a real terminal (e.g.
// cmd.OutOrStdout()'s *os.File) in a plain trackingWriter would make
// clog treat a live TTY as a non-descriptor writer — silently losing
// TTY-based color/width/divider-fallback detection, a real regression
// that has nothing to do with where the error-tracking wrapper sits.
// newTrackingWriter only returns this variant when the wrapped writer
// itself exposes Fd(); a bytes.Buffer or other test double stays a plain
// trackingWriter.
type fdTrackingWriter struct {
	*trackingWriter
}

func (f *fdTrackingWriter) Fd() uintptr {
	return f.w.(interface{ Fd() uintptr }).Fd()
}

// Read forwards when the wrapped writer happens to also be a Reader (a
// real *os.File is); colorprofile.Detect's term.File assertion needs the
// method to exist, not that it ever gets called on a write-only human
// render path.
func (f *fdTrackingWriter) Read(p []byte) (int, error) {
	if r, ok := f.w.(io.Reader); ok {
		return r.Read(p)
	}
	return 0, os.ErrInvalid
}

// Close is a no-op: this wrapper does not own the underlying writer's
// lifecycle (cmd.OutOrStdout() is closed, if at all, by its own owner),
// but term.File's shape still requires the method to exist.
func (f *fdTrackingWriter) Close() error {
	return nil
}

// newTrackingWriter builds the tracker and the writer renderHuman should
// actually hand to NewView: the plain trackingWriter, or the
// Fd()-forwarding variant when w supports it. tracked stays addressable
// after fn returns so its recorded error can be checked; dest is what
// draws.
func newTrackingWriter(w io.Writer) (tracked *trackingWriter, dest io.Writer) {
	tracked = &trackingWriter{w: w}
	if _, ok := w.(interface{ Fd() uintptr }); ok {
		return tracked, &fdTrackingWriter{trackingWriter: tracked}
	}
	return tracked, tracked
}
