package issue

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gechr/x/shell"
	xslices "github.com/gechr/x/slices"
	"github.com/spf13/cobra"

	adapter "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

type moveFlags struct {
	input    string
	mutation command.MutationFlags
	poll     bulkPollFlags
	cmd      *cobra.Command
}

func newMove(deps Deps) *cobra.Command {
	var flags moveFlags
	var keyed command.KeyedFlags
	cmd := command.MustNew(command.Spec[envelope.IssueMoveOutput]{
		Op: "issue.move", WritesJira: true, Identity: true, Keyed: true, Passthrough: true,
		Use: "move ISSUE_KEY...", Short: "Move explicit issues with a native bulk payload",
		Long: "Submit one native bulk move job for explicit issue keys. --json-input reads a complete targetToSourcesMapping request; - reads standard input. Destinations use project,issueType or project,issueType,parent. Preserve native mandatory-field/status mappings and unknown extensions. The payload must name exactly the positional issues, through keys or their remotely resolved IDs. Keys, comma lists and ranges expand to at most 1,000 distinct keys; Jira additionally counts subtasks.\n\n" +
			"--dry-run publishes the exact payload and targets without constructing a client. Numeric payload IDs require remote binding; a local preview reports bindings_validated=false rather than guessing. Add --validate-remote to read the named issues and check binding without submitting. Every live submission verifies identity binding first. This checks source identities only; Jira validates destination compatibility, workflow and mandatory fields when processing the request.\n\n" +
			"A move can change project, type, workflow and field values. It requires confirmation; scripts and agents must pass --force. Previews never prompt. The POST is submitted once and never automatically replayed. Its 201 acknowledges a job, not moved issues. Without --wait, return that receipt immediately. With --wait, bounded reads observe terminal progress; only explicit processed issue IDs confirm application. Failed/inaccessible outcomes remain visible. Original issue keys remain in ordered results, with stable IDs when resolved.\n\n" +
			"Cancellation and later read failures retain the task identity and last valid progress. Resume with `jira issue move status TASK_ID`, never by repeating the submission. A polling bound returns poll_limit_reached=true and does not claim completion. Native notification behavior belongs to sendBulkNotification in the payload. Use `jira issue move fields` for the historical confirmed fields/update PUT workflow; native bulk migration requires targetToSourcesMapping.",
		Example: "$ jira issue move KEY-1 --json-input move.json --dry-run --output=json\n\n$ jira issue move KEY-1 --json-input move.json --force --wait --max-polls 60 --poll-interval 1s --output=json",
		Args:    command.ArgSpec{Min: 1, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}}},
		Flags: func(cmd *cobra.Command) {
			flags.cmd = cmd
			command.AddKeyedFlags(cmd, &keyed, command.WithoutParallelism("one native bulk job is submitted; identity reads are bounded and serial"))
			command.AddMutationFlags(cmd, &flags.mutation, command.WithForce(), command.WithValidateRemote())
			command.AddStringVar(cmd, &flags.input, command.FlagJSONInput, "", "Read the whole native bulk move request from a file; - reads standard input", command.FlagOpts{Terse: "Native move payload", Placeholder: "path"})
			command.AddHiddenAliases(cmd, command.ADFRouteAliases())
			flags.poll.register(cmd)
		},
		Validate: func() error {
			if err := resolvePayloadAliases(flags.cmd.Flags()); err != nil {
				return err
			}
			if flags.input == "" {
				err := cli.NewInputError(cli.CodeFlagRequired, "--json-input is required; provide a native targetToSourcesMapping move request")
				err.Field = command.FlagJSONInput
				return err
			}
			if err := flags.poll.validate(); err != nil {
				return err
			}
			return refuseWebOnKeyedWrite(flags.cmd)
		},
		Human: renderMove, Warnings: moveWarnings,
		RetainOnError: func(out envelope.IssueMoveOutput) bool { return len(out.Results) > 0 || out.Acknowledged },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueMoveOutput, error) {
			return runMove(ctx, deps, inv, flags)
		},
	})
	cmd.AddCommand(newMoveStatus(deps), newMoveFields(deps))
	return cmd
}

func moveInputError(err error) error {
	e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf("--json-input: %v", err))
	e.Field = command.FlagJSONInput
	return e
}

// bindMoveSources checks the complete authored set, including aliases that a
// read bound to the same stable ID. Nil aliases deliberately cannot resolve
// numeric IDs during a local preview; unknown issue keys are still refused.
func bindMoveSources(keys []string, targets []jira.IssueMoveTarget, aliases map[string]string) (map[string]string, bool, error) {
	if len(keys) != len(targets) {
		return nil, false, moveInputError(errors.New("payload must declare exactly one source for each positional issue"))
	}
	known := make(map[string]string, len(keys))
	for _, key := range keys {
		known[key] = key
	}
	for alias, key := range aliases {
		known[alias] = key
	}
	destinations := make(map[string]string, len(keys))
	needsRemote := false
	for _, target := range targets {
		source := target.Source
		number, err := strconv.ParseInt(source, 10, 64)
		if err == nil && number > 0 && strconv.FormatInt(number, 10) == source {
			if aliases == nil {
				needsRemote = true
				continue
			}
		} else {
			key, err := command.ParseIssueKey(source)
			if err != nil {
				return nil, false, moveInputError(errors.New("move sources must be explicit issue keys or canonical positive IDs"))
			}
			source = key
		}
		key, ok := known[source]
		if !ok {
			return nil, false, moveInputError(fmt.Errorf("payload source %q is not one of the positional issues", target.Source))
		}
		if _, exists := destinations[key]; exists {
			return nil, false, moveInputError(fmt.Errorf("payload names issue %q more than once through keys or IDs", key))
		}
		destinations[key] = target.Destination
	}
	return destinations, !needsRemote, nil
}

func runMove(ctx context.Context, deps Deps, inv command.Invocation, flags moveFlags) (envelope.IssueMoveOutput, error) {
	out := envelope.IssueMoveOutput{MutationState: envelope.MutationState{DryRun: inv.DryRun}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	keys, err := command.ParseIssueKeys(inv.Args)
	if err != nil {
		return out, err
	}
	data, err := command.ReadInputFileContext(ctx, command.FlagJSONInput, flags.input, inv.Stdin, inv.StdinIsTerminal)
	if err != nil {
		return out, err
	}
	req, err := jira.ParseIssueMovePayload(data)
	if err != nil {
		return out, moveInputError(err)
	}
	targets, err := req.MoveBindings()
	if err != nil {
		return out, moveInputError(err)
	}
	out.Payload, err = jira.MarshalNoEscape(req)
	if err != nil {
		return out, err
	}
	out.Targets = xslices.Map(targets, func(t jira.IssueMoveTarget) envelope.IssueMoveTarget {
		return envelope.IssueMoveTarget{Source: t.Source, Destination: t.Destination}
	})
	destinations, validated, err := bindMoveSources(keys, targets, nil)
	if err != nil {
		return out, err
	}
	out.BindingsValidated = validated
	out.Results = xslices.Map(keys, func(key string) envelope.KeyedResult[envelope.IssueMoveData] {
		return envelope.KeyedResult[envelope.IssueMoveData]{Issue: envelope.IssueRef{Key: key}, OK: true, Data: &envelope.IssueMoveData{Outcome: "planned", Destination: destinations[key]}}
	})
	if inv.DryRun && !flags.mutation.ValidateRemote {
		return out, nil
	}
	if !inv.DryRun && !inv.Interactive {
		if err := confirmMove(ctx, deps, inv, keys, flags.mutation.Force); err != nil {
			return out, err
		}
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	client, err := deps.JiraClient()
	if err != nil {
		return out, err
	}
	aliases := make(map[string]string, len(keys)*2)
	for i, key := range keys {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		out.ReadStarted = true
		read, err := client.GetIssue(ctx, key, []string{"summary"})
		if err != nil {
			return out, err
		}
		if read.Issue == nil || !read.Issue.ID.Set || read.Issue.ID.Null || !read.Issue.Key.Set || read.Issue.Key.Null {
			return out, cli.NewCodedError(cli.CodeBulkOperationResponseInvalid, "move identity read lacks an issue ID or key")
		}
		id := read.Issue.ID.Value
		number, err := strconv.ParseInt(id, 10, 64)
		if err != nil || number <= 0 || strconv.FormatInt(number, 10) != id {
			return out, cli.NewCodedError(cli.CodeBulkOperationResponseInvalid, "move identity read has an invalid issue ID")
		}
		resolved, err := command.ParseIssueKey(read.Issue.Key.Value)
		if err != nil {
			return out, cli.NewCodedError(cli.CodeBulkOperationResponseInvalid, "move identity read has an invalid issue key")
		}
		for _, alias := range []string{id, resolved} {
			if previous, exists := aliases[alias]; exists && previous != key {
				return out, moveInputError(errors.New("positional keys resolve to a repeated issue identity"))
			}
			aliases[alias] = key
		}
		out.Results[i].Issue.ID = id
		if read.Issue.Self.Set && !read.Issue.Self.Null {
			out.Results[i].Issue.Self = read.Issue.Self.Value
		}
	}
	destinations, validated, err = bindMoveSources(keys, targets, aliases)
	if err != nil {
		return out, err
	}
	out.BindingsValidated = validated
	out.RemoteValidated = true
	for i, key := range keys {
		out.Results[i].Data.Destination = destinations[key]
	}
	if inv.DryRun {
		return out, nil
	}
	if inv.Interactive {
		if err := confirmMove(ctx, deps, inv, keys, flags.mutation.Force); err != nil {
			return out, err
		}
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out.Attempted = true
	submission, err := client.SubmitIssueMove(ctx, req)
	out.Acknowledged = submission.Acknowledged
	if !submission.Acknowledged {
		if err == nil {
			err = cli.NewCodedError(cli.CodeBulkOperationResponseInvalid, "move submission returned no acknowledgement")
		}
		for i, key := range keys {
			out.Results[i] = moveFailure(envelope.IssueRef{Key: key, ID: out.Results[i].Issue.ID}, err)
		}
		return out, err
	}
	for i := range out.Results {
		out.Results[i].Data.Outcome = "accepted"
	}
	if err == nil && submission.Task == nil {
		err = errors.New("acknowledged move returned no task receipt")
	}
	if err == nil {
		err = submission.Task.ValidateIdentity()
	}
	if err == nil {
		err = adapter.ValidateBulkTaskID(submission.Task.TaskID.Value)
	}
	if err != nil {
		out.ReceiptError = moveKeyError(cli.NewCodedError(cli.CodeBulkOperationResponseInvalid, err.Error()))
		return out, nil
	}
	out.Receipt = submission.Task
	out.Task = &envelope.BulkTaskRef{ID: submission.Task.TaskID.Value}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !flags.poll.wait {
		return out, nil
	}
	err = pollBulk(ctx, client, &out.BulkTaskState, flags.poll)
	if out.Terminal {
		moveTerminalResults(&out)
	}
	if err != nil {
		return out, err
	}
	raw := make([]error, len(out.Results))
	for i, row := range out.Results {
		if !row.OK {
			raw[i] = cli.NewCodedError(row.Err.Code, row.Err.Message)
		}
	}
	return out, command.KeyedFailure(out.Results, raw)
}

func confirmMove(ctx context.Context, deps Deps, inv command.Invocation, keys []string, force bool) error {
	subject := joinPlainKeys(keys)
	err := confirmDestructive(ctx, deps.Confirm, inv, force, destructiveQuestion{
		Subject: subject, Risk: "moving " + subject + " can change project, type, workflow and field values, so it needs confirmation",
		Question:    []string{"Move ", subject, " using the authored native destinations?"},
		Caveat:      "Jira may migrate subtasks and discard values incompatible with the destination. Review the native payload before continuing.",
		Affirmative: "Move", Negative: "Keep",
	})
	if errors.Is(err, errDestructiveAborted) {
		return cli.NewCodedError(cli.CodeCanceled, "move canceled before submission")
	}
	return err
}

func moveKeyError(err error) *envelope.KeyError {
	c := cli.MapError(err)
	return &envelope.KeyError{Code: c.Code, Message: c.Message, Hint: c.Hint}
}

func moveFailure(ref envelope.IssueRef, err error) envelope.KeyedResult[envelope.IssueMoveData] {
	return envelope.KeyedResult[envelope.IssueMoveData]{Issue: ref, Err: moveKeyError(err)}
}

func moveTerminalResults(out *envelope.IssueMoveOutput) {
	processed := make(map[string]bool, len(out.Progress.ProcessedAccessibleIssues.Value))
	for _, id := range out.Progress.ProcessedAccessibleIssues.Value {
		processed[strconv.FormatInt(id, 10)] = true
	}
	for i, row := range out.Results {
		reasons, failed := out.Progress.FailedAccessibleIssues.Value[row.Issue.ID]
		switch {
		case processed[row.Issue.ID]:
			row.Data.Applied = true
			row.Data.Outcome = "processed"
		case failed:
			message := strings.Join(reasons, "; ")
			if message == "" {
				message = "Jira reports failure for this issue without a reason"
			}
			row = moveFailure(row.Issue, cli.NewCodedError(cli.CodeBulkOperationFailed, message))
		default:
			code := cli.CodeBulkOutcomeUnconfirmed
			if out.Progress.Status.Value != "COMPLETE" {
				code = cli.CodeBulkOperationFailed
			}
			row = moveFailure(row.Issue, cli.NewCodedError(code, "terminal task did not confirm this issue as processed"))
		}
		out.Results[i] = row
	}
}

func moveWarnings(out envelope.IssueMoveOutput) []envelope.Warning {
	warnings := []envelope.Warning{}
	if out.ReceiptError != nil {
		warnings = append(warnings, envelope.Warning{Type: "move_receipt_incomplete", Message: "The move was accepted but its task receipt is unusable. Inspect the issues before submitting another move."})
	}
	if out.PollLimitReached {
		warnings = append(warnings, envelope.Warning{Type: "move_poll_limit", Message: "The polling bound was reached. Resume task reads without repeating the move submission."})
	}
	if out.DryRun && !out.BindingsValidated {
		warnings = append(warnings, envelope.Warning{Type: "move_binding_unchecked", Message: "Numeric payload IDs cannot be bound to positional keys locally. Use --validate-remote to verify the complete target set."})
	}
	return warnings
}

func renderMove(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueMoveOutput)
	if !ok {
		return fmt.Errorf("issue.move: expected IssueMoveOutput, got %T", env.Data)
	}
	for _, row := range out.Results {
		key := cli.Entity(row.Issue.Key)
		switch {
		case !row.OK:
			v.Line(key + cli.Text(": "+row.Err.Message))
			v.Hint(row.Err.Hint)
		case out.DryRun:
			v.Line("Would move " + key + cli.Text(" using "+cli.OrDash(row.Data.Destination)) + ".")
		case row.Data.Applied:
			v.Line(key + ": Jira reports the move processed.")
		case out.Acknowledged:
			v.Line(key + ": move job accepted; issue outcome unconfirmed.")
		default:
			v.Line(key + ": not submitted.")
		}
	}
	if out.DryRun {
		v.Line(cli.Text("Payload: " + string(out.Payload)))
		v.Hint("Nothing was submitted. Review the payload, then pass --force to allow the move.")
	}
	if out.ReceiptError != nil {
		v.Hint(out.ReceiptError.Hint)
	}
	renderBulkState(v, out.BulkTaskState)
	return nil
}

func renderBulkState(v *output.View, state envelope.BulkTaskState) {
	if state.Task == nil {
		return
	}
	v.Fields(output.Field{Label: "Task", Value: cli.Entity(state.Task.ID)})
	if state.Progress != nil {
		v.Fields(output.Field{Label: "State", Value: cli.Text(state.Progress.Status.Value)})
	}
	if !state.Terminal {
		v.Hint("Resume with `jira issue move status --wait -- " + cli.Text(shell.Quote(state.Task.ID)) + "`. Do not repeat the submission.")
	}
}
