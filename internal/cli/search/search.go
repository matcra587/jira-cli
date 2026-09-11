package search

import (
	"context"
	"fmt"
	"maps"
	"slices"

	clib "github.com/gechr/clib/cli/cobra"
	"github.com/matcra587/jira-cli/internal/browser"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/cmdutil"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/jql"
	"github.com/spf13/cobra"
)

// sortedQueryNames returns the saved-query names in stable alphabetical order,
// for the "did you mean" suggestions on an unknown `search saved NAME`.
func sortedQueryNames(queries map[string]config.Query) []string {
	return slices.Sorted(maps.Keys(queries))
}

// NewCommand returns the `search` command group for running Jira searches.
func NewCommand() *cobra.Command {
	cmd := cmdutil.GroupCommand("search", "Run Jira searches", "resources")
	cmd.Long = "Run Jira searches from a query or a saved file. `jira search jql` runs a JQL " +
		"string and returns matching issues; `jira search saved` runs a `.jql` file from your " +
		"`queries_path` by name.\n\n" +
		"Results page transparently and honor `--output`; use `jira jql build` first if you " +
		"want help composing the query."
	cmd.Example = `$ jira search jql "project = ENG AND statusCategory != Done"

# Run a saved query by name
$ jira search saved my-open-bugs`
	cmd.AddCommand(searchJQLCommand())
	cmd.AddCommand(searchSavedCommand())
	return cmd
}

type searchOptions struct {
	fields    []string
	full      bool
	web       bool
	count     bool
	all       bool
	limit     int
	unbounded bool
	cursor    string
}

func searchJQLCommand() *cobra.Command {
	var opts searchOptions
	cmd := &cobra.Command{
		Use:   "jql QUERY",
		Short: "Run a JQL query",
		Long: "Run an inline JQL query against Jira and print matching issues. Use it when " +
			"you already have a query string from `jira jql build`, a saved filter, or a " +
			"Jira URL.\n\n" +
			"`--web` builds and opens the Jira search URL without running the query. " +
			"`--count` asks Jira for an approximate match count without fetching issues. " +
			"`--all` drains pages with default caps unless `--unbounded` is set.",
		Example: `$ jira search jql "status = Done AND assignee = currentUser()"

# Select only the fields you need
$ jira search jql "project = PROJ" --fields summary,status

# Ask Jira for an approximate count without fetching issues
$ jira search jql "project = PROJ" --count

# Restrict fields and keep the result parseable
$ jira search jql "project = PROJ AND status != Done" --fields key,summary,status --output=json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.web {
				return openSearchWeb(cmd, args[0])
			}
			if opts.count {
				return runSearchCount(cmd, args[0])
			}
			fields, detail, err := searchOutputFields(opts)
			if err != nil {
				return err
			}
			client, _, ok, err := cmdutil.JiraClientForCommand(cmd)
			if err != nil {
				return err
			}
			if ok {
				svc := cmdutil.ServicesForClient(client).Search()
				limit := opts.limit
				if limit <= 0 {
					limit = 50
				}
				req := &jira.SearchRequest{JQL: args[0], Fields: fields, ListOptions: jira.ListOptions{MaxResults: limit, NextPageToken: opts.cursor}} // pagination-exempt: opaque --cursor pass-through
				if opts.all {
					var (
						issues []*jira.Issue
						info   jira.DrainInfo
					)
					err = cmdutil.Spin(cmd, "search.jql", func(ctx context.Context) error {
						var spinErr error
						issues, info, spinErr = jira.DrainSearch(ctx, svc, req, jira.DrainOptions{Unbounded: opts.unbounded})
						return spinErr
					})
					if err != nil {
						return err
					}
					cmdutil.RecordIssuesSeen(cmd, issues)
					data := envelope.SearchJQLOutput{Source: "inline", JQL: args[0], Issues: searchIssueOutput(issues, fields, detail)}
					// The drain knows its terminal state: the result set is
					// complete unless a bound truncated it. /search/jql has no
					// reliable total, so report the count we actually hold —
					// and the resume cursor when a bound cut the walk on a
					// page boundary.
					pagination := &cli.Pagination{
						MaxResults: len(issues),
						Total:      new(len(issues)),
						IsLast:     !info.Truncated,
						NextCursor: info.NextPageToken, // pagination-exempt: opaque resume token from the drain
					}
					return cmdutil.WriteEnvelopeWithPaginationAndRawWarnings(cmd, "search.jql", data, pagination, cmdutil.DrainTruncationWarnings(info))
				}
				var (
					found2 []*jira.Issue
					resp   *jira.Response
				)
				err = cmdutil.Spin(cmd, "search.jql", func(ctx context.Context) error {
					var spinErr error
					found2, resp, spinErr = svc.JQL(ctx, req)
					return spinErr
				})
				if err != nil {
					return err
				}
				cmdutil.RecordIssuesSeen(cmd, found2)
				return cmdutil.WriteEnvelopeWithResponse(cmd, "search.jql", envelope.SearchJQLOutput{Source: "inline", JQL: args[0], Issues: searchIssueOutput(found2, fields, detail)}, resp)
			}
			return cmdutil.WriteEnvelope(cmd, "search.jql", envelope.SearchJQLOutput{
				Source: "inline",
				JQL:    args[0],
				Issues: []any{},
			})
		},
	}
	addSearchOutputFlags(cmd, &opts)
	addSearchCountFlag(cmd, &opts)
	addSearchPaginationFlags(cmd, &opts)
	return cmd
}

func searchSavedCommand() *cobra.Command {
	var opts searchOptions
	cmd := &cobra.Command{
		Use:         "saved NAME",
		Annotations: map[string]string{"clib": "dynamic-args='savedquery'"},
		Short:       "Run a saved JQL query",
		Long: "Load a named query from the configured queries file and run it against Jira. " +
			"Use it for team or personal searches that are too long to keep in shell " +
			"history.\n\n" +
			"The saved command uses the same output field selectors as inline search, but " +
			"does not implement `--count` or full pagination controls.",
		Example: `$ jira search saved my-open-bugs

# Run a saved query and select only the fields you need
$ jira search saved my-open-bugs --fields summary,status

# Keep saved-query results parseable
$ jira search saved my-open-bugs --output=json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fields, detail, err := searchOutputFields(opts)
			if err != nil {
				return err
			}
			cfg, err := config.Load(config.WithPath(cmdutil.ConfigPath(cmd)))
			if err != nil {
				return err
			}
			queries, err := config.LoadQueries(cfg.QueriesPath)
			if err != nil {
				return err
			}
			query, ok := queries[args[0]]
			if !ok {
				// A bad query name is bad command-line input (validation,
				// exit 3) — the lookup is a local file, not a Jira resource,
				// and the valid names live in queries_path, not --help, so it
				// gets its own code with the names offered as suggestions.
				e := cli.NewCLIInputError(cli.InputSavedQueryUnknown, fmt.Sprintf("saved query %q not found", args[0]))
				e.Suggestions = sortedQueryNames(queries)
				return e
			}
			client, _, hasClient, err := cmdutil.JiraClientForCommand(cmd)
			if err != nil {
				return err
			}
			issues := any([]any{})
			var resp *jira.Response
			if hasClient {
				var found []*jira.Issue
				err = cmdutil.Spin(cmd, "search.jql", func(ctx context.Context) error {
					var spinErr error
					found, resp, spinErr = cmdutil.ServicesForClient(client).Search().JQL(ctx, &jira.SearchRequest{
						JQL:         query.JQL,
						Fields:      fields,
						ListOptions: jira.ListOptions{MaxResults: 50},
					})
					return spinErr
				})
				if err != nil {
					return err
				}
				cmdutil.RecordIssuesSeen(cmd, found)
				issues = searchIssueOutput(found, fields, detail)
			}
			data := envelope.SearchSavedOutput{
				Source:      "saved",
				Key:         args[0],
				Name:        query.Name,
				Description: query.Description,
				Project:     query.Project,
				JQL:         query.JQL,
				Issues:      issues,
			}
			return cmdutil.WriteEnvelopeWithResponse(cmd, "search.saved", data, resp)
		},
	}
	addSearchOutputFlags(cmd, &opts)
	return cmd
}

func addSearchOutputFlags(cmd *cobra.Command, opts *searchOptions) {
	fs := cmd.Flags()
	cmdutil.AddStringSliceVar(fs, &opts.fields, "fields", nil, "Narrow each issue to these fields, comma-separated [example: summary,status,assignee]", clib.FlagExtra{Group: "Output", Placeholder: "FIELD", Complete: "predictor=cachefield,comma"})
	cmdutil.AddBoolVar(fs, &opts.full, "full", false, "Request Jira's full issue payload (`*all` fields)", clib.FlagExtra{Group: "Output"})
	cmdutil.AddBoolVar(fs, &opts.web, "web", false, "Open the query in a browser instead of printing results", clib.FlagExtra{Group: "Output"})
	cmd.MarkFlagsMutuallyExclusive("fields", "full")
}

// addSearchPaginationFlags attaches --all/--limit/--unbounded. Like --count,
// they live only on `search jql`, not the shared output flags, so `search
// saved` doesn't publish flags its runner ignores.
func addSearchPaginationFlags(cmd *cobra.Command, opts *searchOptions) {
	fs := cmd.Flags()
	cmdutil.AddBoolVar(fs, &opts.all, "all", false, "Walk every page until `isLast` (bounded; use `--unbounded` to lift the caps)", clib.FlagExtra{Group: "Pagination"})
	cmdutil.AddIntVar(fs, &opts.limit, "limit", 50, "Page size requested from Jira; `0` uses the default", clib.FlagExtra{Group: "Pagination", Placeholder: "N"})
	cmdutil.AddBoolVar(fs, &opts.unbounded, "unbounded", false, "With `--all`, lift the default 100-page / 10 000-issue caps", clib.FlagExtra{Group: "Pagination"})
	cmdutil.AddStringVar(fs, &opts.cursor, "cursor", "", "Resume from a `nextCursor` returned by a previous page", clib.FlagExtra{Group: "Pagination", Placeholder: "TOKEN"})
	// --count fetches nothing and --web opens a browser, so the page controls
	// are meaningless alongside either. --cursor composes with --limit (page
	// size of the resumed page) and --all (resume the drain from the cursor).
	cmd.MarkFlagsMutuallyExclusive("count", "all")
	cmd.MarkFlagsMutuallyExclusive("count", "limit")
	cmd.MarkFlagsMutuallyExclusive("count", "cursor")
	cmd.MarkFlagsMutuallyExclusive("web", "all")
	cmd.MarkFlagsMutuallyExclusive("web", "limit")
	cmd.MarkFlagsMutuallyExclusive("web", "cursor")
}

// Drain truncation warnings are shared via cmdutil.DrainTruncationWarnings —
// `issue list --all` emits the identical contract.

// addSearchCountFlag attaches --count. It lives only on `search jql`, not on the
// shared output flags, because `search saved` does not implement count — adding
// it there would publish a flag the saved runner silently ignores. Must be
// called after addSearchOutputFlags so the flags it conflicts with exist.
func addSearchCountFlag(cmd *cobra.Command, opts *searchOptions) {
	cmdutil.AddBoolVar(cmd.Flags(), &opts.count, "count", false, "Return only the approximate match count, without fetching issues", clib.FlagExtra{Group: "Output"})
	// --count fetches no issues, so the field/full selectors and the browser
	// opener are all meaningless alongside it.
	cmd.MarkFlagsMutuallyExclusive("count", "fields")
	cmd.MarkFlagsMutuallyExclusive("count", "full")
	cmd.MarkFlagsMutuallyExclusive("count", "web")
}

// runSearchCount fetches Jira's approximate match count for jqlStr and emits it
// without retrieving any issues. Unlike `--web` and `--as-jql`-style previews,
// the count comes from Jira, so a configured profile is required.
func runSearchCount(cmd *cobra.Command, jqlStr string) error {
	client, _, ok, err := cmdutil.JiraClientForCommand(cmd)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("validation: --count queries Jira for the estimate and needs a configured profile")
	}
	var (
		count int
		resp  *jira.Response
	)
	err = cmdutil.Spin(cmd, "search.count", func(ctx context.Context) error {
		var spinErr error
		count, resp, spinErr = cmdutil.ServicesForClient(client).Search().ApproximateCount(ctx, jqlStr)
		return spinErr
	})
	if err != nil {
		return err
	}
	return cmdutil.WriteEnvelopeWithResponse(cmd, "search.count", envelope.SearchCountOutput{
		Source: "inline",
		JQL:    jqlStr,
		Count:  count,
	}, resp)
}

// openSearchWeb builds the JQL search URL from the active profile and opens it
// in a browser when interactive, reporting the URL in the envelope either way.
// It needs no Jira call — only the configured base URL.
func openSearchWeb(cmd *cobra.Command, jqlQuery string) error {
	profile, err := cmdutil.ProfileForCommand(cmd)
	if err != nil {
		return err
	}
	u := browser.SearchURL(profile.BaseURL, jqlQuery)
	if u == "" {
		return fmt.Errorf("validation: opening a query in the browser requires a configured base URL")
	}
	return cmdutil.WriteWebEnvelope(cmd, "search.jql", u, envelope.WebOpenSearchOutput{Source: "inline", Jql: jqlQuery})
}

func searchOutputFields(opts searchOptions) ([]string, bool, error) {
	fields := jql.CompactStrings(opts.fields)
	if opts.full && len(fields) > 0 {
		return nil, false, fmt.Errorf("validation: --fields and --full are mutually exclusive")
	}
	if opts.full {
		return []string{"*all"}, true, nil
	}
	if len(fields) > 0 {
		return fields, false, nil
	}
	return jira.DefaultIssueListFields(), false, nil
}

// searchIssueOutput renders issues for the envelope. --full returns the raw
// wire records; every other path — default and --fields alike — projects the
// compact summary shape narrowed to the requested fields, so a field selector
// can never silently change the output contract.
func searchIssueOutput(issues []*jira.Issue, fields []string, detail bool) any {
	if detail {
		return issues
	}
	return cmdutil.IssueOutputFields(issues, fields)
}
