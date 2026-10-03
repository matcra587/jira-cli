package worklog

import (
	"context"
	"fmt"

	xslices "github.com/gechr/x/slices"
	xsync "github.com/gechr/x/sync"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func newAdd(deps Deps) *cobra.Command {
	var keyed command.KeyedFlags
	var mut command.MutationFlags
	var input inputFlags
	return command.MustNew(command.Spec[envelope.WorklogAddOutput]{
		Op: "worklog.add", WritesJira: true, Identity: true, Keyed: true, Passthrough: true,
		Use: "add ISSUE_KEY...", Short: "Record effort on explicit issues",
		Long: "Post one worklog per distinct issue. Keys, comma lists and inclusive ranges expand to at most 1,000 distinct keys. " +
			"Provide integer effort with `--time-spent`, using d/h/m components. A day uses workday_seconds (default 28,800 seconds), not 24 hours. " +
			"`--started` accepts ISO timestamps (offset optional), now, yesterday or duration ago. Local times use the local zone; explicit offsets are retained. " +
			"When absent, the current local time is captured once for the whole request. Relative duration days are 24 hours. " +
			"An optional comment comes from `--markdown`, `--markdown-file` or the native ADF comment in a whole `--json-input` payload. Markdown conversion reports losses. " +
			"Native JSON accepts timeSpentSeconds or timeSpent; legacy time_spent/comment_markdown members remain accepted. Effort and start flags explicitly replace those JSON values. Other native request members survive unchanged. " +
			"`--dry-run` publishes the exact POST payload and ordered targets without building a client or contacting Jira. " +
			"Live POSTs are never automatically replayed. Jira's 201 response supplies the stored record without another read. " +
			"An acknowledged creation with an unusable receipt remains applied and reports receipt_error; inspect `jira worklog list` before retrying. " +
			"`--notify=false` requests notification suppression; output reports the request, not delivery. Jira applies its default automatic remaining-estimate adjustment. " +
			"Use `jira api` for other estimate-adjustment policies. A failed key does not stop the others; only total failure fails the command.",
		Example: "$ jira worklog add KEY-123 --time-spent 2h30m --no-input\n\n# Preview time and a comment\n$ jira worklog add KEY-1:3 --time-spent 1d --started 2026-01-02T12:30:00Z --markdown 'Investigated the incident.' --dry-run --output=json",
		Args:    command.ArgSpec{Min: 1, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY"}}},
		Flags: func(cmd *cobra.Command) {
			command.AddKeyedFlags(cmd, &keyed)
			command.AddMutationFlags(cmd, &mut, command.WithNotify())
			input.register(cmd)
		},
		Validate: func() error {
			if err := keyed.Validate(); err != nil {
				return err
			}
			if err := mut.ResolveNotify(input.cmd.Flags()); err != nil {
				return err
			}
			return input.validate()
		},
		Human: renderAdd, Warnings: addWarnings, RetainOnError: func(out envelope.WorklogAddOutput) bool { return len(out.Results) > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.WorklogAddOutput, error) {
			return runAdd(ctx, deps, inv, input, keyed.Parallelism, mut.Notify)
		},
	})
}

func runAdd(ctx context.Context, deps Deps, inv command.Invocation, input inputFlags, parallelism int, notify bool) (envelope.WorklogAddOutput, error) {
	out := envelope.WorklogAddOutput{MutationState: envelope.MutationState{DryRun: inv.DryRun}, Notify: notify}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	keys, err := command.ParseIssueKeys(inv.Args)
	if err != nil {
		return out, err
	}
	day, err := deps.workday()
	if err != nil {
		return out, err
	}
	if day <= 0 {
		return out, cli.NewInputError(cli.CodeFlagValueInvalid, "workday_seconds must be positive")
	}
	out.WorkdaySeconds = day
	req, route, err := input.compose(ctx, deps, inv, day)
	if err != nil {
		return out, err
	}
	out.BodyRoute = route
	out.Payload, err = req.MarshalJSON()
	if err != nil {
		return out, err
	}
	if inv.DryRun {
		out.Results = xslices.Map(keys, func(key string) envelope.KeyedResult[envelope.WorklogAddData] {
			return envelope.KeyedResult[envelope.WorklogAddData]{Issue: envelope.IssueRef{Key: key}, OK: true, Data: &envelope.WorklogAddData{}}
		})
		return out, nil
	}
	client, err := deps.JiraClient()
	if err != nil {
		return out, err
	}
	var outcomes []addOutcome
	_, spinErr := command.Spin(ctx, "Adding worklogs", func(ctx context.Context) (struct{}, error) {
		outcomes = make([]addOutcome, len(keys))
		xsync.Parallel(parallelism, len(keys), func(i int) {
			key := keys[i]
			if err := ctx.Err(); err != nil {
				outcomes[i] = failedAdd(key, err)
				return
			}
			write, err := client.AddWorklog(ctx, key, req, notify)
			if write.Acknowledged {
				data := &envelope.WorklogAddData{Applied: true}
				if err == nil && write.Worklog != nil {
					err = write.Worklog.ValidateIdentity()
				}
				if err == nil && write.Worklog == nil {
					err = fmt.Errorf("acknowledged worklog has no record")
				}
				if err != nil {
					data.ReceiptError = keyError(cli.NewCodedError(cli.CodeWorklogResponseInvalid, err.Error()))
				} else {
					data.Worklog = &envelope.WorklogView{ID: write.Worklog.ID.Value, Fields: *write.Worklog}
				}
				outcomes[i].result = envelope.KeyedResult[envelope.WorklogAddData]{Issue: envelope.IssueRef{Key: key}, OK: true, Data: data}
				return
			}
			if err == nil {
				err = cli.NewCodedError(cli.CodeWorklogResponseInvalid, "worklog creation returned no acknowledgement")
			}
			outcomes[i] = failedAdd(key, err)
		})
		return struct{}{}, nil
	})
	if outcomes == nil {
		return out, spinErr
	}
	raw := make([]error, len(outcomes))
	out.Results = make([]envelope.KeyedResult[envelope.WorklogAddData], len(outcomes))
	for i, o := range outcomes {
		out.Results[i] = o.result
		raw[i] = o.err
	}
	if spinErr != nil {
		return out, spinErr
	}
	return out, command.KeyedFailure(out.Results, raw)
}

type addOutcome struct {
	result envelope.KeyedResult[envelope.WorklogAddData]
	err    error
}

func keyError(err error) *envelope.KeyError {
	c := cli.MapError(err)
	return &envelope.KeyError{Code: c.Code, Message: c.Message, Hint: c.Hint}
}

func failedAdd(key string, err error) addOutcome {
	return addOutcome{result: envelope.KeyedResult[envelope.WorklogAddData]{Issue: envelope.IssueRef{Key: key}, Err: keyError(err)}, err: err}
}

func addWarnings(out envelope.WorklogAddOutput) []envelope.Warning {
	for _, r := range out.Results {
		if r.Data != nil && r.Data.ReceiptError != nil {
			return []envelope.Warning{{Type: "worklog_receipt_incomplete", Message: "A worklog was created but its receipt is incomplete. Inspect worklog list before retrying."}}
		}
	}
	return nil
}
