package cache

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/adapters"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
)

func TestCachePrimeProgressFollowsActualReadAndSave(t *testing.T) {
	for _, name := range []string{"preview", "fresh", "saved", "read failure", "save failure"} {
		t.Run(name, func(t *testing.T) {
			var memory adapters.MemoryCache
			resource, _ := metadataResource("fields")
			old, err := encodeMetadata("fields", fieldMetadata(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if name != "preview" {
				if err := memory.Set(resource.Key("https://example.com"), old); err != nil {
					t.Fatal(err)
				}
			}
			cause := errors.New("fixture failure")
			store := &primerStore{Cache: &memory}
			if name == "save failure" {
				store.setErr = cause
			}
			calls := 0
			reader := metadataReaderStub{api: func(context.Context, core.APIRequest) (*core.APIResponse, error) {
				calls++
				if name == "read failure" {
					return nil, cause
				}
				return metadataResponse("[" + string(fieldMetadata().Items[0]) + "]"), nil
			}}
			var phases []core.ProgressPhase
			ctx := core.WithProgressObserver(t.Context(), func(progress core.Progress) { phases = append(phases, progress.Phase) })
			out, err := runPrime(ctx, primerDeps(store, reader), command.Invocation{DryRun: name == "preview"}, resource, primeOptions{force: name != "fresh"})
			want := []core.ProgressPhase{core.ProgressCacheInspect}
			switch name {
			case "preview":
				want = append(want, core.ProgressCachePreview)
			case "fresh":
				want = append(want, core.ProgressCacheReused)
			case "saved":
				want = append(want, core.ProgressCacheRead, core.ProgressCacheSave, core.ProgressCacheSaved)
			case "read failure":
				want = append(want, core.ProgressCacheRead)
			case "save failure":
				want = append(want, core.ProgressCacheRead, core.ProgressCacheSave)
			}
			if !slices.Equal(phases, want) || out.Saved != (name == "saved") {
				t.Fatalf("progress invented a fetch or save: %v, %+v, %v", phases, out, err)
			}
			if name == "fresh" || name == "preview" {
				if calls != 0 || store.sets != 0 || err != nil {
					t.Fatalf("local path performed remote/storage work: %d, %d, %v", calls, store.sets, err)
				}
			} else if calls != 1 || (err != nil) != (name != "saved") {
				t.Fatalf("read policy changed: %d, %v", calls, err)
			}
		})
	}
}

func TestCacheRefreshProgressCountsResourceResultsWithoutNestedStages(t *testing.T) {
	var memory adapters.MemoryCache
	var calls atomic.Int32
	reader := metadataReaderStub{api: func(_ context.Context, request core.APIRequest) (*core.APIResponse, error) {
		calls.Add(1)
		if request.Endpoint == "/rest/api/3/status" {
			return nil, errors.New("fixture read failure")
		}
		return metadataResponse("[" + string(fieldMetadata().Items[0]) + "]"), nil
	}}
	var observed []core.Progress
	ctx := core.WithProgressObserver(t.Context(), func(progress core.Progress) { observed = append(observed, progress) })
	out, err := runRefresh(ctx, primerDeps(&memory, reader), command.Invocation{Args: []string{"fields", "statuses"}}, primeOptions{force: true}, command.KeyedFlags{Parallelism: 2})
	if err != nil || calls.Load() != 2 || len(out.Results) != 2 || !out.Results[0].OK || out.Results[1].OK {
		t.Fatalf("resource policy changed: %+v, %v", out, err)
	}
	if len(observed) != 3 {
		t.Fatalf("nested resource stages overwrote aggregate progress: %+v", observed)
	}
	for i, progress := range observed {
		if progress.Phase != core.ProgressCacheResources || progress.Total != 2 || progress.Completed != int64(i) {
			t.Fatalf("resource progress regressed or changed units: %+v", observed)
		}
	}
	if !observed[2].Complete || observed[2].Failed != 1 {
		t.Fatalf("refresh lost its partial failure: %+v", observed[2])
	}
}
