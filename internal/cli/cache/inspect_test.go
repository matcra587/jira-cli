package cache

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/adapters"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
)

type inspectionStore struct {
	*managementStub
	written      map[string]time.Time
	gets         []string
	writes       int
	afterEntries func()
	afterGet     func()
}

func (s *inspectionStore) Entries(ctx context.Context) ([]core.CacheEntry, error) {
	entries, err := s.managementStub.Entries(ctx)
	if s.afterEntries != nil {
		s.afterEntries()
	}
	return entries, err
}

func (s *inspectionStore) Get(key string) ([]byte, time.Time, bool) {
	s.gets = append(s.gets, key)
	raw, written, ok := s.Cache.Get(key)
	if value, exists := s.written[key]; exists {
		written = value
	}
	if s.afterGet != nil {
		s.afterGet()
	}
	return raw, written, ok
}

func (s *inspectionStore) Set(string, []byte) error {
	s.writes++
	return errors.New("inspection attempted a write")
}

func (s *inspectionStore) Forget(string) error {
	s.writes++
	return errors.New("inspection attempted invalidation")
}

func inspectionDeps(t *testing.T, store core.Cache) PrimeDeps {
	t.Helper()
	return PrimeDeps{Scope: func(local bool) (MetadataScope, error) {
		if !local {
			t.Fatal("inspection requested credential-backed scope")
		}
		return MetadataScope{Store: store, Site: "https://example.com", Email: "person@example.com"}, nil
	}, JiraClient: func() (core.JiraClient, error) {
		t.Fatal("inspection constructed Jira client")
		return nil, nil
	}}
}

func TestCacheInspectPreflightNeverOpensStorage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		args   []string
		cancel bool
		code   string
	}{
		{name: "unknown resource", args: []string{"fieldz"}, code: cli.CodeArgValueInvalid},
		{name: "canceled", cancel: true, code: cli.CodeCanceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			deps := PrimeDeps{Scope: func(bool) (MetadataScope, error) {
				t.Fatal("refused input opened storage")
				return MetadataScope{}, nil
			}}
			out, err := runInspect(ctx, deps, command.Invocation{Args: tc.args})
			if cli.MapError(err).Code != tc.code || out.Entries != nil || out.Complete {
				t.Fatalf("preflight=%+v, %v", out, err)
			}
		})
	}
}

func TestCacheInspectSelectionRetainsUnknownInventoryWithoutTrustingMalformedFacts(t *testing.T) {
	t.Parallel()
	stamp := time.Now().Add(-time.Minute)
	store := &inspectionStore{managementStub: &managementStub{Cache: &adapters.MemoryCache{}, entries: []core.CacheEntry{
		{ID: "board-list", Key: BoardListKey, Written: stamp, SizeBytes: 7},
		{ID: "selector", Key: BoardSelectorPrefix + "https://example.com:id:12", Written: stamp, SizeBytes: 11},
		{ID: "history", Key: IssueKeysKey, Written: stamp, SizeBytes: 15},
		{ID: "unknown", Key: "future:resource", Written: stamp, SizeBytes: 3},
		{ID: "malformed", Key: BoardListKey, Written: stamp, SizeBytes: 999, Malformed: true},
	}}}
	for _, selected := range []bool{false, true} {
		inv := command.Invocation{}
		if selected {
			inv.Args = []string{"boards"}
		}
		out, err := runInspect(t.Context(), inspectionDeps(t, store), inv)
		if err != nil || !out.Complete || out.BaseURL != "https://example.com" || out.Email != "person@example.com" || out.Unclassified != 2 || len(store.deleted) != 0 || store.writes != 0 {
			t.Fatalf("scope inspection=%+v, %v", out, err)
		}
		if selected {
			if out.Count != 2 || len(out.Entries) != 2 || out.KnownPayloadBytes != 18 || len(out.Resources) != 1 || out.Resources[0].Resource != "boards" {
				t.Fatalf("board selection=%+v", out)
			}
			for _, row := range out.Entries {
				if row.Resource != "boards" {
					t.Fatal("selected inspection included unrelated entries")
				}
			}
		} else {
			if out.Count != 5 || len(out.Entries) != 5 || out.KnownPayloadBytes != 36 || len(out.Resources) != 11 {
				t.Fatalf("whole scope=%+v", out)
			}
			row := out.Entries[4]
			if !row.Malformed || row.Key != "" || row.Resource != "unknown" || row.SizeBytes != nil || row.WrittenAt != "" {
				t.Fatalf("malformed entry leaked untrusted facts: %+v", row)
			}
		}
	}
}

func TestCacheInspectUsesProducerValidationFreshnessAndHistoryWithoutTTL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, resource, raw, state string
		written                    time.Time
		count                      int
		complete                   bool
		ttl                        *int
	}{
		{name: "fresh fields", resource: "fields", raw: `{"version":1,"items":[{"id":"name","name":"Native"}],"count":1,"pages":1,"complete":true}`, state: "fresh", written: time.Now(), count: 1, complete: true, ttl: new(20160)},
		{name: "stale fields", resource: "fields", raw: `{"version":1,"items":[{"id":"name","name":"Native"}],"count":1,"pages":1,"complete":true}`, state: "stale", written: time.Now().Add(-15 * 24 * time.Hour), count: 1, complete: true, ttl: new(20160)},
		{name: "future fields", resource: "fields", raw: `{"version":1,"items":[],"count":0,"pages":1,"complete":true}`, state: "stale", written: time.Now().Add(time.Hour), complete: true, ttl: new(20160)},
		{name: "malformed fields", resource: "fields", raw: `{"version":1,"items":[],"pages":1,"complete":true}`, state: "malformed", written: time.Now(), ttl: new(20160)},
		{name: "old history", resource: "issuekeys", raw: `["DEMO-7"]`, state: "present", written: time.Now().Add(-365 * 24 * time.Hour), count: 1, complete: true},
		{name: "malformed history", resource: "issuekeys", raw: `["demo-7"]`, state: "malformed", written: time.Now()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := IssueKeysKey
			if tc.resource != "issuekeys" {
				resource, _ := metadataResource(tc.resource)
				key = resource.Key("https://example.com")
			}
			var memory adapters.MemoryCache
			if err := memory.Set(key, []byte(tc.raw)); err != nil {
				t.Fatal(err)
			}
			store := &inspectionStore{managementStub: &managementStub{Cache: &memory}, written: map[string]time.Time{key: tc.written}}
			out, err := runInspect(t.Context(), inspectionDeps(t, store), command.Invocation{Args: []string{tc.resource}})
			if err != nil || !out.Complete || len(out.Resources) != 1 {
				t.Fatalf("resource inspection=%+v, %v", out, err)
			}
			row := out.Resources[0]
			if row.CacheState != tc.state || row.Count != tc.count || row.Complete != tc.complete || (row.TTLMinutes == nil) != (tc.ttl == nil) || tc.ttl != nil && *row.TTLMinutes != *tc.ttl || store.writes != 0 || !slices.Equal(store.gets, []string{key}) {
				t.Fatalf("validated resource=%+v, gets=%v", row, store.gets)
			}
			if tc.state == "malformed" && row.WrittenAt != "" || tc.state != "malformed" && row.WrittenAt != tc.written.UTC().Format(time.RFC3339Nano) || row.AgeSeconds < 0 {
				t.Fatalf("resource timestamp=%+v", row)
			}
			if raw, _, ok := memory.Get(key); !ok || string(raw) != tc.raw {
				t.Fatal("inspection modified cached bytes")
			}
		})
	}
}

func TestCacheInspectRetainsPartialInventoryAndLateCanceledReads(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name              string
		constructorCancel bool
		inventoryCancel   bool
		readCancel        bool
		inventoryFailure  bool
		selectedResource  bool
	}{
		{name: "constructor cancellation", constructorCancel: true},
		{name: "inventory cancellation", inventoryCancel: true},
		{name: "inventory failure", inventoryFailure: true},
		{name: "read cancellation", readCancel: true},
		{name: "final selected read cancellation", readCancel: true, selectedResource: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cause := errors.New("owned inventory failure")
			store := &inspectionStore{managementStub: &managementStub{Cache: &adapters.MemoryCache{}, entries: []core.CacheEntry{{ID: "observed", Key: BoardListKey, Written: time.Now(), SizeBytes: 3}}}}
			if tc.inventoryCancel {
				store.afterEntries = cancel
			}
			if tc.readCancel {
				store.afterGet = cancel
			}
			if tc.inventoryFailure {
				store.readErr = cause
			}
			deps := inspectionDeps(t, store)
			original := deps.Scope
			deps.Scope = func(local bool) (MetadataScope, error) {
				if tc.constructorCancel {
					cancel()
				}
				return original(local)
			}
			inv := command.Invocation{}
			if tc.selectedResource {
				inv.Args = []string{"boards"}
			}
			out, err := runInspect(ctx, deps, inv)
			if out.Complete || tc.inventoryFailure && !errors.Is(err, cause) || !tc.inventoryFailure && !errors.Is(err, context.Canceled) || store.writes != 0 || len(store.deleted) != 0 {
				t.Fatalf("interrupted inspection=%+v, %v", out, err)
			}
			if tc.constructorCancel {
				if out.Entries != nil || len(store.gets) != 0 {
					t.Fatal("canceled construction fabricated an inventory")
				}
			} else if out.Count != 1 || len(out.Entries) != 1 || out.Entries[0].ID != "observed" || len(out.Resources) != len(store.gets) || tc.readCancel && len(out.Resources) != 1 {
				t.Fatalf("completed observations were lost: %+v gets=%v", out, store.gets)
			}
		})
	}
}

func TestCacheInspectRetainsIncompleteCollectionResumeFacts(t *testing.T) {
	t.Parallel()
	resource, _ := metadataResource("labels")
	var memory adapters.MemoryCache
	next := 1
	value := MetadataValue{Items: []json.RawMessage{json.RawMessage(`"one"`)}, Count: 1, Pages: 1, Complete: false, NextStart: &next, TruncatedReason: "max_results"}
	raw, err := encodeMetadata("labels", value, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Set(resource.Key("https://example.com"), raw); err != nil {
		t.Fatal(err)
	}
	store := &inspectionStore{managementStub: &managementStub{Cache: &memory}}
	out, err := runInspect(t.Context(), inspectionDeps(t, store), command.Invocation{Args: []string{"labels"}})
	if err != nil || !out.Complete || len(out.Resources) != 1 || out.Resources[0].Complete || out.Resources[0].Count != 1 || out.Resources[0].Pages != 1 || out.Resources[0].TruncatedReason != "max_results" || out.Resources[0].NextStart == nil || *out.Resources[0].NextStart != 1 || out.Resources[0].CacheState != "fresh" {
		t.Fatalf("bounded collection became complete or lost resume facts: %+v, %v", out, err)
	}
}
