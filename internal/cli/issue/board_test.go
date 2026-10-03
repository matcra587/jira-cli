package issue

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

// A board is three round trips away — board, configuration, filter — and
// the interesting decisions are all about what happens when the first of
// those is ambiguous, plus what the resulting query composes into.

// memoryCache is a KEYED cache double. The link-type tests' agedCache is
// single-slot and ignores the key entirely, which is fine for one list
// per site but cannot show what these tests need to: that two sites, or
// two boards, do not collide.
type memoryCache struct {
	mu      sync.Mutex
	entries map[string]memoryEntry
}

type memoryEntry struct {
	value   []byte
	written time.Time
}

func newMemoryCache() *memoryCache {
	return &memoryCache{entries: map[string]memoryEntry{}}
}

func (c *memoryCache) Get(key string) ([]byte, time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	return e.value, e.written, ok
}

func (c *memoryCache) Set(key string, value []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = memoryEntry{value: value, written: time.Now()}
	return nil
}

func (c *memoryCache) Forget(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
	return nil
}

// fakeBoardClient serves the three reads a board resolution makes.
type fakeBoardClient struct {
	unusedJiraClient

	boards []jira.Board
	isLast bool
	// filterID is what the configuration points at, and jql what that
	// filter holds.
	filterID string
	jql      string
	// calls records which seams ran, so a cache hit can be told from a
	// cold read.
	calls []string
}

func (f *fakeBoardClient) GetBoards(_ context.Context, name string, _, _ int) (core.BoardRead, error) {
	f.calls = append(f.calls, "boards:"+name)
	return core.BoardRead{Page: &jira.BoardPage{Values: jira.NullableOf(f.boards), IsLast: jira.NullableOf(f.isLast), Total: jira.NullableOf(len(f.boards)), StartAt: jira.NullableOf(0), MaxResults: jira.NullableOf(boardPageSize)}}, nil
}

func (f *fakeBoardClient) GetBoardConfiguration(_ context.Context, id int64) (core.BoardConfigurationRead, error) {
	f.calls = append(f.calls, "config")
	return core.BoardConfigurationRead{Configuration: &jira.BoardConfiguration{
		ID:     id,
		Name:   "Platform Sprint",
		Filter: jira.BoardFilter{ID: f.filterID},
	}}, nil
}

func (f *fakeBoardClient) GetFilter(_ context.Context, id string) (core.FilterRead, error) {
	f.calls = append(f.calls, "filter:"+id)
	return core.FilterRead{Filter: &jira.Filter{Jql: jira.NullableOf(f.jql)}}, nil
}

func boardFixture() *fakeBoardClient {
	return &fakeBoardClient{
		boards:   []jira.Board{{ID: jira.NullableOf(int64(42)), Name: jira.NullableOf("Platform Sprint"), Type: jira.NullableOf("scrum")}},
		isLast:   true,
		filterID: "10042",
		jql:      "project = KEY ORDER BY Rank ASC",
	}
}

// TestBoardResolvesThroughFilterToItsQuery pins the whole chain, which
// is the part a reader cannot guess: a board is not an endpoint that
// returns issues, it is a configuration pointing at a saved filter, and
// the filter's query is what "the issues on this board" means.
func TestBoardResolvesThroughFilterToItsQuery(t *testing.T) {
	fake := boardFixture()
	read, err := resolveBoard(t.Context(), fake, noCache{}, "site", boardOptions{name: "Platform Sprint"})
	if err != nil {
		t.Fatalf("resolveBoard: %v", err)
	}
	if read.value.ID != 42 || read.value.Name != "Platform Sprint" {
		t.Errorf("resolved %+v, want board 42 Platform Sprint", read.value)
	}
	if read.value.JQL != "project = KEY ORDER BY Rank ASC" {
		t.Errorf("jql = %q, want the filter's own query verbatim", read.value.JQL)
	}
	want := []string{"boards:Platform Sprint", "config", "filter:10042"}
	if strings.Join(fake.calls, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %v, want %v", fake.calls, want)
	}
}

// TestBoardIDSkipsTheNameLookup. --board-id is the unambiguous route and
// must also be the cheaper one; spending a name lookup on a caller who
// already gave the number would be a round trip bought for nothing.
func TestBoardIDSkipsTheNameLookup(t *testing.T) {
	fake := boardFixture()
	read, err := resolveBoard(t.Context(), fake, noCache{}, "site", boardOptions{id: 42})
	if err != nil {
		t.Fatalf("resolveBoard: %v", err)
	}
	for _, c := range fake.calls {
		if strings.HasPrefix(c, "boards:") {
			t.Error("--board-id still performed a name lookup")
		}
	}
	if read.value.Name != "Platform Sprint" {
		t.Errorf("name = %q, want the configuration's name so the output can name the board", read.value.Name)
	}
}

// TestAmbiguousBoardNameIsRefusedWithCandidates is the decision that
// differs from --filter's, and the reason it can differ: the refusal
// hands back the exact --board-id to use next, so the ambiguity is
// actionable rather than fatal.
func TestAmbiguousBoardNameIsRefusedWithCandidates(t *testing.T) {
	fake := boardFixture()
	fake.boards = []jira.Board{
		{ID: jira.NullableOf(int64(42)), Name: jira.NullableOf("Sprint Board"), Type: jira.NullableOf("scrum")},
		{ID: jira.NullableOf(int64(77)), Name: jira.NullableOf("Old Sprint Board"), Type: jira.NullableOf("kanban")},
	}
	_, err := resolveBoard(t.Context(), fake, noCache{}, "site", boardOptions{name: "Sprint"})
	if err == nil {
		t.Fatal("an ambiguous board name resolved; the CLI must not guess which board to list")
	}
	var input *cli.InputError
	if !errors.As(err, &input) {
		t.Fatalf("refusal is %T, want a typed *cli.InputError", err)
	}
	if input.Field != flagBoard {
		t.Errorf("field = %q, want %q", input.Field, flagBoard)
	}
	for _, want := range []string{"42", "77", "Sprint Board", "Old Sprint Board", "--" + flagBoardID} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not mention %q — a caller must be able to act on it without a second command", err, want)
		}
	}
}

// TestExactNameWinsOverAPartialMatch. Jira matches board names LOOSELY,
// so a board whose full name the caller typed would otherwise be refused
// merely for being a substring of another board's name — which would
// make the flag unusable on any site with a "Sprint" and a "Sprint
// Archive".
func TestExactNameWinsOverAPartialMatch(t *testing.T) {
	fake := boardFixture()
	fake.boards = []jira.Board{
		{ID: jira.NullableOf(int64(42)), Name: jira.NullableOf("Sprint"), Type: jira.NullableOf("scrum")},
		{ID: jira.NullableOf(int64(77)), Name: jira.NullableOf("Sprint Archive"), Type: jira.NullableOf("kanban")},
	}
	read, err := resolveBoard(t.Context(), fake, noCache{}, "site", boardOptions{name: "Sprint"})
	if err != nil {
		t.Fatalf("an exact name match was refused: %v", err)
	}
	if read.value.ID != 42 {
		t.Errorf("resolved board %d, want 42 — the exact match", read.value.ID)
	}
}

// TestOneMatchOnAPartialPageIsRefused. A single candidate is only an
// answer when the lookup saw everything; on a truncated page a better
// match may exist that this request never read, and picking would be a
// guess dressed as an answer.
func TestOneMatchOnAPartialPageIsRefused(t *testing.T) {
	fake := boardFixture()
	fake.boards = []jira.Board{{ID: jira.NullableOf(int64(42)), Name: jira.NullableOf("Sprint Board"), Type: jira.NullableOf("scrum")}}
	fake.isLast = false

	_, err := resolveBoard(t.Context(), fake, noCache{}, "site", boardOptions{name: "Sprint"})
	if err == nil {
		t.Fatal("a single match on a partial page was accepted; there may be a better one the lookup never saw")
	}
	if !strings.Contains(err.Error(), "--"+flagBoardID+" 42") {
		t.Errorf("refusal %q does not offer the candidate's own id as the way forward", err)
	}
}

// A name can be exact and still duplicated on an unread page. Only an ID can
// resolve the ambiguity without fetching the rest of this bounded lookup.
func TestExactMatchOnNonFinalBoardPageIsRefused(t *testing.T) {
	fake := boardFixture()
	fake.isLast = false
	_, err := resolveBoard(t.Context(), fake, noCache{}, "site", boardOptions{name: "Platform Sprint"})
	if err == nil || cli.MapError(err).Code != cli.CodeFlagValueInvalid {
		t.Fatalf("exact match on an incomplete page selected a board: %v", err)
	}
	if !strings.Contains(err.Error(), "--board-id 42") {
		t.Fatalf("refusal does not provide the explicit candidate identity: %v", err)
	}
}

func TestBoardLookupDoesNotTrustOlderAmbiguousCache(t *testing.T) {
	cache := newMemoryCache()
	if err := cache.Set("v1:board:site:name:Platform Sprint", []byte(`{"id":42,"name":"Platform Sprint","jql":"project = KEY"}`)); err != nil {
		t.Fatal(err)
	}
	fake := boardFixture()
	fake.isLast = false
	_, err := resolveBoard(t.Context(), fake, cache, "site", boardOptions{name: "Platform Sprint"})
	if err == nil || cli.MapError(err).Code != cli.CodeFlagValueInvalid {
		t.Fatalf("cached pre-validation selection bypassed current uniqueness check: %v", err)
	}
}

// TestBoardAndBoardIDTogetherAreRefused: two ways of naming one board is
// the ambiguity every selector rule in this tree refuses.
func TestBoardAndBoardIDTogetherAreRefused(t *testing.T) {
	err := boardOptions{name: "Sprint", id: 42}.validate("")
	if err == nil {
		t.Fatal("--board beside --board-id was accepted")
	}
	if got := cli.MapError(err).Code; got != cli.CodeFlagConflict {
		t.Errorf("code = %q, want %q", got, cli.CodeFlagConflict)
	}
}

// TestBoardComposesWithFiltersAndRefusesJQL is the composition ruling.
// A board is a SCOPE the convenience filters narrow; an explicit --jql
// is a second complete query and cannot be merged with one.
func TestBoardComposesWithFiltersAndRefusesJQL(t *testing.T) {
	t.Run("narrowed by a filter", func(t *testing.T) {
		got, err := command.ComposeJQL(command.JQLInput{
			BoardJQL: "project = KEY OR project = OTHER ORDER BY Rank ASC",
			Status:   []string{"Done"},
		})
		if err != nil {
			t.Fatalf("command.ComposeJQL: %v", err)
		}
		// The parentheses are the whole point: without them the OR binds
		// looser than the AND and every issue in KEY comes back whatever
		// its status.
		want := `(project = KEY OR project = OTHER) AND status in ("Done") ORDER BY Rank ASC`
		if got != want {
			t.Errorf("composed %q, want %q", got, want)
		}
	})

	t.Run("board ordering yields to --order-by", func(t *testing.T) {
		got, err := command.ComposeJQL(command.JQLInput{
			BoardJQL: "project = KEY ORDER BY Rank ASC",
			OrderBy:  "created",
		})
		if err != nil {
			t.Fatalf("command.ComposeJQL: %v", err)
		}
		if !strings.HasSuffix(got, "ORDER BY created DESC") {
			t.Errorf("composed %q, want it to end with the caller's own ordering", got)
		}
		if strings.Contains(got, "Rank") {
			t.Errorf("composed %q still carries the board's ordering alongside the caller's", got)
		}
	})

	t.Run("refused beside --jql", func(t *testing.T) {
		err := boardOptions{name: "Sprint"}.validate("project = OTHER")
		if err == nil {
			t.Fatal("--board beside --jql was accepted; both are complete queries")
		}
		if got := cli.MapError(err).Code; got != cli.CodeFlagNotApplicable {
			t.Errorf("code = %q, want %q", got, cli.CodeFlagNotApplicable)
		}
	})
}

// TestSplitOrderByIgnoresLiteralsAndParens is the fiddly half of
// composition. A saved filter's query routinely contains both, and a
// naive substring search would cut one in half — silently turning a
// filter on the words "order by" into a broken query.
func TestSplitOrderByIgnoresLiteralsAndParens(t *testing.T) {
	for _, tt := range []struct {
		name             string
		query            string
		conditions, want string
	}{
		{
			name:       "plain",
			query:      "project = KEY ORDER BY Rank ASC",
			conditions: "project = KEY",
			want:       "ORDER BY Rank ASC",
		},
		{
			name:       "none at all",
			query:      "project = KEY",
			conditions: "project = KEY",
			want:       "",
		},
		{
			name:       "inside a string literal",
			query:      `summary ~ "order by priority"`,
			conditions: `summary ~ "order by priority"`,
			want:       "",
		},
		{
			name:       "literal plus a real one",
			query:      `summary ~ "order by priority" ORDER BY created DESC`,
			conditions: `summary ~ "order by priority"`,
			want:       "ORDER BY created DESC",
		},
		{
			name:       "not a word boundary",
			query:      "summary ~ reorder AND status = byte",
			conditions: "summary ~ reorder AND status = byte",
			want:       "",
		},
		{
			name:       "lowercase, as a filter may store it",
			query:      "project = KEY order by rank",
			conditions: "project = KEY",
			want:       "order by rank",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			conditions, order := command.SplitJQLOrderBy(tt.query)
			if conditions != tt.conditions {
				t.Errorf("conditions = %q, want %q", conditions, tt.conditions)
			}
			if order != tt.want {
				t.Errorf("order = %q, want %q", order, tt.want)
			}
		})
	}
}

// TestBoardResolutionIsCached. Three round trips per invocation for
// configuration that changes monthly is the cost this cache exists to
// remove, and `issue list --board X` is run many times an afternoon.
func TestBoardResolutionIsCached(t *testing.T) {
	fake := boardFixture()
	cache := newMemoryCache()

	first, err := resolveBoard(t.Context(), fake, cache, "site", boardOptions{name: "Platform Sprint"})
	if err != nil {
		t.Fatalf("first resolveBoard: %v", err)
	}
	if first.cached {
		t.Error("the first read reported itself cached")
	}
	calls := len(fake.calls)

	second, err := resolveBoard(t.Context(), fake, cache, "site", boardOptions{name: "Platform Sprint"})
	if err != nil {
		t.Fatalf("second resolveBoard: %v", err)
	}
	if !second.cached {
		t.Error("the second read went to the site; the resolution should have come off the cache")
	}
	if len(fake.calls) != calls {
		t.Errorf("calls grew from %d to %d; a cache hit must make no requests", calls, len(fake.calls))
	}
	if second.value.JQL != first.value.JQL {
		t.Errorf("cached jql = %q, want %q", second.value.JQL, first.value.JQL)
	}
}

// TestRefreshBypassesTheBoardCache. A cache whose staleness cannot be
// busted is worse than no cache: a board re-pointed at a new filter this
// morning would otherwise list yesterday's issues until tomorrow.
func TestRefreshBypassesTheBoardCache(t *testing.T) {
	fake := boardFixture()
	cache := newMemoryCache()

	if _, err := resolveBoard(t.Context(), fake, cache, "site", boardOptions{name: "Platform Sprint"}); err != nil {
		t.Fatalf("resolveBoard: %v", err)
	}
	calls := len(fake.calls)

	read, err := resolveBoard(t.Context(), fake, cache, "site",
		boardOptions{name: "Platform Sprint", cacheOptions: cacheOptions{refresh: true}})
	if err != nil {
		t.Fatalf("resolveBoard --refresh: %v", err)
	}
	if read.cached {
		t.Error("--refresh served a cached resolution")
	}
	if len(fake.calls) == calls {
		t.Error("--refresh made no requests; it must re-read")
	}
}

// TestBoardCacheIsKeyedBySite. Boards are per-tenant, so a key without
// the site would serve one tenant's board to another — and the failure
// would look like Jira having forgotten a board rather than a cache bug.
func TestBoardCacheIsKeyedBySite(t *testing.T) {
	fake := boardFixture()
	cache := newMemoryCache()

	if _, err := resolveBoard(t.Context(), fake, cache, "site-a", boardOptions{name: "Platform Sprint"}); err != nil {
		t.Fatalf("resolveBoard: %v", err)
	}
	read, err := resolveBoard(t.Context(), fake, cache, "site-b", boardOptions{name: "Platform Sprint"})
	if err != nil {
		t.Fatalf("resolveBoard on a second site: %v", err)
	}
	if read.cached {
		t.Error("a second site was served the first site's cached board")
	}
}
