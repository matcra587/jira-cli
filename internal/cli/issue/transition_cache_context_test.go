package issue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/adapters"
	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type contextTransitionClient struct {
	*fakeTransitionClient
	read  func(context.Context, string) (core.TransitionRead, error)
	write func(context.Context, string, jira.TransitionRequest) (core.TransitionWrite, error)
}

func (c contextTransitionClient) GetTransitions(ctx context.Context, key string) (core.TransitionRead, error) {
	return c.read(ctx, key)
}

func (c contextTransitionClient) DoTransition(ctx context.Context, key string, req jira.TransitionRequest) (core.TransitionWrite, error) {
	return c.write(ctx, key, req)
}

func TestTransitionFinalizationUsesCacheResolvedBeforeCancellation(t *testing.T) {
	for _, write := range []bool{false, true} {
		name := "completed failed discovery"
		if write {
			name = "attempted uncertain write"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var memory adapters.MemoryCache
			fixture := &fakeTransitionClient{screens: map[string]bool{"DEMO-1": false, "DEMO-2": false, "DEMO-3": false, "DEMO-4": false}}
			read, err := fixture.GetTransitions(t.Context(), "DEMO-1")
			if err != nil {
				t.Fatal(err)
			}
			observed := time.Now().UTC()
			for _, key := range []string{"DEMO-1", "DEMO-2", "DEMO-3", "DEMO-4"} {
				if err := cachecmd.RecordTransitionHints(t.Context(), &memory, key, read.Transitions, observed); err != nil {
					t.Fatal(err)
				}
			}
			cacheCalls := 0
			opened := false
			requests := 0
			inspect := func() {
				t.Helper()
				requests++
				if !opened {
					t.Error("Jira operation started before cache scope resolved")
				}
			}
			client := contextTransitionClient{
				fakeTransitionClient: fixture,
				read: func(ctx context.Context, key string) (core.TransitionRead, error) {
					inspect()
					switch key {
					case "DEMO-1":
						return core.TransitionRead{}, errors.New("completed native failure")
					case "DEMO-2":
						cancel()
						return core.TransitionRead{}, ctx.Err()
					default:
						return fixture.GetTransitions(ctx, key)
					}
				},
				write: func(ctx context.Context, _ string, _ jira.TransitionRequest) (core.TransitionWrite, error) {
					inspect()
					cancel()
					return core.TransitionWrite{}, ctx.Err()
				},
			}
			deps := Deps{JiraClient: func(context.Context) (core.JiraClient, error) { return client, nil }, Cache: func(ctx context.Context) (core.Cache, error) {
				cacheCalls++
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				opened = true
				return &memory, nil
			}}
			if write {
				opts := transitionOpts(t, false)
				opts.parallelism = 1
				out, err := runTransition(ctx, deps, invocation(false, "DEMO-1", "DEMO-2", "31"), opts)
				if !errors.Is(err, context.Canceled) || len(out.Results) != 2 || out.Results[0].Data == nil || !out.Results[0].Data.Attempted || out.Results[0].Data.Applied || out.Results[1].Data != nil || requests != 1 {
					t.Fatalf("uncertain write outcome changed: error=%v results=%#v requests=%d", err, out.Results, requests)
				}
			} else {
				out, err := runTransitionList(ctx, deps, []string{"DEMO-4", "DEMO-1", "DEMO-2", "DEMO-3"}, 1)
				if !errors.Is(err, context.Canceled) || len(out.Results) != 4 || !out.Results[0].OK || out.Results[1].OK || requests != 3 {
					t.Fatalf("joined discovery outcome changed: error=%v results=%#v requests=%d", err, out.Results, requests)
				}
			}
			if cacheCalls != 1 {
				t.Fatalf("cache scope resolution count=%d; want one pre-operation resolution", cacheCalls)
			}
			if _, _, state := cachecmd.TransitionHints(&memory, "DEMO-1"); state != "missing" {
				t.Fatal("later cancellation retained a known invalid hint")
			}
			for _, key := range []string{"DEMO-2", "DEMO-3", "DEMO-4"} {
				_, captured, state := cachecmd.TransitionHints(&memory, key)
				if state != "fresh" || !captured.Equal(observed) {
					t.Fatalf("canceled/unstarted observation modified %s: state=%s captured=%v", key, state, captured)
				}
			}
		})
	}
}
