package user

import (
	"context"
	"fmt"

	"github.com/gechr/primer/table"
	"github.com/gechr/x/shell"
	xslices "github.com/gechr/x/slices"
	xstrings "github.com/gechr/x/strings"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

const (
	flagStart = "start"
	flagLimit = "limit"
	flagAll   = "all"
	pageSize  = 50
	// The pinned findUsers operation filters before paging and exposes only
	// its first 1,000 matches. Other user-search endpoints differ.
	searchLimit = 1000
)

// Deps keeps help, schema and completion independent of authentication.
type Deps struct {
	JiraClient func(ctx context.Context) (core.JiraClient, error)
}

// New builds the account discovery group. Assignment remains a separate action.
func New(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use: "user", Short: "Discover Jira account identities", Args: cobra.NoArgs,
		Long:    "Find account IDs for assignment, watchers and ADF mentions. Search returns every visible candidate; choose the intended account explicitly.",
		Example: "$ jira user search sam --output=json",
	}
	cmd.AddCommand(newSearch(deps))
	return cmd
}

func newSearch(deps Deps) *cobra.Command {
	var start, limit int
	var all bool
	var cmd *cobra.Command
	return command.MustNew(command.Spec[envelope.UserSearchOutput]{
		Op: "user.search", Identity: true,
		Use: "search QUERY", Short: "Find active users by name or email prefix",
		Long: "Find visible active accounts by display-name or email prefix. " +
			"Every candidate is returned; even an email query does not automatically choose an account. Duplicate native row members or noncanonical identity/activity/deletion keys refuse the page before publication.\n\n" +
			"`--start` and `--limit` select a server page. `--all` walks pages from zero and conflicts with either explicit paging flag. " +
			"Jira exposes only the first 1,000 matching accounts. Reaching that ceiling reports `truncated: true`; narrow the query.\n\n" +
			"The API provides no total or final-page flag. A short page does not prove completion: `pagination.next_start` permits a further probe. " +
			"`pagination.complete` means an empty page was observed within this search window. Later failures retain earlier validated pages.\n\n" +
			"Inactive, deleted and unknown-active rows are excluded and counted in `skipped`; paging advances by fetched rows. " +
			"Privacy can omit or null email. Missing Browse users and groups permission can produce an empty successful search; " +
			"no matches does not prove an account is absent.",
		Example: `$ jira user search sam

# Inspect candidate account IDs
$ jira user search sam@example.com --output=json

# Read another server page
$ jira user search sam --start 50 --limit 10

# Walk the endpoint's visible search window
$ jira user search sam --all --output=json`,
		Args: command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "QUERY"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			command.AddIntVar(c, &start, flagStart, 0, "Offset in filtered matches (0–999)", command.FlagOpts{Terse: "Page offset", Placeholder: "n"})
			command.AddIntVar(c, &limit, flagLimit, pageSize, "Maximum fetched rows per page (1–1000)", command.FlagOpts{Terse: "Page size", Placeholder: "n"})
			command.AddBoolVar(c, &all, flagAll, false, "Walk pages until empty or the 1,000-match endpoint ceiling", command.FlagOpts{Terse: "Search window"})
		},
		Validate: func() error {
			if all && (cmd.Flags().Changed(flagStart) || cmd.Flags().Changed(flagLimit)) {
				err := cli.NewInputError(cli.CodeFlagConflict, "--all conflicts with explicit --start or --limit")
				err.Field = flagAll
				return err
			}
			if start < 0 || start >= searchLimit {
				err := cli.NewInputError(cli.CodeFlagValueInvalid, "--start must be between 0 and 999")
				err.Field = flagStart
				return err
			}
			if limit < 1 || limit > searchLimit {
				err := cli.NewInputError(cli.CodeFlagValueInvalid, "--limit must be between 1 and 1000")
				err.Field = flagLimit
				return err
			}
			return nil
		},
		Human:         renderSearch,
		RetainOnError: func(out envelope.UserSearchOutput) bool { return out.Pagination.Fetched > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.UserSearchOutput, error) {
			return runSearch(ctx, deps, inv.Args[0], start, limit, all)
		},
	})
}

func runSearch(ctx context.Context, deps Deps, query string, start, limit int, all bool) (envelope.UserSearchOutput, error) {
	out := envelope.UserSearchOutput{
		Query: query, Users: []envelope.UserSearchMatch{},
		Pagination: envelope.UserSearchPagination{StartAt: start, MaxResults: min(limit, searchLimit-start), SearchLimit: searchLimit},
	}
	if xstrings.IsBlank(query) {
		err := cli.NewInputError(cli.CodeArgValueInvalid, "query must not be blank")
		err.Field = "QUERY"
		return out, err
	}
	client, err := deps.JiraClient(ctx)
	if err != nil {
		return out, err
	}
	seen := make(map[string]struct{})
	_, err = command.Spin(ctx, "Searching users", func(ctx context.Context) (struct{}, error) {
		for {
			out.Pagination.NextStart = new(start)
			page, err := client.SearchUsers(ctx, query, start, min(limit, searchLimit-start))
			if err != nil {
				return struct{}{}, err
			}
			matches, err := projectPage(page.Users, min(limit, searchLimit-start), seen)
			if err != nil {
				return struct{}{}, err
			}
			out.Users = append(out.Users, matches...)
			out.Count = len(out.Users)
			out.Skipped += len(page.Users) - len(matches)
			out.Pagination.Fetched += len(page.Users)
			if len(page.Users) == 0 {
				out.Pagination.Complete = true
				out.Pagination.NextStart = nil
				return struct{}{}, nil
			}
			start += len(page.Users)
			if start == searchLimit {
				out.Truncated = true
				out.TruncatedReason = "server_limit"
				out.Pagination.NextStart = nil
				return struct{}{}, nil
			}
			out.Pagination.NextStart = new(start)
			if !all {
				return struct{}{}, nil
			}
		}
	})
	return out, err
}

// projectPage validates the whole page before accepting any of it. Checking
// duplicate identities includes skipped rows: a repeated page must not masquerade
// as progress just because every row was inactive. No total is inferred.
func projectPage(users []*jira.User, maxResults int, seen map[string]struct{}) ([]envelope.UserSearchMatch, error) {
	active, err := core.ActiveUserSearchPage(users, maxResults, seen)
	if err != nil {
		return nil, err
	}
	return xslices.Map(active, func(user *jira.User) envelope.UserSearchMatch {
		return envelope.UserSearchMatch{AccountID: user.AccountID.Value, DisplayName: user.DisplayName.Value, EmailAddress: user.EmailAddress}
	}), nil
}

func renderSearch(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.UserSearchOutput)
	if !ok {
		return fmt.Errorf("user: expected UserSearchOutput, got %T", env.Data)
	}
	v.Line(cli.Text(fmt.Sprintf("%d visible active matches for %q", out.Count, out.Query)))
	if len(out.Users) > 0 {
		output.Table(v, cli.ResolvedTheme(), []table.Column[envelope.UserSearchMatch]{
			{Name: "account_id", Header: "ACCOUNT ID", Render: func(u envelope.UserSearchMatch, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.Text(u.AccountID))
			}},
			{Name: "display_name", Header: "NAME", Flex: true, Render: func(u envelope.UserSearchMatch, _ *table.RenderContext) table.Cell {
				return table.StyledCell(cli.OrDash(cli.Entity(u.DisplayName)), cli.Text(u.DisplayName))
			}},
			{Name: "email_address", Header: "EMAIL", Render: func(u envelope.UserSearchMatch, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.OrDash(cli.Text(u.EmailAddress.Value)))
			}},
		}, out.Users)
	}
	if out.Skipped > 0 {
		v.Line(cli.Text(fmt.Sprintf("%d inactive, deleted or unknown-active rows excluded.", out.Skipped)))
	}
	if out.Truncated {
		v.Hint("Jira's 1,000-match ceiling reached. Narrow the search query.")
	}
	if next := out.Pagination.NextStart; next != nil {
		v.Line(cli.Text(fmt.Sprintf("Next probe: jira user search %s --start %d", shell.Quote(out.Query), *next)))
	}
	v.Hint("Visibility depends on permissions; email depends on privacy settings. Empty results do not prove an account is absent.")
	return nil
}
