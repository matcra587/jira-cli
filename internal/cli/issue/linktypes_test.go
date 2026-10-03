package issue

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

// What is pinned here is the CACHE POLICY, which is the part of `issue
// link types` that can be wrong in ways nobody notices: that a fresh
// entry spares the round trip, that a stale one does not, that --refresh
// busts it, that every cache failure degrades to a read rather than an
// error, and that the key carries the site so one tenant's types can
// never be served to another.

// fakeLinkTypeClient counts reads, so a test can prove a round trip was
// SPARED — which is the only thing a cache is for and the only thing a
// result-shape assertion cannot show.
type fakeLinkTypeClient struct {
	// Embedded so this fake declares only the two calls its command makes;
	// every other seam fails by name. See unusedJiraClient.
	unusedJiraClient

	types []jira.IssueLinkType
	err   error

	mu    sync.Mutex
	reads int
}

func (f *fakeLinkTypeClient) GetIssueLinkTypes(context.Context) (core.LinkTypeRead, error) {
	f.mu.Lock()
	f.reads++
	f.mu.Unlock()
	if f.err != nil {
		return core.LinkTypeRead{}, f.err
	}
	return core.LinkTypeRead{Types: &jira.IssueLinkTypes{
		IssueLinkTypes: jira.NullableOf(f.types),
	}}, nil
}

// linkTypeFixture is the list the fake serves.
func linkTypeFixture() []jira.IssueLinkType {
	return []jira.IssueLinkType{{
		ID:      jira.NullableOf("10000"),
		Name:    jira.NullableOf("Blocks"),
		Inward:  jira.NullableOf("is blocked by"),
		Outward: jira.NullableOf("blocks"),
	}}
}

// agedCache is a core.Cache whose entries can be given an arbitrary write
// time, so a test can age one without sleeping.
type agedCache struct {
	mu      sync.Mutex
	value   []byte
	written time.Time
	stored  bool
	// setErr, when set, is what Set returns — the full-disk case.
	setErr error
	// keys records every key written, so a test can assert the site is in
	// it.
	keys []string
	// forgets counts Forget calls, which is how --refresh is proved to
	// DROP the entry rather than merely bypass it.
	forgets int
}

func (c *agedCache) Get(string) ([]byte, time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.stored {
		return nil, time.Time{}, false
	}
	return c.value, c.written, true
}

func (c *agedCache) Set(key string, value []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.setErr != nil {
		return c.setErr
	}
	c.value, c.written, c.stored = value, time.Now(), true
	c.keys = append(c.keys, key)
	return nil
}

func (c *agedCache) Forget(string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stored = false
	c.forgets++
	return nil
}

// primed returns a cache already holding the fixture, written at age ago.
func primed(t *testing.T, age time.Duration) *agedCache {
	t.Helper()
	raw, err := json.Marshal(linkTypeFixture())
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return &agedCache{value: raw, written: time.Now().Add(-age), stored: true}
}

// TestLinkTypesServedFromAFreshCache is the whole point of the cache: the
// round trip is SPARED, not merely made faster.
func TestLinkTypesServedFromAFreshCache(t *testing.T) {
	client := &fakeLinkTypeClient{types: linkTypeFixture()}
	cache := primed(t, time.Hour)

	read, err := readLinkTypes(t.Context(), client, cache, "site", linkTypeOptions{})
	if err != nil {
		t.Fatalf("readLinkTypes: %v", err)
	}
	if client.reads != 0 {
		t.Errorf("hit the site %d times on a cache hit; the round trip must be spared", client.reads)
	}
	if !read.cached {
		t.Error("read reports itself fresh; a caller could not tell it was cached")
	}
	if read.age < time.Hour-time.Minute {
		t.Errorf("age = %v, want about an hour", read.age)
	}
	if len(read.types) != 1 {
		t.Fatalf("got %d types, want the cached one", len(read.types))
	}
}

// TestLinkTypesRereadsWhenTheCacheIsStale: past the TTL the entry is not
// trusted, which is the half that makes the cache safe.
func TestLinkTypesRereadsWhenTheCacheIsStale(t *testing.T) {
	client := &fakeLinkTypeClient{types: linkTypeFixture()}
	cache := primed(t, 48*time.Hour) // twice the default lifetime

	read, err := readLinkTypes(t.Context(), client, cache, "site", linkTypeOptions{})
	if err != nil {
		t.Fatalf("readLinkTypes: %v", err)
	}
	if client.reads != 1 {
		t.Errorf("hit the site %d times, want 1 — a stale entry must not be served", client.reads)
	}
	if read.cached {
		t.Error("a re-read reported itself as cached")
	}
}

// TestLinkTypesTTLIsTheCallers: --ttl-minutes decides, so the SAME entry
// is fresh under one lifetime and stale under another.
func TestLinkTypesTTLIsTheCallers(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ttlMinutes int
		wantReads  int
	}{
		{name: "inside the window", ttlMinutes: 120, wantReads: 0},
		{name: "outside the window", ttlMinutes: 30, wantReads: 1},
		// Zero and negative fall back to the default (24h), under which a
		// one-hour-old entry is fresh — they do NOT mean "no cache".
		{name: "zero falls back to the default", ttlMinutes: 0, wantReads: 0},
		{name: "negative falls back to the default", ttlMinutes: -5, wantReads: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeLinkTypeClient{types: linkTypeFixture()}
			if _, err := readLinkTypes(t.Context(), client, primed(t, time.Hour), "site",
				linkTypeOptions{cacheOptions: cacheOptions{ttlMinutes: tc.ttlMinutes}}); err != nil {
				t.Fatalf("readLinkTypes: %v", err)
			}
			if client.reads != tc.wantReads {
				t.Errorf("site reads = %d, want %d", client.reads, tc.wantReads)
			}
		})
	}
}

// TestLinkTypesRefreshDropsTheEntry: --refresh must FORGET rather than
// merely bypass, so a refresh that then fails to reach Jira does not leave
// the stale entry to be trusted by the next invocation.
func TestLinkTypesRefreshDropsTheEntry(t *testing.T) {
	client := &fakeLinkTypeClient{types: linkTypeFixture()}
	cache := primed(t, time.Minute) // well inside any TTL

	read, err := readLinkTypes(t.Context(), client, cache, "site", linkTypeOptions{cacheOptions: cacheOptions{refresh: true}})
	if err != nil {
		t.Fatalf("readLinkTypes: %v", err)
	}
	if client.reads != 1 {
		t.Errorf("site reads = %d, want 1 — --refresh must ignore a fresh entry", client.reads)
	}
	if read.cached {
		t.Error("a --refresh result reported itself as cached")
	}
	if cache.forgets != 1 {
		t.Errorf("Forget called %d times, want 1 — bypassing without dropping leaves a stale entry behind", cache.forgets)
	}
}

// TestLinkTypesCacheKeyCarriesTheSite: link types are per-tenant, so a key
// missing the site would serve one tenant's types to another — and the
// failure would look like Jira forgetting a type rather than a cache bug.
func TestLinkTypesCacheKeyCarriesTheSite(t *testing.T) {
	client := &fakeLinkTypeClient{types: linkTypeFixture()}
	cache := &agedCache{}
	const site = "https://example.atlassian.net"

	if _, err := readLinkTypes(t.Context(), client, cache, site, linkTypeOptions{}); err != nil {
		t.Fatalf("readLinkTypes: %v", err)
	}
	if len(cache.keys) != 1 {
		t.Fatalf("wrote %d keys, want 1", len(cache.keys))
	}
	if !strings.Contains(cache.keys[0], site) {
		t.Errorf("cache key %q does not carry the site; two tenants would share an entry", cache.keys[0])
	}
	if linkTypeCacheKey("a") == linkTypeCacheKey("b") {
		t.Error("two sites produce the same cache key")
	}
}

// TestLinkTypesSurvivesEveryCacheFailure is the bargain the cache makes:
// it can make the command faster, never make it fail.
func TestLinkTypesSurvivesEveryCacheFailure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cache core.Cache
	}{
		{name: "no cache at all", cache: noCache{}},
		{name: "unwritable cache", cache: &agedCache{setErr: errors.New("disk full")}},
		{name: "corrupt entry", cache: &agedCache{value: []byte("{not json"), written: time.Now(), stored: true}},
		// An empty stored list is a miss rather than an answer: serving it
		// would report every type the caller names as unknown.
		{name: "empty stored list", cache: &agedCache{value: []byte("[]"), written: time.Now(), stored: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeLinkTypeClient{types: linkTypeFixture()}
			read, err := readLinkTypes(t.Context(), client, tc.cache, "site", linkTypeOptions{})
			if err != nil {
				t.Fatalf("a cache failure failed the command: %v", err)
			}
			if len(read.types) != 1 {
				t.Errorf("got %d types, want the list read from the site", len(read.types))
			}
			if client.reads != 1 {
				t.Errorf("site reads = %d, want 1 — every cache failure falls through to the network", client.reads)
			}
		})
	}
}

// TestLinkTypesRefusesAnEmptyResponse: an empty list would report every
// type as unknown, and CACHING one would turn a single bad response into a
// day of them.
func TestLinkTypesRefusesAnEmptyResponse(t *testing.T) {
	client := &fakeLinkTypeClient{types: nil}
	cache := &agedCache{}

	if _, err := readLinkTypes(t.Context(), client, cache, "site", linkTypeOptions{}); err != nil {
		// nil Types is the loud case; an empty-but-non-nil list is
		// legitimate (a site can have none), so it must NOT be cached as
		// an answer either — storeLinkTypes writing it is harmless because
		// cachedLinkTypes treats an empty list as a miss.
		t.Fatalf("readLinkTypes: %v", err)
	}
	if len(cache.keys) != 1 {
		t.Fatalf("wrote %d keys, want 1", len(cache.keys))
	}
	// The stored empty list must not be served back as an answer.
	client2 := &fakeLinkTypeClient{types: linkTypeFixture()}
	read, err := readLinkTypes(t.Context(), client2, cache, "site", linkTypeOptions{})
	if err != nil {
		t.Fatalf("second readLinkTypes: %v", err)
	}
	if read.cached {
		t.Error("an empty cached list was served as an answer; it must be a miss")
	}
	if client2.reads != 1 {
		t.Errorf("site reads = %d, want 1", client2.reads)
	}
}
