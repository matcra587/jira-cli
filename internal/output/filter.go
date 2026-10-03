package output

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"time"

	"github.com/itchyny/gojq"

	"github.com/matcra587/jira-cli/internal/envelope"
)

// Filter is a compiled jq program applied to what a MACHINE mode would
// otherwise have emitted. It is compiled once, at flag-resolution time,
// so a malformed expression fails before any network work happens
// rather than after a mutation has already run.
//
// WithVariables is the ONLY gojq compiler option passed, and it grants
// no reach: it binds caller-supplied data to names rather than opening a
// door to something outside the document. The options deliberately NOT
// passed are what constrain an expression — WithEnvironLoader (the
// environment: `env`/`$ENV` return an empty object without it),
// WithModuleLoader (the filesystem: import/include fail to compile) and
// WithInputIter (extra inputs: `input`/`inputs` fail to compile). Do not
// add one without a reason that survives the question "what can a
// hostile filter reach now?".
//
// Honest limit, so nobody reads more into this than it says: an
// expression is NOT hermetic. gojq's time builtins are unconditional, so
// `now` reads the host clock and `localtime`/`strflocaltime` observe the
// process timezone — identical input, different output under a different
// TZ. Nothing there reads Jira data or credentials, but "output depends
// only on the document" would be false.
type Filter struct {
	src    string
	code   *gojq.Code
	values []any
}

// FilterVar binds a value the expression reaches as $Name. Values are
// passed to the engine as DATA, never spliced into the program text —
// which is the whole point: a caller filtering on Jira-authored content
// would otherwise concatenate it into the expression, where a quote or a
// backslash in a summary silently changes what the program means.
type FilterVar struct {
	// Name is the variable without its leading "$".
	Name string
	// Value is any JSON-representable value: a string for --jq-arg, a
	// decoded document for --jq-argjson.
	Value any
}

// Source returns the expression as the caller wrote it, for error
// messages that quote what was typed.
func (f *Filter) Source() string { return f.src }

// CompileFilter parses and compiles src. Both failure modes are the
// caller's expression being wrong, and both are reported through
// ErrFilterInvalid so the command layer can map one code to them; a
// gojq.ParseError carries the offset, which its Error() already renders.
func CompileFilter(src string, vars ...FilterVar) (*Filter, error) {
	query, err := gojq.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrFilterInvalid, err.Error())
	}
	// Names and values are positional to each other in gojq's API, so the
	// two slices are built in one pass and never reordered.
	names := make([]string, 0, len(vars))
	values := make([]any, 0, len(vars))
	for _, v := range vars {
		names = append(names, "$"+v.Name)
		values = append(values, v.Value)
	}
	code, err := gojq.Compile(query, gojq.WithVariables(names))
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrFilterInvalid, err.Error())
	}
	return &Filter{src: src, code: code, values: values}, nil
}

// ErrFilterInvalid marks an expression the jq engine refused to parse or
// compile. ErrFilterFailed marks one that compiled but blew up against
// the actual document — a different fault with a different fix, so the
// taxonomy gives them different codes.
var (
	ErrFilterInvalid = errors.New("jq expression is not valid")
	ErrFilterFailed  = errors.New("jq expression failed")
	// ErrFilterTooLarge marks a filter whose output outgrew the buffer.
	// Distinct from ErrFilterFailed because the fix differs: the
	// expression is not wrong, it simply asked for more than this CLI
	// will hold, and the recovery is to narrow it or paginate.
	ErrFilterTooLarge = errors.New("jq output too large")
)

// FilterError retains the operation's unfiltered receipt when evaluation fails
// before writing any projection bytes. The command may already have mutated
// Jira; losing its data would prevent callers from checking what completed.
// Destination failures remain WriteError because output may already be partial.
type FilterError struct {
	Err      error
	Envelope envelope.Envelope
}

// Error reports the original projection failure.
func (e *FilterError) Error() string { return e.Err.Error() }

// Unwrap preserves the filter, cancellation, and timeout taxonomy.
func (e *FilterError) Unwrap() error { return e.Err }

// maxFilterOutput bounds what one filter may accumulate.
//
// Results are buffered so a filter that emits and THEN fails cannot leave
// valid lines ahead of an error frame. That correctness is worth keeping,
// but unbounded it makes a one-token expression — `repeat(.)`,
// `range(1e18)` — an out-of-memory kill: the Go runtime dies, and the
// process reports an exit code the taxonomy has already assigned another
// meaning to. A cap keeps the all-or-nothing write AND bounds the cost.
//
// 64 MiB is far above any real Jira envelope (a full search page is
// kilobytes) while stopping an infinite generator in about a second. A
// var, not a const, so tests can bind it small instead of allocating the
// real ceiling.
var maxFilterOutput = 64 << 20

// maxFilterRuntime bounds how long one filter may run.
//
// The byte cap alone is not enough, because the expressions that hurt
// most produce no output to count: `repeat(.) | empty` spins forever
// emitting nothing, and `[repeat(.)]` grows inside gojq before a single
// value is ever yielded. gojq checks for cancellation between VM
// instructions, so a deadline is the only thing that reaches inside its
// evaluation loop.
//
// This is a backstop against a runaway expression, not a performance
// budget: filtering one envelope is sub-millisecond work, so 30s is
// several orders of magnitude of headroom. A var so tests can bind it
// short.
var maxFilterRuntime = 30 * time.Second

// apply runs the filter over ONE document and writes its results to w.
//
// Per-document is the contract, not an implementation detail. Today a
// command emits exactly one document per invocation, so the distinction
// is invisible — but if paginated results ever stream a document per
// page, "the filter runs against each document" extends without
// redefining anything, whereas "the filter runs against the response"
// would have to change meaning. Stating it now is what keeps that
// additive.
//
// Every result is buffered before ANY byte is written: an expression can
// emit several values and then fail, and a partial list followed by an
// error frame would be a document no consumer can parse. Nothing reaches
// the writer unless the whole run succeeded.
//
// Strings emit raw, without JSON quotes, which is jq's -r rather than
// jq's default. That is what makes `--jq '.data.issues[].key' | xargs`
// work, and it matches gh. The cost is that a value containing a newline
// becomes indistinguishable from two results; `@json` is the escape
// hatch, and the flag's help says so.
func (f *Filter) apply(ctx context.Context, w io.Writer, doc any) error {
	// gojq walks plain JSON types only, so the typed Output struct has to
	// round-trip through encoding/json first. A failure here is ours, not
	// the caller's: it means a registered Output cannot be marshaled.
	raw, err := json.Marshal(doc)
	if err != nil {
		return &WriteError{Err: fmt.Errorf("marshal document for --jq: %w", err)}
	}
	input, err := DecodeJSONNumbers(raw)
	if err != nil {
		return &WriteError{Err: fmt.Errorf("decode document for --jq: %w", err)}
	}

	// The deadline is the CLI's backstop, not a replacement for the
	// caller's: an already-shorter deadline on ctx still wins.
	ctx, cancel := context.WithTimeout(ctx, maxFilterRuntime)
	defer cancel()

	var out []byte
	iter := f.code.RunWithContext(ctx, input, f.values...)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, isErr := v.(error); isErr {
			// A bare `halt` stops iteration without being a failure. Its
			// exit code is deliberately NOT honored — a user expression
			// must never forge this CLI's published exit contract — but it
			// IS read to tell the two apart: gojq builds bare `halt` with
			// code 0, while `null | halt_error(7)` also carries a nil
			// value and code 7. Checking only the value turned a
			// deliberate error into silent success at exit 0.
			var halt *gojq.HaltError
			if errors.As(err, &halt) && halt.Value() == nil && halt.ExitCode() == 0 {
				break
			}
			// A canceled or timed-out context surfaces as its own error so
			// the taxonomy maps it to canceled/timeout, not to a bad filter.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("%w: %s", ErrFilterFailed, err.Error())
		}
		line, err := formatFilterValue(v)
		if err != nil {
			return err
		}
		if len(out)+len(line) > maxFilterOutput {
			return fmt.Errorf("%w: stopped after %d bytes", ErrFilterTooLarge, len(out))
		}
		out = append(out, line...)
	}

	n, err := w.Write(out)
	if err != nil {
		return &WriteError{Err: err}
	}
	if n != len(out) {
		// A writer may report fewer bytes with a nil error; ignoring the
		// count silently truncates the result and calls it success.
		return &WriteError{Err: io.ErrShortWrite}
	}
	return nil
}

// formatFilterValue renders one jq result as a line. gojq.Marshal is used
// rather than encoding/json because it already declines to escape <, >, &
// and U+2028/U+2029 — the same byte fidelity encodeJSON works to keep,
// arrived at from the other direction.
func formatFilterValue(v any) ([]byte, error) {
	if s, ok := v.(string); ok {
		return append([]byte(s), '\n'), nil
	}
	b, err := gojq.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrFilterFailed, err.Error())
	}
	return append(b, '\n'), nil
}

// DecodeJSONNumbers unmarshals JSON into the value types gojq evaluates,
// preserving integers that do not survive float64.
//
// Plain json.Unmarshal into `any` turns every number into a float64, so
// 9007199254740993 comes back as ...992 — silently, with no error, in
// both directions: values bound by --jq-argjson and every document a
// filter runs over. Jira hands out large numeric identifiers, and a
// reporting tool that quietly changes an ID is worse than one that
// refuses.
//
// gojq evaluates int, *big.Int and float64 (its own decoder does the
// same widening), so each json.Number is resolved to the narrowest of
// those that holds it exactly.
func DecodeJSONNumbers(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return normalizeNumbers(v), nil
}

// normalizeNumbers walks a decoded document converting json.Number into
// the concrete numeric types gojq understands. Containers are rebuilt in
// place; every other value passes through untouched.
func normalizeNumbers(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := strconv.Atoi(t.String()); err == nil {
			return i
		}
		if b, ok := new(big.Int).SetString(t.String(), 10); ok {
			return b
		}
		// Not an integer: float64, with the precision jq itself would
		// give it. A magnitude float64 cannot hold (1e1000) parses to
		// ±Inf under ErrRange, which is kept deliberately — gojq.Marshal
		// clamps infinities to the float64 limit, exactly as jq prints
		// them. Returning the raw text instead would silently turn a
		// number into a string.
		f, err := strconv.ParseFloat(t.String(), 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return t.String()
		}
		return f
	case map[string]any:
		for k, e := range t {
			t[k] = normalizeNumbers(e)
		}
		return t
	case []any:
		for i, e := range t {
			t[i] = normalizeNumbers(e)
		}
		return t
	default:
		return v
	}
}
