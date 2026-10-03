package boards

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gechr/primer/table"
	xslices "github.com/gechr/x/slices"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	metacache "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

const (
	pageSize  = 50
	maxPages  = 100
	maxBoards = 10000
	cacheKey  = metacache.BoardListKey
)

type Deps struct {
	JiraClient func() (core.JiraClient, error)
	Cache      func() (core.Cache, error)
}

type options struct {
	cache          metacache.Options
	limit          int
	all, unbounded bool
}

func New(deps Deps) *cobra.Command {
	cmd := &cobra.Command{Use: "boards", Short: "Browse visible Agile boards", Args: cobra.NoArgs, Long: "Discover board IDs, types and associated project keys. Cached results are isolated by site and configured account.", Example: "$ jira boards list --output=json"}
	cmd.AddCommand(newList(deps))
	return cmd
}

func newList(deps Deps) *cobra.Command {
	opts := options{}
	return command.MustNew(command.Spec[envelope.BoardsListOutput]{
		Op: "boards.list", Identity: true, Use: "list", Short: "List visible boards and associated project keys",
		Long: "Read visible Agile boards and independently fetch each board's associated projects. Results are sorted by numeric board ID. " +
			"A board's location project is not substituted for its full project relation.\n\n" +
			"A fresh cache serves without Jira requests. Missing, stale or malformed entries fetch again. `--refresh` invalidates the previous entry first; " +
			"`--ttl-minutes` sets freshness, with nonpositive values using the one-day default. Cache failures still return fetched data. " +
			"Cache scope follows the configured site and account; permission changes within that account can require refresh.\n\n" +
			"Fetching is capped at 100 pages or 10,000 boards. `--unbounded` removes those board caps and refetches a cache truncated by them. " +
			"Project enrichment remains bounded at 100 pages or 10,000 keys per board. " +
			"`--limit` controls only the output window (nonpositive uses 50); `--all` returns the whole fetched snapshot. Neither disables fetch caps.\n\n" +
			"`pagination.total` counts the fetched/cache snapshot. `pagination.is_last` describes the output window; `pagination.complete` describes the server walk. " +
			"Truncation retains a resume offset and warning. Throttling after a validated board page returns a cached partial snapshot; other read failures retain validated rows in an error frame. " +
			"Project failures retain each board with incomplete keys and explicit diagnostics. Use `--refresh` to retry. Visibility always depends on permissions.",
		Example: `$ jira boards list

# Refetch before listing
$ jira boards list --refresh --output=json

# Return every fetched board
$ jira boards list --all --output=json

# Walk every board page, retaining project enrichment bounds
$ jira boards list --refresh --unbounded --all --output=json`,
		Flags: func(cmd *cobra.Command) {
			metacache.AddFlags(cmd, &opts.cache.Refresh, &opts.cache.TTLMinutes, "board list")
			command.AddIntVar(cmd, &opts.limit, "limit", pageSize, "Maximum displayed boards; nonpositive uses 50", command.FlagOpts{Terse: "Output limit", Placeholder: "n"})
			command.AddBoolVar(cmd, &opts.all, "all", false, "Return every board in the fetched/cache snapshot", command.FlagOpts{Terse: "Whole snapshot"})
			command.AddBoolVar(cmd, &opts.unbounded, "unbounded", false, "Disable the 100-page / 10,000-board fetch caps", command.FlagOpts{Terse: "Walk every board page"})
		}, Validate: func() error { return opts.cache.Validate() },
		Human: renderList, Warnings: listWarnings,
		RetainOnError: func(out envelope.BoardsListOutput) bool { return len(out.Boards) > 0 },
		Run: func(ctx context.Context, _ command.Invocation) (envelope.BoardsListOutput, error) {
			return runList(ctx, deps, opts)
		},
	})
}

type snapshot struct {
	Boards            []envelope.BoardRow            `json:"boards"`
	FetchedAt         time.Time                      `json:"fetched_at"`
	PagesFetched      int                            `json:"pages_fetched"`
	Complete          bool                           `json:"complete"`
	Truncated         bool                           `json:"truncated"`
	Reason            string                         `json:"reason,omitempty"`
	NextStart         *int                           `json:"next_start,omitempty"`
	RetryAfterSeconds int                            `json:"retry_after_seconds,omitempty"`
	Failures          []envelope.BoardProjectFailure `json:"project_failures"`
}

func runList(ctx context.Context, deps Deps, opts options) (envelope.BoardsListOutput, error) {
	store := metacache.Open(deps.Cache)
	read, err := metacache.ReadValue(ctx, store, cacheKey, opts.cache, func(value snapshot) bool {
		if !validSnapshot(value) {
			return false
		}
		if !opts.unbounded {
			return true
		}
		return value.Reason != "max_pages" && value.Reason != "max_results"
	}, func(ctx context.Context) (snapshot, error) {
		client, err := deps.JiraClient()
		if err != nil {
			return snapshot{}, err
		}
		return command.Spin(ctx, "Reading boards and associated projects", func(ctx context.Context) (snapshot, error) {
			return fetchSnapshot(ctx, client, opts.unbounded, maxPages, maxBoards)
		})
	})
	value := read.Value
	slices.SortFunc(value.Boards, func(a, b envelope.BoardRow) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	limit := opts.limit
	if limit <= 0 {
		limit = pageSize
	}
	rows := value.Boards
	if rows == nil {
		rows = []envelope.BoardRow{}
	}
	total := len(rows)
	if !opts.all && len(rows) > limit {
		rows = rows[:limit]
	}
	failures := value.Failures
	if failures == nil {
		failures = []envelope.BoardProjectFailure{}
	}
	state := read.SourceState
	if total == 0 {
		state = "empty"
	}
	out := envelope.BoardsListOutput{Boards: rows, Count: len(rows), Pagination: envelope.BoardsPagination{MaxResults: limit, Total: total, IsLast: len(rows) == total, Complete: value.Complete}, FromCache: read.Cached, CacheSaved: read.Saved, CacheState: state, CacheSourceState: read.SourceState, CacheEmpty: total == 0, PagesFetched: value.PagesFetched, Truncated: value.Truncated, TruncatedReason: value.Reason, NextStart: value.NextStart, RetryAfterSeconds: value.RetryAfterSeconds, ProjectFailures: failures}
	if !value.FetchedAt.IsZero() {
		out.FetchedAt = value.FetchedAt.UTC().Format(time.RFC3339Nano)
	}
	return out, err
}

func validSnapshot(s snapshot) bool {
	if s.Boards == nil || s.Failures == nil || s.FetchedAt.IsZero() || s.FetchedAt.After(time.Now()) || s.PagesFetched < 1 || s.Complete == s.Truncated {
		return false
	}
	if s.Truncated && (s.NextStart == nil || *s.NextStart != len(s.Boards) || (s.Reason != "max_pages" && s.Reason != "max_results" && s.Reason != "rate_limit")) {
		return false
	}
	if s.Complete && (s.NextStart != nil || s.Reason != "") {
		return false
	}
	if s.RetryAfterSeconds < 0 || (s.RetryAfterSeconds != 0 && s.Reason != "rate_limit") {
		return false
	}
	failures := make(map[int64]bool, len(s.Failures))
	for _, failure := range s.Failures {
		if failure.BoardID <= 0 || failure.Error.Code == "" || failures[failure.BoardID] {
			return false
		}
		failures[failure.BoardID] = true
	}
	seen := make(map[int64]bool, len(s.Boards))
	for _, b := range s.Boards {
		if b.ID <= 0 || strings.TrimSpace(b.Name) == "" || b.ProjectKeys == nil || seen[b.ID] {
			return false
		}
		if b.ProjectsComplete {
			if b.ProjectsNextStart != nil || failures[b.ID] {
				return false
			}
		} else if b.ProjectsNextStart == nil || *b.ProjectsNextStart < len(b.ProjectKeys) || !failures[b.ID] {
			return false
		}
		keys := make(map[string]bool, len(b.ProjectKeys))
		for _, key := range b.ProjectKeys {
			if !safeProjectKey(key) || keys[key] {
				return false
			}
			keys[key] = true
		}
		seen[b.ID] = true
	}
	for id := range failures {
		if !seen[id] {
			return false
		}
	}
	return true
}

func fetchSnapshot(ctx context.Context, client core.JiraClient, unbounded bool, pageCap, resultCap int) (snapshot, error) {
	s := snapshot{Boards: []envelope.BoardRow{}, Failures: []envelope.BoardProjectFailure{}}
	seen := map[int64]bool{}
	start := 0
	for {
		s.NextStart = new(start)
		read, err := client.GetBoards(ctx, "", start, pageSize)
		if err != nil {
			if s.PagesFetched > 0 && cli.MapError(err).Code == "jira_rate_limited" {
				s.Truncated = true
				s.Reason = "rate_limit"
				s.RetryAfterSeconds = cli.MapError(err).RetryAfterSeconds
				break
			}
			return finishSnapshot(s), err
		}
		if read.Page == nil || !read.Page.IsLast.Set || read.Page.IsLast.Null || !read.Page.Values.Set || read.Page.Values.Null {
			return finishSnapshot(s), protocolError("board page lacks completion or values")
		}
		rows := read.Page.Values.Value
		pageIDs := map[int64]bool{}
		for _, b := range rows {
			if !b.ID.Set || b.ID.Null || b.ID.Value <= 0 || !b.Name.Set || b.Name.Null || strings.TrimSpace(b.Name.Value) == "" || seen[b.ID.Value] || pageIDs[b.ID.Value] {
				return finishSnapshot(s), protocolError("board walk repeats or lacks an identity")
			}
			pageIDs[b.ID.Value] = true
		}
		if len(rows) == 0 && !read.Page.IsLast.Value {
			return finishSnapshot(s), protocolError("board walk makes no progress")
		}
		if len(rows) > math.MaxInt-start {
			return finishSnapshot(s), protocolError("board offset overflow")
		}
		start += len(rows)
		s.PagesFetched++
		for _, b := range rows {
			if !unbounded && len(s.Boards) >= resultCap {
				break
			}
			seen[b.ID.Value] = true
			s.Boards = append(s.Boards, envelope.BoardRow{ID: b.ID.Value, Name: strings.TrimSpace(b.Name.Value), Type: b.Type, ProjectKeys: []string{}})
		}
		if read.Page.IsLast.Value && len(s.Boards) == start {
			s.Complete = true
			s.NextStart = nil
			break
		}
		if !unbounded && len(s.Boards) >= resultCap {
			s.Truncated = true
			s.Reason = "max_results"
			s.NextStart = new(len(s.Boards))
			break
		}
		if !unbounded && s.PagesFetched >= pageCap {
			s.Truncated = true
			s.Reason = "max_pages"
			s.NextStart = new(start)
			break
		}
	}
	for i := range s.Boards {
		row := &s.Boards[i]
		keys, next, err := projectKeys(ctx, client, row.ID, maxPages, maxBoards)
		row.ProjectKeys = keys
		row.ProjectsNextStart = next
		row.ProjectsComplete = err == nil
		if err == nil {
			continue
		}
		classification := cli.MapError(err)
		s.Failures = append(s.Failures, envelope.BoardProjectFailure{BoardID: row.ID, Error: envelope.Error{Code: classification.Code, Message: classification.Message, Hint: classification.Hint}})
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return finishSnapshot(s), err
		}
	}
	return finishSnapshot(s), nil
}

func finishSnapshot(s snapshot) snapshot { s.FetchedAt = time.Now().UTC(); return s }
func protocolError(message string) error {
	return cli.NewCodedError(cli.CodeBoardResponseInvalid, message)
}

func projectKeys(ctx context.Context, client core.JiraClient, id int64, pageCap, resultCap int) (keys []string, next *int, err error) {
	keys = []string{}
	defer func() { xslices.SortNatural(keys); keys = slices.Compact(keys) }()
	seen := map[string]bool{}
	start := 0
	for pages := 0; pages < pageCap; pages++ {
		read, err := client.GetBoardProjects(ctx, id, start, pageSize)
		if err != nil {
			return keys, new(start), err
		}
		if read.Page == nil || !read.Page.IsLast.Set || read.Page.IsLast.Null || !read.Page.Values.Set || read.Page.Values.Null {
			return keys, new(start), protocolError("project page lacks completion or values")
		}
		rows := read.Page.Values.Value
		pageIDs := map[string]bool{}
		for _, p := range rows {
			if !p.ID.Set || p.ID.Null || p.ID.Value == "" || !p.Key.Set || p.Key.Null || !safeProjectKey(p.Key.Value) || seen[p.ID.Value] || pageIDs[p.ID.Value] {
				return keys, new(start), protocolError("project walk repeats an identity or contains an unsafe key")
			}
			pageIDs[p.ID.Value] = true
		}
		if len(rows) == 0 && !read.Page.IsLast.Value {
			return keys, new(start), protocolError("project walk makes no progress")
		}
		if len(rows) > math.MaxInt-start {
			return keys, new(start), protocolError("project offset overflow")
		}
		for _, p := range rows {
			seen[p.ID.Value] = true
			keys = append(keys, p.Key.Value)
		}
		start += len(rows)
		if len(keys) > resultCap {
			return keys[:resultCap], new(resultCap), protocolError("associated projects exceeded the key bound")
		}
		if read.Page.IsLast.Value {
			return keys, nil, nil
		}
		if len(keys) >= resultCap {
			return keys, new(start), protocolError("associated projects reached the key bound")
		}
	}
	return keys, new(start), protocolError("associated projects reached the page bound")
}

// Keys are interpolated into JQL by downstream workflows; preserve the
// baseline's refusal of whitespace and JQL metacharacters, without rewriting.
func safeProjectKey(key string) bool {
	if key == "" {
		return false
	}
	return !strings.ContainsAny(key, " \t\n\r,()'\"")
}

func listWarnings(out envelope.BoardsListOutput) []envelope.Warning {
	var warnings []envelope.Warning
	if out.Truncated {
		warnings = append(warnings, envelope.Warning{Type: "cache_truncated", Message: "Board snapshot is incomplete (" + out.TruncatedReason + "). Retry with --refresh; use --unbounded to remove board fetch caps.", Lossy: true})
	}
	if len(out.ProjectFailures) > 0 {
		warnings = append(warnings, envelope.Warning{Type: "board_projects_incomplete", Message: fmt.Sprintf("Associated project keys are incomplete for %d boards; inspect project_failures and retry with --refresh.", len(out.ProjectFailures)), Lossy: true})
	}
	return warnings
}

func renderList(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.BoardsListOutput)
	if !ok {
		return fmt.Errorf("boards: expected BoardsListOutput, got %T", env.Data)
	}
	source := "site"
	if out.FromCache {
		source = "cache"
	}
	v.Line(cli.Text(fmt.Sprintf("Showing %d of %d fetched boards (%s).", out.Count, out.Pagination.Total, source)))
	if len(out.Boards) > 0 {
		output.Table(v, cli.ResolvedTheme(), []table.Column[envelope.BoardRow]{
			{Name: "id", Header: "ID", Render: func(b envelope.BoardRow, _ *table.RenderContext) table.Cell {
				return table.TextCell(strconv.FormatInt(b.ID, 10))
			}},
			{Name: "name", Header: "NAME", Flex: true, Render: func(b envelope.BoardRow, _ *table.RenderContext) table.Cell {
				return table.StyledCell(cli.Entity(b.Name), cli.Text(b.Name))
			}},
			{Name: "type", Header: "TYPE", Render: func(b envelope.BoardRow, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.OrDash(cli.Text(b.Type.Value)))
			}},
			{Name: "project_keys", Header: "PROJECTS", Render: func(b envelope.BoardRow, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.OrDash(cli.Text(strings.Join(b.ProjectKeys, ", "))))
			}},
		}, out.Boards)
	}
	if !out.Pagination.IsLast {
		v.Hint("Use --all to return the whole fetched snapshot.")
	}
	if out.Truncated {
		v.Hint("Board snapshot is incomplete. Inspect next_start and truncated_reason; retry with --refresh.")
	}
	if len(out.ProjectFailures) > 0 {
		v.Hint("Some project keys are incomplete. Inspect project_failures in --output=json.")
	}
	return nil
}
