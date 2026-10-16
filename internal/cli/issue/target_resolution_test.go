package issue

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/prompt"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type duplicateTargetSearch struct {
	fakeSelectorClient
	laterPage bool
	reads     int
}

func (f *duplicateTargetSearch) SearchIssues(_ context.Context, jql string, fields []string, token string, size int, _ ...string) (core.IssueSearchRead, error) {
	f.reads++
	if jql != "project = KEY" || !slices.Equal(fields, []string{"key"}) || (f.reads == 1 && (token != "" || size != 101)) || (f.reads == 2 && (token != "next" || size != 100)) {
		return core.IssueSearchRead{}, errors.New("unexpected independent selector request")
	}
	keys := []jira.IssueBean{{Key: jira.NullableOf("KEY-1")}}
	last, next := true, ""
	if f.laterPage && f.reads == 1 {
		last, next = false, "next"
	} else if !f.laterPage {
		keys = append(keys, jira.IssueBean{Key: jira.NullableOf("KEY-1")})
	}
	return core.IssueSearchRead{Results: &jira.SearchResults{Issues: jira.NullableOf(keys), IsLast: jira.NullableOf(last), NextPageToken: jira.NullableOf(next)}}, nil
}

func TestDuplicateRemoteSelectorRefusesWithoutPartialTargetsOrWrites(t *testing.T) {
	for _, later := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-page", true: "later-page"}[later], func(t *testing.T) {
			fake := &duplicateTargetSearch{laterPage: later}
			newClient := func(context.Context) (core.JiraClient, error) { return fake, nil }
			selector := command.SelectorFlags{JQL: "project = KEY"}
			client, targets, err := command.ResolveTargets(t.Context(), newClient, selector, nil, false, false, nil, false)
			if cli.MapError(err).Code != cli.CodeSelectorIncomplete || client != nil || len(targets.Keys) != 0 || targets.Summary != nil {
				t.Fatalf("remote duplicates published partial selection: %+v %v", targets, err)
			}
			fake.reads = 0
			out, err := runDelete(t.Context(), Deps{JiraClient: newClient, Confirm: func(context.Context, prompt.Confirm) (bool, error) {
				t.Fatal("duplicate selector reached confirmation")
				return false, nil
			}}, invocation(false), deleteOptions{selector: selector, force: true, parallelism: 1})
			wantReads := 1
			if later {
				wantReads = 2
			}
			if cli.MapError(err).Code != cli.CodeSelectorIncomplete || len(out.Results) != 0 || len(fake.timeline()) != 0 || fake.reads != wantReads {
				t.Fatalf("remote duplicate selector reached a writer or lost refusal: reads=%d events=%v out=%+v err=%v", fake.reads, fake.timeline(), out, err)
			}
		})
	}
}

func TestTransitionExpandsOnlyExtractedIssueKeys(t *testing.T) {
	for _, grammar := range []string{"positional", "flag", "native-input"} {
		t.Run(grammar, func(t *testing.T) {
			fake := &fakeTransitionClient{screens: map[string]bool{"KEY-1": true, "KEY-2": true, "KEY-3": true}}
			opts := transitionOpts(t, false)
			opts.parallelism = 1
			args := []string{"KEY-2:3", "KEY-1..KEY-2", "KEY-0003"}
			switch grammar {
			case "positional":
				args = append(args, "Done")
			case "flag":
				opts.flagTransition = "Done"
			case "native-input":
				opts.body.JSONInput = "-"
			}
			inv := invocation(false, args...)
			if grammar == "native-input" {
				inv.Stdin = strings.NewReader(`{"transition":{"name":"Done"}}`)
			}
			out, err := runTransition(t.Context(), transitionDeps(fake), inv, opts)
			want := []string{"KEY-2", "KEY-3", "KEY-1"}
			var got []string
			for _, row := range out.Results {
				got = append(got, row.Issue.Key)
				if !row.OK || row.Err != nil || row.Data == nil {
					t.Fatalf("normalized transition target failed: %+v", row)
				}
			}
			if err != nil || !slices.Equal(got, want) || !slices.Equal(fake.listed, want) || len(fake.moved) != 3 {
				t.Fatalf("transition lost ordered unique issue targets: got=%v listed=%v writes=%d err=%v", got, fake.listed, len(fake.moved), err)
			}
			for i, move := range fake.moved {
				if move.key != want[i] || move.req.Transition.ID != "31" {
					t.Fatalf("status was parsed as an issue or wrong transition sent: %+v", move)
				}
			}
		})
	}
}

func TestEditInputUsesOrderedUniqueExplicitTargets(t *testing.T) {
	t.Parallel()
	var observed []string
	client := &inputMetadataClient{edit: func(_ context.Context, key string) (core.IssueEditMetadataRead, error) {
		observed = append(observed, key)
		return core.IssueEditMetadataRead{Native: inputEditDocument(`"summary":{"key":"summary","name":"Summary","required":false,"schema":{"type":"string"},"operations":["set"]}`)}, nil
	}}
	out, err := runEditInput(t.Context(), inputMetadataDeps(client), invocation(false, "KEY-2:3", "KEY-1..KEY-2", "KEY-0003"), command.SelectorFlags{}, 1)
	want := []string{"KEY-2", "KEY-3", "KEY-1"}
	var got []string
	for _, row := range out.Results {
		got = append(got, row.Issue.Key)
		if !row.OK || row.Data == nil || row.Err != nil {
			t.Fatalf("normalized metadata target failed: %+v", row)
		}
	}
	if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(observed, want) {
		t.Fatalf("edit metadata repeated or changed targets: got=%v reads=%v err=%v", got, observed, err)
	}
}

func TestTransitionAndEditInputRejectLateInvalidTargetBeforeClient(t *testing.T) {
	deps := Deps{
		JiraClient: func(context.Context) (core.JiraClient, error) {
			t.Fatal("invalid target constructed Jira client")
			return nil, errors.New("unexpected client")
		},
		IssueMetadata: func(context.Context) (InputDiscoveryClient, error) {
			t.Fatal("invalid target constructed metadata client")
			return nil, errors.New("unexpected metadata client")
		},
	}
	transition, err := runTransition(t.Context(), deps, invocation(false, "KEY-1", "KEY-2,", "Done"), transitionOpts(t, false))
	if cli.MapError(err).Code != cli.CodeArgValueInvalid || len(transition.Results) != 0 {
		t.Fatalf("transition retained partial invalid targets: %+v %v", transition, err)
	}
	input, err := runEditInput(t.Context(), deps, invocation(false, "KEY-1", "KEY-2,"), command.SelectorFlags{}, 1)
	if cli.MapError(err).Code != cli.CodeArgValueInvalid || len(input.Results) != 0 {
		t.Fatalf("metadata retained partial invalid targets: %+v %v", input, err)
	}
}
