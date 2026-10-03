package output

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/gechr/clog"

	"github.com/matcra587/jira-cli/internal/envelope"
)

// Inputs carries everything Assemble needs, all injected: the caller
// supplies the clock reading and request id, so Assemble stays pure and
// tests pin exact envelopes without freezing time.
type Inputs struct {
	// Op is the dotted operation name ("version", "issue.view") that
	// becomes meta.command and keys the human render registry.
	Op string
	// Data is the operation's typed Output struct (internal/envelope).
	Data any
	// Warnings and Errors ride the frame; nil slices normalize to empty
	// arrays so the emitted JSON never carries null where consumers
	// iterate.
	Warnings []envelope.Warning
	Errors   []envelope.Error
	// ExitCode is the contract exit for the failure; it reaches the
	// envelope only when Errors is non-empty (absence means success).
	ExitCode int
	// Now and RequestID are injected, never read from the environment.
	Now       time.Time
	RequestID string
	// Human is this operation's human renderer, supplied by the caller
	// that owns the command rather than looked up by name.
	//
	// The package-level registry keyed by op string cannot serve a
	// command construction path: it panics on a duplicate, and the
	// command tree is built many times in one test process, so
	// registering at construction dies on the second tree. Carrying the
	// renderer with the result removes the shared state instead of
	// weakening the duplicate check that protects it — the same reason
	// command keys command metadata by *cobra.Command rather than by
	// name. nil falls back to the registry, so a command that has not
	// been converted is unaffected.
	Human HumanRenderFunc
	// Filter is the compiled --jq expression, or nil. Assemble drops it
	// on a failure envelope, so the error path cannot filter its own
	// error frame however it was built.
	Filter *Filter
	// ColorMode is the resolved --color decision for this invocation
	// (cli.ResolvedColorMode()), carried in rather than resolved here:
	// internal/output must not import internal/cli, which already
	// imports output, so the color decision travels as data instead of
	// a function call. renderHuman reads it off Result to build the
	// View with the SAME mode the pure value sinks (cli.Status,
	// cli.Entity, ...) already resolved for the same writer; the
	// machine renderers ignore it.
	ColorMode clog.ColorMode
}

// Result is the one assembled value every renderer consumes. A named
// type rather than the bare envelope so the assembled side can grow
// (render hints, sanitisation state) without touching the contract type.
type Result struct {
	Envelope envelope.Envelope
	// Filter is applied by the MACHINE renderers to whatever document
	// their mode would have emitted. renderHuman never reads it, so a
	// filter and human output cannot coexist even if the flag layer let
	// them through.
	Filter *Filter
	// Human renders this operation for a person; nil means "look the
	// operation up in the registry" — see Inputs.Human.
	Human HumanRenderFunc
	// ColorMode is Inputs.ColorMode, carried through so renderHuman
	// builds the View with the same mode Assemble was given.
	ColorMode clog.ColorMode
}

// Assemble builds the envelope frame from injected facts. ok is derived,
// never passed: a result with errors is not ok, and there is no third
// state. It performs no I/O — Render owns the bytes.
func Assemble(in Inputs) Result {
	env := envelope.Envelope{
		OK: len(in.Errors) == 0,
		Meta: envelope.Meta{
			Command:         in.Op,
			ContractVersion: envelope.ContractVersion,
			Timestamp:       in.Now.UTC().Format(time.RFC3339),
			RequestID:       in.RequestID,
		},
		Data:     in.Data,
		Errors:   in.Errors,
		Warnings: in.Warnings,
	}
	if env.Errors == nil {
		env.Errors = []envelope.Error{}
	}
	if env.Warnings == nil {
		env.Warnings = []envelope.Warning{}
	}
	if !env.OK {
		// A failure claiming exit 0 would tell an agent that shortcuts on
		// exit_code the command succeeded. Forgetting to set ExitCode on a
		// failure Inputs is a programming bug, and like every other
		// contract violation in this layer it fails loud, not plausible.
		if in.ExitCode == 0 {
			panic("output: failure envelope assembled with exit code 0 — set Inputs.ExitCode from the error taxonomy")
		}
		exit := in.ExitCode
		env.Meta.ExitCode = &exit
	}
	if len(in.Errors) > 0 {
		// An error frame is never filtered. A jq expression is written
		// against a command's SUCCESS shape, so running it over an error
		// frame yields nothing useful and can swallow the very report the
		// caller needs — including the report that the filter is broken.
		// Dropping it here, next to the ok/exit-code invariants, means no
		// renderer has to remember the rule.
		return Result{Envelope: env, Human: in.Human, ColorMode: in.ColorMode}
	}
	return Result{Envelope: env, Filter: in.Filter, Human: in.Human, ColorMode: in.ColorMode}
}

// NewRequestID returns a 32-character hex correlation id for
// meta.request_id. crypto/rand never returns an error on supported
// platforms (it crashes the process on the unrecoverable path), so there
// is deliberately no fallback branch to pretend otherwise.
func NewRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
