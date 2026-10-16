package issue

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// defaultListLimit is `issue list`'s default TOTAL issue count — not one
// request's page size (see --limit's own flag help: this command
// auto-paginates on its own) — mirroring defaultCommentLimit's own
// reasoning (comment.go): big enough to show a useful page of matches,
// small enough that a huge search never comes back as one unbounded
// response.
const defaultListLimit = 50

// maxAllPages and maxAllIssues are --all's own safety cap, DOCUMENTED
// here (and restated in --all's own flag help) because a JQL match
// numbering in the hundreds of thousands is exactly the case an
// unbounded drain would exhaust memory against, one page at a time,
// with nothing stopping it short of the process itself running out.
// Hitting either cap stops the walk — never silently: the response
// says so honestly (envelope.IssueListOutput's own Truncated/
// TruncatedReason, surfaced to a human by nextPageHint below).
const (
	maxAllPages  = command.MaxSearchPages
	maxAllIssues = command.MaxSearchIssues
)

// The three flag names this command shares with columns.go, spelled once
// so registration there and the refusals here cannot drift. The rest of
// this command's flags are named by literal at their single use, as they
// were before these three arrived.
const (
	flagAsJQL     = "as-jql"
	flagColumns   = "columns"
	flagUnbounded = "unbounded"
)

// defaultListFields is what `issue list` asks Jira for when --fields is
// not given. Unlike `issue view`, whose nil --fields defers to GetIssue's
// own generous server default, the search endpoint's OWN default is "id"
// only (jira.SearchRequest's doc comment) — sending nil here would
// return a table with nothing but keys in it. This is the curated set
// the table's own columns need: summary, status, type, priority,
// assignee, and when the result last changed.
var defaultListFields = []string{"summary", "status", "issuetype", "priority", "assignee", "updated"}

// listAliases is this command's own hidden-alias table — the ONE list
// command.AddHiddenAliases (registration) and
// command.ResolveHiddenAliases (resolution, in Validate below) both read,
// so neither half can drift from the other.
//
// --cursor is 0.15's spelling for the same value (design §5), and it is a
// genuine synonym rather than a near-miss: both name the opaque token a
// prior response handed back, and both are passed through to the search
// endpoint verbatim. The canonical spelling stays --next-page-token
// because it matches the wire field a caller reads the value OUT of
// (data.pagination.next_page_token), so the flag and the JSON key a
// script pipes into it are one word.
//
// Not in the shared list registrar (command.AddListFlags): --cursor's
// canonical is this command's own --next-page-token, which that registrar
// does not own. A future cursor-paginated list command registers this
// same pair for itself.
var listAliases = []command.HiddenAlias{
	{Flag: "cursor", Canonical: "next-page-token"},
}

// newList returns `jira issue list`.
func newList(deps Deps) *cobra.Command {
	return newIssueList(deps, false)
}

// newMine shares the list runner while retaining a required account scope.
func newMine(deps Deps) *cobra.Command {
	return newIssueList(deps, true)
}

func newIssueList(deps Deps, currentAccount bool) *cobra.Command {
	var (
		jql                                  string
		saved                                string
		savedView                            *envelope.SavedQueryView
		projects, assignees, statuses, types []string
		labels, fields, expand               []string
		keys, epics                          []string
		created, updated, resolved           string
		limit                                int
		all, unbounded, asJQL                bool
		nextPageToken                        string
		composedJQL                          string
		presetOrder                          bool
		columns                              []string
		// boardView is the resolved board, carried from Run to the
		// envelope so a caller can see WHICH board answered and what query
		// it stood for — a board name is not enough to reproduce a result.
		boardView *envelope.IssueBoardView
		// board carries --board/--board-id and the cache flags that govern
		// their lookup. Resolved in Run rather than Validate: a board's
		// query is three reads away, and Validate runs before any I/O.
		board boardOptions
		// jqlInput is every value command.ComposeJQL needs except the board's own
		// query, remembered from Validate so Run can compose a second time
		// once the board has resolved — without restating fourteen fields
		// at a second call site that could drift from the first.
		jqlInput command.JQLInput
		// selected is --columns' own parsed, validated result, carried
		// from Validate to Human the way site and estimate below are.
		// Empty means "no --columns", which issueListColumns reads as the
		// whole shared set.
		selected []issueField
		// listFlags carries everything command.AddListFlags registers —
		// --order-by/--reverse/the date range/--reporter/--priority/
		// --watching/--count — the shared surface a future list command
		// reuses by calling that one function too.
		listFlags command.ListFlags
		// cmd is the built *cobra.Command, captured by Flags below so
		// Validate can ask it Flags().Changed("limit") — whether --limit
		// was explicitly typed, as opposed to just holding its default —
		// the same shape issue view's own newView captures cmd for
		// (view.go's own comment explains the timing: Flags runs once at
		// construction, before any parsing, so Changed can only be read
		// later, once cobra has parsed this invocation's actual flags).
		// The SAME timing is why command.ResolveListFlagAliases (Validate,
		// below) cannot run any earlier than this either.
		cmd *cobra.Command
	)

	// site carries the Jira base URL runList's own successful client
	// construction resolved, from Run to Human — the same shape issue
	// view's newView uses for its own site variable (view.go), and for
	// the same reason: the KEY column's hyperlink (issueListColumns, via
	// cli.IssueKey) needs the site, but Human has no deps and cannot
	// construct a client itself.
	var detail bool
	var site string

	// estimate carries runList's optional approximate-count fetch from
	// Run to Human — deliberately NOT part of envelope.IssueListOutput,
	// the same reason site above stays outside it: the count is fetched
	// ONLY for a human render (fetchCountEstimate's own gate below), and
	// a field always absent from machine output would be worse than no
	// field at all. Its zero value (known false) renders the count-free
	// hint, so a Human that somehow ran without Run stays benign — the
	// same shape commentsPreview's own zero value gives issue view
	// (view.go's newView).
	var estimate countEstimate

	spec := command.Spec[envelope.IssueListOutput]{
		Op:            "issue.list",
		Identity:      true,
		Passthrough:   true,
		RetainOnError: func(out envelope.IssueListOutput) bool { return len(out.Issues) > 0 || len(out.SearchPages) > 0 },
		Use:           "list",
		Short:         "Search issues by JQL or filters",
		Long: "Search issues with a JQL query, or with convenience filters that " +
			"compose one for you.\n\n" +
			"--detail requests all native fields plus renderedFields,names,schema,transitions,operations,changelog. --expand chooses an explicit expansion list; --expand= disables expansions. Native issue metadata is retained at .issues[].details and page metadata at .search_pages[].\n\n" +
			"`--project`, `--assignee`, `--status`, `--type` and `--label` are " +
			"the escape hatch's convenience shorthand: each is repeatable, and " +
			"repeated values on the SAME flag combine with OR (`--label bug " +
			"--label urgent` matches either label), while DIFFERENT flags " +
			"combine with AND (`--project ABC --status Open` matches both) — " +
			"Assignee and reporter values me/currentUser() mean the current account; none/empty/unassigned mean EMPTY. " +
			"Status values !NAME exclude a status; <, <=, > and >= compare the categories To Do, In Progress and Done. " +
			"Positive status predicates combine with OR; exclusions combine with AND. Use --jql for literal names beginning with these operators. " +
			"The usual facet-search shape means a second value on one facet " +
			"widens it and a second facet narrows the result.\n\n" +
			"`--jql` is the raw escape hatch everything above composes into " +
			"when it is absent. It is NOT composed with the convenience " +
			"filters: giving --jql alongside any of them is refused rather " +
			"than guessed at, because there is no way to silently combine an " +
			"explicit query the caller wrote and verified themselves with an " +
			"implied filter without changing what that query means, in a way " +
			"the caller cannot see before it runs. Fold the filter into --jql " +
			"yourself instead.\n\n" +
			"`--board` lists the issues on an Agile board, by name, and " +
			"`--board-id` does the same by number. A board IS a saved " +
			"filter, so this is a SCOPE the convenience filters narrow " +
			"further — `--board Sprint --status Done` means what it reads " +
			"like. It is refused alongside `--jql` for the reason those " +
			"filters are: a board is already a complete query and so is " +
			"yours, so one would have to be ignored.\n\n" +
			"Board names are matched loosely by Jira and are not unique, so " +
			"`--board` refuses rather than guesses when several match — and " +
			"the refusal lists each candidate with the `--board-id` that " +
			"names it exactly. Name lookup reads one bounded page; even an exact " +
			"name is refused on a non-final page because unread pages may contain " +
			"a duplicate. A board's own ordering is kept unless " +
			"`--order-by` asks for a different one. Resolving a board costs " +
			"three reads, so the answer is cached for a day; `--refresh` " +
			"ignores the cache and `--ttl-minutes` changes how long it " +
			"lives.\n\n" +
			"Every filter value is quoted and escaped before it ever reaches " +
			"Jira, so a project key or label containing a quote or backslash " +
			"cannot alter the query's structure.\n\n" +
			"`--limit` is how many issues you want, not one request's page " +
			"size: this command fetches successive pages on its own, behind " +
			"a single spinner, until it has collected that many or the search " +
			"runs out. This endpoint has no offset, only an internal cursor " +
			"this command walks on its own between pages.\n\n" +
			fmt.Sprintf("Empty pages are followed when their cursor advances. The walk "+
				"stops after %d consecutive empty pages unless --all --unbounded "+
				"is given. A missing or repeated cursor stops every walk. These "+
				"stops report truncated=true and a truncated_reason; they do not "+
				"mean the search is exhausted.\n\n", command.MaxSearchEmptyPages) +
			"Missing or null completion/issue-array fields, repeated issue keys and pages exceeding the requested count fail with search_response_invalid. " +
			"Later read failures retain earlier validated issues in the error frame; --jq never filters that failure receipt. " +
			"When both the page and issue safety bounds are reached, truncated_reason names max_issues.\n\n" +
			"`--all` drains the whole search instead, up to a fixed safety " +
			"cap (see --all's own help); it is refused alongside an explicit " +
			"--limit rather than silently picking a winner between them.\n\n" +
			"`--next-page-token` resumes a PRIOR invocation's own cursor " +
			"— its value rides machine output as " +
			"`data.pagination.next_page_token`, never a human view, which " +
			"names only the flag to raise. There is no total; `is_last` is " +
			"the authoritative end-of-results signal.\n\n" +
			"`--order-by` sorts the result by one of a fixed set of " +
			"fields (run with an unknown one to see the accepted set); " +
			"`--reverse` flips that field's own default direction. Both " +
			"are refused alongside --jql for the same reason the " +
			"convenience filters are: an explicit query owns its own " +
			"ordering.\n\n" +
			"`--created-after`/`--created-before`/`--updated-after`/" +
			"`--updated-before` bound the search by date — an absolute " +
			"date (`2026-08-01`) or Jira's own relative form " +
			"(`-7d`, `-2w`).\n\n" +
			"`--reporter` and `--priority` compose exactly like " +
			"`--assignee`/`--status` above; `--watching` matches only " +
			"issues the current user watches.\n\n" +
			"`--count` reports how many issues match, without fetching " +
			"any of them — refused alongside `--limit`/`--all`, which " +
			"have nothing to act on when nothing is being fetched.\n\n" +
			"`--unbounded` lifts `--all`'s safety cap, and needs `--all` " +
			"beside it. Use it when a capped drain reported `truncated` " +
			"and you genuinely want the rest. It also lifts the consecutive " +
			"empty-page cap, but cancellation and unusable cursors can still " +
			"stop it. A query matching hundreds of " +
			"thousands of issues will collect every one of them in memory.\n\n" +
			"`--as-jql` prints the composed query and stops, so it is how " +
			"to check what the convenience filters built before spending a " +
			"search on it, and the output is ready to paste straight back " +
			"into `--jql`. Refused alongside `--count`, `--web`, `--limit` " +
			"and `--all`, none of which has anything to act on when no " +
			"search runs.\n\n" +
			"It contacts Jira not at all EXCEPT with `--board`, whose query " +
			"only the site knows: resolving a board is three reads, and " +
			"printing the composed query without them would print a query " +
			"missing the very scope that was asked for. No search runs " +
			"either way — the reads resolve the board, they do not execute " +
			"the result.\n\n" +
			"`--columns` chooses and orders the columns of the HUMAN table, " +
			"and is distinct from `--fields`: `--fields` decides what Jira " +
			"sends, `--columns` decides what is drawn from it. The columns " +
			"appear in the order you name them. Asking for a column whose " +
			"field `--fields` did not request is refused rather than drawn " +
			"blank, since a column of dashes reads as an empty value and " +
			"not as an unfetched one. Machine output is unaffected — it " +
			"carries every field that was fetched, and `--jq` selects from " +
			"it.",
		Example: "# every open issue in one project\n" +
			"$ jira issue list --project ABC --status Open\n\n" +
			"# issues carrying either label\n" +
			"$ jira issue list --label bug --label urgent\n\n" +
			"# the raw escape hatch\n" +
			"$ jira issue list --jql 'assignee = currentUser() ORDER BY updated DESC'\n\n" +
			"# a bigger pull, auto-paginated behind one spinner\n" +
			"$ jira issue list --project ABC --limit 500\n\n" +
			"# every matching issue, up to the safety cap\n" +
			"$ jira issue list --project ABC --all\n\n" +
			"# resume a prior response's cursor (data.pagination.next_page_token)\n" +
			"$ jira issue list --project ABC --next-page-token abc123\n\n" +
			"# see the composed query without running it\n" +
			"$ jira issue list --project ABC --status Open --as-jql\n\n" +
			"# just the columns you want, in your own order\n" +
			"$ jira issue list --project ABC --columns key,summary,assignee\n\n" +
			"# every match, cap lifted\n" +
			"$ jira issue list --project ABC --all --unbounded\n\n" +
			"# the issues on a board, by name\n" +
			"$ jira issue list --board 'Platform Sprint'\n\n" +
			"# the same board, narrowed — the board is a scope, not a whole query\n" +
			"$ jira issue list --board 'Platform Sprint' --status Done\n\n" +
			"# by number, which is unambiguous and skips the name lookup\n" +
			"$ jira issue list --board-id 42",
		Flags: func(c *cobra.Command) {
			cmd = c
			jqlHelp := "Raw JQL query — the escape hatch every convenience filter composes into; refused alongside any of them"
			if currentAccount {
				jqlHelp = "Raw conditions narrow assignee = currentUser() and the other filters; raw ORDER BY wins"
			}
			command.AddStringVar(c, &jql, "jql", "",
				jqlHelp,
				command.FlagOpts{Terse: "JQL query", Placeholder: "query"})
			command.AddStringVar(c, &saved, flagSaved, "", "Run a local NAME.jql file from queries_path; metadata does not add filters",
				command.FlagOpts{Terse: "Saved query", Placeholder: "name", Complete: "predictor=savedquery"})
			command.AddJQLFilterVar(c, &projects, "project",
				"Match one of these project keys (repeatable; combines with OR)",
				command.FlagOpts{Terse: "Project", Placeholder: "key"})
			command.AddJQLFilterVar(c, &keys, "key",
				"Match issue keys or PROJECT-1..PROJECT-5 ranges (repeatable; combines with OR)",
				command.FlagOpts{Terse: "Issue keys", Placeholder: "key"})
			command.AddJQLFilterVar(c, &epics, "epic",
				"Match direct children of these epic keys (repeatable; combines with OR)",
				command.FlagOpts{Terse: "Epic keys", Placeholder: "key"})
			if !currentAccount {
				command.AddJQLFilterVar(c, &assignees, "assignee",
					"Match assignees; me is the current user, none means unassigned (repeatable; combines with OR)",
					command.FlagOpts{Terse: "Assignee", Placeholder: "name"})
			}
			command.AddJQLFilterVar(c, &statuses, "status",
				"Match status names or category comparisons; !NAME excludes a status (repeatable)",
				command.FlagOpts{Terse: "Status", Placeholder: "name"})
			command.AddJQLFilterVar(c, &types, "type",
				"Match one of these issue types (repeatable; combines with OR)",
				command.FlagOpts{Terse: "Type", Placeholder: "name"})
			command.AddJQLFilterVar(c, &labels, "label",
				"Match one of these labels (repeatable; combines with OR)",
				command.FlagOpts{Terse: "Label", Placeholder: "label"})
			command.AddStringSliceVar(c, &fields, "fields", nil,
				"Restrict the fields Jira returns (comma-separated); without "+
					"it, the fields the table's own columns need",
				command.FlagOpts{Terse: "Fields", Placeholder: "list", Complete: "predictor=cachefield,comma"})
			command.AddBoolVar(c, &detail, "detail", false,
				"Fetch all native fields and the full list expansion preset", command.FlagOpts{Terse: "Full details"})
			command.AddStringSliceVar(c, &expand, "expand", nil,
				"Native issue expansions (comma-separated); overrides the detail preset; --expand= disables them",
				command.FlagOpts{Terse: "Expansions", Placeholder: "list", Enum: strings.Split(command.IssueExpansionValues, ",")})
			command.AddIntVar(c, &limit, "limit", defaultListLimit,
				fmt.Sprintf("Total issues to fetch, auto-paginating; stops after %d "+
					"consecutive empty pages or an unusable cursor", command.MaxSearchEmptyPages),
				command.FlagOpts{Terse: "Issue count", Placeholder: "n"})
			command.AddBoolVar(c, &all, "all", false,
				fmt.Sprintf("Drain every page of the search, up to a safety cap "+
					"of %d pages / %d issues total; refused alongside an explicit "+
					"--limit", maxAllPages, maxAllIssues),
				command.FlagOpts{Terse: "Drain all pages"})
			command.AddBoolVar(c, &unbounded, flagUnbounded, false,
				fmt.Sprintf("Lift --all's %d-page / %d-issue safety cap and drain "+
					"the whole search, also lifting the consecutive empty-page cap; "+
					"requires --all", maxAllPages, maxAllIssues),
				command.FlagOpts{Terse: "Uncapped drain"})
			command.AddStringVar(c, &nextPageToken, "next-page-token", "",
				"The cursor from a previous page's response, to continue from "+
					"it — this endpoint has no offset",
				command.FlagOpts{Terse: "Page cursor", Placeholder: "token"})
			command.AddStringSliceVar(c, &columns, flagColumns, nil,
				"Choose and order the columns of the human table — "+
					"distinct from --fields, which chooses what Jira sends",
				command.FlagOpts{Terse: "Table columns", Placeholder: "list", Enum: issueFieldNames()})
			command.AddBoolVar(c, &asJQL, flagAsJQL, false,
				"Print the composed JQL and stop, without running the search",
				command.FlagOpts{Terse: "Show composed JQL"})
			command.AddStringVar(c, &board.name, flagBoard, "",
				"List the issues on this board, by name; narrow it further with the filters above",
				command.FlagOpts{Terse: "Board", Placeholder: "name", Complete: "predictor=cacheboard"})
			command.AddIntVar(c, &board.id, flagBoardID, 0,
				"List the issues on this board, by its numeric `ID` — the unambiguous form of --board",
				command.FlagOpts{Terse: "Board id", Placeholder: "id", Complete: "predictor=cacheboardid"})
			addCacheFlags(c, &board.cacheOptions, "board")
			command.AddStringVar(c, &created, "created", "",
				"Match creation dates using a date, signed period, comparison or inclusive A..B range",
				command.FlagOpts{Terse: "Created range", Placeholder: "range"})
			command.AddStringVar(c, &updated, "updated", "",
				"Match update dates using a date, signed period, comparison or inclusive A..B range",
				command.FlagOpts{Terse: "Updated range", Placeholder: "range"})
			command.AddStringVar(c, &resolved, "resolved", "",
				"Match resolution dates using a date, signed period, comparison or inclusive A..B range",
				command.FlagOpts{Terse: "Resolved range", Placeholder: "range"})
			command.AddListFlags(c, &listFlags)
			command.AddHiddenAliases(c, listAliases)
		},
		Validate: func() error {
			if cmd.Flags().Changed(flagBoard) && cmd.Flags().Changed(flagBoardID) {
				input := cli.NewInputError(cli.CodeFlagConflict, "--board and --board-id are alternatives")
				input.Field = flagBoard
				return input
			}
			if cmd.Flags().Changed(flagBoardID) && board.id <= 0 {
				input := cli.NewInputError(cli.CodeFlagValueInvalid, "--board-id must be a positive board id")
				input.Field = flagBoardID
				return input
			}
			if cmd.Flags().Changed(flagBoard) && strings.TrimSpace(board.name) == "" {
				board.name = ""
			}
			if cmd.Flags().Changed(flagSaved) {
				if err := config.ValidateQueryName(saved); err != nil {
					e := cli.NewInputError(cli.CodeFlagValueInvalid, err.Error())
					e.Field = flagSaved
					return e
				}
				if cmd.Flags().Changed("jql") || cmd.Flags().Changed(flagBoard) || cmd.Flags().Changed(flagBoardID) {
					e := cli.NewInputError(cli.CodeFlagConflict, "--saved, --jql and board selectors are alternative query sources; choose one")
					e.Field = flagSaved
					return e
				}
			}
			if err := command.ValidateIssueExpansions(expand); err != nil {
				return err
			}
			if detail && cmd.Flags().Changed("fields") {
				e := cli.NewInputError(cli.CodeFlagConflict, "--detail requests all fields; use --fields with --expand for a narrowed expanded read")
				e.Field = "fields"
				return e
			}
			if listFlags.Count || isWeb(cmd) {
				for _, name := range []string{"detail", "expand"} {
					if cmd.Flags().Changed(name) {
						e := cli.NewInputError(cli.CodeFlagNotApplicable, "--"+name+" requires issue records; --count and --web do not fetch them")
						e.Field = name
						return e
					}
				}
			}
			// Aliases resolve FIRST: command.ComposeJQL below reads listFlags' own
			// canonical fields, and an alias given instead of its
			// canonical (--sort instead of --order-by) has not reached
			// them until this runs — see AddListFlags' own doc comment
			// for why this cannot happen any earlier than Validate.
			if err := command.ResolveListFlagAliases(cmd.Flags()); err != nil {
				return err
			}
			// This command's own alias, folded in the same place and for
			// the same timing reason: --cursor has not reached
			// nextPageToken until now, and Run reads that variable rather
			// than the flag set.
			if err := command.ResolveHiddenAliases(cmd.Flags(), listAliases); err != nil {
				return err
			}
			// Composed here, before any I/O: a JQL/filter combination the
			// command refuses (command.ComposeJQL's own ambiguity rule) or a value
			// that cannot be safely quoted must never reach a network call.
			if err := board.validate(jql); err != nil {
				return err
			}
			for _, date := range []struct{ name, value string }{
				{"created", created}, {"updated", updated}, {"resolved", resolved},
			} {
				if cmd.Flags().Changed(date.name) && strings.TrimSpace(date.value) == "" {
					e := cli.NewInputError(cli.CodeFlagValueInvalid, "--"+date.name+" needs a nonempty date or range")
					e.Field = date.name
					return e
				}
			}
			jqlInput = command.JQLInput{
				JQL:                   jql,
				AssignedToCurrentUser: currentAccount,
				Project:               projects,
				Key:                   keys,
				Epic:                  epics,
				Created:               created,
				Updated:               updated,
				Resolved:              resolved,
				Assignee:              assignees,
				Status:                statuses,
				IssueType:             types,
				Label:                 labels,
				Reporter:              listFlags.Reporter,
				Priority:              listFlags.Priority,
				OrderBy:               listFlags.OrderBy,
				Reverse:               listFlags.Reverse,
				CreatedAfter:          listFlags.CreatedAfter,
				CreatedBefore:         listFlags.CreatedBefore,
				UpdatedAfter:          listFlags.UpdatedAfter,
				UpdatedBefore:         listFlags.UpdatedBefore,
				Watching:              listFlags.Watching,
			}
			if saved != "" {
				// Exercise the same raw-query conflict checks before reading
				// any files. Run replaces this placeholder with the stored JQL.
				jqlInput.JQL = "saved query"
			}
			if currentAccount {
				// The current mine preset sorts by updated ascending, while
				// ordinary lists retain their field-specific directions.
				if jqlInput.OrderBy == "" {
					if strings.TrimSpace(jql) == "" && !board.given() && saved == "" {
						jqlInput.OrderBy = "updated"
						presetOrder = true
					} else if listFlags.Reverse {
						e := cli.NewInputError(cli.CodeFlagNotApplicable, "--reverse needs --order-by when raw JQL or board scope supplies the ordering")
						e.Field = "reverse"
						return e
					}
				}
				if jqlInput.OrderBy != "" {
					jqlInput.Descending = &listFlags.Reverse
					jqlInput.Reverse = false
					jqlInput.CustomOrder = true
				}
			}
			// Composed WITHOUT the board, which is the most this can check
			// before any I/O: every refusal that does not depend on the
			// board's own query — a --jql/filter collision, a value that
			// cannot be safely quoted — must still fire here rather than
			// after a caller has waited for three round trips. Run composes
			// again once the board has resolved.
			q, err := command.ComposeJQL(jqlInput)
			if err != nil {
				return err
			}
			composedJQL = q
			limitChanged := cmd.Flags().Changed("limit")
			if err := validateCountFlags(listFlags.Count, limitChanged, all); err != nil {
				return err
			}
			if err := validateAllLimit(all, limitChanged); err != nil {
				return err
			}
			if err := validateUnbounded(unbounded, all); err != nil {
				return err
			}
			if err := validateAsJQL(asJQL, limitChanged, all, listFlags.Count, isWeb(cmd)); err != nil {
				return err
			}
			if len(columns) > 0 {
				// Parsed in Validate rather than in Human for the reason
				// every other refusal here is: a typo in a column name must
				// fail before the search runs, not after a caller has waited
				// for one. Human then renders a selection already known good.
				sel, err := parseIssueFields(columns)
				if err != nil {
					return err
				}
				if err := refuseUnfetchedColumns(sel, fields); err != nil {
					return err
				}
				selected = sel
			}
			return nil
		},
		Human: func(v *output.View, env envelope.Envelope) error {
			return renderList(v, env, site, estimate, selected)
		},
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueListOutput, error) {
			preferences, err := issuePreferences(ctx, deps)
			if err != nil {
				return envelope.IssueListOutput{}, err
			}
			defaultBoard := !cmd.Flags().Changed(flagBoard) && !cmd.Flags().Changed(flagBoardID) && strings.TrimSpace(preferences.DefaultBoard) != ""
			if !defaultBoard && !board.given() && strings.TrimSpace(jqlInput.JQL) == "" && saved == "" && len(jqlInput.Project) == 0 && !cmd.Flags().Changed("project") && strings.TrimSpace(preferences.DefaultProject) != "" {
				jqlInput.Project = []string{strings.TrimSpace(preferences.DefaultProject)}
			}
			if defaultBoard && presetOrder {
				if listFlags.Reverse {
					input := cli.NewInputError(cli.CodeFlagNotApplicable, "--reverse needs --order-by when configured board scope supplies the ordering")
					input.Field = "reverse"
					return envelope.IssueListOutput{}, input
				}
				jqlInput.OrderBy = ""
				jqlInput.Descending = nil
				jqlInput.CustomOrder = false
			}
			composedJQL, err = command.ComposeJQL(jqlInput)
			if err != nil {
				return envelope.IssueListOutput{}, err
			}
			if saved != "" {
				view, err := readSavedQuery(ctx, saved)
				if err != nil {
					return envelope.IssueListOutput{}, err
				}
				savedView = view
				jqlInput.JQL = view.JQL
				composedJQL, err = command.ComposeJQL(jqlInput)
				if err != nil {
					return envelope.IssueListOutput{}, err
				}
			}
			// The board resolves FIRST, before every branch below,
			// because all of them need the composed query — --as-jql
			// prints it, --web builds a URL from it, --count counts it and
			// the search runs it. Resolving per branch would be four
			// chances to forget one.
			if board.given() {
				resolved, err := resolveListBoard(ctx, deps, board, &jqlInput)
				if err != nil {
					return envelope.IssueListOutput{}, err
				}
				composedJQL = resolved.jql
				boardView = resolved.view
			}
			if defaultBoard {
				resolved, err := defaultListBoard(ctx, deps, preferences, &jqlInput)
				if err != nil {
					return envelope.IssueListOutput{}, err
				}
				composedJQL, boardView = resolved.jql, resolved.view
			}
			if asJQL {
				// FIRST, ahead of --web and --count both, because this is
				// the only branch that reaches nothing at all: command.ComposeJQL
				// already ran in Validate, so the answer is in hand and no
				// client is even constructed. --web resolves one for its
				// site and --count spends a round trip; neither can precede
				// a branch that promises to contact nothing.
				return envelope.IssueListOutput{Issues: []envelope.IssueView{}, JQL: composedJQL, Board: boardView, SavedQuery: savedView}, nil
			}
			runDeps := deps
			if defaultBoard {
				runDeps, err = bindDefaultBoardClient(ctx, deps, preferences)
				if err != nil {
					return envelope.IssueListOutput{}, err
				}
			}

			if isWeb(cmd) {
				// --web's own short-circuit, checked BEFORE --count: both
				// are single-purpose "don't run the ordinary search"
				// paths, and a caller combining --web with --count/
				// --limit/--all/--fields gets the browser-URL answer —
				// none of the others has anything left to compose with a
				// search this branch never runs. See runListWeb's own
				// doc comment for why this skips SearchIssues entirely.
				// execOpener is the production browserOpener; a test
				// calls runListWeb directly with a fake one instead.
				out, err := runListWeb(ctx, runDeps, composedJQL, execOpener)
				out.Board = boardView
				out.SavedQuery = savedView
				return out, err
			}
			if listFlags.Count {
				// A single round trip to the approximate-count endpoint —
				// never runList's own auto-paginating search: --count's
				// whole point is to answer "how many" without paying for
				// "which ones".
				// site is deliberately NOT resolved here: renderList
				// short-circuits to renderCount, which prints a bare
				// total, and site only ever reaches issueListColumns on
				// the table path below. Resolving it would imply --count's
				// output carries hyperlinked keys, which it has none of.
				out, err := runCount(ctx, runDeps, composedJQL)
				out.Board = boardView
				out.SavedQuery = savedView
				return out, err
			}
			requestFields, requestExpand := fields, expand
			if detail {
				requestFields = []string{"*all"}
				if !cmd.Flags().Changed("expand") {
					requestExpand = strings.Split(command.IssueListExpansions, ",")
				}
			}
			out, est, err := runList(ctx, runDeps, composedJQL, requestFields, nextPageToken,
				listWalk{limit: limit, all: all, unbounded: unbounded}, requestExpand...)
			estimate = est
			out.Board = boardView
			out.SavedQuery = savedView
			if err == nil {
				// A second deps.JiraClient() call, not a second
				// resolution — see newView's own identical comment
				// (view.go): root/deps.go's sync.OnceValues means every
				// call after the first, including runList's own just
				// above, returns the SAME cached client.
				if client, cerr := runDeps.JiraClient(ctx); cerr == nil {
					site = client.BaseURL()
				}
			}
			return out, err
		},
	}
	if currentAccount {
		spec.Op = "issue.mine"
		spec.Use = "mine"
		spec.Short = "List issues assigned to the current account"
		spec.Long = "List issues assigned to the authenticated account. Every query includes " +
			"assignee = currentUser(); there is no --assignee override.\n\n" +
			"Convenience filters narrow that scope. --jql adds parenthesized conditions, " +
			"so OR cannot escape the account scope. Its trailing ORDER BY wins over " +
			"--order-by. Without raw JQL or board scope, the default is updated ASC; " +
			"--reverse flips an explicit or default sort direction. With raw JQL or " +
			"board scope it requires --order-by; raw ORDER BY still takes precedence.\n\n" +
			"Uses the same fields, human columns, cursor walk and failure receipts as " +
			"issue list. --limit bounds the total; --all drains pages within the " +
			"documented safety caps, and --unbounded lifts them. --next-page-token " +
			"resumes a prior search. --fields '*all' requests all native fields; --detail also requests native expansions. --expand replaces that preset. Issue metadata is at .issues[].details; native page metadata is at .search_pages[].\n\n" +
			"--as-jql previews the query without credentials or a search; explicit " +
			"board scope requires its normal board resolution. --count requests an " +
			"approximate count; --web opens the composed search."
		spec.Example = "$ jira issue mine --project ABC --status '!Done'\n\n" +
			"# Narrow custom conditions to the current account\n" +
			"$ jira issue mine --jql 'project = ABC OR project = DEF ORDER BY key ASC'\n\n" +
			"# Preview locally\n" +
			"$ jira issue mine --project ABC --as-jql --output=json"
	}
	spec.Long += "\n\ndefault_project supplies an omitted structured project when no raw/saved query or board is active. " +
		"default_board uses only a retained named-board saved-filter snapshot for the configured public site and account; it never warms remotely. " +
		"Explicit board selectors override it; --board= suppresses it. Missing or malformed snapshots refuse the query. " +
		"Configured board scope narrows raw/saved queries and keeps mine account scope. Machine results retain default_board provenance and cached_at. " +
		"Human --as-jql remains bare pipeable JQL. Live search/count/browser refuses when the resolved site/account differs from the snapshot's public context."
	spec.Long += "\n\n--saved NAME reads NAME.jql from queries_path, with optional YAML or TOML frontmatter. " +
		"Metadata name, description and project are retained at .saved_query; project never adds a hidden constraint. " +
		"--saved, --jql and board selectors are alternatives. Ordinary list keeps raw-query filter exclusivity; mine narrows the saved query beneath its account scope. " +
		"--as-jql reads local files without credentials or Jira requests. Shared paging, field/detail, count and browser routes apply."
	spec.Example += "\n\n# Inspect and run a named local query\n$ jira issue " + spec.Use + " --saved inbox --as-jql\n$ jira issue " + spec.Use + " --saved inbox --all"
	return command.MustNew(spec)
}

// runListWeb answers `issue list --web`: the composed search's
// issue-navigator URL (cli.SearchBrowseURL), built entirely from jql —
// no SearchIssues call, per --web's own "no API call" rule. Client
// construction still happens, for the site alone (mirrors
// openIssueBrowseTarget's own reasoning, web.go). Issues stays an
// empty, non-nil slice and Pagination its zero value, the same "nothing
// real to report" shape runCount above already gives --count's own
// single-purpose path.
//
// open is the browserOpener openForHuman is handed — a parameter, not a
// hard-coded execOpener, so a test can supply a fake (mirrors
// openIssueBrowseTarget's own reasoning, web.go).
//
// Deliberately NOT folded into openIssueBrowseTarget, which the two
// issue-scoped --web paths share: this one builds a different URL
// (cli.SearchBrowseURL, from the site alone) and fails with a different code.
// Merging them would need a builder func and an error-code parameter
// for exactly one caller.
//
// A site that cannot become a search URL fails the command outright,
// the same "producing this URL IS the point of this invocation" posture
// openIssueBrowseTarget applies to a bad key — cli.CodeConfigInvalid here, not
// CodePathInvalid, because jql plays no part in cli.SearchBrowseURL's own ok
// gate: only site does, and a resolved client whose own BaseURL will not
// parse is a configuration fault, not an input one.
func runListWeb(ctx context.Context, deps Deps, jql string, open browserOpener) (envelope.IssueListOutput, error) {
	var zero envelope.IssueListOutput
	client, err := deps.JiraClient(ctx)
	if err != nil {
		return zero, err
	}
	target, ok := cli.SearchBrowseURL(client.BaseURL(), jql)
	if !ok {
		return zero, cli.NewCodedError(cli.CodeConfigInvalid,
			fmt.Sprintf("the configured site (%q) is not a URL a Jira search link could be built from", client.BaseURL()))
	}
	openForHuman(ctx, open, target)
	return envelope.IssueListOutput{
		Issues:  []envelope.IssueView{},
		WebLink: envelope.WebLink{WebURL: target},
	}, nil
}

// validateAllLimit refuses --all alongside an explicit --limit — pulled
// out of newList's Validate closure so the rule is directly testable
// without building a cobra command. limitChanged is
// cmd.Flags().Changed("limit"): --all combined with --limit's own
// UNTOUCHED default must not trip this, since that is not really a
// competing request, only the flag's zero-effect default sitting there
// unread — the same "Changed, not the value" reasoning issue view's own
// newView already applies to --comments (view.go).
//
// --all already asks for every result there is; an EXPLICIT --limit can
// only disagree with that, never sharpen it (a bounded count is not a
// bigger drain), so the combination is refused rather than silently
// picking a winner between them — command.ComposeJQL's own --jql/filter
// refusal (jql.go) is the identical rule for a different pair of flags.
func validateAllLimit(all, limitChanged bool) error {
	if !all || !limitChanged {
		return nil
	}
	e := cli.NewInputError(cli.CodeFlagNotApplicable,
		"--limit sets a fixed number of issues to fetch, but --all already "+
			"drains every result there is — combining them is refused rather "+
			"than silently picking a winner between them; drop --limit, or "+
			"drop --all and let --limit's own auto-pagination fetch the count "+
			"you want")
	e.Field = "limit"
	return e
}

// validateCountFlags refuses --count alongside an explicit --limit or
// --all — the same "Changed, not the value" posture validateAllLimit
// applies just above, for the identical reason: --count fetches nothing
// at all (runCount below never calls autoPaginate), so a page size or a
// drain-everything request has nothing left to act on. Refused rather
// than silently ignored, matching this file's own standing rule for
// every other flag combination that cannot both mean something at once
// (validateAllLimit above, command.ComposeJQL's own --jql/filter refusal in
// jql.go): a caller who typed --limit or --all alongside --count almost
// certainly meant one or the other, and guessing which would be the
// surprise, not the refusal.
func validateCountFlags(count, limitChanged, all bool) error {
	if !count {
		return nil
	}
	switch {
	case limitChanged:
		e := cli.NewInputError(cli.CodeFlagNotApplicable,
			"--count reports how many issues match without fetching any of "+
				"them, so --limit's own page size has nothing to act on — "+
				"combining them is refused rather than silently ignored; drop "+
				"--limit, or drop --count and read data.pagination.returned "+
				"from an ordinary search instead")
		e.Field = "limit"
		return e
	case all:
		e := cli.NewInputError(cli.CodeFlagNotApplicable,
			"--count reports how many issues match without fetching any of "+
				"them, so --all's own drain has nothing to act on — combining "+
				"them is refused rather than silently ignored; drop --all, or "+
				"drop --count and let --all drain the search instead")
		e.Field = "all"
		return e
	default:
		return nil
	}
}

// validateUnbounded refuses --unbounded without --all.
//
// --unbounded does exactly one thing: it removes the cap that stops
// --all's drain (maxAllPages/maxAllIssues). Without --all there is no
// drain and therefore no cap, so the flag has nothing to lift — and a
// caller who typed it almost certainly believed it would fetch
// everything on its own. Refusing says which flag actually drains;
// accepting it silently would leave them with --limit's default 50 and
// no way to tell that the flag they reached for did nothing.
//
// It is deliberately NOT an alias for `--all --unbounded`. --all is the
// flag that says "drain", and a second spelling of it that also lifts
// the safety cap would mean the shortest way to ask for everything is
// also the way that never stops.
func validateUnbounded(unbounded, all bool) error {
	if !unbounded || all {
		return nil
	}
	e := cli.NewInputError(cli.CodeFlagNotApplicable,
		"--unbounded lifts the safety cap on --all's drain, but --all was not given, "+
			"so there is no drain and no cap to lift — add --all to drain every page, "+
			"or use --limit to fetch a fixed number")
	e.Field = flagUnbounded
	return e
}

// validateAsJQL refuses --as-jql alongside any flag that describes a
// search this invocation will not run.
//
// The same posture validateCountFlags and validateAllLimit take one
// function apart, applied to a third single-purpose path: --as-jql
// composes the query and stops, so --limit's count, --all's drain,
// --count's round trip and --web's browser URL each have nothing left to
// act on. Refused rather than silently ignored, because a caller who
// typed `--as-jql --count` wanted one of two different answers and
// guessing which would be the surprise.
//
// --fields and --next-page-token are deliberately NOT refused, and the
// line is drawn where validateCountFlags already draws it: neither is
// refused beside --count either, and widening the rule for the newer
// flag would leave two nearly-identical previews with different
// tolerances for no reason a caller could infer.
func validateAsJQL(asJQL, limitChanged, all, count, web bool) error {
	if !asJQL {
		return nil
	}
	refuse := func(flag, what string) error {
		e := cli.NewInputError(cli.CodeFlagNotApplicable, fmt.Sprintf(
			"--%s prints the composed JQL without running the search, so %s has nothing to act on — "+
				"combining them is refused rather than silently ignored; drop --%s, or drop --%s "+
				"and run the search",
			flagAsJQL, what, flag, flagAsJQL,
		))
		e.Field = flag
		return e
	}
	switch {
	case count:
		return refuse("count", "--count's own round trip")
	case web:
		return refuse("web", "--web's own browser URL")
	case limitChanged:
		return refuse("limit", "--limit's own issue count")
	case all:
		return refuse("all", "--all's own drain")
	default:
		return nil
	}
}

// listWalk is how much of a search the pagination walk is being asked
// for: a fixed count, a capped drain, or an uncapped one.
//
// A struct rather than three more positional parameters, because the
// three are only meaningful TOGETHER — limit is ignored once all is set,
// and unbounded means nothing without it — and a call site reading
// `nil, "", 50, false, false` cannot be checked against that by anyone
// reading it. Named fields also let the common cases stay short:
// listWalk{limit: 25} and listWalk{all: true} say what they are.
type listWalk struct {
	// limit is the TOTAL issue count wanted, honored only when all is
	// false.
	limit int
	// all drains every page instead, up to the caps below.
	all bool
	// unbounded lifts those caps. Only ever set alongside all —
	// validateUnbounded refuses it on its own, so this pairing is
	// enforced at the flag surface rather than trusted here.
	unbounded bool
}

// countEstimate carries issue list's optional "of ~N" scale hint,
// resolved by fetchCountEstimate below and threaded from Run to Human
// via newList's own estimate variable (see its doc comment for why this
// rides a closed-over variable rather than the envelope). known is
// false whenever the count was never fetched at all (see
// fetchCountEstimate's own gates) or the fetch itself failed — either
// way, nextPageHint falls back to the count-free wording, never a zero
// or a stale number.
type countEstimate struct {
	value int64
	known bool
}

// runCount answers `--count`: how many issues jql matches, WITHOUT
// fetching a single one of them. A single round trip to the
// approximate-count endpoint (core.JiraClient.CountIssues) — this is
// that seam's SECOND caller, not a second path to it; fetchCountEstimate
// below (runList's own "of ~N" hint) is the first. Issues comes back as
// an empty, non-nil slice (never nil — the same reason buildListOutput's
// own Issues always is) and Pagination stays at its zero value: neither
// field has anything real to report when the search this type otherwise
// always describes never ran.
func runCount(ctx context.Context, deps Deps, jql string) (envelope.IssueListOutput, error) {
	var zero envelope.IssueListOutput

	client, err := deps.JiraClient(ctx)
	if err != nil {
		return zero, err
	}
	read, err := command.Spin(ctx, "Counting issues", func(spinCtx context.Context) (core.IssueCountRead, error) {
		return client.CountIssues(spinCtx, jql)
	})
	if err != nil {
		return zero, err
	}
	return envelope.IssueListOutput{
		Issues: []envelope.IssueView{},
		Count:  &envelope.IssueCountEstimate{Value: read.Count, Approximate: true},
	}, nil
}

// runList runs the composed JQL search, auto-paginating across as many
// pages as needed to satisfy limit (or, with all, to drain the search up
// to its own safety cap) — pulled out of the command.Spec closure so it
// is directly testable against a fake core.JiraClient, matching
// runView's and runCommentList's own split.
func runList(ctx context.Context, deps Deps, jql string, fields []string, nextPageToken string, walk listWalk, expand ...string) (envelope.IssueListOutput, countEstimate, error) {
	var zero envelope.IssueListOutput

	if walk.limit < 0 {
		e := cli.NewInputError(cli.CodeFlagValueInvalid,
			fmt.Sprintf("--limit expects an issue count of 0 or more, got %d", walk.limit))
		e.Field = "limit"
		return zero, countEstimate{}, e
	}

	client, err := deps.JiraClient(ctx)
	if err != nil {
		return zero, countEstimate{}, err
	}
	requestFields := fields
	if len(requestFields) == 0 {
		requestFields = defaultListFields
	}
	// ONE spinner around the whole walk, not one per page: a human sees a
	// single progress line regardless of how many requests it takes
	// underneath (command.Spin's own doc comment — every blocking Jira
	// call goes through it so waiting looks the same everywhere). The
	// optional count fetch below gets its OWN spinner instead of folding
	// into this one: it is a separate, later network call the search
	// itself never needed, so it reads as its own step rather than
	// stretching this one silently.
	out, err := command.Spin(ctx, "Searching issues", func(spinCtx context.Context) (envelope.IssueListOutput, error) {
		return autoPaginate(spinCtx, client, jql, requestFields, nextPageToken, walk, expand...)
	})
	if err != nil {
		return out, countEstimate{}, err
	}
	return out, fetchCountEstimate(ctx, client, jql, out), nil
}

// fetchCountEstimate resolves issue list's optional "of ~N" scale hint —
// gated to fire ONLY where it adds information a human can act on,
// since it costs a SECOND HTTP round trip on the most-used command in
// this CLI. Three shapes never make the call at all, each answering
// with the zero countEstimate{} (known false), which nextPageHint reads
// as "render the count-free wording":
//
//   - out.Pagination.IsLast: the walk already collected everything
//     there is, so out.Pagination.Returned already IS the exact count.
//     Asking a SEPARATE endpoint for an ESTIMATE of a number this
//     invocation already knows exactly would be worse than useless —
//     the estimate is explicitly allowed to disagree with reality
//     ("recent updates might not be immediately visible", the
//     endpoint's own words, core.JiraClient.CountIssues's doc comment).
//   - the resolved output is not human: --output=json/compact callers
//     already get is_last and next_page_token in the envelope and can
//     count for themselves — spending a round trip on a number nothing
//     will render is pure waste. This reuses the SAME cli.
//     FormatFromContext resolution the comment preview (issue view's
//     isHuman, view.go) and the auto-pagination walk's own spinner
//     already read — one notion of "is this human" for the whole CLI,
//     never a second one invented here that could drift from it.
//   - the fetch itself fails: see the trailing branch below.
func fetchCountEstimate(ctx context.Context, client core.JiraClient, jql string, out envelope.IssueListOutput) countEstimate {
	// IsLast: the walk reached the end, so the count is exactly what came
	// back and no estimate is needed. Truncated: the safety-cap or cursor-fault
	// hint never renders an estimate, so fetching one would waste a request.
	if out.Pagination.IsLast || out.Truncated {
		return countEstimate{}
	}
	format, formatOK := cli.FormatFromContext(ctx)
	if !formatOK || format != output.FormatHuman {
		return countEstimate{}
	}

	read, err := command.Spin(ctx, "Estimating total", func(spinCtx context.Context) (core.IssueCountRead, error) {
		return client.CountIssues(spinCtx, jql)
	})
	if err != nil {
		// The search itself already succeeded — out is real, complete
		// data for however many issues this walk collected. A count-call
		// failure (network, timeout, or the endpoint refusing an
		// unbounded JQL — its own documented requirement) is a display
		// aid breaking, not the primary command breaking, so it degrades
		// silently to the count-free hint rather than failing the whole
		// invocation: the same "a hiccup fetching a display aid must
		// never take down the result the caller actually asked to see"
		// rule issue view's own degradeOrFail applies to its comment
		// fetch (view.go).
		return countEstimate{}
	}
	return countEstimate{value: read.Count, known: true}
}

// autoPaginate delegates issue and epic pagination to the shared search owner.
func autoPaginate(ctx context.Context, client core.JiraClient, jql string, fields []string, startToken string, walk listWalk, expand ...string) (envelope.IssueListOutput, error) {
	return command.WalkIssues(ctx, client, jql, fields, startToken, walk.shared(), expand...)
}

func (w listWalk) shared() command.SearchWalk {
	return command.SearchWalk{Limit: w.limit, All: w.all, Unbounded: w.unbounded}
}

// renderList writes the human view: a table when there is at least one
// result, one honest line when there is none — never an empty table
// (output.Table itself writes nothing at all for zero items; the empty
// state is this command's own to own, the same way renderCommentList's
// "No comments." is comment.go's, not a shared primitive's). Every
// non-empty result gets a trailing hint naming the scale of what was
// returned, and — when there is more to see — the ACTION a human can
// take: never an EXACT total the search endpoint never sent (see
// envelope.CursorPagination's own doc comment), never the raw cursor
// either (that stays machine-only, in data.pagination.next_page_token —
// see nextPageHint's own doc comment for why), and never an estimate
// presented as exact (the `~` prefix is load-bearing whenever estimate
// carries one — see fetchCountEstimate). site is the Jira base URL
// runList's own client resolved, carried the same way issue view's
// preview/site are (view.go's newView) — for the KEY column's hyperlink
// (issueListColumns, via cli.IssueKey); "" degrades every key in the
// column back to plain, which is cli.IssueKey's own job to handle.
// estimate is runList's own optional count fetch, carried the same way
// — see newList's own estimate variable for why it rides a closed-over
// variable rather than the envelope.
func renderList(v *output.View, env envelope.Envelope, site string, estimate countEstimate, columns []issueField) error {
	out, ok := env.Data.(envelope.IssueListOutput)
	if !ok {
		return fmt.Errorf("issue.list: human renderer received %T, want envelope.IssueListOutput", env.Data)
	}

	if out.JQL != "" {
		// --as-jql's own short-circuit, checked FIRST for the reason Run
		// checks it first: no search ran, so out.Issues is empty for a
		// reason that has nothing to do with a zero-result search, and
		// every branch below would misread it as one.
		//
		// Printed as a bare line rather than through a Hint or a Section:
		// the whole point of the flag is that the output can be piped
		// straight back into `--jql`, and a decorated line would have to
		// be unpicked first. cli.Text because the string is composed
		// partly from values the caller typed and partly from a --jql
		// they may have pasted from elsewhere.
		v.Line(cli.Text(out.JQL))
		return nil
	}

	if out.WebURL != "" {
		// --web's own short-circuit: runListWeb never ran the search, so
		// out.Issues is genuinely empty for a reason that has nothing to
		// do with a zero-result search — check this FIRST, before
		// out.Count's own branch or the ordinary "No issues found."
		// fallback below could misread it as one.
		return renderWebURL(v, out.WebURL)
	}

	if out.Count != nil {
		return renderCount(v, *out.Count)
	}

	if out.Board != nil && out.Board.Precedence == "default_board" && out.Board.CachedAt != nil {
		v.Line(cli.Text(fmt.Sprintf("Configured cached board: %s (%d), saved %s", out.Board.Name, out.Board.ID, out.Board.CachedAt.UTC().Format(time.RFC3339))))
	}

	if len(out.Issues) == 0 {
		if out.Truncated {
			v.Line(cli.Text("No issues returned before the search stopped."))
			v.Hint(nextPageHint(out, estimate))
			return nil
		}
		v.Line(cli.Text("No issues found."))
		return nil
	}

	output.Table(v, cli.ResolvedTheme(), issueListColumns(site, statusPillWidth(out.Issues, issueRowFromView), columns), out.Issues)

	v.Line()
	v.Hint(nextPageHint(out, estimate))
	return nil
}

// renderCount writes `--count`'s own one-line human answer: the
// estimate alone, no table, since --count's whole point is reporting
// how many issues match WITHOUT listing them. The `~` prefix is the same
// marker nextPageHint's own estimate branch uses (see its own doc
// comment) and is equally load-bearing here — count.Approximate is
// always true (envelope.IssueCountEstimate's own doc comment explains
// why), so a human reading this line must never mistake it for an exact
// total the approximate-count endpoint never promised. Nothing
// Jira-authored reaches this string — count.Value is a number this CLI
// received, "issues match." is CLI-authored prose — so there is no
// sanitizing sink to run first, the same reasoning nextPageHint's own
// doc comment gives.
func renderCount(v *output.View, count envelope.IssueCountEstimate) error {
	v.Line(fmt.Sprintf("~%s issues match.", cli.Number(int(count.Value))))
	return nil
}

// nextPageHint names the scale of what was returned, and — when there
// is more to see — the ACTION a human can take, never the cursor. The
// base64 next_page_token used to ride straight into this line (`jira
// issue list --next-page-token <60-odd chars>`), which is exactly the
// "humans copying a cursor" problem this slice exists to close: the
// token stays in machine output (data.pagination.next_page_token) for a
// script to resume from, and a human view says only what to DO — raise
// --limit, inspect a cursor fault, or lift a safety cap — never the data
// itself. Three shapes, in priority order:
//
//   - out.Truncated: the specific cap or cursor fault, with its remedy.
//   - more results exist (!out.Pagination.IsLast): "Showing N", plus,
//     when fetchCountEstimate resolved one (estimate.known), " of ~M"
//     with both N and M rendered through cli.Number — thousands-grouped
//     via gechr/x/human.FormatNumber, and colored with clog's own
//     numeric-field style when styling is on. The `~` prefix stays
//     OUTSIDE cli.Number's own span deliberately: it is load-bearing
//     prose — core.JiraClient.CountIssues's own doc comment explains why
//     the endpoint can only ever estimate — and must never be dropped,
//     nor this line ever read "of M" as though M were exact, nor be
//     swallowed into the number's own colored span. Without a resolved
//     estimate (the fetch was skipped or degraded — see
//     fetchCountEstimate), this falls back to the same count-free
//     wording the pre-estimate version of this function always used.
//   - everything was returned (out.Pagination.IsLast, not Truncated):
//     "Showing N." — no count call is ever made for this shape (see
//     fetchCountEstimate's own first gate), because Returned already IS
//     the exact count. This is new: the shape used to render nothing at
//     all here, leaving a human with no sense of scale for a small,
//     complete result the same as a huge one truncated by --all.
//
// Nothing Jira-authored reaches this string: it is built entirely from
// numbers this CLI itself computed or received (out.Pagination.Returned,
// estimate.value) and static CLI-authored prose, so — unlike the
// pre-auto-pagination version of this function — there is no sanitizing
// sink to run first. The result is written by View.Hint, which puts it
// on clog's own message path: that styles the backtick-quoted flag
// mention when styling is on and leaves the sentence plain, backticks
// included, when it is not (H4) — the same bytes the sink produced when
// this function styled the text itself, since both go through clog's
// one backtick renderer. Every cli.Number call here sits OUTSIDE a backtick span, so
// its own colored span is never re-opened inside code's span (H5) — see
// TestRenderListHintColoredNumberNestsWithBacktickSpan in list_test.go
// for the byte-level proof.
func nextPageHint(out envelope.IssueListOutput, estimate countEstimate) string {
	switch {
	case out.Truncated:
		switch out.TruncatedReason {
		case envelope.TruncatedReasonMissingCursor:
			return fmt.Sprintf("Showing %s — Jira omitted its continuation cursor; search incomplete. Inspect `--output=json` before restarting.", cli.Number(out.Pagination.Returned))
		case envelope.TruncatedReasonRepeatedCursor:
			return fmt.Sprintf("Showing %s — Jira repeated a pagination cursor; search incomplete. Inspect `--output=json` before restarting.", cli.Number(out.Pagination.Returned))
		case envelope.TruncatedReasonMaxEmptyPages:
			return fmt.Sprintf("Showing %s — stopped after %s consecutive empty pages; search incomplete. Resume using the cursor in `--output=json`, or use `--all --unbounded` without `--limit` to lift safety caps.", cli.Number(out.Pagination.Returned), cli.Number(command.MaxSearchEmptyPages))
		}
		return fmt.Sprintf(
			"Showing %s — stopped after `--all`'s %s; more results exist. Add `--unbounded` to lift it.",
			cli.Number(out.Pagination.Returned), truncatedCapDescription(out.TruncatedReason),
		)
	case !out.Pagination.IsLast:
		if estimate.known {
			return fmt.Sprintf("Showing %s of ~%s — raise `--limit` for more.",
				cli.Number(out.Pagination.Returned), cli.Number(int(estimate.value)))
		}
		return fmt.Sprintf("Showing %s — raise `--limit` for more.", cli.Number(out.Pagination.Returned))
	default:
		return fmt.Sprintf("Showing %s.", cli.Number(out.Pagination.Returned))
	}
}

// truncatedCapDescription turns TruncatedReason's own short machine
// code into the prose nextPageHint's --all branch reports, naming the
// actual cap that stopped the walk rather than a generic "some limit" —
// mirroring maxAllPages/maxAllIssues's own values so the number in the
// message can never drift from the constant that produced it.
func truncatedCapDescription(reason string) string {
	switch reason {
	case envelope.TruncatedReasonMaxPages:
		return fmt.Sprintf("page cap (%s pages)", cli.Number(maxAllPages))
	case envelope.TruncatedReasonMaxIssues:
		return fmt.Sprintf("issue cap (%s issues)", cli.Number(maxAllIssues))
	default:
		return "safety cap"
	}
}
