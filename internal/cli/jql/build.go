package jql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/boardscope"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// BuildPreferences carries public identity and configured board context.
// Loading it must not open the credential store.
type BuildPreferences = core.IssuePreferences

type buildOptions struct {
	filters  command.JQLInput
	board    string
	boardID  int
	boardSet bool
	desc     bool
}

func newBuild(deps Deps) *cobra.Command {
	var opts buildOptions
	var cmd *cobra.Command
	return command.MustNew(command.Spec[envelope.JQLBuildOutput]{
		Op: "jql.build", Use: "build", Short: "Compose a JQL query locally from filters",
		Long: "Compose inspectable JQL without contacting Jira or opening the credential store. Filters narrow each other with AND; CSV and repeated values on a filter are alternatives. " +
			"Assignee and reporter accept me/currentUser() or none/empty/unassigned. Status accepts names, !NAME exclusions and category comparisons such as >=In Progress. Categories are ordered To Do, In Progress, Done.\n\n" +
			"Date filters accept explicitly signed durations (-7d or +2w), absolute dates, comparisons and inclusive A..B ranges with optional endpoints. " +
			"With no filter conditions, use updated >= -365d. Ordering defaults to updated descending; --desc=false selects ascending, --order-by=none disables ordering. Safe custom field identifiers are supported.\n\n" +
			"Omitted board selectors inherit default_board; --board= clears it and explicit selectors override it. default_project and default_issue_type do not affect this independent builder. " +
			"--board, --board-id or default_board requires an existing saved-filter resolution cached by issue list for the same configured site and account. " +
			"Read this snapshot regardless of age; board_scope.cached_at reports its timestamp. Missing or malformed cache is refused, never fetched. " +
			"Configure base_url and email in preferences or JIRA_BASE_URL/JIRA_EMAIL for offline board lookup; keyring-only identity is unavailable locally. " +
			"The saved filter is parenthesized before narrowing. The optional URL uses site preferences and never launches a browser. " +
			"Composition does not prove current board membership, Jira validity or matching issues; use jql validate to check the query.",
		Example: `$ jira jql build --project KEY --status 'In Progress'

# Current user's recently updated issues
$ jira jql build --assignee me --updated=-7d --order-by updated

# Explicit keys and inclusive date range
$ jira jql build --key KEY-1..KEY-3 --created='2026-01-01..2026-02-01' --desc=false

# Warm a board's saved-filter cache, then compose locally
$ jira issue list --board-id 42 --as-jql
$ jira jql build --board-id 42 --status Done --output=json`,
		Flags: func(c *cobra.Command) {
			cmd = c
			addBuildFlags(c, &opts)
		},
		Validate: func() error {
			opts.boardSet = cmd.Flags().Changed("board") || cmd.Flags().Changed("board-id")
			if err := opts.validate(cmd); err != nil {
				return err
			}
			_, err := prepareBuild(opts.filters)
			return err
		},
		Human: renderBuild,
		Run: func(ctx context.Context, _ command.Invocation) (envelope.JQLBuildOutput, error) {
			return runBuild(ctx, deps, opts)
		},
	})
}

func addBuildFlags(cmd *cobra.Command, opts *buildOptions) {
	for _, f := range []struct {
		name, usage string
		values      *[]string
	}{
		{"project", "Restrict to project keys (CSV or repeatable)", &opts.filters.Project},
		{"key", "Restrict to explicit issue keys or PROJECT-1..PROJECT-5 ranges", &opts.filters.Key},
		{"epic", "Restrict to direct children of these epic keys", &opts.filters.Epic},
		{"status", "Status names, !NAME exclusions or category comparisons such as >=In Progress", &opts.filters.Status},
		{"priority", "Restrict to priority names", &opts.filters.Priority},
		{"label", "Restrict to labels", &opts.filters.Label},
		{"type", "Restrict to issue type names", &opts.filters.IssueType},
		{"assignee", "Assignees; me is currentUser(), none means unassigned", &opts.filters.Assignee},
		{"reporter", "Reporters; me is currentUser(), none means empty", &opts.filters.Reporter},
	} {
		command.AddJQLFilterVar(cmd, f.values, f.name, f.usage, command.FlagOpts{Terse: f.name, Placeholder: "value"})
	}
	for _, f := range []struct {
		name  string
		value *string
	}{
		{"created", &opts.filters.Created}, {"updated", &opts.filters.Updated}, {"resolved", &opts.filters.Resolved},
	} {
		command.AddStringVar(cmd, f.value, f.name, "", "Filter "+f.name+" by signed duration, absolute date, comparison or inclusive A..B range", command.FlagOpts{Terse: f.name + " date", Placeholder: "date"})
	}
	command.AddStringVar(cmd, &opts.filters.OrderBy, "order-by", "updated", "Sort by a field identifier; none disables ordering", command.FlagOpts{Terse: "Sort field", Placeholder: "field", Enum: []string{"updated", "created", "priority", "status", "key", "summary", "assignee", "none"}, EnumDefault: "updated"})
	command.AddBoolVar(cmd, &opts.desc, "desc", true, "Sort descending; false selects ascending", command.FlagOpts{Terse: "Descending order"})
	command.AddStringVar(cmd, &opts.board, "board", "", "Scope to a board's cached saved filter by exact name; empty means no scope", command.FlagOpts{Terse: "Cached board", Placeholder: "name", Complete: "predictor=cacheboard"})
	command.AddIntVar(cmd, &opts.boardID, "board-id", 0, "Scope to a board's cached saved filter by positive numeric ID", command.FlagOpts{Terse: "Cached board ID", Placeholder: "id", Complete: "predictor=cacheboardid"})
	opts.filters.Descending = &opts.desc
	opts.filters.CustomOrder = true
}

func (o buildOptions) validate(cmd *cobra.Command) error {
	if cmd.Flags().Changed("board") && cmd.Flags().Changed("board-id") {
		return buildInputError(cli.CodeFlagConflict, "board", "--board and --board-id are alternatives; supply one selector")
	}
	if cmd.Flags().Changed("board-id") && o.boardID <= 0 {
		return buildInputError(cli.CodeFlagValueInvalid, "board-id", "--board-id must be a positive integer")
	}
	if o.board != "" {
		if strings.TrimSpace(o.board) == "" {
			return buildInputError(cli.CodeFlagValueInvalid, "board", "--board must be an exact nonempty board name")
		}
		if _, err := command.QuoteJQL(o.board); err != nil {
			return buildInputError(cli.CodeFlagValueInvalid, "board", err.Error())
		}
	}
	for _, name := range []string{"created", "updated", "resolved", "order-by"} {
		if cmd.Flags().Changed(name) {
			value, _ := cmd.Flags().GetString(name) //nolint:errcheck // known registered string flags
			if strings.TrimSpace(value) == "" {
				return buildInputError(cli.CodeFlagValueInvalid, name, "--"+name+" needs a nonempty value")
			}
		}
	}
	return nil
}

// prepareBuild adds only historical builder defaults. The shared grammar owns
// all authored filters and ordering; list callers retain their own defaults.
func prepareBuild(in command.JQLInput) (command.JQLInput, error) {
	query, err := command.ComposeJQL(in)
	if err != nil {
		return in, err
	}
	conditions, _ := command.SplitJQLOrderBy(query)
	if conditions == "" {
		in.Updated = "-365d"
	}
	return in, nil
}

func runBuild(ctx context.Context, deps Deps, opts buildOptions) (envelope.JQLBuildOutput, error) {
	in, err := prepareBuild(opts.filters)
	if err != nil {
		return envelope.JQLBuildOutput{}, err
	}
	if err := ctx.Err(); err != nil {
		return envelope.JQLBuildOutput{}, err
	}
	var prefs BuildPreferences
	var prefErr error
	if deps.Preferences != nil {
		prefs, prefErr = deps.Preferences()
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return envelope.JQLBuildOutput{}, contextErr
	}
	if prefErr != nil {
		return envelope.JQLBuildOutput{}, prefErr
	}
	fromDefault := !opts.boardSet && opts.board == "" && opts.boardID == 0 && strings.TrimSpace(prefs.DefaultBoard) != ""
	if fromDefault {
		opts.board = strings.TrimSpace(prefs.DefaultBoard)
	}
	out := envelope.JQLBuildOutput{Precedence: "none"}
	if opts.board != "" || opts.boardID > 0 {
		board, written, err := boardscope.ResolveCached(ctx, deps.CacheFor, prefs, opts.board, opts.boardID)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return out, err
		}
		if errors.Is(err, boardscope.ErrCachedContextInvalid) {
			field := "board"
			if opts.boardID > 0 {
				field = "board-id"
			}
			return out, buildInputError(cli.CodeConfigInvalid, field, "offline board lookup needs valid base_url and email preferences; it cannot use keyring-only identity")
		}
		if err != nil {
			return out, cachedBoardError(opts, err.Error())
		}
		in.BoardJQL = board.JQL
		out.Precedence = "flag"
		if fromDefault {
			out.Precedence = "default_board"
			in.BoardFromDefault = true
		}
		out.BoardScope = envelope.JQLBuildBoard{Applied: true, ID: board.ID, Name: board.Name, JQL: board.JQL, CachedAt: written}
	}
	if err := ctx.Err(); err != nil {
		return envelope.JQLBuildOutput{}, err
	}
	out.JQL, err = command.ComposeJQL(in)
	if err != nil {
		if out.BoardScope.Applied {
			return envelope.JQLBuildOutput{}, cachedBoardError(opts, "cached board filter has no usable conditions")
		}
		return envelope.JQLBuildOutput{}, err
	}
	if prefErr == nil {
		out.URL, _ = cli.SearchBrowseURL(prefs.BaseURL, out.JQL)
	}
	return out, nil
}

func cachedBoardError(opts buildOptions, reason string) error {
	field := "board"
	if opts.boardID > 0 {
		field = "board-id"
	}
	return cli.NewCodedError(cli.CodeBoardCacheUnavailable, "--"+field+": "+reason)
}

func buildInputError(code, field, message string) error {
	err := cli.NewInputError(code, message)
	err.Field = field
	return err
}

func renderBuild(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.JQLBuildOutput)
	if !ok {
		return fmt.Errorf("jql: expected JQLBuildOutput, got %T", env.Data)
	}
	v.Line(cli.Text(out.JQL))
	if out.URL != "" {
		v.Line(cli.Text(out.URL))
	}
	if out.BoardScope.Applied {
		label := "Cached board"
		if out.Precedence == "default_board" {
			label = "Configured cached board"
		}
		v.Line(cli.Text(fmt.Sprintf("%s: %s (%d), saved %s", label, out.BoardScope.Name, out.BoardScope.ID, out.BoardScope.CachedAt.UTC().Format(time.RFC3339))))
		v.Hint("Cached scope may differ from current board membership. Use jql validate for server validation.")
	}
	return nil
}
