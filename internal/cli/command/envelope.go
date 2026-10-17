package command

import (
	"time"

	"github.com/gechr/clog"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// WriteEnvelope emits op's success envelope with no warnings — the exit
// for the RunE of a command not yet built through the construction path,
// which writes its own envelope from the data Run returns. The result
// must be returned, not discarded: a destination failure (broken pipe,
// full disk) is the command's failure.
func WriteEnvelope(cmd *cobra.Command, op string, data any) error {
	return writeEnvelope(cmd, op, data, nil, nil, false)
}

// WriteEnvelopeWithWarnings is WriteEnvelope for commands whose work
// produced non-fatal diagnostics. In json the warnings ride the frame;
// in compact they do not exist — compact success is bare data, so a
// fact that changed what the caller got must ride the Output struct
// itself (design C1), never only a warning.
func WriteEnvelopeWithWarnings(cmd *cobra.Command, op string, data any, warnings []envelope.Warning) error {
	return writeEnvelope(cmd, op, data, warnings, nil, false)
}

// writeEnvelope is the one place a success envelope is assembled and
// written, and the middle of the write-ownership chain: the command
// hands its typed data here, this package injects the clock reading and
// request id and resolves the format the root seeded, and output.Render
// writes to cobra's writer. Envelopes are stdout-only; human status
// belongs on stderr through clog, owned by the human renderer.
//
// human is the command's OWN renderer, or nil to leave rendering to the
// op-keyed registry. The construction path supplies one because that
// registry panics on a duplicate and lives for the process, while a
// command tree is built many times in one test run — so a path that
// registered there would die on the second tree. Carrying the renderer
// on the result removes the shared state instead of weakening the check
// that guards it.
func writeEnvelope(cmd *cobra.Command, op string, data any, warnings []envelope.Warning, human output.HumanRenderFunc, page bool) error {
	format, ok := cli.FormatFromContext(cmd.Context())
	if !ok {
		// A missing format means root's PersistentPreRunE never ran —
		// wiring broke, and defaulting here would hide it behind
		// plausible output.
		//
		// Typed, not bare: an untyped error takes the generic validation
		// row, whose hint tells the reader to check the values they
		// passed. Nothing they passed caused this, and there is nothing
		// they can pass to fix it. The output-failure code is the honest
		// one — the work happened and could not be emitted — and it
		// carries the exit the contract reserves for exactly that.
		return cli.NewCodedError(cli.CodeOutputWriteFailed,
			"no output format in the command context: root's output resolution did not run")
	}
	allWarnings := append(cli.TakeWarnings(), cli.TakeRequestWarnings(cmd.Context())...)
	res := output.Assemble(output.Inputs{
		Op:   op,
		Data: data,
		// Startup warnings lead: they describe the state everything else
		// in this envelope was produced under.
		Warnings:  append(allWarnings, warnings...),
		Now:       time.Now(),
		RequestID: output.NewRequestID(),
		// nil unless --jq was given. Assemble drops it on a failure
		// envelope, so this path cannot filter an error frame even though
		// it is the success path.
		Filter: cli.FilterFromContext(cmd.Context()),
		Human:  human,
		// The published --color decision, so renderHuman's View agrees
		// with what the pure value sinks (cli.Status, cli.Entity, ...)
		// already resolved for this same writer — see output.Inputs's
		// ColorMode field for why it travels as data instead of output
		// importing cli to ask directly.
		ColorMode: cli.ResolvedColorMode(),
	})
	// Warnings reach a PERSON on stderr, through clog, before the
	// operation's own output. renderHuman delegates entirely to the
	// per-op renderer, so without this a warning exists only in machine
	// modes — present in the contract, invisible to whoever is looking
	// at the screen.
	if format == output.FormatHuman {
		emitWarnings(res.Envelope.Warnings)
	}
	if page {
		return output.RenderDocument(cmd.Context(), cmd.OutOrStdout(), format, res, output.DocumentOptions{
			Input: cmd.InOrStdin(), Diagnostics: cmd.ErrOrStderr(),
		})
	}
	return output.Render(cmd.Context(), cmd.OutOrStdout(), format, res)
}

// emitWarnings prints warnings on stderr, the same channel every other
// human diagnostic uses. Stdout stays the envelope's alone.
func emitWarnings(warnings []envelope.Warning) {
	for _, w := range warnings {
		clog.Warn().Msg(w.Message)
	}
}

// EmitWarnings is cli.TakeWarnings for human mode: the warnings go to a
// person on stderr instead of into an envelope, and are cleared the same
// way so a later envelope cannot repeat them.
func EmitWarnings() {
	emitWarnings(cli.TakeWarnings())
}
