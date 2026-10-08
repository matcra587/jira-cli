package issue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/adapters"
	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestTransitionListRecordsOnlySuccessfulIssueHints(t *testing.T) {
	t.Parallel()
	var memory adapters.MemoryCache
	client := &fakeTransitionClient{screens: map[string]bool{"DEMO-1": false}}
	previous, err := client.GetTransitions(t.Context(), "DEMO-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := cachecmd.RecordTransitionHints(t.Context(), &memory, "DEMO-2", previous.Transitions, time.Now()); err != nil {
		t.Fatal(err)
	}
	deps := Deps{
		JiraClient: func() (core.JiraClient, error) { return client, nil },
		Cache:      func() (core.Cache, error) { return &memory, nil },
	}
	out, err := runTransitionList(t.Context(), deps, []string{"DEMO-1", "DEMO-2"}, 1)
	if err != nil || len(out.Results) != 2 || !out.Results[0].OK || out.Results[1].OK || out.Results[1].Err == nil {
		t.Fatal("discovery did not retain its actual ordered success and failure")
	}
	hints, observed, state := cachecmd.TransitionHints(&memory, "DEMO-1")
	if state != "fresh" || observed.IsZero() || len(hints) != 2 || hints[0].ID != "11" || hints[1].ID != "31" {
		t.Fatalf("successful discovery did not produce issue hints: %s %v %#v", state, observed, hints)
	}
	if _, _, state := cachecmd.TransitionHints(&memory, "DEMO-2"); state != "missing" {
		t.Fatal("failed discovery retained previous workflow hints")
	}
}

func TestTransitionAttemptInvalidatesHintsWithoutChangingOutcome(t *testing.T) {
	for _, failure := range []bool{false, true} {
		name := "acknowledged"
		if failure {
			name = "uncertain failure"
		}
		t.Run(name, func(t *testing.T) {
			var memory adapters.MemoryCache
			client := &fakeTransitionClient{screens: map[string]bool{"DEMO-1": false}}
			read, err := client.GetTransitions(t.Context(), "DEMO-1")
			if err != nil {
				t.Fatal(err)
			}
			if err := cachecmd.RecordTransitionHints(t.Context(), &memory, "DEMO-1", read.Transitions, time.Now()); err != nil {
				t.Fatal(err)
			}
			cause := errors.New("synthetic uncertain write")
			if failure {
				client.transitionErr = map[string]error{"DEMO-1": cause}
			}
			deps := Deps{
				JiraClient: func() (core.JiraClient, error) { return client, nil },
				Cache:      func() (core.Cache, error) { return &memory, nil },
			}
			out, err := runTransition(t.Context(), deps, invocation(false, "DEMO-1", "Done"), transitionOpts(t, false))
			if len(out.Results) != 1 || out.Results[0].Data == nil || !out.Results[0].Data.Attempted || failure && !errors.Is(err, cause) || !failure && err != nil {
				t.Fatalf("transition outcome changed: err=%v results=%#v", err, out.Results)
			}
			if _, _, state := cachecmd.TransitionHints(&memory, "DEMO-1"); state != "missing" {
				t.Fatal("attempted transition retained possibly stale workflow hints")
			}
		})
	}
}

func TestTransitionHintsCancellationStillInvalidatesCompletedFailures(t *testing.T) {
	t.Parallel()
	var memory adapters.MemoryCache
	client := &fakeTransitionClient{screens: map[string]bool{"DEMO-1": false}}
	read, err := client.GetTransitions(t.Context(), "DEMO-1")
	if err != nil {
		t.Fatal(err)
	}
	observed := time.Now()
	for _, key := range []string{"DEMO-1", "DEMO-2", "DEMO-3", "DEMO-4"} {
		if err := cachecmd.RecordTransitionHints(t.Context(), &memory, key, read.Transitions, observed); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	outcomes := []transitionReadOutcome{
		{result: envelope.KeyedResult[jira.Transitions]{Issue: envelope.IssueRef{Key: "DEMO-1"}}, observed: observed, err: errors.New("completed native failure")},
		{result: envelope.KeyedResult[jira.Transitions]{Issue: envelope.IssueRef{Key: "DEMO-2"}}, observed: observed, err: context.Canceled},
		{result: envelope.KeyedResult[jira.Transitions]{Issue: envelope.IssueRef{Key: "DEMO-3"}}, err: context.Canceled},
		{result: envelope.KeyedResult[jira.Transitions]{Issue: envelope.IssueRef{Key: "DEMO-4"}, OK: true, Data: read.Transitions}, observed: observed},
	}
	recordTransitionHints(ctx, Deps{Cache: func() (core.Cache, error) { return &memory, nil }}, outcomes)
	if _, _, state := cachecmd.TransitionHints(&memory, "DEMO-1"); state != "missing" {
		t.Fatal("cancellation retained a known invalid completed observation")
	}
	for _, key := range []string{"DEMO-2", "DEMO-3", "DEMO-4"} {
		_, captured, state := cachecmd.TransitionHints(&memory, key)
		if state != "fresh" || !captured.Equal(observed) {
			t.Fatalf("canceled discovery modified %s: %s %v", key, state, captured)
		}
	}
}
