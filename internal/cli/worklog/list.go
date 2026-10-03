package worklog

import (
	"context"
	"math"

	xsync "github.com/gechr/x/sync"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

const (
	maxWorklogPages    = 50
	maxWorklogs        = 5000
	maxWorklogPageSize = 100
)

type listOptions struct {
	start, limit int
	all          bool
	keyed        command.KeyedFlags
	cmd          *cobra.Command
}

func newList(deps Deps) *cobra.Command {
	var o listOptions
	return command.MustNew(command.Spec[envelope.WorklogListOutput]{
		Op: "worklog.list", Identity: true, Keyed: true, Passthrough: true, Use: "list ISSUE_KEY...", Short: "Read worklogs with per-issue pagination",
		Long: "Fetch one worklog page per explicit issue (default 50 records). `--start` selects an offset and `--limit` a page size up to 100. " +
			"`--all` drains up to 50 pages or 5,000 records per issue and conflicts with explicit start/limit. " +
			"Results retain request order. Each worklog exposes its id and the complete native server record at fields, including raw ADF comments and nullable authors/times. " +
			"Incomplete walks retain validated earlier pages, report complete=false with a reason, and expose read_error for later failures. " +
			"An empty page before total is incomplete rather than proof of an empty issue. Offset lists are observations, not transaction snapshots.",
		Example: "$ jira worklog list KEY-123 --output=json\n\n# Drain several issues\n$ jira worklog list KEY-1 KEY-2 --all --parallelism=2\n\n# Resume a page\n$ jira worklog list KEY-123 --start=50 --limit=25",
		Args:    command.ArgSpec{Min: 1, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY"}}},
		Flags: func(cmd *cobra.Command) {
			o.cmd = cmd
			command.AddKeyedFlags(cmd, &o.keyed)
			command.AddIntVar(cmd, &o.start, "start", 0, "Zero-based worklog offset", command.FlagOpts{Terse: "Page offset", Placeholder: "n"})
			command.AddIntVar(cmd, &o.limit, "limit", 50, "Worklogs per page, at most 100", command.FlagOpts{Terse: "Page size", Placeholder: "n"})
			command.AddBoolVar(cmd, &o.all, "all", false, "Drain up to 50 pages or 5,000 worklogs per issue", command.FlagOpts{Terse: "Drain worklogs"})
		},
		Validate: func() error {
			if err := o.keyed.Validate(); err != nil {
				return err
			}
			if o.start < 0 {
				return inputError("start", cli.NewInputError(cli.CodeFlagValueInvalid, "offset must be nonnegative"))
			}
			if o.limit < 1 || o.limit > maxWorklogPageSize {
				return inputError("limit", cli.NewInputError(cli.CodeFlagValueInvalid, "page size must be from 1 to 100"))
			}
			if o.all && (o.cmd.Flags().Changed("start") || o.cmd.Flags().Changed("limit")) {
				return cli.NewInputError(cli.CodeFlagConflict, "--all conflicts with explicit --start or --limit")
			}
			return nil
		},
		Human: renderList, Warnings: listWarnings, RetainOnError: func(out envelope.WorklogListOutput) bool { return len(out.Results) > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.WorklogListOutput, error) {
			return runList(ctx, deps, inv.Args, o)
		},
	})
}

type listOutcome struct {
	result envelope.KeyedResult[envelope.WorklogListData]
	err    error
}

func runList(ctx context.Context, deps Deps, inputs []string, o listOptions) (envelope.WorklogListOutput, error) {
	out := envelope.WorklogListOutput{}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	keys, err := command.ParseIssueKeys(inputs)
	if err != nil {
		return out, err
	}
	client, err := deps.JiraClient()
	if err != nil {
		return out, err
	}
	var outcomes []listOutcome
	_, spinErr := command.Spin(ctx, "Loading worklogs", func(ctx context.Context) (struct{}, error) {
		outcomes = make([]listOutcome, len(keys))
		xsync.Parallel(o.keyed.Parallelism, len(keys), func(i int) {
			key := keys[i]
			data, err := readWorklogs(ctx, client, key, o)
			outcomes[i].result.Issue.Key = key
			if err != nil && len(data.Worklogs) == 0 {
				outcomes[i].result.Err = keyError(err)
				outcomes[i].err = err
				return
			}
			if err != nil {
				data.Complete = false
				data.Truncated = true
				data.TruncatedReason = "read_failed"
				data.ReadError = keyError(err)
			}
			outcomes[i].result.OK = true
			outcomes[i].result.Data = &data
		})
		return struct{}{}, nil
	})
	if outcomes == nil {
		return out, spinErr
	}
	out.Results = make([]envelope.KeyedResult[envelope.WorklogListData], len(outcomes))
	raw := make([]error, len(outcomes))
	for i, o := range outcomes {
		out.Results[i] = o.result
		raw[i] = o.err
	}
	if spinErr != nil {
		return out, spinErr
	}
	return out, command.KeyedFailure(out.Results, raw)
}

func readWorklogs(ctx context.Context, client core.JiraClient, key string, o listOptions) (envelope.WorklogListData, error) {
	out := envelope.WorklogListData{Worklogs: []envelope.WorklogView{}}
	start, size := o.start, o.limit
	if o.all {
		start, size = 0, maxWorklogPageSize
	}
	out.Pagination.StartAt = start
	seen := make(map[string]struct{})
	for pages := 0; ; pages++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		read, err := client.GetWorklogs(ctx, key, start, size)
		if err != nil {
			return out, err
		}
		if read.Page == nil {
			return out, cli.NewCodedError(cli.CodeWorklogResponseInvalid, "worklog read returned no page")
		}
		page := read.Page
		if err := page.ValidatePage(start, size); err != nil {
			return out, cli.NewCodedError(cli.CodeWorklogResponseInvalid, err.Error())
		}
		if page.Total.Value > int64(math.MaxInt) || page.MaxResults.Value > int64(math.MaxInt) {
			return out, cli.NewCodedError(cli.CodeWorklogResponseInvalid, "worklog paging facts exceed platform integer range")
		}
		for _, w := range page.Worklogs.Value {
			if _, exists := seen[w.ID.Value]; exists {
				return out, cli.NewCodedError(cli.CodeWorklogResponseInvalid, "worklog walk repeats a record identity")
			}
		}
		for _, w := range page.Worklogs.Value {
			seen[w.ID.Value] = struct{}{}
			out.Worklogs = append(out.Worklogs, envelope.WorklogView{ID: w.ID.Value, Fields: w})
		}
		out.Pagination.MaxResults = int(page.MaxResults.Value)
		out.Pagination.Total = int(page.Total.Value)
		out.Pagination.Returned = len(out.Worklogs)
		count := len(page.Worklogs.Value)
		out.Complete = start >= out.Pagination.Total || count >= out.Pagination.Total-start
		if out.Complete {
			return out, nil
		}
		switch {
		case count == 0:
			out.TruncatedReason = "non_progress"
		case len(out.Worklogs) >= maxWorklogs:
			out.TruncatedReason = "max_worklogs"
		case pages+1 >= maxWorklogPages:
			out.TruncatedReason = envelope.TruncatedReasonMaxPages
		}
		if out.TruncatedReason != "" {
			out.Truncated = true
			return out, nil
		}
		if !o.all {
			return out, nil
		}
		start += count
	}
}

func listWarnings(out envelope.WorklogListOutput) []envelope.Warning {
	for _, r := range out.Results {
		if r.Data != nil && r.Data.Truncated {
			return []envelope.Warning{{Type: "worklog_read_incomplete", Message: "A worklog read is incomplete. Inspect per-issue pagination, truncation and read_error before resuming."}}
		}
	}
	return nil
}
