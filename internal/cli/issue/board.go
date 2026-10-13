package issue

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/boardscope"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// Board flag names, spelled once so registration and every refusal name
// the same strings.
const (
	flagBoard   = "board"
	flagBoardID = "board-id"
)

// boardPageSize bounds the name lookup's single page.
//
// Fifty is enough that a real site's boards matching one name phrase fit
// in it, and small enough that the refusal it feeds — which LISTS the
// candidates — stays readable. When more exist the response says so
// (isLast), and resolveBoardName refuses rather than picking from a view
// it knows is partial.
const boardPageSize = 50

// boardOptions is how a caller asks for a board.
type boardOptions struct {
	// name and id are the two spellings; at most one is ever set, which
	// validate enforces before this is read.
	//
	// id is an int because that is what the flag registrar binds
	// (there is no int64 helper, and a board id fits an int on every
	// platform this ships to); it widens at the seam, which is where the
	// spec's int64 lives.
	name string
	id   int
	cacheOptions
}

// given reports whether the caller named a board at all.
func (o boardOptions) given() bool { return o.name != "" || o.id > 0 }

// validate refuses the board flag combinations that cannot be honored,
// before any I/O.
//
// The two spellings are alternatives, not layers: naming a board twice
// in one invocation is the same ambiguity a selector beside another
// selector is, and there is no merge rule worth inventing — a caller who
// wrote both has a wrong belief about which one runs.
//
// A negative id is refused rather than silently ignored: it cannot
// identify a board, and treating it as "no board given" would run an
// unscoped list for a caller who plainly meant to scope one.
// The --jql refusal lives HERE rather than in command.ComposeJQL, where it
// started, because it depends only on which flags were typed — never on
// the board's own query. command.ComposeJQL runs twice: once in Validate with
// BoardJQL empty, and once in Run after the board has resolved, so a
// rule keyed on BoardJQL being non-empty could only fire on the second
// pass. That made a flags-only mistake cost three round trips before it
// was refused, which is exactly what Validate's own comment promises
// will not happen.
func (o boardOptions) validate(jql string) error {
	if o.given() && strings.TrimSpace(jql) != "" {
		e := cli.NewInputError(cli.CodeFlagNotApplicable, fmt.Sprintf(
			"--%s cannot be combined with --jql: a board is already a complete query and so is yours, so one of them would have to be ignored — fold the board's own query into your --jql if you need both", flagBoard))
		e.Field = flagBoard
		return e
	}
	if o.name != "" && o.id != 0 {
		e := cli.NewInputError(cli.CodeFlagConflict, fmt.Sprintf(
			"--%s and --%s both name a board, and they are alternatives rather than layers — pick the one you meant",
			flagBoard, flagBoardID,
		))
		e.Field = flagBoard
		return e
	}
	if o.id < 0 {
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"--%s must be a positive board id, got %d", flagBoardID, o.id))
		e.Field = flagBoardID
		return e
	}
	return nil
}

// resolveBoard turns --board or --board-id into the board's identity and
// query, from cache when one is fresh and from the site otherwise.
//
// **Three round trips on a cold cache** — boards, configuration, filter
// — which is exactly why the result is cached rather than re-derived per
// invocation. `issue list --board Sprint` is a command somebody runs
// twenty times an afternoon.
func resolveBoard(
	ctx context.Context,
	client core.JiraClient,
	cache core.Cache,
	site string,
	opts boardOptions,
) (cachedRead[boardscope.Resolution], error) {
	kind, selector := "name", opts.name
	if opts.id > 0 {
		kind, selector = "id", strconv.Itoa(opts.id)
	}

	return readCached(ctx, cache, boardscope.CacheKey(site, kind, selector), opts.cacheOptions,
		// A resolution with no query is useless and would compose into a
		// query matching the whole site. It is indistinguishable from a bad
		// decode for the caller's purposes, so it is a miss.
		func(b boardscope.Resolution) bool { return b.JQL != "" && b.ID > 0 },
		func(ctx context.Context) (boardscope.Resolution, error) {
			return fetchBoard(ctx, client, opts)
		})
}

// fetchBoard resolves a board against the live site.
func fetchBoard(ctx context.Context, client core.JiraClient, opts boardOptions) (boardscope.Resolution, error) {
	id, name := int64(opts.id), ""
	if id == 0 {
		resolved, err := resolveBoardName(ctx, client, opts.name)
		if err != nil {
			return boardscope.Resolution{}, err
		}
		id, name = resolved.ID.Value, resolved.Name.Value
	}

	jql, cfgName, err := resolveBoardJQL(ctx, client, id)
	if err != nil {
		return boardscope.Resolution{}, err
	}
	if name == "" {
		// --board-id skipped the name lookup, so the configuration is where
		// the name comes from. Publishing it is what lets the output name
		// the board a caller identified only by number.
		name = cfgName
	}
	return boardscope.Resolution{ID: id, Name: name, JQL: jql}, nil
}

// resolveBoardName turns a board NAME into exactly one board, or
// refuses.
//
// **Names are allowed here where `--filter` refused them, and the
// difference is real rather than a change of mind.** `--filter` takes an
// id only because a filter name cannot be disambiguated by the caller
// afterwards: there is no `--filter-id` sibling to fall back to, and the
// id is already sitting in the filter's own URL, so the name bought
// nothing and risked acting on the wrong query. A board is the opposite
// on both counts. `--board-id` exists as the precise escape, and a board
// is a thing people know by NAME and almost never by number — nobody
// says "let me check board 42".
//
// So the name is accepted, and the ambiguity it can carry is made
// ACTIONABLE instead of fatal: every candidate is listed with its id, so
// the refusal hands the caller the exact `--board-id` to use next. That
// is the difference the selector rules actually care about — not whether
// a value can be ambiguous, but whether the caller can resolve the
// ambiguity themselves.
//
// **The endpoint's name filter is a PARTIAL match**, in its own words,
// so "Sprint" can return "Sprint Board" and "Old Sprint Board" alike. An
// EXACT (case-insensitive) match therefore wins outright when there is
// exactly one on a final page. A full name should not be refused merely
// because it is a substring of another, but unread pages could carry another
// board with that exact name.
func resolveBoardName(ctx context.Context, client core.JiraClient, name string) (jira.Board, error) {
	read, err := command.Spin(ctx, "Finding board "+name, func(ctx context.Context) (core.BoardRead, error) {
		return client.GetBoards(ctx, name, 0, boardPageSize)
	})
	if err != nil {
		return jira.Board{}, fmt.Errorf("resolving --%s: %w", flagBoard, err)
	}
	if read.Page == nil {
		// GetBoards' contract is a non-nil Page alongside a nil error.
		// Failing loudly rather than trusting it matters here because a nil
		// page would otherwise read as "no board of that name", which is a
		// refusal the caller would go and act on.
		return jira.Board{}, fmt.Errorf("resolving --%s: empty board response", flagBoard)
	}

	boards := read.Page.Values.Value
	if len(boards) == 0 {
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"no board matches --%s %q — the name is matched loosely, so a board whose name merely CONTAINS this would have been found too; check the spelling, or pass --%s if you know the number",
			flagBoard, name, flagBoardID,
		))
		e.Field = flagBoard
		return jira.Board{}, e
	}

	exact := make([]jira.Board, 0, 1)
	for _, b := range boards {
		if strings.EqualFold(strings.TrimSpace(b.Name.Value), strings.TrimSpace(name)) {
			exact = append(exact, b)
		}
	}
	// Zero exact matches leaves the partial ones; several exact matches is
	// a site with two boards of the same name, which Jira permits and
	// which only an id can tell apart. Both are decided the same way
	// below: one candidate is an answer, more than one is a refusal.
	candidates := exact
	if len(candidates) == 0 {
		candidates = boards
	}
	if len(candidates) == 1 && read.Page.IsLast.Set && !read.Page.IsLast.Null && read.Page.IsLast.Value {
		return candidates[0], nil
	}
	if len(candidates) == 1 {
		// One candidate on a page that is not the last: there may be a
		// better match this lookup never saw, so picking would be a guess
		// dressed as an answer.
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"--%s %q matched more boards than this lookup could read, so the one match on the first page may not be the one you mean — pass --%s %d if it is",
			flagBoard, name, flagBoardID, candidates[0].ID.Value,
		))
		e.Field = flagBoard
		return jira.Board{}, e
	}

	e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
		"--%s %q matches %d boards, and the CLI will not guess which — pass --%s with one of: %s",
		flagBoard, name, len(candidates), flagBoardID, describeBoards(candidates),
	))
	e.Field = flagBoard
	return jira.Board{}, e
}

// describeBoards renders candidates for a refusal: the id a caller
// should pass, the name, and the type — which is often the only thing
// distinguishing two boards that share a name.
func describeBoards(boards []jira.Board) string {
	parts := make([]string, 0, len(boards))
	for _, b := range boards {
		part := fmt.Sprintf("%d (%s", b.ID.Value, b.Name.Value)
		if b.Type.Value != "" {
			part += ", " + b.Type.Value
		}
		parts = append(parts, part+")")
	}
	return strings.Join(parts, "; ")
}

// resolveBoardJQL reads a board's configuration and then the filter
// behind it, returning the board's query and its name.
//
// **The subQuery is deliberately not applied.** A Kanban board carries
// one — typically `resolution = EMPTY` — which Jira ANDs with the filter
// to hide finished work from the board's columns. Applying it here would
// make `issue list --board X` disagree with `issue list --board X
// --status Done`, silently returning nothing for a query that reads as
// perfectly sensible; and it would make the board flag mean "the board's
// visible columns" rather than "the board's issues", which is not what
// the other filters compose against. The board's own scope is its
// filter; the sub-query is a display rule, and a caller who wants it can
// add `--status`.
func resolveBoardJQL(ctx context.Context, client core.JiraClient, id int64) (jql, name string, err error) {
	cfg, err := command.Spin(ctx, "Reading board "+strconv.FormatInt(id, 10), func(ctx context.Context) (core.BoardConfigurationRead, error) {
		return client.GetBoardConfiguration(ctx, id)
	})
	if err != nil {
		return "", "", fmt.Errorf("reading board %d: %w", id, err)
	}
	if cfg.Configuration == nil {
		return "", "", fmt.Errorf("reading board %d: the site returned no configuration", id)
	}
	filterID := strings.TrimSpace(cfg.Configuration.Filter.ID)
	if filterID == "" {
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"board %d has no filter behind it, so there is no query to list from", id))
		e.Field = flagBoard
		return "", "", e
	}

	read, err := command.Spin(ctx, "Reading the board's filter", func(ctx context.Context) (core.FilterRead, error) {
		return client.GetFilter(ctx, filterID)
	})
	if err != nil {
		// A board can be visible while its filter is not shared with the
		// caller, which is a real and confusing state: the board renders in
		// the UI and the CLI cannot read what it contains. Saying so beats
		// passing a bare filter error up, which would name a filter id the
		// caller never typed.
		return "", "", fmt.Errorf("reading the filter behind board %d (a board's filter must be shared with you to be readable): %w", id, err)
	}
	if read.Filter == nil {
		return "", "", fmt.Errorf("reading the filter behind board %d: the site returned no filter", id)
	}
	query := strings.TrimSpace(read.Filter.Jql.Value)
	if query == "" {
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"board %d's filter holds no query, and an empty query matches every issue on the site rather than the board's — refusing rather than listing all of them", id))
		e.Field = flagBoard
		return "", "", e
	}
	return query, cfg.Configuration.Name, nil
}

// listBoardResolution is what `issue list` needs back from a board
// lookup: the query to run, and the block to publish.
type listBoardResolution struct {
	jql  string
	view *envelope.IssueBoardView
}

// resolveListBoard resolves the board and re-composes the query around
// it.
//
// **The second composition is the authoritative one**, and the first
// (in Validate) is not wasted: it is what refuses a bad filter value or
// a --jql collision before a caller pays for three round trips. This one
// can only run after the site has answered, because the board's query is
// the one input Validate cannot know.
func resolveListBoard(
	ctx context.Context,
	deps Deps,
	opts boardOptions,
	in *command.JQLInput,
) (listBoardResolution, error) {
	client, err := deps.JiraClient(ctx)
	if err != nil {
		return listBoardResolution{}, err
	}
	read, err := resolveBoard(ctx, client, issueCache(ctx, deps), client.BaseURL(), opts)
	if err != nil {
		return listBoardResolution{}, err
	}

	in.BoardJQL = read.value.JQL
	jql, err := command.ComposeJQL(*in)
	if err != nil {
		return listBoardResolution{}, err
	}
	return listBoardResolution{
		jql: jql,
		view: &envelope.IssueBoardView{
			ID:   read.value.ID,
			Name: read.value.Name,
			JQL:  read.value.JQL,
		},
	}, nil
}
