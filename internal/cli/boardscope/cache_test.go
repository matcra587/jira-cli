package boardscope

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/adapters"
	"github.com/matcra587/jira-cli/internal/core"
)

type snapshotCache struct {
	core.Cache
	raw       []byte
	written   time.Time
	key       string
	hit       bool
	mutations int
}

func (c *snapshotCache) Get(key string) ([]byte, time.Time, bool) {
	c.key = key
	return c.raw, c.written, c.hit
}

func (c *snapshotCache) Set(string, []byte) error {
	c.mutations++
	return errors.New("offline lookup must never save")
}

func (c *snapshotCache) Forget(string) error {
	c.mutations++
	return errors.New("offline lookup must never forget")
}

func TestCachedBoardRetainsStaleSnapshotAndExactSelectorNamespace(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		id   int
		key  string
	}{
		{" sprint ", 0, "v2:board:https://example.atlassian.net:name: sprint "},
		{"", 42, "v2:board:https://example.atlassian.net:id:42"},
	} {
		written := time.Now().Add(-30 * 24 * time.Hour)
		store := &snapshotCache{raw: []byte(`{"id":42,"name":"Sprint","jql":"project = KEY OR labels = scope ORDER BY Rank ASC"}`), written: written, hit: true}
		board, stamp, err := ReadCached(store, "https://example.atlassian.net", test.name, test.id)
		if err != nil || board.ID != 42 || board.Name != "Sprint" || board.JQL != "project = KEY OR labels = scope ORDER BY Rank ASC" || stamp != written || store.key != test.key || store.mutations != 0 {
			t.Fatalf("snapshot or selector changed: %+v %v %s %v", board, stamp, store.key, err)
		}
	}
}

func TestCachedBoardRefusesIncompleteOrMismatchedSnapshot(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		raw   string
		stamp time.Time
		hit   bool
		name  string
		id    int
	}{
		{`{"id":42,"name":"Sprint","jql":"project = KEY"}`, time.Now(), false, "", 42},
		{`null`, time.Now(), true, "", 42},
		{`{}`, time.Now(), true, "", 42},
		{`{"id":42,"jql":"project = KEY"} {}`, time.Now(), true, "", 42},
		{`{"id":42,"jql":"project = KEY"}`, time.Time{}, true, "", 42},
		{`{"id":42,"jql":"project = KEY"}`, time.Now().Add(time.Hour), true, "", 42},
		{`{"id":0,"jql":"project = KEY"}`, time.Now(), true, "", 42},
		{`{"id":42,"jql":""}`, time.Now(), true, "", 42},
		{`{"id":42,"jql":" \t "}`, time.Now(), true, "", 42},
		{`{"id":43,"jql":"project = KEY"}`, time.Now(), true, "", 42},
		{`{"id":42,"name":"Different","jql":"project = KEY"}`, time.Now(), true, "Sprint", 0},
	} {
		store := &snapshotCache{raw: []byte(test.raw), written: test.stamp, hit: test.hit}
		if board, stamp, err := ReadCached(store, "https://example.atlassian.net", test.name, test.id); err == nil || board.ID != 0 || !stamp.IsZero() || store.mutations != 0 {
			t.Fatalf("bad snapshot accepted: %+v %v %v", board, stamp, err)
		}
	}
	if _, _, err := ReadCached(nil, "site", "Sprint", 0); err == nil {
		t.Fatal("missing cache was accepted")
	}
}

func TestCachedBoardCannotCrossAccountSiteOrSelector(t *testing.T) {
	t.Parallel()
	disk := &adapters.MemoryCache{}
	first, err := adapters.NewScopedCache(disk, "https://one.example", "one@example.com")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(Resolution{ID: 42, Name: "Sprint", JQL: "project = KEY"})
	if err != nil || first.Set(CacheKey("https://one.example", "id", "42"), raw) != nil {
		t.Fatal("cannot seed scoped cache")
	}
	for _, test := range []struct{ site, account string }{{"https://two.example", "one@example.com"}, {"https://one.example", "two@example.com"}} {
		other, err := adapters.NewScopedCache(disk, test.site, test.account)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := ReadCached(other, test.site, "", 42); err == nil {
			t.Fatal("offline board crossed site/account namespace")
		}
	}
	if _, _, err := ReadCached(first, "https://one.example", "Sprint", 0); err == nil {
		t.Fatal("ID record silently satisfied a name lookup")
	}
	if err := disk.Set(CacheKey("https://two.example", "id", "42"), raw); err != nil {
		t.Fatal(err)
	}
	other, err := adapters.NewScopedCache(disk, "https://two.example", "one@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadCached(other, "https://two.example", "", 42); err == nil {
		t.Fatal("unscoped legacy cache satisfied a local lookup")
	}
}

func TestPublicCachedScopePreservesSiteSpellingAndRefusesCanceledFactories(t *testing.T) {
	t.Parallel()
	preferences := core.IssuePreferences{BaseURL: "https://EXAMPLE.atlassian.net/", Account: "public@example.invalid"}
	store := &snapshotCache{raw: []byte(`{"id":42,"name":"42","jql":"project = KEY ORDER BY Rank ASC"}`), written: time.Now().Add(-time.Hour), hit: true}
	board, _, err := ResolveCached(t.Context(), func(site, account string) (core.Cache, error) {
		if site != preferences.BaseURL || account != preferences.Account {
			t.Fatal("public namespace was changed before scoped cache constructor")
		}
		return store, nil
	}, preferences, "42", 0)
	if err != nil || board.Name != "42" || store.key != "v2:board:https://EXAMPLE.atlassian.net/:name:42" || store.mutations != 0 {
		t.Fatal("site spelling or numeric board name changed scope", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	_, _, err = ResolveCached(ctx, func(string, string) (core.Cache, error) {
		cancel()
		return nil, errors.New("private constructor error")
	}, preferences, "42", 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("factory cancellation became missing board", err)
	}
	calls := 0
	_, _, err = ResolveCached(ctx, func(string, string) (core.Cache, error) { calls++; return store, nil }, preferences, "42", 0)
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("already-canceled lookup entered cache factory", err)
	}
}

func TestDeliveredV2KeysRemainReadableAcrossSiteAndNameSpelling(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	disk, err := adapters.NewDiskCache()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		key, site, name, storedName string
		id                          int
	}{
		{"v2:board:https://EXAMPLE.invalid/:id:42", "https://EXAMPLE.invalid/", "", "Sprint", 42},
		{"v2:board:https://EXAMPLE.invalid/:name:sprint", "https://example.invalid", " Sprint ", "Sprint", 0},
		{"v2:board:https://example.invalid:name:Σ", "https://example.invalid", "ς", "Σ", 0},
		{"v2:board:https://example.invalid/path:name:part:name:42", "https://example.invalid/path:name:part", "42", "42", 0},
	} {
		t.Run(test.key, func(t *testing.T) {
			store, err := adapters.NewScopedCache(disk, "fixed-public-site", test.key)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(Resolution{ID: 42, Name: test.storedName, JQL: "project = KEY ORDER BY Rank ASC"})
			if err != nil || store.Set(test.key, raw) != nil {
				t.Fatal("cannot seed independently spelled delivered key")
			}
			board, stamp, err := readCached(t.Context(), store, test.site, test.name, test.id)
			if err != nil || board.ID != 42 || stamp.IsZero() || board.JQL != "project = KEY ORDER BY Rank ASC" {
				t.Fatal("delivered key or historical case-insensitive name became unreadable", err)
			}
			for _, scope := range []struct{ site, account string }{{"other-public-site", test.key}, {"fixed-public-site", "other-account"}} {
				other, err := adapters.NewScopedCache(disk, scope.site, scope.account)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := readCached(t.Context(), other, test.site, test.name, test.id); err == nil {
					t.Fatal("spelling fallback crossed account/site namespace")
				}
			}
			if _, _, err := readCached(t.Context(), store, "https://foreign.invalid", test.name, test.id); err == nil {
				t.Fatal("spelling fallback accepted a foreign key site")
			}
		})
	}
}

func TestEquivalentCachedNamesRefuseDifferentBoardIdentities(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	disk, err := adapters.NewDiskCache()
	if err != nil {
		t.Fatal(err)
	}
	store, err := adapters.NewScopedCache(disk, "https://example.invalid", "public@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	for key, raw := range map[string]string{
		"v2:board:https://example.invalid:name:sprint":  `{"id":42,"name":"Sprint","jql":"project = FIRST"}`,
		"v2:board:https://example.invalid/:name:SPRINT": `{"id":43,"name":"Sprint","jql":"project = SECOND"}`,
	} {
		if err := store.Set(key, []byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := readCached(t.Context(), store, "https://example.invalid", "Sprint", 0); err == nil {
		t.Fatal("equivalent spelling selected an ambiguous board")
	}
}

type failingInventoryCache struct{ core.Cache }

func (failingInventoryCache) Entries(context.Context) ([]core.CacheEntry, error) {
	return []core.CacheEntry{{Key: "v2:board:https://example.invalid:name:Sprint"}}, errors.New("partial inventory failure")
}

func (failingInventoryCache) DeleteEntry(context.Context, string) (bool, error) {
	panic("offline board lookup cannot remove entries")
}

func TestPublicCachedScopeOnlyMissingPermitsCanonicalNamespaceRetry(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"missing", "malformed", "partial inventory", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			primary := &adapters.MemoryCache{}
			canonical := &adapters.MemoryCache{}
			if err := canonical.Set("v2:board:https://example.invalid:name:Sprint", []byte(`{"id":42,"name":"Sprint","jql":"project = BOARD"}`)); err != nil {
				t.Fatal(err)
			}
			if mode == "partial inventory" {
				if err := primary.Set("v2:board:https://example.invalid:name:Sprint", []byte(`{"id":42,"name":"Sprint","jql":"project = BOARD"}`)); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "malformed" {
				if err := primary.Set("v2:board:https://EXAMPLE.invalid/:name:Sprint", []byte(`{"id":0}`)); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			board, _, err := ResolveCached(ctx, func(site, account string) (core.Cache, error) {
				calls++
				if account != "public@example.invalid" || (calls == 1 && site != "https://EXAMPLE.invalid/") || (calls == 2 && site != "https://example.invalid") {
					t.Fatal("bounded retry changed site/account identity")
				}
				if calls == 2 {
					return canonical, nil
				}
				if mode == "partial inventory" {
					return failingInventoryCache{Cache: primary}, nil
				}
				if mode == "canceled" {
					cancel()
				}
				return primary, nil
			}, core.IssuePreferences{BaseURL: "https://EXAMPLE.invalid/", Account: "public@example.invalid"}, "Sprint", 0)
			if mode == "missing" {
				if err != nil || calls != 2 || board.ID != 42 {
					t.Fatal("missing authored namespace did not retain canonical producer", err)
				}
			} else if err == nil || calls != 1 || board.ID != 0 {
				t.Fatal("failed authored namespace incorrectly fell back", mode, err, calls)
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("bounded namespace retry lost cancellation")
			}
		})
	}
}

func TestPublicCachedScopeRefusesDamagedDiskWrapperBeforeCanonicalRetry(t *testing.T) {
	owned := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", owned)
	disk, err := adapters.NewDiskCache()
	if err != nil {
		t.Fatal(err)
	}
	primary, err := adapters.NewScopedCache(disk, "https://EXAMPLE.invalid/", "public@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"id":42,"name":"Sprint","jql":"project = ORIGINAL"}`)
	if err := primary.Set("v2:board:https://EXAMPLE.invalid/:name:Sprint", raw); err != nil {
		t.Fatal(err)
	}
	entries, err := primary.Entries(t.Context())
	if err != nil || len(entries) != 1 || entries[0].Malformed {
		t.Fatal("owned authored namespace was not seeded", err)
	}
	var paths []string
	if err := filepath.WalkDir(owned, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == entries[0].ID {
			paths = append(paths, path)
		}
		return nil
	}); err != nil || len(paths) != 1 {
		t.Fatal("cannot identify the owned wrapper for corruption", err)
	}
	if err := os.WriteFile(paths[0], []byte(`{"invalid wrapper`), 0o600); err != nil {
		t.Fatal(err)
	}
	canonical, err := adapters.NewScopedCache(disk, "https://example.invalid", "public@example.invalid")
	if err != nil || canonical.Set("v2:board:https://example.invalid:name:Sprint", []byte(`{"id":43,"name":"Sprint","jql":"project = SUBSTITUTE"}`)) != nil {
		t.Fatal("cannot seed the alternative namespace", err)
	}
	calls := 0
	board, _, err := ResolveCached(t.Context(), func(site, account string) (core.Cache, error) {
		calls++
		if account != "public@example.invalid" {
			t.Fatal("damaged wrapper changed account")
		}
		if site == "https://EXAMPLE.invalid/" {
			return primary, nil
		}
		return canonical, nil
	}, core.IssuePreferences{BaseURL: "https://EXAMPLE.invalid/", Account: "public@example.invalid"}, "Sprint", 0)
	if err == nil || board.ID != 0 || calls != 1 {
		t.Fatal("damaged authored wrapper silently substituted canonical scope", err, board, calls)
	}
}
