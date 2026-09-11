package issue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"charm.land/huh/v2"
	clib "github.com/gechr/clib/cli/cobra"
	"github.com/gechr/x/ptr"
	xslices "github.com/gechr/x/slices"
	xstrings "github.com/gechr/x/strings"
	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/browser"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/boardscope"
	"github.com/matcra587/jira-cli/internal/cli/cmdutil"
	clijql "github.com/matcra587/jira-cli/internal/cli/jql"
	"github.com/matcra587/jira-cli/internal/config"
	editorpkg "github.com/matcra587/jira-cli/internal/editor"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/issuekey"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/jql"
	"github.com/matcra587/jira-cli/internal/pipeline"
	"github.com/spf13/cobra"
)

// NewCommand returns the `issue` verb and all its sub-commands.
func NewCommand() *cobra.Command {
	cmd := cmdutil.GroupCommand("issue", "Work with Jira issues", "resources")
	cmd.Long = "Read, create, and manage Jira issues across their lifecycle. `jira issue " +
		"list`, `jira issue mine`, and `jira issue view` read; `jira issue create`, `jira " +
		"issue edit`, and `jira issue transition` change; and the comment, link, attachment, " +
		"and watcher subcommands cover the metadata around an issue.\n\n" +
		"Subcommands honor the active profile's defaults (project, issue type, board) and " +
		"take `--output` for machine formats; every mutation accepts `--dry-run` to preview " +
		"without contacting Jira."
	cmd.Example = `$ jira issue list --assignee me

# Create an issue, printing the machine envelope
$ jira issue create --summary "Fix login" --output=json

# Advance an issue through its workflow
$ jira issue transition PROJ-12 "In Progress"`
	// Group the subcommands so `jira issue --help` reads as a task flow rather
	// than one flat list: what to read, what to change, and the metadata around
	// an issue (comments, links, attachments, watchers).
	cmd.AddGroup(
		&cobra.Group{ID: "read", Title: "Read"},
		&cobra.Group{ID: "write", Title: "Write"},
		&cobra.Group{ID: "manage", Title: "Manage"},
	)
	add := func(group string, sub *cobra.Command) {
		sub.GroupID = group
		cmd.AddCommand(sub)
	}
	add("read", issueListCommand())
	add("read", issueMineCommand())
	add("read", issueViewCommand())
	add("write", issueCreateCommand())
	add("write", issueEditCommand())
	add("write", issueTransitionCommand())
	add("write", issueRankCommand())
	add("write", destructiveIssueCommand("clone", "Clone an issue"))
	add("write", destructiveIssueCommand("move", "Move an issue"))
	add("write", destructiveIssueCommand("delete", "Delete an issue"))
	add("manage", issueCommentGroup())
	add("manage", issueLinkSubCommand())
	add("manage", issueWebLinkCommand())
	add("manage", IssueAttachmentCommand())
	for _, mk := range WatcherCommands {
		add("manage", mk())
	}
	return cmd
}

// issueMineCommand is a thin shorthand for `jira issue list --assignee me`.
// Shares the same runner as `issue list` so any future change to the list
// path (caching, output shape, …) propagates without diverging.
func issueMineCommand() *cobra.Command {
	var opts issueListOptions
	cmd := &cobra.Command{
		Use:   "mine",
		Short: "List issues assigned to you",
		Long: "List issues where the assignee is the current Jira user. Use it for the " +
			"common personal triage view without typing `--assignee me`.\n\n" +
			"This command shares the `issue list` runner and output shape. It accepts the " +
			"same filters except assignee and reporter, with assignee pinned to " +
			"`currentUser()`.",
		Example: `$ jira issue mine

# List your open issues in a project, newest first
$ jira issue mine --project PROJ --status '!Done' --order-by created

# Preview generated JQL without contacting Jira
$ jira issue mine --project PROJ --as-jql`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.builder.Assignee = "me"
			return runIssueList(cmd, opts)
		},
	}
	cmdutil.AddBoolVar(cmd.Flags(), &opts.detail, "detail", false, "Fetch full issue records", clib.FlagExtra{Group: "Output"})
	cmdutil.AddStringVar(cmd.Flags(), &opts.jqlQuery, "jql", "", "Add custom JQL clauses (combined with `assignee = currentUser()`)", clib.FlagExtra{Group: "Filters", Placeholder: "JQL"})
	cmdutil.AddBoolVar(cmd.Flags(), &opts.asJQL, "as-jql", false, "Print the built JQL without calling Jira", clib.FlagExtra{Group: "Output"})
	// Share `issue list`'s filter surface minus assignee/reporter: assignee is
	// pinned to currentUser() in RunE above, so the two cannot drift.
	clijql.AddFilterFlags(cmd, &opts.builder)
	clijql.AddSortFlags(cmd, &opts.builder)
	clijql.AddDateFilterFlags(cmd, &opts.builder)
	cmdutil.AddIssueColumnFlags(cmd.Flags(), &opts.columns, &opts.tsv)
	addIssueListPaginationFlags(cmd, &opts)
	return cmd
}

func issueViewCommand() *cobra.Command {
	var parallelism int
	var web bool
	var fields []string
	cmd := &cobra.Command{
		Use:         "view KEY...",
		Annotations: issueKeyArg,
		Short:       "View issue details",
		Long: "Fetch one or more issues with rendered fields, names, schema, transitions, " +
			"and operations expanded. Use it when you need the full issue context before " +
			"editing, commenting, or transitioning.\n\n" +
			"`--fields` narrows the fetched payload to the named fields, matching the " +
			"selector on `jira search jql`. A narrowed read skips the transitions, " +
			"operations, and editmeta expansions — omit `--fields` when you need the " +
			"edit context.\n\n" +
			"`--web` opens a single issue URL and does not call Jira beyond reading the " +
			"configured base URL. Multiple issue keys are fetched with bounded parallelism " +
			"and return partial results when some keys fail.",
		Example: `$ jira issue view PROJ-123

# Fetch only the fields you need
$ jira issue view PROJ-123 --fields summary,status,assignee

# View several issues, four requests at a time
$ jira issue view PROJ-1 PROJ-2 PROJ-3 --parallelism 4

# Open an issue in the browser instead of printing it
$ jira issue view PROJ-123 --web`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			keys, err := issuekey.ParseExpressions(args, issuekey.Options{MaxExpansion: issuekey.DefaultMaxExpansion})
			if err != nil {
				return err
			}
			if web {
				if len(keys) != 1 {
					return fmt.Errorf("validation: --web opens a single issue; pass exactly one key")
				}
				return openIssueWeb(cmd, keys[0], "issue.view")
			}
			viewFields := jql.CompactStrings(fields)
			if len(keys) == 1 {
				return runIssueViewSingle(cmd, keys[0], viewFields)
			}
			return runIssueViewMany(cmd, keys, parallelism, viewFields)
		},
	}
	cmdutil.AddParallelismFlag(cmd, &parallelism)
	cmdutil.AddBoolVar(cmd.Flags(), &web, "web", false, "Open the issue in a browser instead of printing it", clib.FlagExtra{Group: "Output"})
	cmdutil.AddStringSliceVar(cmd.Flags(), &fields, "fields", nil, "Narrow each issue to these fields, comma-separated [example: summary,status,assignee]", clib.FlagExtra{Group: "Output", Placeholder: "FIELD", Complete: "predictor=cachefield,comma"})
	cmd.MarkFlagsMutuallyExclusive("fields", "web")
	return cmd
}

// openIssueWeb builds the issue's browse URL from the active profile and opens
// it in a browser when interactive, reporting the URL in the envelope either
// way. It needs no Jira call — only the configured base URL. Shared by
// `issue view --web` and the top-level `jira open`.
func openIssueWeb(cmd *cobra.Command, key, command string) error {
	cmdutil.RecordIssueKeys(cmd, key)
	profile, err := cmdutil.ProfileForCommand(cmd)
	if err != nil {
		return err
	}
	u := browser.IssueURL(profile.BaseURL, key)
	if u == "" {
		return fmt.Errorf("validation: opening an issue in the browser requires a configured base URL")
	}
	return cmdutil.WriteWebEnvelope(cmd, command, u, envelope.WebOpenIssueOutput{Issue: cmdutil.IssueRef{Key: key}})
}

// NewOpenCommand returns the top-level `jira open KEY` shortcut that opens an
// issue in the browser — the muscle-memory command, sharing openIssueWeb with
// `issue view --web`.
func NewOpenCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "open KEY",
		Annotations: issueKeyArg,
		Short:       "Open an issue in a browser",
		Long: "Open one Jira issue in the default browser. Use it as a short top-level " +
			"shortcut when you already know the issue key.\n\n" +
			"The command builds the browse URL from the active profile base URL. It does " +
			"not fetch the issue from Jira.",
		Example: `$ jira open PROJ-123

# Print the URL envelope as JSON
$ jira open PROJ-123 --output=json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			keys, err := issuekey.ParseExpressions(args, issuekey.Options{MaxExpansion: issuekey.DefaultMaxExpansion})
			if err != nil {
				return err
			}
			if len(keys) != 1 {
				return fmt.Errorf("validation: open takes exactly one issue key")
			}
			return openIssueWeb(cmd, keys[0], "open")
		},
	}
	return cmd
}

func runIssueViewSingle(cmd *cobra.Command, key string, fields []string) error {
	cmdutil.RecordIssueKeys(cmd, key)
	client, _, ok, err := cmdutil.JiraClientForCommand(cmd)
	if err != nil {
		return err
	}
	if ok {
		var (
			issue *jira.Issue
			resp  *jira.Response
		)
		if err := cmdutil.Spin(cmd, "issue.view", func(ctx context.Context) error {
			var e error
			issue, resp, e = cmdutil.ServicesForClient(client).Issue().Get(ctx, key, issueViewGetOptions(fields))
			return e
		}); err != nil {
			return err
		}
		// ADF render-loss warnings describe what the HUMAN renderer drops when
		// it flattens ADF to Markdown. The json/compact envelope carries the
		// full ADF in data.issue, so nothing is lost there.
		var warnings []adf.Warning
		if cmdutil.UsePlainOutput(cmd) {
			warnings = collectIssueLossyWarnings(issue)
		}
		return cmdutil.WriteEnvelopeWithResponseAndWarnings(cmd, "issue.view", envelope.IssueViewOutput{Issue: issue}, resp, warnings)
	}
	return cmdutil.WriteEnvelope(cmd, "issue.view", envelope.IssueViewOutput{Issue: &jira.Issue{Key: new(key)}})
}

type issueViewManyData struct {
	Results   []issueViewResult `json:"results"`
	Succeeded int               `json:"succeeded"`
	Failed    int               `json:"failed"`
}

type issueViewResult struct {
	Key   string      `json:"key"`
	OK    bool        `json:"ok"`
	Issue *jira.Issue `json:"issue,omitempty"`
	Error *cli.Error  `json:"error,omitempty"`
}

func runIssueViewMany(cmd *cobra.Command, keys []string, parallelism int, fields []string) error {
	client, _, ok, err := cmdutil.JiraClientForCommand(cmd)
	if err != nil {
		return err
	}
	if !ok {
		data := issueViewManyData{Results: make([]issueViewResult, 0, len(keys)), Succeeded: len(keys)}
		for _, key := range keys {
			data.Results = append(data.Results, issueViewResult{
				Key:   key,
				OK:    true,
				Issue: &jira.Issue{Key: new(key)},
			})
		}
		return cmdutil.WriteEnvelope(cmd, "issue.view", data)
	}

	service := cmdutil.ServicesForClient(client).Issue()
	results, err := cmdutil.FanOutKeysProgress(cmd.Context(), "issue.view", keys, parallelism, func(ctx context.Context, key string) (*jira.Issue, error) {
		issue, _, err := service.Get(ctx, key, issueViewGetOptions(fields))
		return issue, err
	})
	if err != nil {
		return err
	}
	data, errorsOut, topErr := issueViewManyEnvelopeData(results)
	if len(errorsOut) > 0 {
		if err := cmdutil.WriteEnvelopeWithErrors(cmd, "issue.view", data, errorsOut); err != nil {
			return cli.PreservePrimaryError(topErr, err)
		}
		err := cmdutil.EnvelopeWritten(topErr)
		if cmdutil.UsePlainOutput(cmd) {
			return cmdutil.DiagnosticWritten(err)
		}
		return err
	}
	return cmdutil.WriteEnvelope(cmd, "issue.view", data)
}

func issueViewGetOptions(fields []string) *jira.IssueGetOptions {
	// A --fields read is an explicit request for a slim payload: keep the
	// render/interpretation expansions (Jira narrows them to the requested
	// fields) and skip the edit-support ones, which ignore the fields
	// parameter and would dominate the response.
	if len(fields) > 0 {
		return &jira.IssueGetOptions{Fields: fields, Expand: []string{"renderedFields", "names", "schema"}}
	}
	// transitions and editmeta ride the same GET and are surfaced in the
	// issue payload (Issue.Transitions / Issue.EditMeta) so one read
	// answers "what can I transition to" and "what may I edit".
	return &jira.IssueGetOptions{Expand: []string{"renderedFields", "names", "schema", "transitions", "operations", "editmeta"}}
}

func issueViewManyEnvelopeData(results []cmdutil.KeyResult[*jira.Issue]) (issueViewManyData, []cli.Error, error) {
	data := issueViewManyData{Results: make([]issueViewResult, len(results))}
	type mappedFailure struct {
		err  cli.Error
		src  error
		exit int
	}
	var failures []mappedFailure
	for i, result := range results {
		entry := issueViewResult{Key: result.Key, OK: result.Err == nil}
		if result.Err != nil {
			mapped := cli.MapError(result.Err)
			entry.Error = &mapped
			data.Failed++
			failures = append(failures, mappedFailure{err: mapped, src: result.Err, exit: cli.ExitCode(mapped)})
		} else {
			entry.Issue = result.Value
			data.Succeeded++
		}
		data.Results[i] = entry
	}
	sort.SliceStable(failures, func(i, j int) bool {
		return failures[i].exit > failures[j].exit
	})
	errorsOut := make([]cli.Error, len(failures))
	var topErr error
	for i, failure := range failures {
		errorsOut[i] = failure.err
		if i == 0 {
			topErr = issueViewPartialFailureError(data, failure.err)
		}
	}
	return data, errorsOut, topErr
}

func issueViewPartialFailureError(data issueViewManyData, top cli.Error) error {
	total := data.Succeeded + data.Failed
	reason := issueViewFailureReason(top)
	status := "successful issues are shown above"
	if data.Succeeded == 0 {
		status = "no issues were read successfully"
	}
	msg := fmt.Sprintf("issue view completed with %d of %d failed (%s); %s", data.Failed, total, reason, status)
	return cli.NewCodedError(cli.AggregateCode(top), msg)
}

func issueViewFailureReason(top cli.Error) string {
	if top.Code != "" {
		return strings.ReplaceAll(top.Code, "_", " ")
	}
	if top.Type != "" {
		return top.Type
	}
	return "error"
}

// collectIssueLossyWarnings inspects an *jira.Issue for ADF surfaces
// (description, embedded comment bodies) and returns a structured
// warning per surface that contained at least one construct the
// ADF→Markdown renderer can't fully express. The warning shape mirrors
// the comment-list lossy-warning contract: Field identifies the source
// (`description` or `comment:<id>`), NodeType is the dropped node
// name, and Message enumerates the construct list. Multiple lossy
// constructs on a single source produce multiple warnings (one per
// construct) so they fit the existing flat cli.Warning envelope shape;
// consumers that want a per-source rollup can group on Field.
func collectIssueLossyWarnings(issue *jira.Issue) []adf.Warning {
	if issue == nil {
		return nil
	}
	var warnings []adf.Warning
	if issue.Fields != nil && issue.Fields.Description != nil {
		res := adf.ToMarkdownLossy(*issue.Fields.Description)
		for _, c := range res.LossyConstructs {
			warnings = append(warnings, adf.Warning{
				Type:     "adf_lossy_render",
				Message:  fmt.Sprintf("description ADF construct %q dropped during Markdown render; render in --output=json for full ADF fidelity", c),
				Field:    "description",
				NodeType: c,
				Lossy:    true,
			})
		}
	}
	for _, comment := range issue.Comments {
		if comment == nil || comment.Body == nil {
			continue
		}
		res := adf.ToMarkdownLossy(*comment.Body)
		if len(res.LossyConstructs) == 0 {
			continue
		}
		commentID := ""
		if comment.ID != nil {
			commentID = *comment.ID
		}
		field := "comment"
		if commentID != "" {
			field = "comment:" + commentID
		}
		for _, c := range res.LossyConstructs {
			warnings = append(warnings, adf.Warning{
				Type:     "adf_lossy_render",
				Message:  fmt.Sprintf("comment %s ADF construct %q dropped during Markdown render; render in --output=json for full ADF fidelity", commentID, c),
				Field:    field,
				NodeType: c,
				Lossy:    true,
			})
		}
	}
	return warnings
}

// issueListOptions captures every input the issue-list runner needs.
// Both `issue list` and `issue mine` populate it from their flags.
type issueListOptions struct {
	builder     jql.BuildOptions
	jqlQuery    string
	parallelism int
	detail      bool
	asJQL       bool
	count       bool
	all         bool
	unbounded   bool
	limit       int
	cursor      string
	columns     []string
	tsv         bool
}

// addIssueListPaginationFlags attaches the general pagination contract —
// --limit / --all / --unbounded / --cursor — shared by `issue list` and
// `issue mine`. Must run after the --count / --as-jql flags exist so the
// mutexes can reference them.
func addIssueListPaginationFlags(cmd *cobra.Command, opts *issueListOptions) {
	fs := cmd.Flags()
	cmdutil.AddBoolVar(fs, &opts.all, "all", false, "Walk every page until `isLast` (bounded; use `--unbounded` to lift the caps)", clib.FlagExtra{Group: "Pagination"})
	cmdutil.AddIntVar(fs, &opts.limit, "limit", 50, "Page size requested from Jira; `0` uses the default", clib.FlagExtra{Group: "Pagination", Placeholder: "N"})
	cmdutil.AddBoolVar(fs, &opts.unbounded, "unbounded", false, "With `--all`, lift the default 100-page / 10 000-issue caps", clib.FlagExtra{Group: "Pagination"})
	cmdutil.AddStringVar(fs, &opts.cursor, "cursor", "", "Resume from a `nextCursor` returned by a previous page", clib.FlagExtra{Group: "Pagination", Placeholder: "TOKEN"})
	// --count fetches no issues and --as-jql never calls Jira, so the page
	// controls are meaningless alongside either. --cursor composes with
	// --limit and --all, mirroring `search jql`.
	for _, short := range []string{"count", "as-jql"} {
		if cmd.Flags().Lookup(short) == nil {
			continue
		}
		cmd.MarkFlagsMutuallyExclusive(short, "all")
		cmd.MarkFlagsMutuallyExclusive(short, "limit")
		cmd.MarkFlagsMutuallyExclusive(short, "cursor")
	}
}

// runIssueList is the shared body for `issue list` and `issue mine`. It
// applies profile defaults, builds the JQL, optionally short-circuits with
// --as-jql, then either calls Jira or returns an empty envelope when no
// client is configured. Output flows through the same `issue.list` envelope
// shape so consumers can't tell which command emitted it.
func runIssueList(cmd *cobra.Command, opts issueListOptions) error {
	if err := cli.ValidateIssueColumns(opts.columns); err != nil {
		// Typed so the offending --columns value (which is echoed into the
		// message) can't trip the substring error classifier — e.g.
		// `--columns auth` must map to a flag-value validation error, not auth.
		inputErr := cli.NewCLIInputError(cli.InputFlagValueInvalid, err.Error())
		inputErr.Flag = "columns"
		return inputErr
	}
	scope, precedence, scopeErr := boardscope.FromFlags(cmd)
	if scopeErr != nil {
		return scopeErr
	}
	//  default_board wins exclusively over default_project on
	// commands that consume --board. When the board scope is active,
	// the builder must NOT inherit default_project — the board's
	// project keys are the sole project clause.
	scopeActive := len(scope.Board.ProjectKeys) > 0
	if opts.asJQL {
		// --as-jql must not require a credential — it never calls Jira.
		// boardscope.FromFlags is cache-only (no client probe), and we
		// load the profile directly here instead of cmdutil.JiraClientForCommand
		// so the secret backend (e.g. 1Password) stays untouched.
		cfg, cfgErr := config.Load(config.WithPath(cmdutil.ConfigPath(cmd)))
		if cfgErr != nil {
			return cfgErr
		}
		profile := cmdutil.ActiveProfile(cmd, cfg)
		builder := opts.builder
		if !scopeActive && xstrings.IsBlank(opts.jqlQuery) {
			builder = issueListBuilderWithProfileDefaults(builder, profile)
		}
		query, err := jql.IssueList(opts.jqlQuery, builder)
		if err != nil {
			return err
		}
		query = boardscope.ApplyClauseToJQL(query, scope)
		data := boardScopedListData(cmd, []map[string]any{}, opts.detail, query, scope, precedence)
		// The deep link is built offline from the profile base URL — the same
		// builder `search jql --web` uses — so the preview never calls Jira.
		data["url"] = browser.SearchURL(profile.BaseURL, query)
		return cmdutil.WriteEnvelope(cmd, "issue.list.jql", data)
	}
	client, profile, ok, err := cmdutil.JiraClientForCommand(cmd)
	if err != nil {
		return err
	}
	builder := opts.builder
	if !scopeActive && xstrings.IsBlank(opts.jqlQuery) {
		builder = issueListBuilderWithProfileDefaults(builder, profile)
	}
	query, err := jql.IssueList(opts.jqlQuery, builder)
	if err != nil {
		return err
	}
	query = boardscope.ApplyClauseToJQL(query, scope)
	if opts.count {
		// --count returns Jira's approximate estimate for the built query and
		// fetches no issues, so it needs a live client (unlike --as-jql).
		if !ok {
			return fmt.Errorf("validation: --count queries Jira for the estimate and needs a configured profile")
		}
		var (
			count int
			resp  *jira.Response
		)
		if err := cmdutil.Spin(cmd, "issue.list.count", func(ctx context.Context) error {
			var countErr error
			count, resp, countErr = cmdutil.ServicesForClient(client).Search().ApproximateCount(ctx, query)
			return countErr
		}); err != nil {
			return err
		}
		// issue.list.count carries only the estimate and the query; the shared
		// IssueListOutput's issues/detail/board fields stay nil and omitempty
		// drops them, reproducing the {count, jql} shape byte-for-byte.
		return cmdutil.WriteEnvelopeWithResponse(cmd, "issue.list.count", envelope.IssueListOutput{Jql: query, Count: &count}, resp)
	}
	if !ok {
		return cmdutil.WriteEnvelope(cmd, "issue.list", boardScopedListData(cmd, []map[string]any{}, opts.detail, query, scope, precedence))
	}
	service := cmdutil.ServicesForClient(client).Issue()
	keyChunks, err := issueListKeyChunks(builder.Keys)
	if err != nil {
		return err
	}
	fields := jira.DefaultIssueListFields()
	var expand []string
	if opts.detail {
		fields = []string{"*all"}
		expand = issueListDetailExpand()
	}
	if len(keyChunks) > 0 {
		// A key listing is a set of exact lookups, not a page walk — the
		// pagination controls have nothing to apply to, so refusing them
		// beats silently ignoring them.
		if opts.all || opts.cursor != "" || cmd.Flags().Changed("limit") {
			return fmt.Errorf("validation: --limit, --all, and --cursor page the JQL listing; a key list is looked up whole, so drop the pagination flags or the keys")
		}
		return runIssueListKeyChunks(cmd, issueListKeyChunkInputs{
			service:     service,
			opts:        opts,
			builder:     builder,
			scope:       scope,
			precedence:  precedence,
			fullQuery:   query,
			keyChunks:   keyChunks,
			fields:      fields,
			expand:      expand,
			parallelism: opts.parallelism,
		})
	}
	limit := opts.limit
	if limit <= 0 {
		limit = 50
	}
	if opts.all {
		// Same drain, bounds, truncation warnings, and resume cursor as
		// `search jql --all` — one pagination contract across both list
		// surfaces.
		req := &jira.SearchRequest{
			JQL:         query,
			Fields:      fields,
			Expand:      expand,
			ListOptions: jira.ListOptions{MaxResults: limit, NextPageToken: opts.cursor}, // pagination-exempt: opaque --cursor pass-through
		}
		searchSvc := cmdutil.ServicesForClient(client).Search()
		var (
			issues []*jira.Issue
			info   jira.DrainInfo
		)
		if err := cmdutil.Spin(cmd, "issue.list", func(ctx context.Context) error {
			var drainErr error
			issues, info, drainErr = jira.DrainSearch(ctx, searchSvc, req, jira.DrainOptions{Unbounded: opts.unbounded})
			return drainErr
		}); err != nil {
			return err
		}
		pagination := &cli.Pagination{
			MaxResults: len(issues),
			Total:      new(len(issues)),
			IsLast:     !info.Truncated,
			NextCursor: info.NextPageToken, // pagination-exempt: opaque resume token from the drain
		}
		cmdutil.RecordIssuesSeen(cmd, issues)
		issueData := cmdutil.IssueOutput(issues, opts.detail)
		data := boardScopedListData(cmd, issueData, opts.detail, query, scope, precedence)
		return cmdutil.WriteEnvelopeWithPaginationAndRawWarnings(cmd, "issue.list", data, pagination, cmdutil.DrainTruncationWarnings(info))
	}
	var (
		issues []*jira.Issue
		resp   *jira.Response
	)
	if err := cmdutil.Spin(cmd, "issue.list", func(ctx context.Context) error {
		var listErr error
		issues, resp, listErr = service.List(ctx, &jira.IssueListOptions{
			MaxResults: limit, NextPageToken: opts.cursor, // pagination-exempt: opaque --cursor pass-through
			JQL:    query,
			Fields: fields,
			Expand: expand,
		})
		return listErr
	}); err != nil {
		return err
	}
	issueData := cmdutil.IssueOutput(issues, opts.detail)
	return cmdutil.WriteEnvelopeWithResponse(cmd, "issue.list", boardScopedListData(cmd, issueData, opts.detail, query, scope, precedence), resp)
}

const issueListKeyChunkSize = 50

type issueListKeyChunkInputs struct {
	service     jira.IssueService
	opts        issueListOptions
	builder     jql.BuildOptions
	scope       jira.BoardScope
	precedence  string
	fullQuery   string
	keyChunks   []string
	fields      []string
	expand      []string
	parallelism int
}

type issueListKeyChunkFailure struct {
	KeyExpr string    `json:"key_expr"`
	Error   cli.Error `json:"error"`
}

func runIssueListKeyChunks(cmd *cobra.Command, in issueListKeyChunkInputs) error {
	results, err := cmdutil.FanOutKeysProgress(cmd.Context(), "issue.list", in.keyChunks, in.parallelism, func(ctx context.Context, keyExpr string) ([]*jira.Issue, error) {
		builder := in.builder
		builder.Keys = []string{keyExpr}
		query, err := jql.IssueList(in.opts.jqlQuery, builder)
		if err != nil {
			return nil, err
		}
		query = boardscope.ApplyClauseToJQL(query, in.scope)
		issues, _, err := in.service.List(ctx, &jira.IssueListOptions{
			MaxResults: issueListKeyChunkSize,
			JQL:        query,
			Fields:     in.fields,
			Expand:     in.expand,
		})
		if err != nil {
			return nil, fmt.Errorf("issue list key chunk %q: %w", keyExpr, err)
		}
		return issues, nil
	})
	if err != nil {
		return err
	}

	issues := make([]*jira.Issue, 0, len(in.keyChunks)*issueListKeyChunkSize)
	failures := make([]issueListKeyChunkFailure, 0)
	errorsOut := make([]cli.Error, 0)
	for _, result := range results {
		if result.Err != nil {
			mapped := cli.MapError(result.Err)
			failures = append(failures, issueListKeyChunkFailure{KeyExpr: result.Key, Error: mapped})
			errorsOut = append(errorsOut, mapped)
			continue
		}
		issues = append(issues, result.Value...)
	}
	order := issueListKeyOrder(in.keyChunks)
	sort.SliceStable(issues, func(i, j int) bool {
		return issueOrderIndex(order, issues[i]) < issueOrderIndex(order, issues[j])
	})
	resp := &jira.Response{
		MaxResults: len(order),
		Total:      len(issues),
		TotalKnown: true,
		IsLast:     true,
		TokenPage:  true,
	}
	cmdutil.RecordIssuesSeen(cmd, issues)
	issueData := cmdutil.IssueOutput(issues, in.opts.detail)
	data := boardScopedListData(cmd, issueData, in.opts.detail, in.fullQuery, in.scope, in.precedence)
	if len(failures) > 0 {
		sort.SliceStable(errorsOut, func(i, j int) bool {
			return cli.ExitCode(errorsOut[i]) > cli.ExitCode(errorsOut[j])
		})
		data["succeeded_key_chunks"] = len(results) - len(failures)
		data["failed_key_chunks"] = failures
		topErr := issueListKeyChunkPartialFailureError(len(results)-len(failures), len(failures), errorsOut[0])
		if err := cmdutil.WriteEnvelopeWithResponseAndErrors(cmd, "issue.list", data, resp, errorsOut); err != nil {
			return cli.PreservePrimaryError(topErr, err)
		}
		return cmdutil.EnvelopeWritten(topErr)
	}
	return cmdutil.WriteEnvelopeWithResponse(cmd, "issue.list", data, resp)
}

func issueListKeyChunkPartialFailureError(succeeded, failed int, top cli.Error) error {
	total := succeeded + failed
	reason := issueViewFailureReason(top)
	status := "successful issues are shown above"
	if succeeded == 0 {
		status = "no issue chunks were read successfully"
	}
	msg := fmt.Sprintf("issue list completed with %d of %d key chunks failed (%s); %s", failed, total, reason, status)
	return cli.NewCodedError(cli.AggregateCode(top), msg)
}

func issueListKeyChunks(inputs []string) ([]string, error) {
	keys, err := issuekey.ParseExpressions(inputs, issuekey.Options{MaxExpansion: issuekey.DefaultMaxExpansion})
	if err != nil {
		return nil, err
	}
	chunks := make([]string, 0, (len(keys)+issueListKeyChunkSize-1)/issueListKeyChunkSize)
	for start := 0; start < len(keys); start += issueListKeyChunkSize {
		end := min(start+issueListKeyChunkSize, len(keys))
		chunks = append(chunks, strings.Join(keys[start:end], ","))
	}
	return chunks, nil
}

func issueListKeyOrder(chunks []string) map[string]int {
	order := map[string]int{}
	index := 0
	for _, chunk := range chunks {
		for key := range strings.SplitSeq(chunk, ",") {
			if key == "" {
				continue
			}
			if _, ok := order[key]; !ok {
				order[key] = index
				index++
			}
		}
	}
	return order
}

func issueOrderIndex(order map[string]int, issue *jira.Issue) int {
	if issue == nil || issue.Key == nil {
		return len(order)
	}
	if index, ok := order[*issue.Key]; ok {
		return index
	}
	return len(order)
}

// boardScopedListData extends IssueListOutputData with the new envelope
// fields per contracts/envelope-shapes.md > issue list --board.
func boardScopedListData(cmd *cobra.Command, issues any, detail bool, query string, scope jira.BoardScope, precedence string) map[string]any {
	data := IssueListOutputData(cmd, issues, detail, query)
	data["jql"] = query
	data["precedence"] = precedence
	data["board_scope"] = boardscope.EnvelopeData(scope)
	return data
}

// issueListCommand has been relocated to cmd/jira/issue_list.go per the
// `issue_<verb>.go` convention. The runner (runIssueList) and helpers
// (issueListBuilderWithProfileDefaults, IssueListOutputData) remain in
// this file because they are shared with `issue mine`.

func issueListBuilderWithProfileDefaults(builder jql.BuildOptions, profile config.Profile) jql.BuildOptions {
	if len(jql.CompactStrings(builder.Projects)) == 0 && profile.DefaultProject != "" {
		builder.Projects = []string{profile.DefaultProject}
	}
	return builder
}

// IssueListOutputData builds the base `issue.list` envelope data — the issues
// payload plus the detail flag, and the resolved JQL only under --debug.
// Exported so the shapes that extend it (boardScopedListData) and the output
// tests produce identical list output.
func IssueListOutputData(cmd *cobra.Command, issues any, detail bool, query string) map[string]any {
	data := map[string]any{"issues": issues, "detail": detail}
	if cmdutil.BoolValue(cmd.Root().PersistentFlags(), "debug") {
		data["jql"] = query
	}
	return data
}

func issueListDetailExpand() []string {
	return []string{"renderedFields", "names", "schema", "transitions", "operations", "changelog"}
}

func issueCreateCommand() *cobra.Command {
	var dryRun, validateRemote, verifyWrite bool
	var summary, jsonInput, assignee, markdownInput, markdownFile string
	var project, issueType, parent, priority string
	var labels []string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an issue",
		Long: "Create a Jira issue from convenience flags, profile defaults, or a JSON " +
			"payload. Use flags for common fields, `--markdown` for a Markdown " +
			"description, and `--json-input` for full Jira field objects, custom " +
			"fields, or rich ADF descriptions.\n\n" +
			"Create requests run through the validate-and-encode mutation pipeline before " +
			"submission. `--dry-run` performs local parsing, alias normalisation, ADF " +
			"validation, and preview rendering without resolving credentials or contacting " +
			"Jira.\n\n" +
			"Headless `--no-input` needs complete input from flags, JSON, or profile " +
			"defaults: project, issue type, and summary must all be known before the " +
			"command can submit.",
		Example: `$ jira issue create --project PROJ --type Task --summary "Fix the build"

# Create a bug assigned to you with a label
$ jira issue create --project PROJ --type Bug --summary "Crash on startup" --assignee me --label regression

# Preview the create payload without contacting Jira
$ jira issue create --project PROJ --type Task --summary "Draft" --dry-run

# Create with a Markdown description (converted to ADF client-side)
$ jira issue create --project PROJ --type Task --summary "Fix" --markdown "## Steps\n\n1. Repro"

# Preview a full JSON payload for an agent
$ jira issue create --json-input issue-create.json --dry-run --output=json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			noInput := cmdutil.NoInputRequested(cmd)
			if validateRemote && !dryRun {
				return fmt.Errorf("validation: --validate-remote is a read-only dry-run pre-flight; combine it with --dry-run (a live submit already validates against the create screen)")
			}
			if verifyWrite && dryRun {
				return fmt.Errorf("validation: --verify re-fetches the issue after a live write; it cannot be combined with --dry-run")
			}
			payload := map[string]any{"summary": summary}
			if jsonInput != "" {
				if err := cmdutil.ReadJSONFile(jsonInput, &payload); err != nil {
					return err
				}
			}
			// Accept the Jira-native {"fields": {...}} shape interchangeably
			// with the flat convenience keys — one payload contract across
			// create and edit.
			payload = pipeline.FieldsFromPayload(payload)
			// Convenience flags layer onto --json-input so common fields need
			// no hand-written JSON. An explicit flag overrides the same key in
			// --json-input; a clashing project/type alias-vs-wire pair is
			// caught later by NormalizeCreateAliasesChecked.
			applyCreateFlags(payload, createFlags{
				project:   project,
				issueType: issueType,
				parent:    parent,
				priority:  priority,
				labels:    labels,
			})
			// --markdown routes through the same `description_markdown`
			// payload key the JSON path uses, so one resolver handles both
			// (extractDescriptionDoc). Mutual exclusion with --json-input
			// means this never overwrites a payload value.
			markdownBody, mdErr := cmdutil.ResolveMarkdownInput(markdownInput, markdownFile)
			if mdErr != nil {
				return mdErr
			}
			if markdownBody != "" {
				payload["description_markdown"] = markdownBody
			}
			// Resolve the profile WITHOUT building a client: --assignee me,
			// --no-input validation, and the dry-run preview only need
			// profile metadata. Credentials are resolved later, at the
			// live-submit boundary.
			profile, err := cmdutil.ProfileForCommand(cmd)
			if err != nil {
				return err
			}
			// --assignee shortcut: feeds the assignee_account_id alias for "me" /
			// a literal account-id; "none" clears it; an email is resolved to an
			// account id after a live client exists.
			var assigneeEmail string
			if v := strings.TrimSpace(assignee); v != "" {
				switch strings.ToLower(v) {
				case "none", "unassigned":
					delete(payload, "assignee_account_id")
				case "me", "@me":
					if profile.AccountID != "" {
						payload["assignee_account_id"] = profile.AccountID
					}
				default:
					if email, ok, aerr := assigneeEmailFrom(v); aerr != nil {
						return aerr
					} else if ok {
						if dryRun {
							return fmt.Errorf("validation: %s", assigneeEmailDryRunErr)
						}
						assigneeEmail = email
					} else {
						payload["assignee_account_id"] = v
					}
				}
			}
			if noInput {
				if err := validateIssueCreateRequired(payload, profile); err != nil {
					return err
				}
			}
			// Fill project / issue type from profile defaults BEFORE
			// normalizing aliases so a default-only create still carries
			// the target into the wire payload.
			// Profile defaults fill a field only when NEITHER spelling —
			// the flat alias or the Jira wire object — carries it, so a
			// wire-only payload never collides with its own default.
			if pipeline.CreateWireValue(payload, "project_key") == "" && profile.DefaultProject != "" {
				payload["project_key"] = profile.DefaultProject
			}
			if pipeline.CreateWireValue(payload, "issue_type") == "" && profile.DefaultIssueType != "" {
				payload["issue_type"] = profile.DefaultIssueType
			}
			// Normalize the CLI create aliases (project_key / issue_type /
			// assignee_account_id) to the Jira wire field ids
			// (project / issuetype / assignee) BEFORE the pipeline runs.
			// Screen validation keys on the wire ids; an un-normalized
			// alias would be flagged off-screen even for a default
			// create. A conflict (alias and wire key both set) is fatal.
			projectForSchema := cmdutil.FirstNonEmpty(pipeline.CreateWireValue(payload, "project_key"), profile.DefaultProject)
			issueTypeForSchema := cmdutil.FirstNonEmpty(pipeline.CreateWireValue(payload, "issue_type"), profile.DefaultIssueType)
			normalizedPayload, normErr := pipeline.NormalizeCreateAliasesChecked(payload)
			if normErr != nil {
				return normErr
			}
			// Lift bare-string system fields ("priority": "Medium",
			// "parent": "PROJ-1") into their canonical wire objects AFTER
			// alias normalization, so the alias-vs-wire conflict check
			// above still sees the value the user wrote. Without the lift,
			// the flat spelling passes the dry-run and 400s on the wire.
			payload = pipeline.LiftSystemFieldShapes(normalizedPayload)
			// The priority set is small, site-wide, and usually cached —
			// a name Jira would reject fails fast here instead of round-
			// tripping. Advisory: no cache, no check.
			if perr := validatePriorityAgainstCache(cmd, profile, payload); perr != nil {
				return perr
			}
			// The issue description is the primary ADF document for
			// this mutation. Whether it arrived as `description_markdown`
			// or as a raw ADF `description`, it is pulled out of the
			// payload here and fed to the pipeline as ADFDoc so stage 2
			// (ValidateDoc + ApplyCompatibility) runs on it BEFORE
			// submission — never as a post-pipeline conversion that
			// skips validation. The post-pipeline SubmitADF is the only
			// description that reaches the wire.
			descriptionDoc, descriptionPresent, descMarkdownWarnings, descErr := extractDescriptionDoc(payload)
			if descErr != nil {
				return descErr
			}
			// Extract any remaining ADF-shaped subfields (e.g.
			// `environment`, `customfield_NNNN` ADF) so stage 2 validates
			// them. `description` was already removed above, so it is
			// not double-validated here. These named docs are
			// validate-only: with no per-field screen schema we cannot
			// know their compatibility envelope, so ApplyCompatibility is
			// not run on them — the same treatment the primary
			// description receives (see extractDescriptionDoc).
			namedADF, adfParseErr := extractNamedADFDocs(payload)
			if adfParseErr != nil {
				return adfParseErr
			}

			// Route through the 5-stage validation pipeline before
			// submission. For a live create we resolve the client up
			// front and attach a screen-schema fetcher so stage 3
			// validates the payload against the project / issue-type
			// create screen. A bare dry-run preview runs without
			// credentials, so it has no fetcher: stages 1+2+4 still run;
			// stage 3 is a no-op when no schema is reachable. Adding
			// --validate-remote to a dry-run resolves the client for the
			// read-only createmeta fetch, so the same stage-3/4 checks a
			// live submit gets run before anything is written.
			var client *jira.Client
			if !dryRun || validateRemote {
				var ok bool
				var err error
				client, profile, ok, err = cmdutil.JiraClientForCommand(cmd)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("jira base URL is required for issue.create")
				}
			}
			// An --assignee email is resolved to an account id now that a live
			// client exists (a dry-run was already rejected above). The wire
			// `assignee` field is set directly — alias normalization has already
			// run and there is no assignee_account_id to conflict with.
			if assigneeEmail != "" {
				wire, rerr := resolveAssigneeEmail(cmd.Context(), client, assigneeEmail)
				if rerr != nil {
					return rerr
				}
				payload["assignee"] = wire
			}
			pipeIn := pipeline.MutationInput{
				Mode:             cmdutil.ADFModeFor(cmd, true),
				Fields:           payload,
				DryRun:           dryRun,
				NamedADFDocs:     namedADF,
				MarkdownWarnings: descMarkdownWarnings,
				// The identity wire fields are structural: Jira accepts
				// project / issuetype on every create regardless of screen
				// configuration, but createmeta does not list them on every
				// tenant. A native REST body must never lose its identity
				// keys to screen validation.
				ScreenValidationExemptFields: map[string]bool{"project": true, "issuetype": true},
			}
			if client != nil && (dryRun || !cmdutil.ReadOnlyEnabled(cmd)) {
				// Skip the schema fetch in read-only live mode: the client
				// refuses the create itself, so resolving its screen is
				// wasted work and would emit a stray read request. A
				// --validate-remote dry-run keeps the fetch even under
				// read-only — the fetch IS the point, and it never writes.
				pipeIn.SchemaFetcher = newScreenSchemaFetcher(
					cmd.Context(), cmdutil.ServicesForClient(client).Project(0),
					cmdutil.ProfileForEnvelope(cmd), projectForSchema, issueTypeForSchema,
				)
			}
			if descriptionPresent {
				// FieldCompatibility is left at its zero value with
				// InlineCardSupported=true: a Jira Cloud `description`
				// field accepts inlineCard, so compatibility degradation
				// would be wrong here. ApplyCompatibility still walks the
				// doc; it just has nothing to degrade.
				pipeIn.ADFDoc = &descriptionDoc
				pipeIn.FieldCompat = &adf.FieldCompatibility{Field: "description", InlineCardSupported: true}
			}
			pipeOut := pipeline.RunMutation(pipeIn)
			if pipeOut.Aborted {
				return pipeOut.Err
			}
			// Every path past validation uses the pipeline's
			// post-validation, post-encoding output — never the raw
			// pre-pipeline payload. SubmitFields is the only map allowed
			// downstream.
			submitFields := pipeOut.SubmitFields
			if submitFields == nil {
				submitFields = map[string]any{}
			}
			// The validated, compatibility-applied description from the
			// pipeline replaces whatever the payload carried — markdown
			// and raw ADF are now handled identically.
			if descriptionPresent && pipeOut.SubmitADF != nil {
				submitFields["description"] = *pipeOut.SubmitADF
			}
			preview, err := issueCreatePreview(submitFields, profile)
			if err != nil {
				return err
			}
			data := envelope.IssueCreateOutput{
				Preview:           preview,
				DryRun:            dryRun,
				ValidatedRemotely: pipeOut.SchemaValidatedRemotely,
			}
			if dryRun {
				return cmdutil.WriteEnvelopeWithWarnings(cmd, "issue.create", data, pipeOut.Warnings)
			}
			// submitFields already carries project / issuetype / assignee
			// in their Jira wire shapes — alias normalization ran before
			// the pipeline. IssueCreateRequest forwards the fields map
			// verbatim; Project / IssueType are left empty so it does not
			// re-wrap a value that is already wire-shaped.
			req := &jira.IssueCreateRequest{
				Summary: cmdutil.StringFromAny(submitFields["summary"]),
				Fields:  submitFields,
			}
			var (
				issue *jira.Issue
				resp  *jira.Response
			)
			if err := cmdutil.Spin(cmd, "issue.create", func(ctx context.Context) error {
				var e error
				issue, resp, e = cmdutil.ServicesForClient(client).Issue().Create(ctx, req)
				return e
			}); err != nil {
				return err
			}
			data.Issue = issue
			data.DryRun = false
			warnings := pipeOut.Warnings
			if verifyWrite {
				createdKey := ""
				if issue != nil {
					createdKey = ptr.Deref(issue.Key)
				}
				if createdKey == "" {
					warnings = append(warnings, adf.Warning{
						Type:    "verification_failed",
						Message: "the write succeeded but the create response carried no issue key to verify against",
					})
				} else {
					verification, vWarnings := runWriteVerification(cmd, client, createdKey, submitFields)
					if verification != nil {
						data.Verification = verification
					}
					warnings = append(warnings, vWarnings...)
				}
			}
			return cmdutil.WriteEnvelopeWithResponseAndWarnings(cmd, "issue.create", data, resp, warnings)
		},
	}
	cmdutil.AddDryRunFlag(cmd.Flags(), &dryRun, "Preview mutation without submitting")
	cmdutil.AddBoolVar(cmd.Flags(), &validateRemote, "validate-remote", false, "With `--dry-run`, validate against the live create screen (read-only createmeta fetch)", clib.FlagExtra{Group: "Validation"})
	cmdutil.AddBoolVar(cmd.Flags(), &verifyWrite, "verify", false, "After a live create, re-fetch the issue and warn about requested fields the server did not apply", clib.FlagExtra{Group: "Validation"})
	cmdutil.AddStringVar(cmd.Flags(), &summary, "summary", "", "Issue summary", clib.FlagExtra{Group: "Fields", Placeholder: "TEXT"})
	cmdutil.AddFileFlag(cmd.Flags(), &jsonInput, "json-input", "", "Read issue create payload from JSON file (canonical for agents)", "Input", "FILE")
	cmdutil.AddMarkdownFlag(cmd, &markdownInput, &markdownFile, "Issue description as Markdown (lossy convenience layer, converted to ADF)", "")
	cmdutil.AddStringVar(cmd.Flags(), &assignee, "assignee", "", "Assign on creation: `me`, an email, or a Jira account ID", clib.FlagExtra{Group: "Fields", Placeholder: "USER", Terse: "assignee", Enum: []string{"me"}, EnumTerse: []string{"current user"}})
	cmdutil.AddStringVar(cmd.Flags(), &project, "project", "", "Project key (overrides the profile default)", clib.FlagExtra{Group: "Fields", Placeholder: "KEY", Complete: "predictor=cacheproject"})
	cmdutil.AddStringVar(cmd.Flags(), &issueType, "type", "", "Issue type name (overrides the profile default)", clib.FlagExtra{Group: "Fields", Placeholder: "NAME", Terse: "issue type", Complete: "predictor=cacheissuetype"})
	cmdutil.AddStringVar(cmd.Flags(), &parent, "parent", "", "Parent issue key (for a subtask or epic child)", clib.FlagExtra{Group: "Fields", Placeholder: "KEY", Complete: "predictor=issuekey"})
	cmdutil.AddStringVar(cmd.Flags(), &priority, "priority", "", "Priority name", clib.FlagExtra{Group: "Fields", Placeholder: "NAME", Terse: "priority", Complete: "predictor=cachepriority"})
	cmdutil.AddStringSliceVar(cmd.Flags(), &labels, "label", nil, "Label to attach (repeatable)", clib.FlagExtra{Group: "Fields", Placeholder: "NAME", Complete: "predictor=cachelabel,comma"})
	return cmd
}

// createFlags carries the issue-create convenience flag values.
type createFlags struct {
	project, issueType, parent, priority string
	labels                               []string
}

// applyCreateFlags merges the convenience flags into the create payload. Each
// value is set only when non-empty, so it overrides a same-key --json-input
// value but an unset flag leaves the payload untouched. project/issueType use
// the create-input aliases the pipeline already normalises; parent/priority
// take their Jira wire object shapes and labels a string array.
func applyCreateFlags(payload map[string]any, f createFlags) {
	if v := strings.TrimSpace(f.project); v != "" {
		payload["project_key"] = v
	}
	if v := strings.TrimSpace(f.issueType); v != "" {
		payload["issue_type"] = v
	}
	if v := strings.TrimSpace(f.parent); v != "" {
		payload["parent"] = map[string]any{"key": v}
	}
	if v := strings.TrimSpace(f.priority); v != "" {
		payload["priority"] = map[string]any{"name": v}
	}
	if cleaned := jql.CompactStrings(f.labels); len(cleaned) > 0 {
		payload["labels"] = cleaned
	}
}

// cmdutil.ConfiguredEditorFor returns the editor.Resolve(...) "configured"
// argument for the active invocation: the active profile's Editor
// field if set, otherwise the global Config.Editor. The resolver in
// internal/editor layers $JIRA_EDITOR / $EDITOR / $VISUAL / "vi" on
// top of whatever this returns.
// resolveAssigneeField interprets an --assignee value into a Jira wire assignee.
// It returns the ready wire value for me/none/account-id; when the value is an
// email address it returns a non-empty email instead, which the caller resolves
// to an account-id via resolveAssigneeEmail once a live client is available.
// set is false only when input is blank.
func resolveAssigneeField(input string, profile config.Profile) (wire any, email string, set bool, err error) {
	v := strings.TrimSpace(input)
	if v == "" {
		return nil, "", false, nil
	}
	switch strings.ToLower(v) {
	case "none", "unassigned":
		return nil, "", true, nil
	case "me", "@me":
		if profile.AccountID != "" {
			return map[string]string{"accountId": profile.AccountID}, "", true, nil
		}
		// Typed as a bad --assignee value so it classifies as
		// flag_value_invalid (validation, exit 3) rather than being
		// substring-guessed as an auth failure off the "jira auth whoami"
		// hint text.
		aerr := cli.NewCLIInputError(cli.InputFlagValueInvalid, "--assignee me requires profile.account_id; run `jira auth whoami --save` to populate it")
		aerr.Flag = "assignee"
		return nil, "", false, aerr
	default:
		if email, ok, err := assigneeEmailFrom(v); err != nil {
			return nil, "", false, err
		} else if ok {
			return nil, email, true, nil
		}
		return map[string]string{"accountId": v}, "", true, nil
	}
}

// assigneeEmailFrom classifies an assignee token: a value containing "@" is
// treated as an email and must be a bare, valid address (no display-name or
// comment form), returned for remote resolution; a value without "@" is a
// literal account id (ok=false, no error). net/mail both validates and trims.
func assigneeEmailFrom(v string) (email string, ok bool, err error) {
	if !strings.Contains(v, "@") {
		return "", false, nil
	}
	addr, perr := mail.ParseAddress(strings.TrimSpace(v))
	if perr != nil || addr.Address != strings.TrimSpace(v) {
		return "", false, fmt.Errorf("invalid assignee email %q", v)
	}
	return addr.Address, true, nil
}

// assigneeEmailDryRunErr is the shared message when an --assignee email cannot
// be resolved because --dry-run makes no live request.
const assigneeEmailDryRunErr = "resolving an assignee email needs a live request; not available with --dry-run — pass an account id instead"

// resolveAssigneeEmail resolves an email to a wire assignee value via the
// /user/search resolver (0 matches → ErrUserNotFound, 1 → that account, 2+ →
// AmbiguousUserError). It requires a live client.
func resolveAssigneeEmail(ctx context.Context, client *jira.Client, email string) (any, error) {
	id, err := cmdutil.ServicesForClient(client).User().ResolveUser(ctx, email)
	if err != nil {
		return nil, err
	}
	return map[string]string{"accountId": id}, nil
}

// validateIssueCreateRequired enforces the spec rule "headless write commands
// require complete input via --no-input + --json-input". It checks that
// project_key, issue_type, and summary are derivable from the supplied JSON
// payload or profile defaults, otherwise returns a validation error.
func validateIssueCreateRequired(payload map[string]any, profile config.Profile) error {
	var missing []string
	if cmdutil.FirstNonEmpty(cmdutil.StringFromAny(payload["summary"])) == "" {
		missing = append(missing, "summary")
	}
	// Either spelling satisfies requiredness: the flat alias or the Jira
	// wire object both unambiguously carry the field.
	if cmdutil.FirstNonEmpty(pipeline.CreateWireValue(payload, "project_key"), profile.DefaultProject) == "" {
		missing = append(missing, "project_key")
	}
	if cmdutil.FirstNonEmpty(pipeline.CreateWireValue(payload, "issue_type"), profile.DefaultIssueType) == "" {
		missing = append(missing, "issue_type")
	}
	if len(missing) > 0 {
		return fmt.Errorf("--no-input requires complete input: missing required fields: %s", strings.Join(missing, ", "))
	}
	return nil
}

// issueCreatePreview builds the dry-run preview shape per command-schemas.md.
// The supplied fields map is the pipeline's post-validation SubmitFields:
// the create aliases have been normalized to the Jira wire field ids
// (project / issuetype / assignee) and the description (whether it
// arrived as markdown or raw ADF) has been converted, validated, and
// compatibility-applied under the bare `description` key. Profile
// defaults fill project / issue type when the payload omits them.
func issueCreatePreview(fields map[string]any, profile config.Profile) (map[string]any, error) {
	preview := map[string]any{
		"project_key": cmdutil.FirstNonEmpty(cmdutil.WireObjectString(fields["project"], "key"), profile.DefaultProject),
		"issue_type":  cmdutil.FirstNonEmpty(cmdutil.WireObjectString(fields["issuetype"], "name"), profile.DefaultIssueType),
		"summary":     cmdutil.StringFromAny(fields["summary"]),
	}
	// description in SubmitFields is the validated ADF document. Surface
	// it under description_adf so the preview names the wire shape.
	if doc, ok := fields["description"]; ok && doc != nil {
		preview["description_adf"] = doc
	}
	if acct := cmdutil.WireObjectString(fields["assignee"], "accountId"); acct != "" {
		preview["assignee_account_id"] = acct
	}
	for _, key := range []string{"priority", "labels", "parent", "components", "epic_key", "custom_fields"} {
		if v, ok := fields[key]; ok {
			preview[key] = v
		}
	}
	return preview, nil
}

// extractDescriptionDoc pulls the issue description out of an issue-create
// payload as a single adf.Document, removing the source key(s) from the
// map so the description is processed exactly once — as the pipeline's
// primary ADFDoc, not as an opaque named subfield.
//
// Two input shapes are accepted, in priority order:
//   - `description_markdown`: a Markdown string, converted via
//     FromMarkdownLossy. Conversion warnings are returned so the pipeline
//     can abort (strict) or surface them (best-effort) on content loss.
//   - `description`: a raw ADF document object.
//
// present is false when neither key is set. When present, the returned
// doc is what the pipeline validates; the caller writes the pipeline's
// SubmitADF back into the fields map under `description`.
func extractDescriptionDoc(payload map[string]any) (doc adf.Document, present bool, warnings []adf.Warning, err error) {
	if md := cmdutil.StringFromAny(payload["description_markdown"]); md != "" {
		delete(payload, "description_markdown")
		delete(payload, "description")
		converted, convWarnings, cerr := adf.FromMarkdownLossy(md)
		if cerr != nil {
			return adf.Document{}, false, nil, fmt.Errorf("convert description_markdown to ADF: %w", cerr)
		}
		return converted, true, convWarnings, nil
	}
	raw, ok := payload["description"]
	if !ok || raw == nil {
		return adf.Document{}, false, nil, nil
	}
	delete(payload, "description")
	encoded, merr := json.Marshal(raw)
	if merr != nil {
		return adf.Document{}, false, nil, fmt.Errorf("marshal description for ADF validation: %w", merr)
	}
	parsed, _, perr := adf.Parse(encoded)
	if perr != nil {
		// A wrong-shape description (a plain string, say) carries a typed
		// identity; name the offending payload key so the envelope's `field`
		// can point at it. A nested path reported by the decoder wins.
		var invalid *adf.InvalidDocumentError
		if errors.As(perr, &invalid) && invalid.Field == "" {
			invalid.Field = "description"
		}
		return adf.Document{}, false, nil, fmt.Errorf("description: %w", perr)
	}
	return parsed, true, nil, nil
}

func issueEditCommand() *cobra.Command {
	var dryRun, validateRemote, verifyWrite bool
	var jsonInput, summary, assignee, markdownInput, markdownFile string
	var parallelism int
	cmd := &cobra.Command{
		Use:         "edit KEY...",
		Annotations: issueKeyArg,
		Short:       "Edit an issue",
		Long: "Edit one or more Jira issues. With no field flags, a single-key edit opens " +
			"the configured external editor on the issue description. Use `--summary`, " +
			"`--assignee`, `--markdown`, or `--json-input` for headless and " +
			"multi-key edits.\n\n" +
			"`--markdown` is the headless way to set the description: it " +
			"converts Markdown to ADF with the same lossy converter `issue create` uses " +
			"and replaces the issue description. A `--json-input` payload may carry the " +
			"`description_markdown` key for the same effect.\n\n" +
			"All field changes run through the validate-and-encode mutation pipeline before " +
			"submission. `--dry-run` previews the validated fields and does not submit.\n\n" +
			"In headless mode (`--no-input`), at least one field flag or `--json-input` " +
			"must be provided because there is no editor to open.",
		Example: `$ jira issue edit PROJ-123 --summary "Updated title"

# Reassign an issue to yourself
$ jira issue edit PROJ-123 --assignee me

# Replace the description from Markdown (headless, no editor)
$ jira issue edit PROJ-123 --markdown "## Steps\n\n1. Repro\n2. Fix"

# Preview a Markdown description as encoded ADF without contacting Jira
$ jira issue edit PROJ-123 --markdown "Done." --dry-run --output=json

# Apply JSON fields to several issues at once
$ jira issue edit PROJ-1 PROJ-2 --json-input issue-edit.json --dry-run --output=json`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			keys, err := issuekey.ParseExpressions(args, issuekey.Options{MaxExpansion: issuekey.DefaultMaxExpansion})
			if err != nil {
				return err
			}
			noInput := cmdutil.NoInputRequested(cmd)
			if validateRemote && !dryRun {
				return fmt.Errorf("validation: --validate-remote is a read-only dry-run pre-flight; combine it with --dry-run (a live submit already validates against the edit screen)")
			}
			if verifyWrite && dryRun {
				return fmt.Errorf("validation: --verify re-fetches the issue after a live write; it cannot be combined with --dry-run")
			}
			if verifyWrite && len(keys) > 1 {
				return fmt.Errorf("validation: --verify supports a single issue key; run per key to verify a fan-out edit")
			}
			payload := map[string]any{}
			if jsonInput != "" {
				if err := cmdutil.ReadJSONFile(jsonInput, &payload); err != nil {
					return err
				}
			}
			// A native REST edit body carries its add/set/remove operations
			// in a top-level `update` block, a sibling of `fields`. Pull it
			// out BEFORE the field-set normalization would fold it into the
			// fields map, and forward it verbatim — Jira validates the
			// operation verbs server-side.
			updateSection, uerr := popUpdateSection(payload)
			if uerr != nil {
				return uerr
			}
			// Accept the Jira-native {"fields": {...}} shape (canonical) or
			// bare field keys at the top level — one payload contract across
			// create and edit.
			fields := pipeline.FieldsFromPayload(payload)
			// --summary / --assignee shortcuts, applied on top of any --json-input.
			// Resolve the profile only — building a client here would
			// resolve credentials even on a dry-run or editor-only path.
			profile, err := cmdutil.ProfileForCommand(cmd)
			if err != nil {
				return err
			}
			if v := strings.TrimSpace(summary); v != "" {
				fields["summary"] = v
			}
			// --markdown is the headless equivalent of the editor flow: a
			// Markdown string converted to ADF via the SAME resolver the
			// create path uses (extractDescriptionDoc below). It is stored
			// under the create alias `description_markdown` so a flag and a
			// `--json-input` payload key share one routing path. Mutual
			// exclusion with --json-input means this never overwrites a
			// payload value; the resolver then prefers Markdown over any
			// literal `description`, mirroring create-side behavior.
			markdownBody, mdErr := cmdutil.ResolveMarkdownInput(markdownInput, markdownFile)
			if mdErr != nil {
				return mdErr
			}
			if markdownBody != "" {
				fields["description_markdown"] = markdownBody
			}
			assigneeWire, assigneeEmail, assigneeSet, aerr := resolveAssigneeField(assignee, profile)
			if aerr != nil {
				return aerr
			}
			if assigneeSet && assigneeEmail == "" {
				fields["assignee"] = assigneeWire
			}
			// An --assignee email is resolved to an account id up front — before
			// the editor-default check and the single/multi-key split — so every
			// path (including multi-key) submits the resolved assignee. It needs
			// a live request and is rejected under --dry-run.
			if assigneeEmail != "" {
				if dryRun {
					return fmt.Errorf("validation: %s", assigneeEmailDryRunErr)
				}
				rc, _, hasRC, cerr := cmdutil.JiraClientForCommand(cmd)
				if cerr != nil {
					return cerr
				}
				if !hasRC {
					return fmt.Errorf("jira base URL is required for issue.edit")
				}
				wire, rerr := resolveAssigneeEmail(cmd.Context(), rc, assigneeEmail)
				if rerr != nil {
					return rerr
				}
				fields["assignee"] = wire
			}
			// Lift bare-string system fields ("priority": "Medium",
			// "parent": "PROJ-1") into their canonical wire objects — the
			// same normalization the create path applies, so both commands
			// accept the same payload spellings. Runs after every field
			// source (json-input, flags, resolved assignee) has merged.
			fields = pipeline.LiftSystemFieldShapes(fields)
			// kubectl-style default: bare `jira issue edit KEY` (no field
			// flags, no --json-input) opens the configured external editor on
			// the issue description. The editor needs an interactive terminal,
			// so any headless context refuses and asks for a field instead.
			// NoInputRequested is the single gate: it is true under explicit
			// --no-input, an agent harness, or a piped/redirected stdin — but
			// stays false for `jira issue edit KEY | tee out.json`, which pipes
			// stdout yet keeps an interactive stdin, so that editor still opens.
			// The priority set is small, site-wide, and usually cached — a
			// name Jira would reject fails fast here. Advisory: no cache,
			// no check.
			if perr := validatePriorityAgainstCache(cmd, profile, fields); perr != nil {
				return perr
			}
			if len(fields) == 0 && len(updateSection) == 0 {
				if noInput {
					return fmt.Errorf("validation: the issue edit editor needs an interactive terminal; in a non-interactive context, provide --summary, --assignee, --markdown, or --json-input")
				}
				if len(keys) > 1 {
					return fmt.Errorf("validation: multi-key issue edit requires --summary, --assignee, --markdown, or --json-input")
				}
				return issueEditWithEditor(cmd, keys[0], dryRun)
			}
			// The issue description is the primary ADF document for this
			// mutation. Whether it arrived as `description_markdown` (the
			// --markdown flag or a --json-input key) or as a raw
			// ADF `description`, it is pulled out of the fields map here and
			// fed to the pipeline as ADFDoc so stage 2 (ValidateDoc +
			// ApplyCompatibility) runs on it BEFORE submission. The
			// post-pipeline SubmitADF is the only description that reaches the
			// wire. This mirrors the create path exactly.
			descriptionDoc, descriptionPresent, descMarkdownWarnings, descErr := extractDescriptionDoc(fields)
			if descErr != nil {
				return descErr
			}
			// Any remaining ADF-shaped value in the fields object (e.g. an
			// `environment` document supplied via --json-input) MUST be
			// validated by stage 2 before submission — otherwise garbage
			// nested ADF would only be checked structurally by the
			// customfield encoder, which does not enforce ADF rules.
			// `description` was already removed above, so it is not
			// double-validated here.
			namedADF, adfParseErr := extractNamedADFDocs(fields)
			if adfParseErr != nil {
				return adfParseErr
			}
			if len(keys) > 1 {
				return runIssueEditMany(cmd, keys, parallelism, issueEditManyInput{
					Fields:           fields,
					Update:           updateSection,
					NamedADFDocs:     namedADF,
					DryRun:           dryRun,
					ValidateRemote:   validateRemote,
					DescriptionDoc:   descriptionDoc,
					DescriptionSet:   descriptionPresent,
					MarkdownWarnings: descMarkdownWarnings,
				})
			}
			// For a live edit, resolve the client up front and attach an
			// edit-screen schema fetcher (editmeta) so stage 3 validates
			// the fields against the issue's edit screen. A bare dry-run
			// runs without credentials and therefore without a fetcher;
			// --validate-remote resolves the client for the read-only
			// editmeta fetch so the same stage-3/4 checks run pre-flight.
			var editClient *jira.Client
			if !dryRun || validateRemote {
				var hasClient bool
				editClient, _, hasClient, err = cmdutil.JiraClientForCommand(cmd)
				if err != nil {
					return err
				}
				if !hasClient {
					return fmt.Errorf("jira base URL is required for issue.edit")
				}
			}
			// Thread the mutation through the 5-stage pipeline.
			editIn := pipeline.MutationInput{
				Mode:             cmdutil.ADFModeFor(cmd, true),
				Fields:           fields,
				NamedADFDocs:     namedADF,
				DryRun:           dryRun,
				MarkdownWarnings: descMarkdownWarnings,
			}
			if descriptionPresent {
				// FieldCompat is left with InlineCardSupported=true: a Jira
				// Cloud `description` accepts inlineCard, so no compatibility
				// degradation applies. Same envelope as create.
				editIn.ADFDoc = &descriptionDoc
				editIn.FieldCompat = &adf.FieldCompatibility{Field: "description", InlineCardSupported: true}
			}
			if editClient != nil && (dryRun || !cmdutil.ReadOnlyEnabled(cmd)) {
				editIn.SchemaFetcher = newEditScreenSchemaFetcher(
					cmd.Context(), cmdutil.ServicesForClient(editClient).Project(0),
					cmdutil.ProfileForEnvelope(cmd), keys[0],
				)
			}
			pipeOut := pipeline.RunMutation(editIn)
			if pipeOut.Aborted {
				return pipeOut.Err
			}
			// Submit and preview the validated SubmitFields, not
			// the raw pre-pipeline fields map.
			submitFields := pipeOut.SubmitFields
			if submitFields == nil {
				submitFields = map[string]any{}
			}
			// The validated, compatibility-applied description from the
			// pipeline replaces whatever the payload carried — Markdown and
			// raw ADF are now handled identically.
			if descriptionPresent && pipeOut.SubmitADF != nil {
				submitFields["description"] = *pipeOut.SubmitADF
			}
			if !dryRun {
				var (
					issue *jira.Issue
					resp  *jira.Response
				)
				if err := cmdutil.Spin(cmd, "issue.edit", func(ctx context.Context) error {
					var e error
					issue, resp, e = cmdutil.ServicesForClient(editClient).Issue().Update(ctx, keys[0], &jira.IssueUpdateRequest{Fields: submitFields, Update: updateSection})
					return e
				}); err != nil {
					return err
				}
				data := envelope.IssueEditOutput{
					Issue:             cmdutil.IssueRef{Key: keys[0]},
					Result:            issue,
					DryRun:            false,
					Fields:            submitFields,
					ValidatedRemotely: pipeOut.SchemaValidatedRemotely,
				}
				if len(updateSection) > 0 {
					data.Update = updateSection
				}
				warnings := pipeOut.Warnings
				if verifyWrite {
					verification, vWarnings := runWriteVerification(cmd, editClient, keys[0], submitFields)
					if verification != nil {
						data.Verification = verification
					}
					warnings = append(warnings, vWarnings...)
				}
				return cmdutil.WriteEnvelopeWithResponseAndWarnings(cmd, "issue.edit", data, resp, warnings)
			}
			data := envelope.IssueEditOutput{
				Issue:             cmdutil.IssueRef{Key: keys[0]},
				DryRun:            true,
				Fields:            submitFields,
				ValidatedRemotely: pipeOut.SchemaValidatedRemotely,
			}
			if len(updateSection) > 0 {
				data.Update = updateSection
			}
			return cmdutil.WriteEnvelopeWithWarnings(cmd, "issue.edit", data, pipeOut.Warnings)
		},
	}
	cmdutil.AddDryRunFlag(cmd.Flags(), &dryRun, "Preview mutation without submitting")
	cmdutil.AddBoolVar(cmd.Flags(), &validateRemote, "validate-remote", false, "With `--dry-run`, validate against the live edit screen (read-only editmeta fetch)", clib.FlagExtra{Group: "Validation"})
	cmdutil.AddBoolVar(cmd.Flags(), &verifyWrite, "verify", false, "After a live edit, re-fetch the issue and warn about requested fields the server did not apply (single key only)", clib.FlagExtra{Group: "Validation"})
	cmdutil.AddFileFlag(cmd.Flags(), &jsonInput, "json-input", "", "Read issue edit payload from JSON file (canonical for agents)", "Input", "FILE")
	cmdutil.AddStringVar(cmd.Flags(), &summary, "summary", "", "Replace the issue summary", clib.FlagExtra{Group: "Fields", Placeholder: "TEXT"})
	cmdutil.AddMarkdownFlag(cmd, &markdownInput, &markdownFile, "Replace the description with Markdown (lossy convenience layer, converted to ADF)", "description-markdown")
	cmdutil.AddStringVar(cmd.Flags(), &assignee, "assignee", "", "Set assignee: `me`, `none`/`unassigned`, an email, or a Jira account ID", clib.FlagExtra{Group: "Fields", Placeholder: "USER", Terse: "assignee", Enum: []string{"me", "none"}, EnumTerse: []string{"current user", "unassign"}})
	cmdutil.AddParallelismFlag(cmd, &parallelism)
	return cmd
}

type issueEditManyInput struct {
	Fields map[string]any
	// Update is the native REST add/set/remove operation block, applied
	// identically to every key alongside the fields.
	Update       map[string]any
	NamedADFDocs map[string]adf.Document
	DryRun       bool
	// ValidateRemote opts a dry-run into the read-only editmeta fetch so
	// stage 3/4 validate each key against its live edit screen.
	ValidateRemote bool
	// DescriptionDoc is the primary ADF document (from
	// --markdown or a `description` / `description_markdown`
	// payload key) routed as the pipeline's ADFDoc. DescriptionSet reports
	// whether it is populated; MarkdownWarnings carries any lossy
	// Markdown-conversion warnings so strict mode can abort.
	DescriptionDoc   adf.Document
	DescriptionSet   bool
	MarkdownWarnings []adf.Warning
}

func runIssueEditMany(cmd *cobra.Command, keys []string, parallelism int, in issueEditManyInput) error {
	var editClient *jira.Client
	var service jira.IssueService
	var err error
	if !in.DryRun || in.ValidateRemote {
		var ok bool
		editClient, _, ok, err = cmdutil.JiraClientForCommand(cmd)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("jira base URL is required for issue.edit")
		}
		service = cmdutil.ServicesForClient(editClient).Issue()
	}
	// A dry-run still fans out (per-key pipeline validation, optionally the
	// --validate-remote editmeta read), so the lifecycle must speak about
	// the preview rather than claim edits.
	fanOut := cmdutil.FanOutKeysProgress[envelope.IssueEditOutput]
	if in.DryRun {
		fanOut = cmdutil.FanOutKeysProgressPreview[envelope.IssueEditOutput]
	}
	results, err := fanOut(cmd.Context(), "issue.edit", keys, parallelism, func(ctx context.Context, key string) (envelope.IssueEditOutput, error) {
		editIn := pipeline.MutationInput{
			Mode:             cmdutil.ADFModeFor(cmd, true),
			Fields:           cmdutil.CopyAnyMap(in.Fields),
			NamedADFDocs:     in.NamedADFDocs,
			DryRun:           in.DryRun,
			MarkdownWarnings: in.MarkdownWarnings,
		}
		if in.DescriptionSet {
			// Give each fan-out goroutine its own copy of the description so
			// the shared input is never aliased across pipeline runs.
			doc := in.DescriptionDoc
			editIn.ADFDoc = &doc
			editIn.FieldCompat = &adf.FieldCompatibility{Field: "description", InlineCardSupported: true}
		}
		if editClient != nil && (in.DryRun || !cmdutil.ReadOnlyEnabled(cmd)) {
			editIn.SchemaFetcher = newEditScreenSchemaFetcher(
				ctx, cmdutil.ServicesForClient(editClient).Project(0),
				cmdutil.ProfileForEnvelope(cmd), key,
			)
		}
		pipeOut := pipeline.RunMutation(editIn)
		if pipeOut.Aborted {
			return envelope.IssueEditOutput{}, pipeOut.Err
		}
		submitFields := pipeOut.SubmitFields
		if submitFields == nil {
			submitFields = map[string]any{}
		}
		if in.DescriptionSet && pipeOut.SubmitADF != nil {
			submitFields["description"] = *pipeOut.SubmitADF
		}
		data := envelope.IssueEditOutput{
			Issue:             cmdutil.IssueRef{Key: key},
			DryRun:            in.DryRun,
			Fields:            submitFields,
			ValidatedRemotely: pipeOut.SchemaValidatedRemotely,
		}
		if len(in.Update) > 0 {
			data.Update = in.Update
		}
		if len(pipeOut.Warnings) > 0 {
			data.Warnings = pipeOut.Warnings
		}
		if in.DryRun {
			return data, nil
		}
		issue, _, err := service.Update(ctx, key, &jira.IssueUpdateRequest{Fields: submitFields, Update: in.Update})
		if err != nil {
			return envelope.IssueEditOutput{}, err
		}
		data.Result = issue
		data.DryRun = false
		return data, nil
	})
	if err != nil {
		return err
	}
	return cmdutil.WriteKeyedResultsEnvelope(cmd, "issue.edit", results, func(_ string, data envelope.IssueEditOutput) any { return data })
}

func issueEditWithEditor(cmd *cobra.Command, key string, dryRun bool) error {
	client, _, ok, err := cmdutil.JiraClientForCommand(cmd)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("jira base URL is required for issue.edit --edit")
	}
	issueService := cmdutil.ServicesForClient(client).Issue()
	var issue *jira.Issue
	if err := cmdutil.Spin(cmd, "issue.view", func(ctx context.Context) error {
		var e error
		issue, _, e = issueService.Get(ctx, key, &jira.IssueGetOptions{})
		return e
	}); err != nil {
		return err
	}
	doc := adf.Document{Type: "doc", Version: 1}
	if issue != nil && issue.Fields != nil && issue.Fields.Description != nil {
		doc = *issue.Fields.Description
	}
	// Route the edit through the opaque-preserving round-trip. Blocks
	// with no faithful Markdown representation — panels, tables,
	// inlineCards, mentions — are carried through the editor buffer as
	// protected opaque fences and reconstituted byte-for-byte, so a
	// no-op save can no longer erase rich Jira content. A plain
	// adf.ToMarkdown render would have dropped them before the user
	// ever saw the buffer.
	updatedDoc, editWarnings, err := editorpkg.RoundTripADF(cmd.Context(), editorpkg.RoundTripADFOptions{
		IssueKey:  key,
		FieldName: "description",
		Document:  doc,
		EditCmd:   cmdutil.ConfiguredEditorFor(cmd),
	})
	if err != nil {
		return err
	}
	// External-editor edits ARE mutations and MUST run through the
	// pipeline. The edited description is the only field, and it is an
	// ADF document — route it solely as ADFDoc so stage 2 (ValidateDoc
	// + ApplyCompatibility) owns it. It is deliberately NOT also placed
	// in Fields: a single field must travel one pipeline channel, so
	// there is no last-write-wins reconciliation between SubmitFields
	// and SubmitADF. Stage 4 (customfield encoding) has nothing to do
	// for an empty Fields map.
	//
	// The round-trip's warnings — lossy Markdown conversions on the
	// edited text plus opaque-preservation notices — travel as
	// MarkdownWarnings so strict mode aborts on genuine content loss
	// before submission.
	pipeOut := pipeline.RunMutation(pipeline.MutationInput{
		Mode:             cmdutil.ADFModeFor(cmd, true),
		ADFDoc:           &updatedDoc,
		FieldCompat:      &adf.FieldCompatibility{Field: "description", InlineCardSupported: true},
		MarkdownWarnings: editWarnings,
		DryRun:           dryRun,
	})
	if pipeOut.Aborted {
		return pipeOut.Err
	}
	// Submit the validated, compatibility-applied SubmitADF — not
	// the pre-pipeline edit. description is the sole field on the wire.
	submitFields := map[string]any{}
	if pipeOut.SubmitADF != nil {
		submitFields["description"] = *pipeOut.SubmitADF
	}
	if dryRun {
		return cmdutil.WriteEnvelopeWithWarnings(cmd, "issue.edit", envelope.IssueEditOutput{
			Issue:  cmdutil.IssueRef{Key: key},
			DryRun: true,
			Fields: submitFields,
		}, pipeOut.Warnings)
	}
	var (
		updatedIssue *jira.Issue
		resp         *jira.Response
	)
	if err := cmdutil.Spin(cmd, "issue.edit", func(ctx context.Context) error {
		var e error
		updatedIssue, resp, e = issueService.Update(ctx, key, &jira.IssueUpdateRequest{Fields: submitFields})
		return e
	}); err != nil {
		return err
	}
	return cmdutil.WriteEnvelopeWithResponseAndWarnings(cmd, "issue.edit", envelope.IssueEditOutput{
		Issue:  cmdutil.IssueRef{Key: key},
		Result: updatedIssue,
		DryRun: false,
		Fields: submitFields,
	}, resp, pipeOut.Warnings)
}

func issueTransitionCommand() *cobra.Command {
	var dryRun, validateRemote bool
	var transitionID, jsonInput, markdownInput, markdownFile string
	var parallelism int
	returnCmd := &cobra.Command{
		Use:         "transition KEY... [STATUS]",
		Annotations: issueKeyArg,
		Short:       "Transition an issue to a new status",
		Long: "Move one or more issues to a workflow status. Use it when advancing work " +
			"through Jira from a script or terminal triage session.\n\n" +
			"Give the target status as a trailing argument, such as `In Progress`, or pass a " +
			"numeric transition ID. A status name is resolved against each issue's available " +
			"transitions at runtime; the same name can map to different IDs across workflows.\n\n" +
			"With no target, the command lists available transitions instead of mutating. " +
			"`--dry-run` is a local preview and echoes the requested target without resolving " +
			"it through Jira.",
		Example: `# List available transitions for an issue
$ jira issue transition PROJ-123

# Move an issue to a named status
$ jira issue transition PROJ-123 "In Progress"

# Preview transitioning several issues without contacting Jira
$ jira issue transition PROJ-123 PROJ-124 Done --dry-run`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, keyArgs := splitTransitionTarget(args, transitionID)
			keys, err := issuekey.ParseExpressions(keyArgs, issuekey.Options{MaxExpansion: issuekey.DefaultMaxExpansion})
			if err != nil {
				return err
			}
			if len(keys) == 0 {
				return fmt.Errorf("validation: issue transition requires an issue key")
			}
			payload, perr := resolveTransitionPayload(jsonInput, markdownInput, markdownFile)
			if perr != nil {
				return perr
			}
			// A native REST body may name its own target in the payload's
			// transition section; reconcile it with the CLI target before
			// any branch reads it.
			target, err = mergeTransitionTarget(target, payload.target)
			if err != nil {
				return err
			}
			if validateRemote {
				if !dryRun {
					return fmt.Errorf("validation: --validate-remote is a read-only dry-run pre-flight; combine it with --dry-run (a live transition already resolves the target)")
				}
				if target == "" {
					return fmt.Errorf("validation: --validate-remote needs a transition target to resolve against the issue's live transitions")
				}
			}
			if len(payload.fields) > 0 {
				// Priority is site-wide and usually cached — a name Jira
				// would reject fails fast. Advisory: no cache, no check.
				if profile, perr := cmdutil.ProfileForCommand(cmd); perr == nil {
					if verr := validatePriorityAgainstCache(cmd, profile, payload.fields); verr != nil {
						return verr
					}
				}
			}
			if len(keys) > 1 {
				if target != "" {
					return runIssueTransitionMany(cmd, keys, parallelism, target, dryRun, validateRemote, payload)
				}
				client, _, ok, err := cmdutil.JiraClientForCommand(cmd)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("jira base URL is required for issue.transitions")
				}
				service := cmdutil.ServicesForClient(client).Issue()
				results, err := cmdutil.FanOutKeysProgress(cmd.Context(), "issue.transitions", keys, parallelism, func(ctx context.Context, key string) ([]*jira.Transition, error) {
					transitions, _, err := service.Transitions(ctx, key)
					return transitions, err
				})
				if err != nil {
					return err
				}
				return cmdutil.WriteKeyedResultsEnvelope(cmd, "issue.transitions", results, func(key string, transitions []*jira.Transition) any {
					return envelope.IssueTransitionsOutput{Issue: cmdutil.IssueRef{Key: key}, Transitions: transitions}
				})
			}
			key := keys[0]
			if !payload.empty() && target == "" {
				return fmt.Errorf("validation: a transition payload needs a target status — fields and comments are applied atomically with the status change")
			}
			// A workflow transition is a Jira mutation and MUST run
			// through the 5-stage pipeline: fields validate and encode,
			// a Markdown comment converts with strict/best-effort gating.
			pipeIn := pipeline.MutationInput{
				Mode:             cmdutil.ADFModeFor(cmd, true),
				Fields:           payload.fields,
				DryRun:           dryRun,
				MarkdownWarnings: payload.warnings,
			}
			if payload.comment != nil {
				pipeIn.ADFDoc = payload.comment
				pipeIn.FieldCompat = &adf.FieldCompatibility{Field: "comment", InlineCardSupported: true}
			}
			pipeOut := pipeline.RunMutation(pipeIn)
			if pipeOut.Aborted {
				return pipeOut.Err
			}
			submitFields := pipeOut.SubmitFields
			comment := payload.comment
			if pipeOut.SubmitADF != nil {
				comment = pipeOut.SubmitADF
			}
			if dryRun {
				// A bare dry-run is local: it echoes the requested target
				// without resolving a name to an id (that needs a live
				// list). --validate-remote opts into the read-only
				// transitions fetch, resolving the target and running the
				// screenless-payload refusal before any state change.
				data := transitionData(key, target, submitFields, comment, payload.update, true, false)
				if validateRemote {
					client, _, ok, cerr := cmdutil.JiraClientForCommand(cmd)
					if cerr != nil {
						return cerr
					}
					if !ok {
						return fmt.Errorf("jira base URL is required for issue.transition --validate-remote")
					}
					service := cmdutil.ServicesForClient(client).Issue()
					var id string
					// The op is issue.transitions (the read-only fetch this
					// preview performs), not issue.transition: a dry-run
					// spinner saying "Transitioning issue" would overstate a
					// validation that transitions nothing.
					if err := cmdutil.Spin(cmd, "issue.transitions", func(ctx context.Context) error {
						var e error
						id, e = resolveTransitionValidated(ctx, service, key, target, !payload.empty())
						return e
					}); err != nil {
						return err
					}
					data.Transition = id
					data.TransitionValidated = true
				}
				return cmdutil.WriteEnvelopeWithWarnings(cmd, "issue.transition", data, pipeOut.Warnings)
			}
			client, _, ok, err := cmdutil.JiraClientForCommand(cmd)
			if err != nil {
				return err
			}
			if target == "" {
				// No target → list available transitions (a read). Offline
				// (no base URL) returns a bare envelope, as before.
				if !ok {
					return cmdutil.WriteEnvelopeWithWarnings(cmd, "issue.transition", envelope.IssueTransitionOutput{Issue: cmdutil.IssueRef{Key: key}, DryRun: dryRun}, pipeOut.Warnings)
				}
				var (
					transitions []*jira.Transition
					resp        *jira.Response
				)
				if err := cmdutil.Spin(cmd, "issue.transitions", func(ctx context.Context) error {
					var e error
					transitions, resp, e = cmdutil.ServicesForClient(client).Issue().Transitions(ctx, key)
					return e
				}); err != nil {
					return err
				}
				return cmdutil.WriteEnvelopeWithResponseAndWarnings(cmd, "issue.transitions", envelope.IssueTransitionsOutput{Issue: cmdutil.IssueRef{Key: key}, Transitions: transitions}, resp, pipeOut.Warnings)
			}
			if !ok {
				return fmt.Errorf("jira base URL is required for issue.transition")
			}
			service := cmdutil.ServicesForClient(client).Issue()
			// resolveTransitionID makes its own metadata fetch, so wrap it and
			// the apply in one spin — they are a single "Transitioning" operation.
			var (
				id   string
				resp *jira.Response
			)
			if err := cmdutil.Spin(cmd, "issue.transition", func(ctx context.Context) error {
				var e error
				id, e = resolveTransitionForPayload(ctx, service, key, target, !payload.empty())
				if e != nil {
					return e
				}
				resp, e = service.Transition(ctx, key, &jira.TransitionRequest{ID: id, Fields: submitFields, Comment: comment, Update: payload.update})
				return e
			}); err != nil {
				return err
			}
			data := transitionData(key, id, submitFields, comment, payload.update, false, true)
			return cmdutil.WriteEnvelopeWithResponseAndWarnings(cmd, "issue.transition", data, resp, pipeOut.Warnings)
		},
	}
	cmdutil.AddDryRunFlag(returnCmd.Flags(), &dryRun, "Preview mutation without submitting")
	cmdutil.AddBoolVar(returnCmd.Flags(), &validateRemote, "validate-remote", false, "With `--dry-run`, resolve the target against the issue's live transitions (read-only fetch)", clib.FlagExtra{Group: "Validation"})
	cmdutil.AddStringVar(returnCmd.Flags(), &transitionID, "transition", "", "Transition id or status name (or pass the status as a positional argument)", clib.FlagExtra{Group: "Transition", Placeholder: "STATUS", Complete: "predictor=cachestatus"})
	cmdutil.AddFileFlag(returnCmd.Flags(), &jsonInput, "json-input", "", "Read transition payload from JSON file (canonical for agents)", "Input", "FILE")
	cmdutil.AddMarkdownFlag(returnCmd, &markdownInput, &markdownFile, "Transition comment as Markdown, posted atomically with the status change", "")
	cmdutil.AddParallelismFlag(returnCmd, &parallelism)
	return returnCmd
}

// transitionPayload carries the optional field updates and comment a
// transition submits atomically with the status change, plus the target
// and update block a native REST transition body may name itself.
type transitionPayload struct {
	fields  map[string]any
	comment *adf.Document
	// update is the native REST update block, forwarded verbatim.
	update map[string]any
	// target is the transition named by the payload's own `transition`
	// section (an id or a name); reconciled with the CLI target.
	target   string
	warnings []adf.Warning
}

func (p transitionPayload) empty() bool {
	return len(p.fields) == 0 && p.comment == nil && len(p.update) == 0
}

// resolveTransitionPayload builds the transition payload from --markdown /
// --markdown-file (the comment) and --json-input. The JSON path accepts the
// exact POST /rest/api/3/issue/{key}/transitions body — `transition`,
// `fields`, and `update` — as well as the flat field shape and the ADF
// `comment` convenience key. The flag mutexes guarantee at most one comment
// source between the flags; a payload that carries both an update.comment
// operation and a comment is refused below for the same reason.
func resolveTransitionPayload(jsonInput, markdown, markdownFile string) (transitionPayload, error) {
	var p transitionPayload
	md, err := cmdutil.ResolveMarkdownInput(markdown, markdownFile)
	if err != nil {
		return p, err
	}
	if md != "" {
		doc, warns, cerr := adf.FromMarkdownLossy(md)
		if cerr != nil {
			return p, cerr
		}
		p.comment = &doc
		p.warnings = warns
	}
	if jsonInput == "" {
		return p, nil
	}
	payload := map[string]any{}
	if err := cmdutil.ReadJSONFile(jsonInput, &payload); err != nil {
		return p, err
	}
	// The native sections are siblings of `fields` and must come out
	// BEFORE the field-set normalization would fold them into the field
	// map as bogus issue fields.
	if raw, ok := payload["transition"]; ok {
		delete(payload, "transition")
		target, terr := transitionTargetFromPayload(raw)
		if terr != nil {
			return p, terr
		}
		p.target = target
	}
	if raw, ok := payload["update"]; ok {
		delete(payload, "update")
		section, isMap := raw.(map[string]any)
		if !isMap {
			return p, fmt.Errorf("validation: transition --json-input update must be an object of field operations, matching the Jira REST update section")
		}
		p.update = section
	}
	if raw, ok := payload["comment"]; ok {
		delete(payload, "comment")
		encoded, merr := json.Marshal(raw)
		if merr != nil {
			return p, merr
		}
		doc, _, perr := adf.Parse(encoded)
		if perr != nil {
			return p, fmt.Errorf("transition --json-input comment: %w", perr)
		}
		p.comment = &doc
	}
	if _, hasUpdateComment := p.update["comment"]; hasUpdateComment && p.comment != nil {
		return p, fmt.Errorf("validation: the transition comment is set twice — as an update.comment operation and as a comment input; supply it once")
	}
	if fields := pipeline.FieldsFromPayload(payload); len(fields) > 0 {
		p.fields = fields
	}
	return p, nil
}

// transitionTargetFromPayload reads a native REST `transition` section:
// {"id": "31"}, {"name": "Done"}, or a bare string. A numeric JSON id is
// accepted too — agents copying API examples write both spellings.
func transitionTargetFromPayload(raw any) (string, error) {
	switch v := raw.(type) {
	case string:
		if s := strings.TrimSpace(v); s != "" {
			return s, nil
		}
	case map[string]any:
		switch id := v["id"].(type) {
		case string:
			if s := strings.TrimSpace(id); s != "" {
				return s, nil
			}
		case float64:
			return strconv.FormatInt(int64(id), 10), nil
		}
		if name, ok := v["name"].(string); ok && !xstrings.IsBlank(name) {
			return strings.TrimSpace(name), nil
		}
	}
	return "", fmt.Errorf("validation: transition --json-input transition must carry an id or a name, matching the Jira REST transition section")
}

// mergeTransitionTarget reconciles the CLI target (positional STATUS or
// --transition) with a native payload's transition section. Either spot may
// name the target and agreement is not a conflict; two different values are —
// the CLI cannot know which one was meant.
func mergeTransitionTarget(flagTarget, payloadTarget string) (string, error) {
	switch {
	case payloadTarget == "":
		return flagTarget, nil
	case flagTarget == "" || strings.EqualFold(flagTarget, payloadTarget):
		return payloadTarget, nil
	}
	return "", fmt.Errorf("validation: the transition target is set twice — %q on the command line and %q in the payload's transition section; supply it once or align the values", flagTarget, payloadTarget)
}

// validatePriorityAgainstCache refuses a priority name absent from the
// cached site priority set. The check is advisory-by-cache: with no
// readable priorities cache it passes silently, but when the site's
// priorities ARE known locally a hallucinated name fails fast — with
// the valid set in the message — instead of round-tripping to Jira.
// An id-addressed priority ({"id": ...}) is not checked; ids are not
// worth caching against.
func validatePriorityAgainstCache(cmd *cobra.Command, profile config.Profile, fields map[string]any) error {
	priority, isMap := fields["priority"].(map[string]any)
	if !isMap {
		return nil
	}
	name, isString := priority["name"].(string)
	if !isString || xstrings.IsBlank(name) {
		return nil
	}
	var cached []struct {
		Name string `json:"name"`
	}
	if !clijql.ReadCacheJSON(cmdutil.CacheKeyForProfile(cmd, profile), "priorities", &cached) || len(cached) == 0 {
		return nil
	}
	names := make([]string, 0, len(cached))
	for _, p := range cached {
		if strings.EqualFold(p.Name, strings.TrimSpace(name)) {
			return nil
		}
		names = append(names, p.Name)
	}
	return fmt.Errorf("validation: priority %q is not one of this site's priorities (%s); refresh with `jira cache priorities --refresh` if the set changed", name, strings.Join(names, ", "))
}

// popUpdateSection removes a native REST `update` block (add / set / remove
// operations) from a --json-input payload before the field-set normalization
// would fold it into the fields map. The block is forwarded verbatim as a
// sibling of fields; Jira validates the operation verbs server-side.
func popUpdateSection(payload map[string]any) (map[string]any, error) {
	raw, ok := payload["update"]
	if !ok {
		return nil, nil
	}
	section, isMap := raw.(map[string]any)
	if !isMap {
		return nil, fmt.Errorf("validation: the update block must be an object of field operations, matching the Jira REST update section")
	}
	delete(payload, "update")
	return section, nil
}

// splitTransitionTarget separates the transition target (a status name or id)
// from the issue-key arguments. An explicit --transition flag is the target and
// leaves every positional as a key. Otherwise the keys are the leading run of
// issue-key expressions and any remaining arguments form the target, joined
// with spaces — so both `transition KEY "In Progress"` and the unquoted
// `transition KEY In Progress` work, while `transition KEY1 KEY2` (all keys)
// still lists transitions. A mistyped key in the trailing position (e.g.
// lowercase) is intentionally read as a status name; it then surfaces as a
// "no transition matching" error rather than an invalid-key one.
func splitTransitionTarget(args []string, flagTarget string) (target string, keyArgs []string) {
	if t := strings.TrimSpace(flagTarget); t != "" {
		return t, args
	}
	split := len(args)
	for i, arg := range args {
		if !issuekey.IsExpression(arg) {
			split = i
			break
		}
	}
	if split == len(args) {
		return "", args
	}
	return strings.TrimSpace(strings.Join(args[split:], " ")), args[:split]
}

// resolveTransitionID turns a target (a transition id or a status name) into a
// transition id. A purely numeric target is treated as an id and used directly,
// preserving the --transition <id> fast path with no extra request. Anything
// else is matched case-insensitively against the issue's available transitions
// by name, then by id.
func resolveTransitionID(ctx context.Context, service jira.IssueService, key, target string) (string, error) {
	target = strings.TrimSpace(target)
	if isAllDigits(target) {
		return target, nil
	}
	transitions, _, err := service.Transitions(ctx, key)
	if err != nil {
		return "", err
	}
	return matchTransition(transitions, target, key)
}

// resolveTransitionForPayload resolves the target like resolveTransitionID,
// but a payload-carrying transition always fetches the transition list —
// even for a numeric target — so the screen check can run: Jira accepts a
// fields/update block on a screenless transition and silently discards it
// (the norm in team-managed projects), which is exactly the silent loss
// this CLI refuses to pass along.
func resolveTransitionForPayload(ctx context.Context, service jira.IssueService, key, target string, hasPayload bool) (string, error) {
	if !hasPayload {
		return resolveTransitionID(ctx, service, key, target)
	}
	return resolveTransitionValidated(ctx, service, key, target, true)
}

// resolveTransitionValidated always fetches the issue's transition list and
// matches the target against it — no numeric fast path, because the whole
// point is proving the id exists on THIS issue. requireScreen additionally
// applies the screenless-payload refusal. This is the --validate-remote
// resolver; a bogus all-digit id must fail here, never echo back "validated".
func resolveTransitionValidated(ctx context.Context, service jira.IssueService, key, target string, requireScreen bool) (string, error) {
	transitions, _, err := service.Transitions(ctx, key)
	if err != nil {
		return "", err
	}
	id, err := matchTransition(transitions, strings.TrimSpace(target), key)
	if err != nil {
		return "", err
	}
	if requireScreen {
		for _, t := range transitions {
			if t.ID == nil || *t.ID != id {
				continue
			}
			if t.HasScreen != nil && !*t.HasScreen {
				return "", fmt.Errorf("validation: this workflow's %q transition has no screen, and Jira silently discards fields and comments sent with a screenless transition; run the transition bare, then add the comment with `jira issue comment add`", target)
			}
			break
		}
	}
	return id, nil
}

// matchTransition finds the id of the transition whose name (case-insensitive)
// or id equals target, preferring a name match. It errors with the available
// transitions when nothing matches.
func matchTransition(transitions []*jira.Transition, target, key string) (string, error) {
	for _, t := range transitions {
		if t.Name != nil && t.ID != nil && strings.EqualFold(strings.TrimSpace(*t.Name), target) {
			return *t.ID, nil
		}
	}
	for _, t := range transitions {
		if t.ID != nil && *t.ID == target {
			return *t.ID, nil
		}
	}
	return "", fmt.Errorf("validation: no transition matching %q for %s; available: %s", target, key, transitionNames(transitions))
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// transitionNames renders the available transitions as "Name (id)" for an
// error hint when a requested status cannot be matched.
func transitionNames(transitions []*jira.Transition) string {
	names := make([]string, 0, len(transitions))
	for _, t := range transitions {
		if t.Name == nil {
			continue
		}
		label := *t.Name
		if t.ID != nil {
			label += " (" + *t.ID + ")"
		}
		names = append(names, label)
	}
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

// transitionData assembles the stable per-issue context for both preview and
// live transition output.
func transitionData(
	key, target string,
	fields map[string]any,
	comment *adf.Document,
	update map[string]any,
	dryRun, validated bool,
) envelope.IssueTransitionOutput {
	data := envelope.IssueTransitionOutput{
		Issue:               cmdutil.IssueRef{Key: key},
		Transition:          target,
		DryRun:              dryRun,
		TransitionValidated: validated,
	}
	if len(fields) > 0 {
		data.Fields = fields
	}
	if comment != nil {
		data.Comment = *comment
	}
	if len(update) > 0 {
		data.Update = update
	}
	return data
}

func runIssueTransitionMany(cmd *cobra.Command, keys []string, parallelism int, target string, dryRun, validateRemote bool, payload transitionPayload) error {
	// One payload, applied identically to every key — the bulk form of
	// "close these with the same resolution note".
	pipeIn := pipeline.MutationInput{
		Mode:             cmdutil.ADFModeFor(cmd, true),
		Fields:           payload.fields,
		DryRun:           dryRun,
		MarkdownWarnings: payload.warnings,
	}
	if payload.comment != nil {
		pipeIn.ADFDoc = payload.comment
		pipeIn.FieldCompat = &adf.FieldCompatibility{Field: "comment", InlineCardSupported: true}
	}
	pipeOut := pipeline.RunMutation(pipeIn)
	if pipeOut.Aborted {
		return pipeOut.Err
	}
	submitFields := pipeOut.SubmitFields
	comment := payload.comment
	if pipeOut.SubmitADF != nil {
		comment = pipeOut.SubmitADF
	}
	if dryRun {
		if validateRemote {
			// Resolve the target per issue with the read-only transitions
			// fetch — the same status name can map to different ids across
			// workflow states, and a payload-carrying transition gets the
			// screenless refusal per key.
			client, _, ok, cerr := cmdutil.JiraClientForCommand(cmd)
			if cerr != nil {
				return cerr
			}
			if !ok {
				return fmt.Errorf("jira base URL is required for issue.transition --validate-remote")
			}
			service := cmdutil.ServicesForClient(client).Issue()
			// This branch is always a dry-run; the per-key work is the
			// read-only target resolution, so the lifecycle previews.
			results, rerr := cmdutil.FanOutKeysProgressPreview(cmd.Context(), "issue.transition", keys, parallelism, func(ctx context.Context, key string) (envelope.IssueTransitionOutput, error) {
				id, resolveErr := resolveTransitionValidated(ctx, service, key, target, !payload.empty())
				if resolveErr != nil {
					return envelope.IssueTransitionOutput{}, resolveErr
				}
				value := transitionData(key, id, submitFields, comment, payload.update, true, true)
				value.Warnings = pipeOut.Warnings
				return value, nil
			})
			if rerr != nil {
				return rerr
			}
			return cmdutil.WriteKeyedResultsEnvelope(cmd, "issue.transition", results, func(_ string, data envelope.IssueTransitionOutput) any { return data })
		}
		results := xslices.Map(keys, func(key string) cmdutil.KeyResult[envelope.IssueTransitionOutput] {
			value := transitionData(key, target, submitFields, comment, payload.update, true, false)
			value.Warnings = pipeOut.Warnings
			return cmdutil.KeyResult[envelope.IssueTransitionOutput]{Key: key, Value: value}
		})
		return cmdutil.WriteKeyedResultsEnvelope(cmd, "issue.transition", results, func(_ string, data envelope.IssueTransitionOutput) any { return data })
	}
	client, _, ok, err := cmdutil.JiraClientForCommand(cmd)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("jira base URL is required for issue.transition")
	}
	service := cmdutil.ServicesForClient(client).Issue()
	// Resolve the target per issue: the same status name can map to
	// different transition ids across issues in different workflow states.
	results, err := cmdutil.FanOutKeysProgress(cmd.Context(), "issue.transition", keys, parallelism, func(ctx context.Context, key string) (envelope.IssueTransitionOutput, error) {
		id, err := resolveTransitionForPayload(ctx, service, key, target, !payload.empty())
		if err != nil {
			return envelope.IssueTransitionOutput{}, err
		}
		if _, err := service.Transition(ctx, key, &jira.TransitionRequest{ID: id, Fields: submitFields, Comment: comment, Update: payload.update}); err != nil {
			return envelope.IssueTransitionOutput{}, err
		}
		value := transitionData(key, id, submitFields, comment, payload.update, false, true)
		value.Warnings = pipeOut.Warnings
		return value, nil
	})
	if err != nil {
		return err
	}
	return cmdutil.WriteKeyedResultsEnvelope(cmd, "issue.transition", results, func(_ string, data envelope.IssueTransitionOutput) any { return data })
}

func destructiveIssueCommand(name, short string) *cobra.Command {
	var dryRun, force, deleteSubtasks bool
	var jsonInput string
	var parallelism int
	cmd := &cobra.Command{
		Use:   name + " KEY...",
		Short: short,
		Long: "Run the `issue " + name + "` mutation for one or more issue keys. Use it " +
			"when you need the shared clone, move, or delete safety path rather than a " +
			"specialised Jira screen.\n\n" +
			"`--dry-run` previews the mutation locally and does not contact Jira. Live " +
			"clone and move payloads run through the validate-and-encode pipeline before " +
			"submission; delete carries no field payload.\n\n" +
			"Live destructive operations require `--force` in headless, agent, or " +
			"`--no-input` mode. Without `--force`, an interactive terminal is prompted.",
		Example: "# Preview the change without applying it\n" +
			"$ jira issue " + name + " PROJ-123 --dry-run\n\n" +
			"# Apply the change non-interactively\n" +
			"$ jira issue " + name + " PROJ-123 --force",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			keys, err := issuekey.ParseExpressions(args, issuekey.Options{MaxExpansion: issuekey.DefaultMaxExpansion})
			if err != nil {
				return err
			}
			noInput := cmdutil.NoInputRequested(cmd)
			payload := map[string]any{"fields": map[string]any{}}
			if jsonInput != "" {
				if err := cmdutil.ReadJSONFile(jsonInput, &payload); err != nil {
					return err
				}
			}
			// Destructive commands ARE mutations and MUST run through
			// the pipeline. delete has no fields to validate (stages
			// 2-4 no-op); clone/move carry a fields payload that stages
			// 3 (screen schema) and 4 (customfield encoding) validate.
			pipeFields := issueFieldsFromPayload(payload)
			if len(keys) > 1 {
				return runDestructiveIssueMany(cmd, name, keys, parallelism, destructiveIssueManyInput{
					Fields:         pipeFields,
					DryRun:         dryRun,
					Force:          force,
					DeleteSubtasks: deleteSubtasks,
				})
			}
			// For a live clone/move, resolve the client up front and
			// attach an edit-screen schema fetcher (editmeta on the
			// source issue) so stage 3 validates the override fields
			// against a real screen. delete carries no fields, and a
			// dry-run runs without credentials — neither gets a fetcher.
			var destructiveClient *jira.Client
			if !dryRun && name != "delete" {
				var hasClient bool
				destructiveClient, _, hasClient, err = cmdutil.JiraClientForCommand(cmd)
				if err != nil {
					return err
				}
				if !hasClient {
					return fmt.Errorf("jira base URL is required for issue.%s", name)
				}
			}
			destructiveIn := pipeline.MutationInput{
				Mode:   cmdutil.ADFModeFor(cmd, true),
				Fields: pipeFields,
				DryRun: dryRun,
			}
			if name == "move" {
				destructiveIn.ScreenValidationExemptFields = map[string]bool{
					"project":   true,
					"issuetype": true,
				}
			}
			if destructiveClient != nil && !cmdutil.ReadOnlyEnabled(cmd) {
				destructiveIn.SchemaFetcher = newEditScreenSchemaFetcher(
					cmd.Context(), cmdutil.ServicesForClient(destructiveClient).Project(0),
					cmdutil.ProfileForEnvelope(cmd), keys[0],
				)
			}
			pipeOut := pipeline.RunMutation(destructiveIn)
			if pipeOut.Aborted {
				return pipeOut.Err
			}
			// Clone/move submit the validated SubmitFields. delete
			// carries no field payload, so SubmitFields is an empty map.
			submitFields := pipeOut.SubmitFields
			if submitFields == nil {
				submitFields = map[string]any{}
			}
			outputPayload := envelope.IssueFieldsPayload{Fields: submitFields}
			if dryRun {
				return cmdutil.WriteEnvelopeWithWarnings(cmd, "issue."+name, envelope.IssueDestructiveOutput{Issue: cmdutil.IssueRef{Key: keys[0]}, Payload: outputPayload, DryRun: true}, pipeOut.Warnings)
			}
			// Destructive op safety: in TTY mode (a human at the
			// keyboard) require either --force OR an interactive
			// "are you sure?" confirmation. Headless / agent shells
			// MUST pass --force explicitly — the auto-detect refuses
			// to prompt them.
			det := cmdutil.DetectorFromContext(cmd)
			if !force {
				// Non-TTY / agent / --no-input → MUST pass --force.
				// We refuse to prompt headless callers and refuse to
				// proceed without explicit consent.
				if !det.IsTTY || det.Agent || noInput {
					return cli.NewCLIInputError(cli.InputForceRequired, fmt.Sprintf("issue %s requires --force in headless / agent / --no-input mode", name))
				}
				// TTY human → huh confirmation prompt.
				if ok, err := confirmDestructive(cmd, name, keys[0]); err != nil {
					return err
				} else if !ok {
					return cli.NewPromptError(cli.PromptAborted, "issue "+name, nil)
				}
			}
			// clone/move already resolved a client up front to drive the
			// schema fetcher; delete resolves one here.
			client, ok := destructiveClient, destructiveClient != nil
			if client == nil {
				client, _, ok, err = cmdutil.JiraClientForCommand(cmd)
				if err != nil {
					return err
				}
			}
			if ok && !dryRun {
				service := cmdutil.ServicesForClient(client).Issue()
				var resp *jira.Response
				var issue *jira.Issue
				if err := cmdutil.Spin(cmd, "issue."+name, func(ctx context.Context) error {
					var e error
					switch name {
					case "delete":
						resp, e = service.Delete(ctx, keys[0], &jira.IssueDeleteOptions{DeleteSubtasks: deleteSubtasks})
					case "clone":
						issue, resp, e = service.Clone(ctx, keys[0], &jira.IssueCloneRequest{Fields: submitFields})
					case "move":
						issue, resp, e = service.Move(ctx, keys[0], &jira.IssueMoveRequest{Fields: submitFields})
					}
					return e
				}); err != nil {
					return err
				}
				return cmdutil.WriteEnvelopeWithResponseAndWarnings(cmd, "issue."+name, envelope.IssueDestructiveOutput{Issue: cmdutil.IssueRef{Key: keys[0]}, Result: issue, Payload: outputPayload, DryRun: false}, resp, pipeOut.Warnings)
			}
			return fmt.Errorf("jira base URL is required for issue.%s", name)
		},
	}
	cmdutil.AddDryRunFlag(cmd.Flags(), &dryRun, "Preview mutation without submitting")
	cmdutil.AddForceFlag(cmd.Flags(), &force, "Confirm destructive mutation")
	cmdutil.AddFileFlag(cmd.Flags(), &jsonInput, "json-input", "", "Read mutation payload from JSON file (canonical for agents)", "Input", "FILE")
	if name == "delete" {
		cmdutil.AddBoolVar(cmd.Flags(), &deleteSubtasks, "delete-subtasks", false, "Also delete the issue's subtasks (Jira refuses delete otherwise when subtasks exist)", clib.FlagExtra{Group: "Safety"})
	}
	cmdutil.AddParallelismFlag(cmd, &parallelism)
	return cmd
}

type destructiveIssueManyInput struct {
	Fields         map[string]any
	DryRun         bool
	Force          bool
	DeleteSubtasks bool
}

func runDestructiveIssueMany(
	cmd *cobra.Command,
	name string,
	keys []string,
	parallelism int,
	in destructiveIssueManyInput,
) error {
	if !in.DryRun && !in.Force {
		return cli.NewCLIInputError(cli.InputForceRequired, fmt.Sprintf("issue %s with multiple keys requires --force", name))
	}
	var client *jira.Client
	var service jira.IssueService
	var err error
	if !in.DryRun {
		var ok bool
		client, _, ok, err = cmdutil.JiraClientForCommand(cmd)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("jira base URL is required for issue.%s", name)
		}
		service = cmdutil.ServicesForClient(client).Issue()
	}
	// A dry-run still fans out for per-key pipeline validation, so the
	// lifecycle must speak about the preview rather than claim deletions.
	fanOut := cmdutil.FanOutKeysProgress[envelope.IssueDestructiveOutput]
	if in.DryRun {
		fanOut = cmdutil.FanOutKeysProgressPreview[envelope.IssueDestructiveOutput]
	}
	results, err := fanOut(cmd.Context(), "issue."+name, keys, parallelism, func(ctx context.Context, key string) (envelope.IssueDestructiveOutput, error) {
		destructiveIn := pipeline.MutationInput{
			Mode:   cmdutil.ADFModeFor(cmd, true),
			Fields: cmdutil.CopyAnyMap(in.Fields),
			DryRun: in.DryRun,
		}
		if name == "move" {
			destructiveIn.ScreenValidationExemptFields = map[string]bool{
				"project":   true,
				"issuetype": true,
			}
		}
		if client != nil && name != "delete" && !cmdutil.ReadOnlyEnabled(cmd) {
			destructiveIn.SchemaFetcher = newEditScreenSchemaFetcher(
				ctx, cmdutil.ServicesForClient(client).Project(0),
				cmdutil.ProfileForEnvelope(cmd), key,
			)
		}
		pipeOut := pipeline.RunMutation(destructiveIn)
		if pipeOut.Aborted {
			return envelope.IssueDestructiveOutput{}, pipeOut.Err
		}
		submitFields := pipeOut.SubmitFields
		if submitFields == nil {
			submitFields = map[string]any{}
		}
		data := envelope.IssueDestructiveOutput{
			Issue:   cmdutil.IssueRef{Key: key},
			Payload: envelope.IssueFieldsPayload{Fields: submitFields},
			DryRun:  in.DryRun,
		}
		if in.DryRun {
			if len(pipeOut.Warnings) > 0 {
				data.Warnings = pipeOut.Warnings
			}
			return data, nil
		}
		var issue *jira.Issue
		switch name {
		case "delete":
			_, err = service.Delete(ctx, key, &jira.IssueDeleteOptions{DeleteSubtasks: in.DeleteSubtasks})
		case "clone":
			issue, _, err = service.Clone(ctx, key, &jira.IssueCloneRequest{Fields: submitFields})
		case "move":
			issue, _, err = service.Move(ctx, key, &jira.IssueMoveRequest{Fields: submitFields})
		default:
			return envelope.IssueDestructiveOutput{}, fmt.Errorf("unknown issue mutation %q", name)
		}
		if err != nil {
			return envelope.IssueDestructiveOutput{}, err
		}
		data.Result = issue
		data.DryRun = false
		if len(pipeOut.Warnings) > 0 {
			data.Warnings = pipeOut.Warnings
		}
		return data, nil
	})
	if err != nil {
		return err
	}
	return cmdutil.WriteKeyedResultsEnvelope(cmd, "issue."+name, results, func(_ string, data envelope.IssueDestructiveOutput) any { return data })
}

// confirmDestructive prompts the user via huh for an interactive
// yes/no confirmation before executing a destructive op
// (delete/clone/move). Only invoked in TTY mode; non-TTY callers
// MUST pass --force and are rejected by the caller before reaching
// this function under stdin discipline.
//
// The prompt runs under the command context so a SIGINT or an elapsed
// --timeout cancels the prompt instead of leaving it blocked on the
// terminal. A canceled prompt returns a typed *cli.PromptError so the
// envelope keeps the cancellation identity; a declined confirmation
// returns (false, nil) and the caller turns it into an abort.
func confirmDestructive(cmd *cobra.Command, action, key string) (bool, error) {
	confirmed := false
	confirm := huh.NewConfirm().
		Title(fmt.Sprintf("About to %s %s", action, key)).
		Description("This is destructive. Continue?").
		Affirmative("Yes, " + action).
		Negative("Cancel").
		Value(&confirmed)
	form := huh.NewForm(huh.NewGroup(confirm))
	if err := form.RunWithContext(cmd.Context()); err != nil {
		switch {
		case errors.Is(err, huh.ErrUserAborted):
			// Esc / Ctrl-C inside the form is a decline, not a fault.
			return false, nil
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return false, cli.NewPromptError(cli.PromptCanceled, action+" confirmation", err)
		default:
			return false, cli.NewPromptError(cli.PromptUnavailable, action+" confirmation", err)
		}
	}
	return confirmed, nil
}

// issueWebLinkCommand wires `jira issue weblink KEY --url URL --title T`.
// Goes through POST /rest/api/3/issue/{key}/remotelink — Jira's
// "Web links" feature, separate from issue-to-issue links.
func issueWebLinkCommand() *cobra.Command {
	var url, title string
	var dryRun bool
	var parallelism int
	cmd := &cobra.Command{
		Use:         "weblink KEY...",
		Annotations: issueKeyArg,
		Short:       "Attach a web link to an issue",
		Long: "Attach a Jira remote web link to one or more issues. Use it to point an " +
			"issue at a pull request, design document, incident, or other external URL.\n\n" +
			"`--dry-run` validates local URL syntax and previews the request without " +
			"contacting Jira or checking whether the target URL is reachable. Multiple " +
			"issue keys return per-key results.",
		Example: `$ jira issue weblink PROJ-123 --url https://github.com/acme/app/pull/42 --title "Fix build"

# Preview adding a link without contacting Jira
$ jira issue weblink PROJ-123 --url https://example.com/design --title "Design note" --dry-run

# Attach the same link to several issues and keep results parseable
$ jira issue weblink PROJ-123 PROJ-124 --url https://example.com/runbook --title "Runbook" --dry-run --output=json`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			keys, err := issuekey.ParseExpressions(args, issuekey.Options{MaxExpansion: issuekey.DefaultMaxExpansion})
			if err != nil {
				return err
			}
			if url == "" {
				err := cli.NewCLIInputError(cli.InputRequiredFlagMissing, "--url is required")
				err.Flag = "url"
				return err
			}
			// Local URL syntax validation runs in BOTH dry-run and live
			// mode. dry-run is a local preview: it checks the URL parses
			// and carries an absolute http/https scheme, but it cannot
			// and does not verify the target is reachable.
			if err := validateWebLinkURL(url); err != nil {
				return err
			}
			if len(keys) > 1 {
				return runIssueWebLinkMany(cmd, keys, parallelism, issueWebLinkInput{URL: url, Title: title, DryRun: dryRun})
			}
			if dryRun {
				// Be explicit that dry-run did NOT contact the target URL —
				// only its syntax was checked locally (url_remote_checked=false).
				return cmdutil.WriteEnvelope(cmd, "issue.weblink", issueWebLinkData(keys[0], issueWebLinkInput{URL: url, Title: title}, true))
			}
			client, _, ok, err := cmdutil.JiraClientForCommand(cmd)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("jira base URL is required for issue.weblink")
			}
			var resp *jira.Response
			if err := cmdutil.Spin(cmd, "issue.weblink", func(ctx context.Context) error {
				var e error
				resp, e = cmdutil.ServicesForClient(client).Issue().AddRemoteLink(ctx, keys[0], &jira.RemoteLinkRequest{
					URL: url, Title: title,
				})
				return e
			}); err != nil {
				return err
			}
			return cmdutil.WriteEnvelopeWithResponse(cmd, "issue.weblink", issueWebLinkData(keys[0], issueWebLinkInput{URL: url, Title: title}, false), resp)
		},
	}
	cmdutil.AddStringVar(cmd.Flags(), &url, "url", "", "Web link target URL (required)", clib.FlagExtra{Group: "Link", Placeholder: "URL", Hint: "url"})
	cmdutil.AddStringVar(cmd.Flags(), &title, "title", "", "Display title for the link", clib.FlagExtra{Group: "Link", Placeholder: "TEXT"})
	cmdutil.AddDryRunFlag(cmd.Flags(), &dryRun, "Preview without creating the link")
	cmdutil.AddParallelismFlag(cmd, &parallelism)
	return cmd
}

type issueWebLinkInput struct {
	URL    string
	Title  string
	DryRun bool
}

func runIssueWebLinkMany(cmd *cobra.Command, keys []string, parallelism int, in issueWebLinkInput) error {
	if in.DryRun {
		results := xslices.Map(keys, func(key string) cmdutil.KeyResult[envelope.IssueWebLinkOutput] {
			return cmdutil.KeyResult[envelope.IssueWebLinkOutput]{Key: key, Value: issueWebLinkData(key, in, true)}
		})
		return cmdutil.WriteKeyedResultsEnvelope(cmd, "issue.weblink", results, func(_ string, data envelope.IssueWebLinkOutput) any { return data })
	}
	client, _, ok, err := cmdutil.JiraClientForCommand(cmd)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("jira base URL is required for issue.weblink")
	}
	service := cmdutil.ServicesForClient(client).Issue()
	results, err := cmdutil.FanOutKeysProgress(cmd.Context(), "issue.weblink", keys, parallelism, func(ctx context.Context, key string) (envelope.IssueWebLinkOutput, error) {
		if _, err := service.AddRemoteLink(ctx, key, &jira.RemoteLinkRequest{URL: in.URL, Title: in.Title}); err != nil {
			return envelope.IssueWebLinkOutput{}, err
		}
		return issueWebLinkData(key, in, false), nil
	})
	if err != nil {
		return err
	}
	return cmdutil.WriteKeyedResultsEnvelope(cmd, "issue.weblink", results, func(_ string, data envelope.IssueWebLinkOutput) any { return data })
}

func issueWebLinkData(key string, in issueWebLinkInput, dryRun bool) envelope.IssueWebLinkOutput {
	return envelope.IssueWebLinkOutput{
		Issue:            cmdutil.IssueRef{Key: key},
		URL:              in.URL,
		Title:            in.Title,
		URLRemoteChecked: false,
		DryRun:           dryRun,
	}
}

// validateWebLinkURL performs the local, offline syntax check the
// weblink dry-run promises: the value must parse and carry an absolute
// http/https URL. It deliberately does NOT fetch the target — dry-run
// is local preview only, and reachability is not its job.
func validateWebLinkURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		inputErr := cli.NewCLIInputError(cli.InputFlagValueInvalid, fmt.Sprintf("--url %q is not a valid URL: %v", raw, err))
		inputErr.Flag = "url"
		return inputErr
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		err := cli.NewCLIInputError(cli.InputFlagValueInvalid, fmt.Sprintf("--url %q must be an absolute http or https URL", raw))
		err.Flag = "url"
		return err
	}
	if u.Host == "" {
		err := cli.NewCLIInputError(cli.InputFlagValueInvalid, fmt.Sprintf("--url %q is missing a host", raw))
		err.Flag = "url"
		return err
	}
	return nil
}

func issueFieldsFromPayload(payload map[string]any) map[string]any {
	if payload == nil {
		return map[string]any{}
	}
	if rawFields, ok := payload["fields"]; ok {
		if fields, ok := rawFields.(map[string]any); ok {
			return cmdutil.CopyAnyMap(fields)
		}
	}
	out := make(map[string]any, len(payload))
	for key, value := range payload {
		if key == "dry_run" {
			continue
		}
		out[key] = value
	}
	return out
}

func extractNamedADFDocs(payload map[string]any) (map[string]adf.Document, error) {
	var named map[string]adf.Document
	for k, v := range payload {
		if !looksLikeADFDoc(v) {
			continue
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("marshal %s for ADF validation: %w", k, err)
		}
		doc, _, err := adf.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		if named == nil {
			named = make(map[string]adf.Document)
		}
		named[k] = doc
	}
	return named, nil
}

// looksLikeADFDoc reports whether v is an object whose top-level shape is an
// ADF root document — type field equals "doc" and a version field is present.
// Cheap shape gate before the full marshal+parse path; avoids parsing every
// nested object (e.g. assignee={accountId:...}, project={key:...}) as ADF.
func looksLikeADFDoc(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	t, _ := m["type"].(string)
	if t != "doc" {
		return false
	}
	_, hasVersion := m["version"]
	return hasVersion
}
