package cache

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/matcra587/jira-cli/internal/adapters"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestCacheRefreshPreviewSelectionAndDefaultTTLs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		args, want []string
	}{
		{name: "all", want: []string{"labels", "projects", "epics", "fields", "issuetypes", "linktypes", "boards", "statuses", "priorities", "resolutions"}},
		{name: "selected deduplicated", args: []string{"fields", "labels", "fields", "boards"}, want: []string{"fields", "labels", "boards"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var memory adapters.MemoryCache
			var scopes, clients atomic.Int32
			deps := primerDeps(&memory, nil)
			deps.Scope = func(local bool) (MetadataScope, error) {
				scopes.Add(1)
				if !local {
					return MetadataScope{}, errors.New("preview requested authenticated scope")
				}
				return MetadataScope{Store: &memory, Site: "https://example.com"}, nil
			}
			deps.JiraClient = func() (core.JiraClient, error) { clients.Add(1); return nil, errors.New("preview constructed client") }
			out, err := runRefresh(t.Context(), deps, command.Invocation{DryRun: true, Args: tc.args}, primeOptions{}, command.KeyedFlags{Parallelism: 3})
			if err != nil || len(out.Results) != len(tc.want) || scopes.Load() != 1 || clients.Load() != 0 {
				t.Fatalf("runRefresh(%v)=%+v, %v scopes=%d clients=%d", tc.args, out, err, scopes.Load(), clients.Load())
			}
			for i, row := range out.Results {
				resource, _ := metadataResource(tc.want[i])
				if row.Resource != tc.want[i] || !row.OK || row.Err != nil || row.Data == nil || !row.Data.DryRun || !row.Data.WouldFetch || row.Data.Attempted || row.Data.Saved || row.Data.TTLMinutes != int(resource.DefaultTTL.Minutes()) || len(row.Data.Items) != 0 {
					t.Fatalf("runRefresh(%v) row[%d]=%+v", tc.args, i, row)
				}
			}
		})
	}
}

func TestCacheRefreshCommandRestoresDefaultAndShorthand(t *testing.T) {
	t.Parallel()
	cmd := newRefresh(PrimeDeps{})
	flag := cmd.Flags().Lookup(command.FlagParallelism)
	if flag == nil || flag.DefValue != "1" || flag.Shorthand != "p" {
		t.Fatalf("cache refresh parallelism flag=%+v, want default1 and -p", flag)
	}
	if err := cmd.ParseFlags([]string{"-p", "16"}); err != nil {
		t.Fatal(err)
	}
	if value, err := cmd.Flags().GetInt(command.FlagParallelism); err != nil || value != 16 {
		t.Fatalf("cache refresh -p16=%d, %v", value, err)
	}
}

func TestCacheRefreshPreflightNeverOpensFactories(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		args         []string
		ttl, workers int
		code         string
	}{
		{name: "unknown after valid", args: []string{"fields", "typo"}, workers: 1, code: cli.CodeArgValueInvalid},
		{name: "ttl overflow", ttl: math.MaxInt, workers: 1, code: cli.CodeFlagValueInvalid},
		{name: "zero workers", workers: 0, code: cli.CodeFlagValueInvalid},
		{name: "too many workers", workers: 17, code: cli.CodeFlagValueInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps := PrimeDeps{Scope: func(bool) (MetadataScope, error) { t.Error("preflight opened scope"); return MetadataScope{}, nil }, JiraClient: func() (core.JiraClient, error) { t.Error("preflight opened client"); return nil, nil }}
			out, err := runRefresh(t.Context(), deps, command.Invocation{Args: tc.args}, primeOptions{ttlMinutes: tc.ttl}, command.KeyedFlags{Parallelism: tc.workers})
			if err == nil || cli.MapError(err).Code != tc.code || len(out.Results) != 0 {
				t.Fatalf("runRefresh(%s)=%+v, %v, want %s before I/O", tc.name, out, err, tc.code)
			}
		})
	}
}

func TestCacheRefreshFreshGroupNeverConstructsClient(t *testing.T) {
	t.Parallel()
	var memory adapters.MemoryCache
	for _, name := range []string{"fields", "statuses"} {
		resource, _ := metadataResource(name)
		raw, err := encodeMetadata(name, fieldMetadata(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := memory.Set(resource.Key("https://example.com"), raw); err != nil {
			t.Fatal(err)
		}
	}
	deps := primerDeps(&memory, nil)
	deps.JiraClient = func() (core.JiraClient, error) {
		t.Error("fresh group opened client")
		return nil, errors.New("unexpected client")
	}
	out, err := runRefresh(t.Context(), deps, command.Invocation{Args: []string{"fields", "statuses"}}, primeOptions{}, command.KeyedFlags{Parallelism: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range out.Results {
		if !row.OK || row.Data == nil || !row.Data.FromCache || row.Data.WouldFetch || row.Data.Attempted || row.Data.Saved || string(row.Data.Items[0]) != string(fieldMetadata().Items[0]) {
			t.Fatalf("fresh runRefresh row=%+v", row)
		}
	}
}

func TestCacheRefreshBoundsWorkersAndPreservesOrder(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var memory adapters.MemoryCache
		var active, peak, calls, clients, scopes atomic.Int32
		second, remaining := make(chan struct{}), make(chan struct{})
		var mu sync.Mutex
		var finished []string
		reader := metadataReaderStub{api: func(ctx context.Context, req core.APIRequest) (*core.APIResponse, error) {
			width := active.Add(1)
			for old := peak.Load(); width > old && !peak.CompareAndSwap(old, width); old = peak.Load() {
			}
			defer active.Add(-1)
			calls.Add(1)
			wait := remaining
			if req.Endpoint == "/rest/api/3/status" {
				wait = second
			}
			select {
			case <-wait:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			mu.Lock()
			finished = append(finished, req.Endpoint)
			mu.Unlock()
			return metadataResponse("[" + string(fieldMetadata().Items[0]) + "]"), nil
		}}
		deps := primerDeps(&memory, reader)
		deps.Scope = func(local bool) (MetadataScope, error) {
			scopes.Add(1)
			return MetadataScope{Store: &memory, Site: "https://example.com"}, nil
		}
		deps.JiraClient = func() (core.JiraClient, error) { clients.Add(1); return reader, nil }
		type outcome struct {
			out envelope.CacheRefreshOutput
			err error
		}
		done := make(chan outcome, 1)
		go func() {
			out, err := runRefresh(t.Context(), deps, command.Invocation{Args: []string{"fields", "statuses", "issuetypes"}}, primeOptions{}, command.KeyedFlags{Parallelism: 2})
			done <- outcome{out: out, err: err}
		}()
		synctest.Wait()
		if calls.Load() != 2 || active.Load() != 2 {
			t.Errorf("runRefresh initial calls=%d active=%d, want 2 each", calls.Load(), active.Load())
		}
		close(second)
		synctest.Wait()
		if calls.Load() != 3 || active.Load() != 2 {
			t.Errorf("runRefresh after second completion calls=%d active=%d, want 3 and 2", calls.Load(), active.Load())
		}
		close(remaining)
		result := <-done
		if result.err != nil || peak.Load() != 2 || clients.Load() != 1 || scopes.Load() != 1 || len(result.out.Results) != 3 {
			t.Fatalf("runRefresh bounded result=%+v, %v peak=%d clients=%d scopes=%d", result.out, result.err, peak.Load(), clients.Load(), scopes.Load())
		}
		if len(finished) != 3 || finished[0] != "/rest/api/3/status" {
			t.Fatalf("completion order=%v, want status first", finished)
		}
		for i, name := range []string{"fields", "statuses", "issuetypes"} {
			row := result.out.Results[i]
			if row.Resource != name || !row.OK || row.Data == nil || !row.Data.Saved || row.Data.Count != 1 {
				t.Fatalf("runRefresh row[%d]=%+v, want saved %s", i, row, name)
			}
		}
	})
}

type refreshStore struct {
	core.Cache
	failKey  string
	cause    error
	afterSet func(string)
}

func (s refreshStore) Set(key string, raw []byte) error {
	if key == s.failKey {
		return s.cause
	}
	if err := s.Cache.Set(key, raw); err != nil {
		return err
	}
	if s.afterSet != nil {
		s.afterSet(key)
	}
	return nil
}

func TestCacheRefreshPartialAndTotalFailuresRetainDataAndOriginalCause(t *testing.T) {
	t.Parallel()
	for _, total := range []bool{false, true} {
		name := "mixed"
		if total {
			name = "total"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var memory adapters.MemoryCache
			fields, _ := metadataResource("fields")
			old := []byte(`{"version":1,"items":[],"count":0,"pages":1,"complete":true}`)
			if err := memory.Set(fields.Key("https://example.com"), old); err != nil {
				t.Fatal(err)
			}
			storageCause := errors.New("storage unavailable")
			authCause := cli.NewCodedError("jira_unauthorized", "credential rejected")
			store := refreshStore{Cache: &memory, failKey: fields.Key("https://example.com"), cause: storageCause}
			reader := metadataReaderStub{api: func(_ context.Context, req core.APIRequest) (*core.APIResponse, error) {
				if total && req.Endpoint == "/rest/api/3/status" {
					return nil, authCause
				}
				return metadataResponse("[" + string(fieldMetadata().Items[0]) + "]"), nil
			}}
			out, err := runRefresh(t.Context(), primerDeps(store, reader), command.Invocation{Args: []string{"fields", "statuses"}}, primeOptions{force: true}, command.KeyedFlags{Parallelism: 2})
			if total && !errors.Is(err, authCause) || !total && err != nil || len(out.Results) != 2 {
				t.Fatalf("runRefresh(%s)=%+v, %v, want retained rows and original severe cause", name, out, err)
			}
			row := out.Results[0]
			if row.OK || row.Err == nil || row.Err.Code != cli.CodeCacheIO || row.Data == nil || row.Data.Count != 1 || !row.Data.Attempted || row.Data.Saved || row.Resource != "fields" {
				t.Fatalf("runRefresh failed publication row=%+v", row)
			}
			raw, _, ok := memory.Get(fields.Key("https://example.com"))
			if !ok || string(raw) != string(old) {
				t.Fatal("runRefresh failed publication replaced old entry")
			}
			if total {
				if out.Results[1].OK || out.Results[1].Err == nil || out.Results[1].Err.Code != "jira_unauthorized" {
					t.Fatalf("runRefresh total second=%+v", out.Results[1])
				}
			} else if !out.Results[1].OK || out.Results[1].Data == nil || !out.Results[1].Data.Saved {
				t.Fatalf("runRefresh lost successful sibling=%+v", out.Results[1])
			}
		})
	}
}

func TestCacheRefreshCancellationRetainsSavedAndQueuedRows(t *testing.T) {
	t.Parallel()
	var memory adapters.MemoryCache
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store := refreshStore{Cache: &memory, afterSet: func(string) { cancel() }}
	var calls atomic.Int32
	reader := metadataReaderStub{api: func(context.Context, core.APIRequest) (*core.APIResponse, error) {
		calls.Add(1)
		return metadataResponse("[" + string(fieldMetadata().Items[0]) + "]"), nil
	}}
	out, err := runRefresh(ctx, primerDeps(store, reader), command.Invocation{Args: []string{"fields", "statuses", "issuetypes"}}, primeOptions{}, command.KeyedFlags{Parallelism: 1})
	if !errors.Is(err, context.Canceled) || len(out.Results) != 3 || calls.Load() != 1 {
		t.Fatalf("runRefresh canceled=%+v, %v calls=%d", out, err, calls.Load())
	}
	for i, row := range out.Results {
		if row.OK || row.Err == nil || row.Err.Code != cli.CodeCanceled || row.Data == nil || row.Data.Saved != (i == 0) || row.Data.Attempted != (i == 0) {
			t.Fatalf("runRefresh canceled row[%d]=%+v", i, row)
		}
	}
}

func TestCacheRefreshCancellationOutranksCompletedSuccess(t *testing.T) {
	t.Parallel()
	var memory adapters.MemoryCache
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var calls atomic.Int32
	reader := metadataReaderStub{api: func(_ context.Context, req core.APIRequest) (*core.APIResponse, error) {
		calls.Add(1)
		if req.Endpoint == "/rest/api/3/status" {
			cancel()
			return nil, ctx.Err()
		}
		return metadataResponse("[" + string(fieldMetadata().Items[0]) + "]"), nil
	}}
	out, err := runRefresh(ctx, primerDeps(&memory, reader), command.Invocation{Args: []string{"fields", "statuses", "issuetypes"}}, primeOptions{}, command.KeyedFlags{Parallelism: 1})
	if !errors.Is(err, context.Canceled) || len(out.Results) != 3 || calls.Load() != 2 {
		t.Fatalf("runRefresh interrupted partial success=%+v, %v calls=%d", out, err, calls.Load())
	}
	if !out.Results[0].OK || out.Results[0].Data == nil || !out.Results[0].Data.Saved {
		t.Fatal("runRefresh discarded completed success on interruption")
	}
	for i := 1; i < 3; i++ {
		row := out.Results[i]
		if row.OK || row.Data == nil || row.Err == nil || row.Err.Code != cli.CodeCanceled || row.Data.Saved || row.Data.Attempted != (i == 1) {
			t.Fatalf("runRefresh interrupted row[%d]=%+v", i, row)
		}
	}
}

func TestCacheRefreshClientFailureIsSharedAndRetainsEveryPlan(t *testing.T) {
	t.Parallel()
	var memory adapters.MemoryCache
	cause := cli.NewCodedError(cli.CodeAuthMissing, "credentials unavailable")
	var calls atomic.Int32
	deps := primerDeps(&memory, nil)
	deps.JiraClient = func() (core.JiraClient, error) { calls.Add(1); return nil, cause }
	out, err := runRefresh(t.Context(), deps, command.Invocation{}, primeOptions{}, command.KeyedFlags{Parallelism: 4})
	if !errors.Is(err, cause) || calls.Load() != 1 || len(out.Results) != 10 {
		t.Fatalf("runRefresh missing client=%+v, %v calls=%d", out, err, calls.Load())
	}
	for _, row := range out.Results {
		if row.OK || row.Err == nil || row.Err.Code != cli.CodeAuthMissing || row.Data == nil || !row.Data.WouldFetch || row.Data.Attempted || row.Data.Saved {
			t.Fatalf("runRefresh missing client row=%+v", row)
		}
	}
}

func TestCacheRefreshRetainsPartialPagesWithoutReplacingPreviousEntry(t *testing.T) {
	t.Parallel()
	var memory adapters.MemoryCache
	resource, _ := metadataResource("labels")
	old := []byte(`{"version":1,"items":["old"],"count":1,"pages":1,"complete":true}`)
	if err := memory.Set(resource.Key("https://example.com"), old); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("second page unavailable")
	reader := metadataReaderStub{api: func(_ context.Context, req core.APIRequest) (*core.APIResponse, error) {
		if req.Endpoint == "/rest/api/3/field" {
			return metadataResponse("[" + string(fieldMetadata().Items[0]) + "]"), nil
		}
		if req.Query.Get("startAt") != "0" {
			return nil, cause
		}
		return metadataResponse(`{"startAt":0,"maxResults":100,"isLast":false,"values":["first"]}`), nil
	}}
	out, err := runRefresh(t.Context(), primerDeps(&memory, reader), command.Invocation{Args: []string{"labels", "fields"}}, primeOptions{force: true}, command.KeyedFlags{Parallelism: 2})
	if err != nil || len(out.Results) != 2 {
		t.Fatalf("runRefresh partial pages=%+v, %v", out, err)
	}
	row := out.Results[0]
	if row.OK || row.Err == nil || row.Data == nil || row.Data.Count != 1 || row.Data.Pages != 1 || row.Data.Complete || row.Data.Saved || row.Data.NextStart == nil || *row.Data.NextStart != 1 || string(row.Data.Items[0]) != `"first"` {
		t.Fatalf("runRefresh partial page row=%+v", row)
	}
	stored, _, ok := memory.Get(resource.Key("https://example.com"))
	if !ok || string(stored) != string(old) || !out.Results[1].OK || out.Results[1].Data == nil || !out.Results[1].Data.Saved {
		t.Fatal("runRefresh partial pages changed previous entry or discarded saved sibling")
	}
}

func TestCacheRefreshCanceledAndScopeFailureRetainAllPlans(t *testing.T) {
	t.Parallel()
	for _, canceled := range []bool{true, false} {
		name := "scope failure"
		if canceled {
			name = "already canceled"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cause := errors.New("scope unavailable")
			if canceled {
				cancel()
				cause = context.Canceled
			}
			var scopes, clients atomic.Int32
			deps := PrimeDeps{Scope: func(bool) (MetadataScope, error) { scopes.Add(1); return MetadataScope{}, cause }, JiraClient: func() (core.JiraClient, error) { clients.Add(1); return nil, nil }}
			out, err := runRefresh(ctx, deps, command.Invocation{Args: []string{"fields", "labels"}}, primeOptions{}, command.KeyedFlags{Parallelism: 2})
			wantScopes := int32(1)
			if canceled {
				wantScopes = 0
			}
			if !errors.Is(err, cause) || scopes.Load() != wantScopes || clients.Load() != 0 || len(out.Results) != 2 {
				t.Fatalf("runRefresh(%s)=%+v, %v scopes=%d clients=%d", name, out, err, scopes.Load(), clients.Load())
			}
			var names []string
			for _, row := range out.Results {
				names = append(names, row.Resource)
				if row.OK || row.Err == nil || row.Data == nil || row.Data.Attempted || row.Data.Saved {
					t.Fatalf("runRefresh(%s) row=%+v", name, row)
				}
			}
			if !reflect.DeepEqual(names, []string{"fields", "labels"}) {
				t.Fatalf("runRefresh(%s) names=%v", name, names)
			}
		})
	}
}
