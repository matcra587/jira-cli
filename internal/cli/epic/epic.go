package epic

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/gechr/primer/table"
	xslices "github.com/gechr/x/slices"
	xstrings "github.com/gechr/x/strings"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

const epicJQL = "issuetype = Epic"

type Deps struct {
	JiraClient func(ctx context.Context) (core.JiraClient, error)
}

type options struct {
	walk     command.SearchWalk
	cursor   string
	projects []string
	query    string
	cmd      *cobra.Command
}

func New(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use: "epic", Short: "Browse epics and manage child membership", Args: cobra.NoArgs,
		Long: "Search visible epics, inspect child status counts and manage explicit child issues using Jira's parent relation.", Example: "$ jira epic list --output=json",
	}
	cmd.AddCommand(newList(deps), newBoard(deps), newAdd(deps), newRemove(deps))
	return cmd
}

const pagingHelp = "Results default to 50 epics. `--limit` is a total count; each request asks for at most 100 issues. " +
	"`--all` drains up to 50 pages or 5,000 epics; `--all --unbounded` lifts those caps and the 50-consecutive-empty-page guard. " +
	"`--next-page-token` resumes the same query. Repeat `--project` to restrict visible epics. " +
	"Pagination retains Jira's final-page and resume facts; safety caps and unusable cursors report explicit truncation. " +
	"Later errors retain validated results. Visibility depends on permissions."

func (o *options) flags(cmd *cobra.Command) {
	o.cmd = cmd
	command.AddIntVar(cmd, &o.walk.Limit, "limit", 50, "Maximum epics across all requested pages", command.FlagOpts{Terse: "Epic limit", Placeholder: "n"})
	command.AddBoolVar(cmd, &o.walk.All, "all", false, "Drain epic pages up to 50 pages or 5,000 epics", command.FlagOpts{Terse: "Drain epics"})
	command.AddBoolVar(cmd, &o.walk.Unbounded, "unbounded", false, "With --all, lift search safety caps", command.FlagOpts{Terse: "Lift search caps"})
	command.AddStringVar(cmd, &o.cursor, "next-page-token", "", "Resume the same query from a reported cursor", command.FlagOpts{Terse: "Resume cursor", Placeholder: "token"})
	command.AddStringArrayVar(cmd, &o.projects, "project", nil, "Restrict epics to these project keys (repeatable)", command.FlagOpts{Terse: "Projects", Placeholder: "key", Complete: "predictor=cacheproject"})
}

func (o *options) validate() error {
	if o.walk.Unbounded && !o.walk.All {
		return cli.NewInputError(cli.CodeFlagConflict, "--unbounded requires --all")
	}
	if o.walk.All && o.cmd.Flags().Changed("limit") {
		return cli.NewInputError(cli.CodeFlagConflict, "--all conflicts with explicit --limit")
	}
	if o.walk.Limit < 1 {
		err := cli.NewInputError(cli.CodeFlagValueInvalid, "--limit must be positive")
		err.Field = "limit"
		return err
	}
	o.query = epicJQL
	if len(o.projects) == 0 {
		return nil
	}
	quoted := make([]string, 0, len(o.projects))
	for _, value := range o.projects {
		value = strings.TrimSpace(value)
		if value == "" {
			return projectError("project must not be blank")
		}
		q, err := command.QuoteJQL(value)
		if err != nil {
			return projectError(err.Error())
		}
		quoted = append(quoted, q)
	}
	o.query += " AND project in (" + strings.Join(quoted, ", ") + ")"
	return nil
}

func projectError(message string) error {
	err := cli.NewInputError(cli.CodeFlagValueInvalid, message)
	err.Field = "project"
	return err
}

func newList(deps Deps) *cobra.Command {
	o := options{}
	return command.MustNew(command.Spec[envelope.EpicListOutput]{
		Op: "epic.list", Identity: true, Passthrough: true, Use: "list", Short: "List visible epics",
		Long:    pagingHelp,
		Example: "$ jira epic list\n\n# Restrict and drain visible epics\n$ jira epic list --project KEY --all --output=json",
		Flags:   o.flags, Validate: o.validate, Human: renderList,
		RetainOnError: func(out envelope.EpicListOutput) bool { return len(out.Epics) > 0 },
		Run: func(ctx context.Context, _ command.Invocation) (envelope.EpicListOutput, error) {
			return runList(ctx, deps, o)
		},
	})
}

func newBoard(deps Deps) *cobra.Command {
	o := options{}
	return command.MustNew(command.Spec[envelope.EpicBoardOutput]{
		Op: "epic.board", Identity: true, Passthrough: true, Use: "board", Short: "Count each epic's visible children by status",
		Long: pagingHelp + "\n\nEach returned epic has an independent `parent = KEY` child search, capped at 50 pages or 5,000 children. " +
			"`--all --unbounded` lifts child caps too. Child searches run sequentially and stop on the first error, retaining validated counts. " +
			"Counts use actual status names, including custom statuses, with zero buckets for To Do, In Progress and Done. " +
			"Missing or blank child status is counted in `unknown_status` and makes counts incomplete. " +
			"Unattempted rows carry null counts. `counts_complete` requires complete epic and child searches with no unknown status. " +
			"These reads are independent search-index snapshots. Jira can delay indexing recent parent or status edits; a completed search is not a transactionally current issue snapshot.",
		Example: "$ jira epic board\n\n# Inspect complete visible project counts\n$ jira epic board --project KEY --all --output=json",
		Flags:   o.flags, Validate: o.validate, Human: renderBoard, Warnings: boardWarnings,
		RetainOnError: func(out envelope.EpicBoardOutput) bool { return len(out.Epics) > 0 },
		Run: func(ctx context.Context, _ command.Invocation) (envelope.EpicBoardOutput, error) {
			return runBoard(ctx, deps, o)
		},
	})
}

func searchEpics(ctx context.Context, client core.JiraClient, o options) (envelope.IssueListOutput, error) {
	return command.WalkIssues(ctx, client, o.query, []string{"summary", "status"}, o.cursor, o.walk)
}

func runList(ctx context.Context, deps Deps, o options) (envelope.EpicListOutput, error) {
	out := envelope.EpicListOutput{Epics: []envelope.IssueView{}, JQL: o.query}
	client, err := deps.JiraClient(ctx)
	if err != nil {
		return out, err
	}
	read, err := command.Spin(ctx, "Searching epics", func(ctx context.Context) (envelope.IssueListOutput, error) {
		return searchEpics(ctx, client, o)
	})
	out.Epics, out.Pagination = read.Issues, read.Pagination
	out.Truncated, out.TruncatedReason = read.Truncated, read.TruncatedReason
	return out, err
}

func zeroCounts() map[string]int {
	return map[string]int{"To Do": 0, "In Progress": 0, "Done": 0}
}

func runBoard(ctx context.Context, deps Deps, o options) (envelope.EpicBoardOutput, error) {
	out := envelope.EpicBoardOutput{Epics: []envelope.EpicBoardRow{}, JQL: o.query, Totals: zeroCounts()}
	client, err := deps.JiraClient(ctx)
	if err != nil {
		return out, err
	}
	_, err = command.Spin(ctx, "Counting epic children", func(ctx context.Context) (struct{}, error) {
		read, err := searchEpics(ctx, client, o)
		out.Pagination, out.Truncated, out.TruncatedReason = read.Pagination, read.Truncated, read.TruncatedReason
		for _, epic := range read.Issues {
			out.Epics = append(out.Epics, envelope.EpicBoardRow{IssueView: epic})
		}
		if err != nil {
			return struct{}{}, err
		}
		for i := range out.Epics {
			if err := ctx.Err(); err != nil {
				return struct{}{}, err
			}
			row := &out.Epics[i]
			key, err := command.QuoteJQL(row.Key)
			if err != nil {
				failure := cli.NewCodedError(cli.CodeSearchResponseInvalid, "epic search returned an unsafe parent key")
				classification := cli.MapError(failure)
				// No child search was attempted, but this row still owns the
				// validation failure rather than looking like a pending row.
				row.Children.Error = &envelope.Error{Code: classification.Code, Message: classification.Message, Hint: classification.Hint}
				return struct{}{}, failure
			}
			row.Children.Attempted = true
			row.Counts = zeroCounts()
			children, err := command.WalkIssues(ctx, client, "parent = "+key, []string{"status"}, "", command.SearchWalk{All: true, Unbounded: o.walk.Unbounded})
			row.Children.Pagination = children.Pagination
			row.Children.Truncated, row.Children.TruncatedReason = children.Truncated, children.TruncatedReason
			for _, child := range children.Issues {
				status := child.Fields.Status
				if !status.Set || status.Null || !status.Value.Name.Set || status.Value.Name.Null || xstrings.IsBlank(status.Value.Name.Value) {
					row.Children.UnknownStatus++
					out.UnknownStatus++
					continue
				}
				name := status.Value.Name.Value
				row.Counts[name]++
				out.Totals[name]++
			}
			row.Children.Complete = err == nil && children.Pagination.IsLast && !children.Truncated && row.Children.UnknownStatus == 0
			if err != nil {
				failure := cli.MapError(err)
				row.Children.Error = &envelope.Error{Code: failure.Code, Message: failure.Message, Hint: failure.Hint}
				return struct{}{}, err
			}
		}
		out.CountsComplete = read.Pagination.IsLast && !read.Truncated && out.UnknownStatus == 0 &&
			!slices.ContainsFunc(out.Epics, func(row envelope.EpicBoardRow) bool { return !row.Children.Complete })
		return struct{}{}, nil
	})
	return out, err
}

func boardWarnings(out envelope.EpicBoardOutput) []envelope.Warning {
	if out.CountsComplete {
		return nil
	}
	return []envelope.Warning{{Type: "epic_counts_incomplete", Message: "Epic status totals are incomplete; inspect epic pagination and each row's children state.", Lossy: true}}
}

func epicColumns() []table.Column[envelope.IssueView] {
	return []table.Column[envelope.IssueView]{
		{Name: "key", Header: "KEY", Render: func(row envelope.IssueView, _ *table.RenderContext) table.Cell {
			return table.TextCell(cli.Text(row.Key))
		}},
		{Name: "summary", Header: "SUMMARY", Flex: true, Render: func(row envelope.IssueView, _ *table.RenderContext) table.Cell {
			return table.StyledCell(cli.OrDash(cli.Entity(row.Fields.Summary.Value)), cli.Text(row.Fields.Summary.Value))
		}},
		{Name: "status", Header: "STATUS", Render: func(row envelope.IssueView, _ *table.RenderContext) table.Cell {
			return table.TextCell(cli.OrDash(cli.Text(row.Fields.Status.Value.Name.Value)))
		}},
	}
}

func renderList(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.EpicListOutput)
	if !ok {
		return fmt.Errorf("epic.list: expected EpicListOutput, got %T", env.Data)
	}
	if len(out.Epics) == 0 {
		v.Line("No visible epics.")
	} else {
		output.Table(v, cli.ResolvedTheme(), epicColumns(), out.Epics)
	}
	searchHint(v, out.Pagination, out.Truncated, out.TruncatedReason)
	return nil
}

func renderBoard(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.EpicBoardOutput)
	if !ok {
		return fmt.Errorf("epic.board: expected EpicBoardOutput, got %T", env.Data)
	}
	if len(out.Epics) == 0 {
		v.Line("No visible epics.")
	} else {
		output.Table(v, cli.ResolvedTheme(), []table.Column[envelope.EpicBoardRow]{
			{Name: "key", Header: "KEY", Render: func(row envelope.EpicBoardRow, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.Text(row.Key))
			}},
			{Name: "summary", Header: "SUMMARY", Flex: true, Render: func(row envelope.EpicBoardRow, _ *table.RenderContext) table.Cell {
				return table.StyledCell(cli.OrDash(cli.Entity(row.Fields.Summary.Value)), cli.Text(row.Fields.Summary.Value))
			}},
			{Name: "counts", Header: "CHILD COUNTS", Flex: true, Render: func(row envelope.EpicBoardRow, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.Text(formatCounts(row.Counts)))
			}},
			{Name: "complete", Header: "COMPLETE", Render: func(row envelope.EpicBoardRow, _ *table.RenderContext) table.Cell {
				return table.TextCell(strconv.FormatBool(row.Children.Complete))
			}},
		}, out.Epics)
	}
	v.Line(cli.Text("Totals: " + formatCounts(out.Totals)))
	if !out.CountsComplete {
		v.Hint("Counts are incomplete. Inspect machine output for per-epic child pagination, unknown statuses and failures.")
	}
	searchHint(v, out.Pagination, out.Truncated, out.TruncatedReason)
	return nil
}

func formatCounts(counts map[string]int) string {
	if counts == nil {
		return "not attempted"
	}
	names := slices.Sorted(maps.Keys(counts))
	parts := xslices.Map(names, func(name string) string { return name + ": " + strconv.Itoa(counts[name]) })
	return strings.Join(parts, "; ")
}

func searchHint(v *output.View, page envelope.CursorPagination, truncated bool, reason string) {
	if truncated {
		v.Hint("Epic search is incomplete (" + reason + "). Inspect the reported cursor before resuming.")
	} else if !page.IsLast {
		v.Hint("More epics remain. Use --all or resume with pagination.next_page_token from machine output.")
	}
}
