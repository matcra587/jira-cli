package issue

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	xslices "github.com/gechr/x/slices"
	"github.com/spf13/cobra"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// The anchor flags, spelled once so registration and every refusal name
// the same strings.
const (
	flagBefore = "before"
	flagAfter  = "after"
)

// newRank returns `jira issue rank`.
//
// The Agile endpoint takes one ordered batch of at most fifty issues, so there
// is no fan-out or parallelism option. A 204 acknowledges every move; a 207
// supplies the native per-issue verdicts validated by rankResults.
func newRank(deps Deps) *cobra.Command {
	var keyed command.KeyedFlags
	var mut command.MutationFlags
	var before, after string
	var cmd *cobra.Command

	return command.MustNew(command.Spec[envelope.IssueRankOutput]{
		Op:         "issue.rank",
		WritesJira: true,
		Identity:   true,
		Keyed:      true,
		Use:        "rank ISSUE_KEY...",
		Short:      "Move issues in the backlog order",
		Long: "Move one or more issues before or after another issue in the " +
			"backlog's ranking.\n\n" +
			"The relationship is a flag, not a positional: `--before KEY-9` puts " +
			"the named issues immediately above KEY-9, `--after KEY-9` " +
			"immediately below it. Exactly one of the two is required — an issue " +
			"cannot go both sides of the same anchor, and defaulting to one of " +
			"them would move things in a direction nobody asked for.\n\n" +
			"**The order you list the issues in is the order they end up in.** " +
			"`jira issue rank KEY-1 KEY-2 --before KEY-9` leaves KEY-1 above " +
			"KEY-2, both above KEY-9.\n\n" +
			"The command sends at most 50 distinct issues in one ordered request. " +
			"The anchor must differ from every issue being moved. That is the endpoint's own shape and it " +
			"is what makes the ordering above mean anything: applying an anchored " +
			"move issue by issue would rank each one against a backlog the " +
			"previous move had already changed. It is also why there is no " +
			"`--parallelism` here — there is nothing to run in parallel.\n\n" +
			"Jira can accept some issues and refuse others in that one response. " +
			"When it does, the entries for the refused issues carry the reason " +
			"and the rest still report success — the moves that landed are real. " +
			"The native breakdown must identify every requested issue exactly once. " +
			"An unusable acknowledgement reports `rank_response_invalid`; inspect " +
			"the backlog before retrying because issues may already have moved. " +
			"Writes are never automatically replayed. Total failure, cancellation " +
			"and timeout retain the ordered outcomes and anchor.\n\n" +
			"Ranking needs a board that has ranking enabled, and the issues have " +
			"to be on it. An issue in a project with no board cannot be ranked " +
			"at all, and Jira says so in its own words.\n\n" +
			"`--dry-run` reports the move and stops. It never contacts Jira, so " +
			"it cannot tell you whether the issues or the anchor exist, or " +
			"whether they share a board.\n\n" +
			"`--web` is refused here. It opens one page and this command writes " +
			"to a list of keys.",
		Example: "# put one issue above another\n" +
			"$ jira issue rank KEY-123 --before KEY-999\n\n" +
			"# and below\n" +
			"$ jira issue rank KEY-123 --after KEY-999\n\n" +
			"# several, keeping the order you list them in\n" +
			"$ jira issue rank KEY-123 KEY-124 KEY-125 --before KEY-999\n\n" +
			"# preview the move without sending it\n" +
			"$ jira issue rank KEY-123 --before KEY-999 --dry-run",
		Args: command.ArgSpec{Min: 1, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			// WithoutParallelism, and the reason is stronger than the
			// registrar's own example: `rank` does not merely need its keys
			// in order, it sends them in ONE request. There is no fan-out
			// for a width to bound, so a --parallelism here would be a flag
			// with nothing to act on.
			command.AddKeyedFlags(c, &keyed, command.WithoutParallelism(
				"rank sends every issue in a single request, so there is no fan-out to widen"))
			command.AddMutationFlags(c, &mut)
			command.AddStringVar(c, &before, flagBefore, "",
				"Put the issues immediately above this one",
				command.FlagOpts{Terse: "Rank above", Placeholder: "key", Complete: "predictor=issuekey"})
			command.AddStringVar(c, &after, flagAfter, "",
				"Put the issues immediately below this one",
				command.FlagOpts{Terse: "Rank below", Placeholder: "key", Complete: "predictor=issuekey"})
		},
		Validate: func() error {
			if err := keyed.Validate(); err != nil {
				return err
			}
			if err := requireOneAnchor(before, after); err != nil {
				return err
			}
			return refuseWebOnKeyedWrite(cmd)
		},
		Human:         renderRank,
		RetainOnError: func(out envelope.IssueRankOutput) bool { return len(out.Results) > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueRankOutput, error) {
			return runRank(ctx, deps, inv, before, after)
		},
	})
}

// requireOneAnchor refuses zero anchors and both at once.
//
// Two refusals in one function because they are one rule — exactly one
// anchor — and splitting them would let a future edit satisfy half of
// it. Neither has a sensible default: an issue cannot go both sides of
// the same anchor, and picking a side for a caller who named none would
// move their backlog in a direction they did not choose.
func requireOneAnchor(before, after string) error {
	switch {
	case before == "" && after == "":
		e := cli.NewInputError(cli.CodeFlagRequired, fmt.Sprintf(
			"say where the issues should go with --%s or --%s, naming the issue to move them relative to",
			flagBefore, flagAfter,
		))
		e.Field = flagBefore
		return e
	case before != "" && after != "":
		e := cli.NewInputError(cli.CodeFlagConflict, fmt.Sprintf(
			"--%s and --%s put the issues on opposite sides of the anchor, and they cannot go both — pick one and drop the other",
			flagBefore, flagAfter,
		))
		e.Field = flagBefore
		return e
	}
	return nil
}

// runRank composes the one request and either previews it or sends it.
//
// The array order is the authored ranking, so the shared client receives the
// whole batch once. Local validation and preview construct no client. Joined
// acknowledgements survive a deadline or cancellation from the spinner owner.
func runRank(ctx context.Context, deps Deps, inv command.Invocation, before, after string) (envelope.IssueRankOutput, error) {
	var zero envelope.IssueRankOutput

	anchor, position := before, envelope.RankPositionBefore
	if after != "" {
		anchor, position = after, envelope.RankPositionAfter
	}
	out := envelope.IssueRankOutput{
		MutationState: envelope.MutationState{DryRun: inv.DryRun},
		Anchor:        anchor,
		Position:      position,
	}
	req := jira.RankRequest{Issues: inv.Args, RankBeforeIssue: before, RankAfterIssue: after}
	if err := core.ValidateRankRequest(req); err != nil {
		e := cli.NewInputError(cli.CodeArgValueInvalid, err.Error())
		e.Field = "ISSUE_KEY"
		if input, ok := errors.AsType[*core.RankRequestError](err); ok {
			switch input.Field {
			case "rankBeforeIssue":
				e.Code, e.Field = cli.CodeFlagValueInvalid, flagBefore
			case "rankAfterIssue":
				e.Code, e.Field = cli.CodeFlagValueInvalid, flagAfter
			}
		}
		return zero, e
	}
	if inv.DryRun {
		out.Results = rankPreviewResults(inv.Args)
		return out, nil
	}

	client, err := deps.JiraClient()
	if err != nil {
		return zero, err
	}
	write, err := command.Spin(ctx, rankSpinnerLabel(inv.Args, position, anchor), func(ctx context.Context) (core.RankWrite, error) {
		return client.RankIssues(ctx, req)
	})
	if write.StatusCode == http.StatusNoContent || write.StatusCode == http.StatusMultiStatus {
		var receiptErr error
		out.Results, receiptErr = rankResults(inv.Args, write)
		if err != nil {
			return out, err
		}
		return out, receiptErr
	}
	if err == nil {
		err = core.ErrRankResponseInvalid
	}
	out.Results = rankFailedResults(inv.Args, err)
	return out, err
}

// rankResults validates complete native attribution before publishing moves.
// An uncertain acknowledgement fails every target without guessing a subset.
func rankResults(keys []string, write core.RankWrite) ([]envelope.KeyedResult[envelope.IssueRankData], error) {
	ordered, err := core.OrderedRankOutcomes(keys, write)
	if err != nil {
		return rankFailedResults(keys, err), err
	}
	results := rankPreviewResults(keys)
	raw := make([]error, len(keys))
	for i, outcome := range ordered {
		if outcome.Status < http.StatusMultipleChoices {
			continue
		}
		cause := &adapterjira.APIError{StatusCode: outcome.Status, Messages: outcome.Errors, Retry: write.Retry}
		class := cli.MapError(cause)
		message := class.Message
		if len(outcome.Errors) > 0 {
			message = outcome.Errors[0]
		}
		results[i].OK = false
		results[i].Data = nil
		results[i].Err = &envelope.KeyError{Code: class.Code, Message: message, Hint: class.Hint}
		raw[i] = cause
	}
	return results, command.KeyedFailure(results, raw)
}

// rankFailedResults retains every authored target when its move is unconfirmed.
func rankFailedResults(keys []string, err error) []envelope.KeyedResult[envelope.IssueRankData] {
	class := cli.MapError(err)
	return xslices.Map(keys, func(key string) envelope.KeyedResult[envelope.IssueRankData] {
		return envelope.KeyedResult[envelope.IssueRankData]{Issue: envelope.IssueRef{Key: key}, Err: &envelope.KeyError{Code: class.Code, Message: class.Message, Hint: class.Hint}}
	})
}

// rankPreviewResults builds the per-key entries for a dry run —
// previewResults' shape, for its reason.
func rankPreviewResults(keys []string) []envelope.KeyedResult[envelope.IssueRankData] {
	results := make([]envelope.KeyedResult[envelope.IssueRankData], len(keys))
	for i, key := range keys {
		results[i] = envelope.KeyedResult[envelope.IssueRankData]{
			Issue: envelope.IssueRef{Key: key},
			OK:    true,
			Data:  &envelope.IssueRankData{},
		}
	}
	return results
}

// rankSpinnerLabel is the one spinner label. Present tense, and reached
// only on the live path — runRank returns before it on a preview.
func rankSpinnerLabel(keys []string, position, anchor string) string {
	if len(keys) == 1 {
		return fmt.Sprintf("Moving %s %s %s", keys[0], position, anchor)
	}
	return fmt.Sprintf("Moving %d issues %s %s", len(keys), position, anchor)
}

// renderRank writes the human view. Preview tense is structural (axis
// H2): the branches share no sentence.
func renderRank(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueRankOutput)
	if !ok {
		return fmt.Errorf("issue.rank: human renderer received %T, want envelope.IssueRankOutput", env.Data)
	}
	keys := weblinkKeys(out.Results)
	if out.DryRun {
		v.Line(fmt.Sprintf("Would move %s %s %s.",
			joinKeys(keys), cli.Text(out.Position), cli.Entity(out.Anchor)))
		v.Hint("Nothing was moved. Drop --dry-run to apply it.")
		return nil
	}
	renderRankResults(v, out)
	return nil
}

// renderRankResults draws the live view: one line per key, in the order
// they were placed.
func renderRankResults(v *output.View, out envelope.IssueRankOutput) {
	anchor := cli.Entity(out.Anchor)
	for _, r := range out.Results {
		key := cli.Entity(r.Issue.Key)
		if !r.OK {
			v.Line(key + cli.Text(": "+r.Err.Message))
			v.Hint(r.Err.Hint)
			continue
		}
		v.Line(key + cli.Text(": moved "+out.Position+" ") + anchor + cli.Text("."))
	}
}
