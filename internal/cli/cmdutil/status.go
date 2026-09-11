package cmdutil

import (
	"context"
	"errors"
	"time"

	"github.com/gechr/clog"
	"github.com/gechr/clog/field/duration"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/jira"
)

// Spin runs a blocking network or disk operation while giving the user
// feedback: a present-progressive spinner on a TTY ("Caching boards") and a
// structured debug lifecycle that records the elapsed time and any failure
// reason as fields rather than baking them into prose:
//
//	DBG Caching boards
//	DBG Cached boards time=320ms
//	DBG Failed to cache boards time=110ms status=403 reason="403 Forbidden"
//
// The spinner mirrors the auth-login gating: NonTTYSilent suppresses it whenever
// stderr is not a terminal (piped, redirected, or an agent capturing output),
// and clog writes to stderr, so machine output on stdout stays clean even under
// --output=json.
//
// Under --debug the spinner is suppressed entirely: the debug lifecycle below
// already narrates progress, and an animated spinner sharing stderr with the
// verbose request/response logging only corrupts both — each redraw frame
// strands onto its own line between the debug records. Verbose narration and
// the spinner are alternatives, never shown together.
func Spin(cmd *cobra.Command, op string, fn func(context.Context) error) error {
	return spinVerb(cmd, cli.VerbFor(op), fn, true)
}

// SpinPreview is Spin for a dry-run: the spinner label and the debug
// lifecycle speak about the preview ("Previewing issue edit", "previewed
// issue edit") instead of claiming the mutation happened. A dry-run path
// that performs a genuine read should keep Spin with the read op instead
// (the transition target resolution uses issue.transitions, for example).
// Preview work is purely local by that convention, so it does not feed the
// elapsed sink — the completion line's elapsed field reports round-trip
// time, never local CPU.
func SpinPreview(cmd *cobra.Command, op string, fn func(context.Context) error) error {
	return spinVerb(cmd, cli.VerbFor(op).Preview(), fn, false)
}

func spinVerb(cmd *cobra.Command, verb cli.OperationVerb, fn func(context.Context) error, recordElapsed bool) error {
	logger := clog.Ctx(cmd.Context())
	logger.Debug().Msg(verb.Gerundf())

	start := time.Now()
	var err error
	if clog.IsVerbose() {
		err = fn(cmd.Context())
	} else {
		// The spinner label is user-facing UI, so it is Sentence-cased; the
		// debug lifecycle above/below stays lower case as a structured log.
		spinner := clog.Spinner(cli.SentenceCase(verb.Gerundf())).
			NonTTYSilent(true)
		// A live countdown to the --timeout context deadline: invisible on
		// machine outputs (the spinner never renders there) and gone from
		// the done row, it only earns attention when a request hangs long
		// enough for "how long until the CLI gives up" to matter.
		if dl, ok := cmd.Context().Deadline(); ok {
			spinner = spinner.Deadline("timeout", time.Until(dl))
		}
		err = spinner.Wait(cmd.Context(), fn).Silent()
	}
	elapsed := time.Since(start)
	if recordElapsed {
		recordAPIElapsed(cmd.Context(), elapsed)
	}

	if err != nil {
		// duration.WithMinimum(0) keeps time= visible below clog's default
		// 1s cutoff: this debug lifecycle documents sub-second timings above.
		// The gradient tops out at 10s — a round trip that slow reads fully
		// red, matching the timeout-scale pain a user actually feels.
		event := logger.Debug().Duration("time", elapsed, duration.WithMinimum(0), duration.WithGradientMax(debugTimeGradientMax))
		// Surface the HTTP status as its own field rather than burying it in
		// the reason string, so failures stay greppable (status=403).
		if apiErr, ok := errors.AsType[*jira.APIError](err); ok {
			event = event.Int("status", apiErr.StatusCode)
		}
		// The error text embeds Jira-supplied messages, so the reason field
		// crosses the terminal sanitizer before reaching stderr.
		event.Str("reason", cli.SanitizeTerminalText(err.Error())).Msg(verb.Failuref())
		return err
	}
	logger.Debug().Duration("time", elapsed, duration.WithMinimum(0), duration.WithGradientMax(debugTimeGradientMax)).Msg(verb.Pastf())
	return nil
}

// debugTimeGradientMax anchors the debug lifecycle's time= gradient: green
// for fast round trips, fully red by 10 seconds — the point where a single
// HTTP call reads as timeout-scale pain rather than ordinary latency.
const debugTimeGradientMax = 10 * time.Second
