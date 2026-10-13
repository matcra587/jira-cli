package issue

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type joinedTransitionReadClient struct{ *fakeTransitionClient }

func (f *joinedTransitionReadClient) GetTransitions(ctx context.Context, key string) (core.TransitionRead, error) {
	read, err := f.fakeTransitionClient.GetTransitions(ctx, key)
	return read, errors.Join(err, context.Canceled)
}

type nilMetadataTransitionListClient struct{ *fakeTransitionClient }

func (f *nilMetadataTransitionListClient) GetTransitions(ctx context.Context, key string) (core.TransitionRead, error) {
	read, err := f.fakeTransitionClient.GetTransitions(ctx, key)
	read.Transitions.Transitions.Value[0].Fields = jira.NullableOf(map[string]json.RawMessage(nil))
	return read, err
}

func TestTransitionListRejectsExplicitNilAlternateMetadata(t *testing.T) {
	t.Parallel()
	client := &nilMetadataTransitionListClient{&fakeTransitionClient{screens: map[string]bool{"KEY-1": false}}}
	out, err := runTransitionList(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, []string{"KEY-1"}, 1)
	if !errors.Is(err, core.ErrTransitionReadInvalid) || len(out.Results) != 1 || out.Results[0].OK || out.Results[0].Data != nil {
		t.Fatal("explicit nil field metadata was published as a usable native object")
	}
}

type deadlineTransitionListClient struct{ *fakeTransitionClient }

func (f *deadlineTransitionListClient) GetTransitions(ctx context.Context, key string) (core.TransitionRead, error) {
	if key == "KEY-2" {
		<-ctx.Done()
		return core.TransitionRead{}, ctx.Err()
	}
	return f.fakeTransitionClient.GetTransitions(ctx, key)
}

func TestTransitionListCommandDeadlineRetainsCompletedAndQueuedTargets(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	client := &deadlineTransitionListClient{&fakeTransitionClient{screens: map[string]bool{"KEY-1": false}}}
	out, err := runTransitionList(ctx, Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, []string{"KEY-1", "KEY-2", "KEY-3"}, 1)
	if !errors.Is(err, context.DeadlineExceeded) || cli.MapError(err).Code != "timeout" || len(out.Results) != 3 {
		t.Fatal("command deadline lost original cause or ordered targets")
	}
	for index, row := range out.Results {
		if row.Issue.Key != []string{"KEY-1", "KEY-2", "KEY-3"}[index] {
			t.Fatal("deadline reordered targets")
		}
		if index == 0 {
			if !row.OK || row.Data == nil || row.Err != nil {
				t.Fatal("deadline discarded completed workflow")
			}
		} else if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != "timeout" {
			t.Fatal("deadline invented metadata for unfinished reads")
		}
	}
}

func TestTransitionListRetainsValidJoinedReadAndCause(t *testing.T) {
	t.Parallel()
	client := &joinedTransitionReadClient{&fakeTransitionClient{screens: map[string]bool{"KEY-1": false}}}
	out, err := runTransitionList(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, []string{"KEY-1"}, 1)
	if !errors.Is(err, context.Canceled) || len(out.Results) != 1 || out.Results[0].OK || out.Results[0].Err == nil || out.Results[0].Err.Code != cli.CodeCanceled || out.Results[0].Data == nil || len(out.Results[0].Data.Transitions.Value) != 2 {
		t.Fatal("joined transition read lost native data or its original cancellation")
	}
}

type emptyTransitionListClient struct{ *fakeTransitionClient }

func (*emptyTransitionListClient) GetTransitions(context.Context, string) (core.TransitionRead, error) {
	return core.TransitionRead{Transitions: &jira.Transitions{Transitions: jira.NullableOf([]jira.IssueTransition{})}}, nil
}

func TestTransitionListAcceptsExplicitEmptyNativeList(t *testing.T) {
	t.Parallel()
	client := &emptyTransitionListClient{&fakeTransitionClient{}}
	out, err := runTransitionList(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}, []string{"KEY-1"}, 1)
	if err != nil || len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Data == nil || out.Results[0].Data.Transitions.Value == nil || len(out.Results[0].Data.Transitions.Value) != 0 {
		t.Fatal("an explicit empty transition list was refused or replaced")
	}
}

type unusableTransitionListClient struct{ *fakeTransitionClient }

func (*unusableTransitionListClient) GetTransitions(context.Context, string) (core.TransitionRead, error) {
	return core.TransitionRead{}, nil
}

func TestTransitionListAndMutationRejectUnusableAlternateRead(t *testing.T) {
	opts := transitionOpts(t, true)
	client := &unusableTransitionListClient{&fakeTransitionClient{}}
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}
	out, err := runTransitionList(t.Context(), deps, []string{"KEY-1"}, 1)
	if cli.MapError(err).Code != "transition_read_invalid" || len(out.Results) != 1 || out.Results[0].OK || out.Results[0].Data != nil {
		t.Fatal("alternate transition discovery fabricated a usable workflow")
	}
	move, err := runTransition(t.Context(), deps, invocation(false, "KEY-1", "Done"), opts)
	if cli.MapError(err).Code != "transition_read_invalid" || len(move.Results) != 1 || move.Results[0].OK || len(client.movedKeys()) != 0 || len(client.commentedKeys()) != 0 {
		t.Fatal("unusable alternate discovery authorized a transition or comment")
	}
}

func TestTransitionListPreCancellationRetainsTargetsWithoutClient(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	calls := 0
	out, err := runTransitionList(ctx, Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) {
		calls++
		return nil, errors.New("client must remain lazy")
	}}, []string{"KEY-1", "KEY-2"}, 1)
	if calls != 0 || !errors.Is(err, context.Canceled) || len(out.Results) != 2 {
		t.Fatal("pre-canceled discovery initialized its client or lost requested targets")
	}
	for index, row := range out.Results {
		if row.Issue.Key != []string{"KEY-1", "KEY-2"}[index] || row.OK || row.Data != nil || row.Err == nil || row.Err.Code != cli.CodeCanceled {
			t.Fatal("pre-canceled discovery reordered targets or fabricated native metadata")
		}
	}
}
