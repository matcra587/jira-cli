package cache

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/adapters"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type primerStore struct {
	core.Cache
	written       time.Time
	setErr        error
	afterSet      func()
	sets, forgets int
}

func (s *primerStore) Get(key string) ([]byte, time.Time, bool) {
	raw, written, ok := s.Cache.Get(key)
	if !s.written.IsZero() {
		written = s.written
	}
	return raw, written, ok
}

func (s *primerStore) Set(key string, raw []byte) error {
	s.sets++
	if s.setErr != nil {
		return s.setErr
	}
	err := s.Cache.Set(key, raw)
	if err == nil && s.afterSet != nil {
		s.afterSet()
	}
	return err
}
func (s *primerStore) Forget(key string) error { s.forgets++; return s.Cache.Forget(key) }
func fieldMetadata() MetadataValue {
	return MetadataValue{Items: []json.RawMessage{json.RawMessage(`{"id":"customfield_10000","name":"Native <field>","unknown":null,"spelling":"\u003c"}`)}, Count: 1, Pages: 1, Complete: true}
}

func primerDeps(store core.Cache, reader core.JiraClient) PrimeDeps {
	return PrimeDeps{Scope: func(ctx context.Context, _ bool) (MetadataScope, error) {
		return MetadataScope{Store: store, Site: "https://example.com"}, nil
	}, JiraClient: func(ctx context.Context) (core.JiraClient, error) { return reader, nil }}
}

func TestCachePrimerPreviewAndFreshHitsNeverConstructClientsOrWrite(t *testing.T) {
	t.Parallel()
	resource, _ := metadataResource("fields")
	for _, tc := range []struct {
		name                           string
		preview, force, present, stale bool
	}{
		{name: "cold preview", preview: true},
		{name: "fresh preview", preview: true, present: true},
		{name: "stale preview", preview: true, present: true, stale: true},
		{name: "forced preview", preview: true, present: true, force: true},
		{name: "fresh live", present: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var memory adapters.MemoryCache
			value := fieldMetadata()
			if tc.present {
				raw, err := encodeMetadata(resource.Name, value, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := memory.Set(resource.Key("https://example.com"), raw); err != nil {
					t.Fatal(err)
				}
			}
			store := &primerStore{Cache: &memory}
			if tc.stale {
				store.written = time.Now().Add(-2 * resource.DefaultTTL)
			}
			deps := primerDeps(store, nil)
			deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
				t.Fatal("preview/fresh hit constructed client")
				return nil, nil
			}
			scopeCalls := 0
			deps.Scope = func(ctx context.Context, local bool) (MetadataScope, error) {
				scopeCalls++
				if local != tc.preview {
					t.Fatal("wrong authentication boundary")
				}
				return MetadataScope{Store: store, Site: "https://example.com"}, nil
			}
			out, err := runPrime(t.Context(), deps, command.Invocation{DryRun: tc.preview}, resource, primeOptions{force: tc.force})
			wantCount := 0
			if tc.present {
				wantCount = 1
			}
			if err != nil || out.Attempted || out.Saved || out.DryRun != tc.preview || out.FromCache != tc.present || out.WouldFetch != (!tc.present || tc.stale || tc.force) || out.Count != wantCount || store.sets != 0 || store.forgets != 0 || scopeCalls != 1 {
				t.Fatalf("offline receipt=%+v err=%v sets=%d", out, err, store.sets)
			}
			if tc.present && !slices.Equal(out.Items[0], value.Items[0]) {
				t.Fatal("preview/reuse changed native bytes")
			}
		})
	}
}

func TestCachePrimerForceTTLAndStorageFailuresRetainNativeReceipt(t *testing.T) {
	t.Parallel()
	resource, _ := metadataResource("fields")
	for _, tc := range []struct {
		name      string
		force     bool
		ttl       int
		age       time.Duration
		writeFail bool
	}{
		{name: "force", force: true, age: time.Minute},
		{name: "short ttl", ttl: 1, age: 2 * time.Minute},
		{name: "default ttl", ttl: 0, age: 15 * 24 * time.Hour},
		{name: "negative default ttl", ttl: -1, age: 15 * 24 * time.Hour},
		{name: "failed publication", force: true, writeFail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var memory adapters.MemoryCache
			old := []byte(`{"version":1,"items":[],"count":0,"pages":1,"complete":true}`)
			if err := memory.Set(resource.Key("https://example.com"), old); err != nil {
				t.Fatal(err)
			}
			cause := errors.New("publication failed")
			store := &primerStore{Cache: &memory, written: time.Now().Add(-tc.age)}
			if tc.writeFail {
				store.setErr = cause
			}
			calls := 0
			reader := metadataReaderStub{api: func(ctx context.Context, _ core.APIRequest) (*core.APIResponse, error) {
				calls++
				if ctx.Err() != nil {
					t.Fatal("unexpected cancellation")
				}
				return metadataResponse("[" + string(fieldMetadata().Items[0]) + "]"), nil
			}}
			out, err := runPrime(t.Context(), primerDeps(store, reader), command.Invocation{}, resource, primeOptions{force: tc.force, ttlMinutes: tc.ttl})
			if !out.Attempted || !out.WouldFetch || out.FromCache || out.Saved == tc.writeFail || out.Count != 1 || !out.Complete || calls != 1 || store.sets != 1 || store.forgets != 0 {
				t.Fatalf("publication receipt=%+v err=%v", out, err)
			}
			raw, _, ok := memory.Get(resource.Key("https://example.com"))
			if !ok {
				t.Fatal("existing entry deleted")
			}
			if tc.writeFail {
				if !errors.Is(err, cause) || cli.MapError(err).Code != cli.CodeCacheIO || !slices.Equal(raw, old) {
					t.Fatal("publication failure lost cause or previous entry")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				value, _, state := InspectMetadata(&memory, "https://example.com", "fields", resource.DefaultTTL, nil)
				if state != "fresh" || !slices.Equal(out.Items[0], fieldMetadata().Items[0]) || !slices.Equal(value.Items[0], fieldMetadata().Items[0]) {
					t.Fatal("persisted native bytes changed")
				}
			}
		})
	}
}

func TestCachePrimerPartialReadsAndCancellationNeverReplaceEarlierCache(t *testing.T) {
	t.Parallel()
	resource, _ := metadataResource("labels")
	for _, canceled := range []bool{false, true} {
		name := "later page failure"
		if canceled {
			name = "canceled page"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var memory adapters.MemoryCache
			old := []byte(`{"version":1,"items":["old"],"count":1,"pages":1,"complete":true}`)
			if err := memory.Set(resource.Key("https://example.com"), old); err != nil {
				t.Fatal(err)
			}
			store := &primerStore{Cache: &memory}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cause := errors.New("later page unavailable")
			calls := 0
			reader := metadataReaderStub{api: func(_ context.Context, req core.APIRequest) (*core.APIResponse, error) {
				calls++
				if req.Query.Get("startAt") == "0" {
					return metadataResponse(`{"startAt":0,"maxResults":100,"isLast":false,"values":["first"]}`), nil
				}
				if canceled {
					cancel()
					return metadataResponse(`{"startAt":1,"maxResults":100,"isLast":true,"values":["second"]}`), nil
				}
				return nil, cause
			}}
			out, err := runPrime(ctx, primerDeps(store, reader), command.Invocation{}, resource, primeOptions{force: true})
			if err == nil || out.Saved || out.Count != 1 || out.Pages != 1 || out.Complete || out.NextStart == nil || *out.NextStart != 1 || store.sets != 0 || store.forgets != 0 || calls != 2 {
				t.Fatalf("partial receipt=%+v err=%v", out, err)
			}
			if canceled && !errors.Is(err, context.Canceled) || !canceled && !errors.Is(err, cause) {
				t.Fatal("original failure discarded")
			}
			raw, _, _ := memory.Get(resource.Key("https://example.com"))
			if !slices.Equal(raw, old) {
				t.Fatal("partial read replaced previous entry")
			}
		})
	}
}

func TestCachePrimerLateCancellationRetainsPublicationAcknowledgment(t *testing.T) {
	t.Parallel()
	var memory adapters.MemoryCache
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store := &primerStore{Cache: &memory, afterSet: cancel}
	reader := metadataReaderStub{api: func(context.Context, core.APIRequest) (*core.APIResponse, error) {
		return metadataResponse("[" + string(fieldMetadata().Items[0]) + "]"), nil
	}}
	resource, _ := metadataResource("fields")
	out, err := runPrime(ctx, primerDeps(store, reader), command.Invocation{}, resource, primeOptions{})
	if !errors.Is(err, context.Canceled) || !out.Saved || !out.Attempted || out.FromCache || out.WrittenAt == "" || out.Count != 1 || store.sets != 1 || store.forgets != 0 {
		t.Fatalf("late cancellation lost acknowledgment: %+v err=%v", out, err)
	}
	value, _, state := InspectMetadata(&memory, "https://example.com", "fields", resource.DefaultTTL, nil)
	if state != "fresh" || !slices.Equal(value.Items[0], out.Items[0]) {
		t.Fatal("acknowledgment lacks persisted native data")
	}
}

func TestCachePrimerPreflightStopsBeforeScopeAndClientConstruction(t *testing.T) {
	t.Parallel()
	resource, _ := metadataResource("fields")
	for _, tc := range []struct {
		name     string
		canceled bool
		ttl      int
		code     string
	}{
		{name: "overflow", ttl: math.MaxInt, code: cli.CodeFlagValueInvalid},
		{name: "canceled", canceled: true, code: cli.CodeCanceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			deps := PrimeDeps{Scope: func(ctx context.Context, _ bool) (MetadataScope, error) {
				t.Fatal("invalid input opened storage")
				return MetadataScope{}, nil
			}}
			_, err := runPrime(ctx, deps, command.Invocation{}, resource, primeOptions{ttlMinutes: tc.ttl})
			if cli.MapError(err).Code != tc.code {
				t.Fatal(err)
			}
		})
	}
}

func TestMetadataInspectionRejectsCorruptionAndFutureFreshness(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, raw, state string
		future           bool
	}{
		{name: "valid", raw: `{"version":1,"items":[],"count":0,"pages":1,"complete":true}`, state: "fresh"},
		{name: "future", raw: `{"version":1,"items":[],"count":0,"pages":1,"complete":true}`, state: "stale", future: true},
		{name: "version", raw: `{"version":2,"items":[],"count":0,"pages":1,"complete":true}`, state: "malformed"},
		{name: "null", raw: `{"version":1,"items":null,"count":0,"pages":1,"complete":true}`, state: "malformed"},
		{name: "count", raw: `{"version":1,"items":[],"count":1,"pages":1,"complete":true}`, state: "malformed"},
		{name: "pages", raw: `{"version":1,"items":[],"count":0,"pages":0,"complete":true}`, state: "malformed"},
		{name: "completion", raw: `{"version":1,"items":[],"count":0,"pages":1,"complete":true,"next_start":0}`, state: "malformed"},
		{name: "duplicate completion", raw: `{"version":1,"items":[],"count":0,"pages":1,"complete":false,"complete":true}`, state: "malformed"},
		{name: "case", raw: `{"version":1,"items":[],"count":0,"pages":1,"Complete":true}`, state: "malformed"},
		{name: "identity", raw: `{"version":1,"items":[{"name":"missing identity"}],"count":1,"pages":1,"complete":true}`, state: "malformed"},
		{name: "unexpected payload", raw: `{"version":1,"items":[],"count":0,"pages":1,"complete":true,"payload":{}}`, state: "malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var memory adapters.MemoryCache
			resource, _ := metadataResource("fields")
			if err := memory.Set(resource.Key("https://example.com"), []byte(tc.raw)); err != nil {
				t.Fatal(err)
			}
			store := &primerStore{Cache: &memory}
			if tc.future {
				store.written = time.Now().Add(time.Hour)
			}
			_, _, state := InspectMetadata(store, "https://example.com", "fields", resource.DefaultTTL, nil)
			if state != tc.state {
				t.Fatalf("state=%s want=%s", state, tc.state)
			}
		})
	}
}

func TestMetadataInspectionRequiresExplicitCacheFacts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, resource, raw string
	}{
		{name: "missing count", resource: "fields", raw: `{"version":1,"items":[],"pages":1,"complete":true}`},
		{name: "null count", resource: "fields", raw: `{"version":1,"items":[],"count":null,"pages":1,"complete":true}`},
		{name: "missing completion", resource: "labels", raw: `{"version":1,"items":["kept"],"count":1,"pages":100,"truncated_reason":"max_pages","next_start":1}`},
		{name: "null completion", resource: "labels", raw: `{"version":1,"items":["kept"],"count":1,"pages":100,"complete":null,"truncated_reason":"max_pages","next_start":1}`},
		{name: "missing version", resource: "fields", raw: `{"items":[],"count":0,"pages":1,"complete":true}`},
		{name: "null version", resource: "fields", raw: `{"version":null,"items":[],"count":0,"pages":1,"complete":true}`},
		{name: "missing pages", resource: "fields", raw: `{"version":1,"items":[],"count":0,"complete":true}`},
		{name: "null pages", resource: "fields", raw: `{"version":1,"items":[],"count":0,"pages":null,"complete":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var store adapters.MemoryCache
			resource, ok := metadataResource(tc.resource)
			if !ok {
				t.Fatal("unknown test resource")
			}
			if err := store.Set(resource.Key("https://example.com"), []byte(tc.raw)); err != nil {
				t.Fatal(err)
			}
			value, written, state := InspectMetadata(&store, "https://example.com", tc.resource, resource.DefaultTTL, nil)
			if state != "malformed" || !written.IsZero() || value.Items == nil || len(value.Items) != 0 || value.Complete {
				t.Fatalf("missing facts accepted: state=%s written=%v value=%+v", state, written, value)
			}
		})
	}
}

func TestMetadataInspectionKeepsBoundedSnapshotsAndUnboundedRefetches(t *testing.T) {
	t.Parallel()
	var memory adapters.MemoryCache
	resource, _ := metadataResource("labels")
	value := MetadataValue{Items: []json.RawMessage{json.RawMessage(`"kept"`)}, Count: 1, Pages: 100, NextStart: new(1), TruncatedReason: "max_pages"}
	raw, err := encodeMetadata("labels", value, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Set(resource.Key("https://example.com"), raw); err != nil {
		t.Fatal(err)
	}
	reader := metadataReaderStub{api: func(context.Context, core.APIRequest) (*core.APIResponse, error) {
		return metadataResponse(`{"startAt":0,"maxResults":100,"isLast":true,"values":["fresh"]}`), nil
	}}
	for _, unbounded := range []bool{false, true} {
		out, err := runPrime(t.Context(), primerDeps(&memory, reader), command.Invocation{}, resource, primeOptions{unbounded: unbounded})
		if err != nil || out.FromCache == unbounded || out.Saved != unbounded || out.Attempted != unbounded || out.Complete != unbounded {
			t.Fatalf("bounded reuse=%+v err=%v", out, err)
		}
	}
}

func TestMetadataLinkTypesReuseOwningArrayFormat(t *testing.T) {
	t.Parallel()
	var memory adapters.MemoryCache
	resource, _ := metadataResource("linktypes")
	reader := metadataReaderStub{api: func(context.Context, core.APIRequest) (*core.APIResponse, error) {
		return metadataResponse(`{"issueLinkTypes":[{"id":"1","name":"Blocks","inward":"is blocked by","outward":"blocks","unknown":null}]}`), nil
	}}
	out, err := runPrime(t.Context(), primerDeps(&memory, reader), command.Invocation{}, resource, primeOptions{})
	if err != nil || !out.Saved {
		t.Fatalf("link prime=%+v err=%v", out, err)
	}
	raw, _, ok := memory.Get(LinkTypesPrefix + "https://example.com")
	var links []jira.IssueLinkType
	if !ok || json.Unmarshal(raw, &links) != nil || len(links) != 1 || links[0].Name.Value != "Blocks" || string(links[0].Extra["unknown"]) != "null" {
		t.Fatal("primer used incompatible link payload")
	}
	value, _, state := InspectMetadata(&memory, "https://example.com", "linktypes", resource.DefaultTTL, nil)
	if state != "fresh" || value.Count != 1 || !value.Complete {
		t.Fatal("existing consumer key was not reusable")
	}
}
