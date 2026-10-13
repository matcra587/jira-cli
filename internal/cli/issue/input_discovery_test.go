package issue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/prompt"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

type inputMetadataClient struct {
	unusedJiraClient
	project func(context.Context, string) (core.IssueCreateProjectRead, error)
	types   func(context.Context, string, int, int) (core.IssueMetadataRead, error)
	fields  func(context.Context, string, string, int, int) (core.IssueMetadataRead, error)
	edit    func(context.Context, string) (core.IssueEditMetadataRead, error)
	search  func(context.Context, string, []string, string, int, ...string) (core.IssueSearchRead, error)
}

func (f *inputMetadataClient) GetCreateIssueProject(ctx context.Context, project string) (core.IssueCreateProjectRead, error) {
	return f.project(ctx, project)
}

func (f *inputMetadataClient) GetCreateIssueTypes(ctx context.Context, project string, offset, size int) (core.IssueMetadataRead, error) {
	return f.types(ctx, project, offset, size)
}

func (f *inputMetadataClient) GetCreateIssueFields(ctx context.Context, project, issueType string, offset, size int) (core.IssueMetadataRead, error) {
	return f.fields(ctx, project, issueType, offset, size)
}

func (f *inputMetadataClient) GetEditIssueFields(ctx context.Context, key string) (core.IssueEditMetadataRead, error) {
	return f.edit(ctx, key)
}

func (f *inputMetadataClient) SearchIssues(ctx context.Context, query string, fields []string, cursor string, size int, expand ...string) (core.IssueSearchRead, error) {
	return f.search(ctx, query, fields, cursor, size, expand...)
}

func inputMetadataDeps(client InputDiscoveryClient) Deps {
	return Deps{IssueMetadata: func(ctx context.Context) (InputDiscoveryClient, error) { return client, nil }}
}

const inputNativeField = `{"fieldId":"customfield_14002","key":"displayKey","name":"Plugin choice","schema":{"type":"future","custom":"plugin:choice","future":null},"required":true,"operations":["set"],"allowedValues":[{"id":"7001","future":{"n":9007199254740993}}],"defaultValue":{"id":"7001"},"future":"\u003c&"}`

func inputPage(member string, offset, total int, entries ...string) core.IssueMetadataRead {
	return core.IssueMetadataRead{Page: &core.IssueMetadataPage{
		Native: json.RawMessage(fmt.Sprintf(`{"startAt":%d,"maxResults":50,"total":%d,%q:[%s],"future":null}`, offset, total, member, strings.Join(entries, ","))),
	}}
}

func inputEditDocument(field string) json.RawMessage {
	return json.RawMessage(`{"fields":{` + field + `},"future":{"n":9007199254740993}}`)
}

func TestCreateInputUsesActualContextAndEveryNativePage(t *testing.T) {
	t.Parallel()
	for _, project := range []string{"KEY", "10402"} {
		t.Run(project, func(t *testing.T) {
			t.Parallel()
			var typeOffsets, fieldOffsets []int
			const nativeProject = `{"id":"10402","key":"KEY","future":null}`
			const nativeType = `{"id":"10002","name":"Task","future":{"n":9007199254740993}}`
			client := &inputMetadataClient{
				project: func(_ context.Context, selected string) (core.IssueCreateProjectRead, error) {
					if selected != project {
						t.Errorf("project selector changed: %q", selected)
					}
					return core.IssueCreateProjectRead{ID: "fabricated", Key: "fabricated", Native: json.RawMessage(nativeProject)}, nil
				},
				types: func(_ context.Context, actual string, offset, size int) (core.IssueMetadataRead, error) {
					if actual != "10402" || size != 50 {
						t.Errorf("type discovery guessed a project identity or page size: %q %d", actual, size)
					}
					typeOffsets = append(typeOffsets, offset)
					if offset == 0 {
						return inputPage("issueTypes", 0, 2, `{"id":"10001","name":"Bug"}`), nil
					}
					return inputPage("issueTypes", offset, 2, nativeType), nil
				},
				fields: func(_ context.Context, actual, actualType string, offset, size int) (core.IssueMetadataRead, error) {
					if actual != "10402" || actualType != "10002" || size != 50 {
						t.Errorf("field discovery used unresolved context: %q %q %d", actual, actualType, size)
					}
					fieldOffsets = append(fieldOffsets, offset)
					if offset == 0 {
						return inputPage("fields", 0, 2, inputNativeField), nil
					}
					return inputPage("fields", offset, 2, `{"fieldId":"summary","key":"summary","name":"Summary","schema":{"type":"string"},"required":true,"operations":["set"]}`), nil
				},
			}
			out, err := runCreateInput(t.Context(), inputMetadataDeps(client), project, "tAsK")
			if err != nil || !out.Complete || out.Fields == nil || len(*out.Fields) != 2 || string(out.Project) != nativeProject || string(out.IssueType) != nativeType || string((*out.Fields)["customfield_14002"]) != inputNativeField {
				t.Fatalf("complete native discovery lost context or data: %+v %v", out, err)
			}
			if !reflect.DeepEqual(typeOffsets, []int{0, 1}) || !reflect.DeepEqual(fieldOffsets, []int{0, 1}) || len(out.TypePages) != 2 || len(out.FieldPages) != 2 {
				t.Fatalf("short nonfinal pages were mistaken for completion: %v %v", typeOffsets, fieldOffsets)
			}
			payload := decodePayload(t, out.Payload)
			if !reflect.DeepEqual(payload["fields"]["project"], map[string]any{"id": "10402"}) || !reflect.DeepEqual(payload["fields"]["issuetype"], map[string]any{"id": "10002"}) {
				t.Fatal("skeleton guessed project/type identities")
			}
			if value, exists := payload["fields"]["customfield_14002"]; !exists || value != nil || len(payload["fields"]) != 4 {
				t.Fatal("skeleton chose a plugin default/value or used its display key")
			}
		})
	}
}

func TestCreateInputRetainsResolutionCandidatesWithoutChoosing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, requested, code string
		entries               []string
	}{
		{name: "unknown", requested: "missing", code: cli.CodeIssueTypeUnknown, entries: []string{`{"id":"10001","name":"Task"}`}},
		{name: "ambiguous", requested: "Task", code: cli.CodeIssueTypeAmbiguous, entries: []string{`{"id":"10001","name":"Task"}`, `{"id":"10002","name":"TASK"}`}},
		{name: "empty", requested: "Task", code: cli.CodeIssueTypeUnknown, entries: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := &inputMetadataClient{
				project: func(context.Context, string) (core.IssueCreateProjectRead, error) {
					return core.IssueCreateProjectRead{Native: json.RawMessage(`{"id":"10402","key":"KEY"}`)}, nil
				},
				types: func(context.Context, string, int, int) (core.IssueMetadataRead, error) {
					return inputPage("issueTypes", 0, len(tc.entries), tc.entries...), nil
				},
				fields: func(context.Context, string, string, int, int) (core.IssueMetadataRead, error) {
					t.Error("unresolved issue type reached field discovery")
					return core.IssueMetadataRead{}, errors.New("unexpected field discovery")
				},
			}
			out, err := runCreateInput(t.Context(), inputMetadataDeps(client), "KEY", tc.requested)
			class := cli.MapError(err)
			if class.Code != tc.code || class.Field != "type" || out.Complete || out.Fields != nil || out.Payload != nil || len(out.Project) == 0 || out.IssueTypes == nil || len(*out.IssueTypes) != len(tc.entries) {
				t.Fatalf("resolution failure fabricated a choice or lost candidates: %+v %+v", out, class)
			}
			raw, marshalErr := json.Marshal(out)
			if marshalErr != nil || (len(tc.entries) == 0 && !strings.Contains(string(raw), `"issue_types":[]`)) {
				t.Fatalf("known empty candidates collapsed into unknown: %s %v", raw, marshalErr)
			}
		})
	}
}

func TestInputDiscoveryRefusesAuthoredOptionsBeforeClientOrInput(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		option, canonical string
	}{
		{"--summary=", "summary"},
		{"--json-input=/missing/input.json", "json-input"},
		{"--from-json=", "json-input"},
		{"--body-adf=-", "json-input"},
		{"--markdown=", "markdown"},
		{"--body=", "markdown"},
		{"--description=", "markdown"},
		{"--markdown-file=/missing/body.md", "markdown-file"},
		{"--template=-", "markdown-file"},
		{"--description-file=", "markdown-file"},
		{"--body-file=", "markdown-file"},
		{"--dry-run=false", "dry-run"},
		{"--verify=false", "verify"},
		{"--validate-remote=false", "validate-remote"},
		{"--labels=", "label"},
	} {
		for _, verb := range []string{"create", "edit"} {
			t.Run(verb+" "+tc.option, func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				deps := Deps{IssueMetadata: func(ctx context.Context) (InputDiscoveryClient, error) {
					calls.Add(1)
					return nil, errors.New("unexpected client")
				}, JiraClient: func(ctx context.Context) (core.JiraClient, error) {
					calls.Add(1)
					return nil, errors.New("unexpected client")
				}}
				cmd := newCreate(deps)
				args := []string{"--generate-json", "--project=KEY", "--type=Task", tc.option}
				var keys []string
				if verb == "edit" {
					cmd = newEdit(deps)
					args = []string{"--generate-json", tc.option, "KEY-1"}
					keys = []string{"KEY-1"}
				}
				if err := cmd.ParseFlags(args); err != nil {
					t.Fatal(err)
				}
				cmd.SetContext(t.Context())
				class := cli.MapError(cmd.RunE(cmd, keys))
				if calls.Load() != 0 || class.Code != cli.CodeFlagNotApplicable || class.Field != tc.canonical {
					t.Fatalf("ignored option reached I/O or wrong refusal: calls=%d %+v", calls.Load(), class)
				}
			})
		}
	}
}

func TestCreateInputRequiresContextBeforeClient(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--generate-json"}, {"--generate-json", "--project=KEY"}, {"--generate-json", "--project= \t", "--type=Task"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			cmd := newCreate(Deps{IssueMetadata: func(ctx context.Context) (InputDiscoveryClient, error) {
				t.Error("missing context reached the authenticated client")
				return nil, core.ErrIssueMetadataUnavailable
			}})
			if err := cmd.ParseFlags(args); err != nil {
				t.Fatal(err)
			}
			cmd.SetContext(t.Context())
			if class := cli.MapError(cmd.RunE(cmd, nil)); class.Code != cli.CodeFlagValueInvalid {
				t.Fatalf("missing context not diagnosed before I/O: %+v", class)
			}
		})
	}
}

func TestEditInputPreservesSeparateContextsAndOrderedPartialFailures(t *testing.T) {
	t.Parallel()
	first := inputEditDocument(`"customfield_14002":` + inputNativeField)
	second := inputEditDocument(`"summary":{"key":"summary","name":"Summary","required":false,"schema":{"type":"string"},"operations":["set"],"defaultValue":"do not choose"}`)
	client := &inputMetadataClient{edit: func(_ context.Context, key string) (core.IssueEditMetadataRead, error) {
		switch key {
		case "KEY-1":
			return core.IssueEditMetadataRead{Native: first, Fields: map[string]json.RawMessage{"fabricated": json.RawMessage(`{}`)}}, nil
		case "KEY-3":
			return core.IssueEditMetadataRead{Native: second}, nil
		default:
			return core.IssueEditMetadataRead{}, &adapterjira.APIError{StatusCode: 404}
		}
	}}
	keys := []string{"KEY-1", "KEY-2", "KEY-3"}
	out, err := runEditInput(t.Context(), inputMetadataDeps(client), command.Invocation{Args: keys}, command.SelectorFlags{}, 2)
	if err != nil || len(out.Results) != len(keys) || out.Selector != nil {
		t.Fatalf("partial discovery lost outcomes or failed entire run: %+v %v", out, err)
	}
	for i, row := range out.Results {
		if row.Issue.Key != keys[i] || row.OK != (i != 1) {
			t.Fatalf("issue order or individual verdict lost: %+v", row)
		}
		if i == 1 {
			if row.Err == nil || row.Err.Code != "jira_not_found" || row.Data != nil {
				t.Fatal("failed context acquired another issue's skeleton")
			}
			continue
		}
		wantField, wantNative := "customfield_14002", first
		if i == 2 {
			wantField, wantNative = "summary", second
		}
		if row.Data == nil || string(row.Data.Metadata) != string(wantNative) || len(row.Data.Fields) != 1 {
			t.Fatal("native issue context was merged, dropped or replaced by fabricated side facts")
		}
		var wanted struct {
			Fields map[string]json.RawMessage `json:"fields"`
		}
		if json.Unmarshal(wantNative, &wanted) != nil || !reflect.DeepEqual(row.Data.Fields, wanted.Fields) {
			t.Fatal("edit field definitions differ from the issue's complete native metadata")
		}
		payload := decodePayload(t, row.Data.Payload)
		if value, exists := payload["fields"][wantField]; !exists || value != nil || len(payload["fields"]) != 1 {
			t.Fatal("edit skeleton guessed values or shared another context")
		}
	}
}

func TestEditInputCancellationBoundsFanoutAndRetainsEveryTarget(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{}, 4)
	var calls, running, maximum atomic.Int32
	client := &inputMetadataClient{edit: func(ctx context.Context, _ string) (core.IssueEditMetadataRead, error) {
		calls.Add(1)
		current := running.Add(1)
		defer running.Add(-1)
		for previous := maximum.Load(); current > previous && !maximum.CompareAndSwap(previous, current); previous = maximum.Load() {
		}
		started <- struct{}{}
		<-ctx.Done()
		return core.IssueEditMetadataRead{}, ctx.Err()
	}}
	type result struct {
		out envelope.IssueEditInputOutput
		err error
	}
	finished := make(chan result, 1)
	keys := []string{"KEY-1", "KEY-2", "KEY-3", "KEY-4"}
	go func() {
		out, err := runEditInput(ctx, inputMetadataDeps(client), command.Invocation{Args: keys}, command.SelectorFlags{}, 2)
		finished <- result{out: out, err: err}
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("metadata fanout did not start both permitted reads")
		}
	}
	cancel()
	select {
	case result := <-finished:
		if !errors.Is(result.err, context.Canceled) || len(result.out.Results) != 4 || maximum.Load() != 2 || calls.Load() != 2 || running.Load() != 0 {
			t.Fatalf("cancellation leaked work, exceeded bound or lost targets: %+v calls=%d maximum=%d running=%d", result, calls.Load(), maximum.Load(), running.Load())
		}
		for i, row := range result.out.Results {
			if row.Issue.Key != keys[i] || row.OK || row.Data != nil || row.Err == nil || row.Err.Code != cli.CodeCanceled {
				t.Fatalf("cancellation fabricated or omitted an outcome: %+v", row)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("metadata fanout failed to join after cancellation")
	}
}

func TestInputSkeletonUsesOnlyAdvertisedNativeOperations(t *testing.T) {
	t.Parallel()
	fields := map[string]json.RawMessage{
		"settable": json.RawMessage(`{"operations":["add","set","remove"],"defaultValue":"do not choose"}`),
		"actions":  json.RawMessage(`{"operations":["add","remove","futureAction"]}`),
		"readonly": json.RawMessage(`{"operations":[]}`),
	}
	raw, err := buildInputSkeleton(fields, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var payload issueInputSkeleton
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Fields) != 1 || string(payload.Fields["settable"]) != "null" || len(payload.Update) != 1 || len(payload.Update["actions"]) != 3 {
		t.Fatalf("skeleton guessed a value or invented an editable route: %s", raw)
	}
	for i, operation := range []string{"add", "remove", "futureAction"} {
		if action := payload.Update["actions"][i]; len(action) != 1 || string(action[operation]) != "null" {
			t.Fatalf("native operation or order lost: %s", raw)
		}
	}
}

func TestInputSkeletonUsesExactNativeOperationKey(t *testing.T) {
	t.Parallel()
	raw, err := buildInputSkeleton(map[string]json.RawMessage{
		"customfield_14002": json.RawMessage(`{"operations":["set"],"Operations":["remove"]}`),
	}, "", "")
	var payload issueInputSkeleton
	if err != nil || json.Unmarshal(raw, &payload) != nil || string(payload.Fields["customfield_14002"]) != "null" || len(payload.Update) != 0 {
		t.Fatalf("unknown case-variant member overrode the exact native operation: payload=%s error=%v", raw, err)
	}
}

func TestEditInputUsesCompleteSelectorAndSameAuthenticatedClient(t *testing.T) {
	t.Parallel()
	var cursors []string
	var constructions, confirmations atomic.Int32
	client := &inputMetadataClient{
		search: func(_ context.Context, query string, fields []string, cursor string, size int, expand ...string) (core.IssueSearchRead, error) {
			if query != "project = KEY" || !reflect.DeepEqual(fields, []string{"key"}) || len(expand) != 0 || size != command.MaxSelectorKeys+1-len(cursors) {
				t.Error("metadata selector changed query, fetched unnecessary fields or lost its bound")
			}
			cursors = append(cursors, cursor)
			key := "KEY-1"
			if cursor != "" {
				key = "KEY-2"
			}
			return core.IssueSearchRead{Results: &jira.SearchResults{
				Issues: jira.NullableOf([]jira.IssueBean{{Key: jira.NullableOf(key)}}),
				IsLast: jira.NullableOf(cursor != ""), NextPageToken: jira.NullableOf("next"),
			}}, nil
		},
		edit: func(context.Context, string) (core.IssueEditMetadataRead, error) {
			return core.IssueEditMetadataRead{Native: inputEditDocument("")}, nil
		},
	}
	deps := inputMetadataDeps(client)
	deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) { constructions.Add(1); return client, nil }
	deps.IssueMetadata = func(ctx context.Context) (InputDiscoveryClient, error) {
		t.Error("selector constructed a second authenticated metadata client")
		return nil, core.ErrIssueMetadataUnavailable
	}
	// An input read must never enter the selector's mutation confirmation.
	deps.Confirm = func(_ context.Context, _ prompt.Confirm) (bool, error) {
		confirmations.Add(1)
		return false, errors.New("unexpected confirmation")
	}
	out, err := runEditInput(t.Context(), deps, command.Invocation{}, command.SelectorFlags{JQL: "project = KEY"}, 2)
	if err != nil || !reflect.DeepEqual(cursors, []string{"", "next"}) || constructions.Load() != 1 || confirmations.Load() != 0 || out.Selector == nil || !out.Selector.Resolved || out.Selector.Kind != "jql" || out.Selector.Query != "project = KEY" || len(out.Results) != 2 {
		t.Fatalf("selector read truncated results, changed authentication or prompted: %+v %v", out, err)
	}
	for i, row := range out.Results {
		if !row.OK || row.Issue.Key != fmt.Sprintf("KEY-%d", i+1) || row.Data == nil || row.Data.Fields == nil || len(row.Data.Fields) != 0 || string(row.Data.Payload) != `{"fields":{}}` {
			t.Fatalf("empty edit fields became unknown or context changed: %+v", row)
		}
	}
}

func TestEditInputReadsExplicitFileTargetsWithoutMutationConfirmation(t *testing.T) {
	t.Parallel()
	client := &inputMetadataClient{edit: func(context.Context, string) (core.IssueEditMetadataRead, error) {
		return core.IssueEditMetadataRead{Native: inputEditDocument("")}, nil
	}}
	deps := inputMetadataDeps(client)
	deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
		t.Error("file selector constructed the query client")
		return nil, core.ErrIssueMetadataUnavailable
	}
	deps.Confirm = func(_ context.Context, _ prompt.Confirm) (bool, error) {
		t.Error("input read requested mutation confirmation")
		return false, errors.New("unexpected confirmation")
	}
	out, err := runEditInput(t.Context(), deps, command.Invocation{Stdin: strings.NewReader("# explicit targets\nKEY-2\n\nKEY-1 # context\n")}, command.SelectorFlags{FromFile: "-"}, 1)
	if err != nil || out.Selector == nil || !out.Selector.Resolved || out.Selector.Kind != "from-file" || len(out.Results) != 2 || out.Results[0].Issue.Key != "KEY-2" || out.Results[1].Issue.Key != "KEY-1" {
		t.Fatalf("file target read lost context or required mutation policy: %+v %v", out, err)
	}
}

func TestInputDiscoveryPreservesConfigurationFailuresAndPreCanceledTargets(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		deps Deps
		code string
	}{
		{name: "missing factory", deps: Deps{}, code: cli.CodeIssueMetadataUnavailable},
		{name: "nil client", deps: inputMetadataDeps(nil), code: cli.CodeIssueMetadataUnavailable},
		{name: "authentication", deps: Deps{IssueMetadata: func(ctx context.Context) (InputDiscoveryClient, error) {
			return nil, cli.NewCodedError(cli.CodeAuthMissing, "missing credentials")
		}}, code: cli.CodeAuthMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			create, err := runCreateInput(t.Context(), tc.deps, "KEY", "Task")
			if cli.MapError(err).Code != tc.code || create.Project != nil || create.Payload != nil || create.Complete {
				t.Fatalf("configuration failure fabricated create facts: %+v %v", create, err)
			}
			edit, err := runEditInput(t.Context(), tc.deps, command.Invocation{Args: []string{"KEY-1", "KEY-2"}}, command.SelectorFlags{}, 2)
			if cli.MapError(err).Code != tc.code || len(edit.Results) != 2 {
				t.Fatalf("configuration failure lost named edit targets: %+v %v", edit, err)
			}
			for _, row := range edit.Results {
				if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != tc.code {
					t.Fatalf("configuration failure became metadata success: %+v", row)
				}
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	deps := Deps{IssueMetadata: func(ctx context.Context) (InputDiscoveryClient, error) {
		t.Error("canceled input discovery opened client")
		return nil, core.ErrIssueMetadataUnavailable
	}}
	create, err := runCreateInput(ctx, deps, "KEY", "Task")
	if !errors.Is(err, context.Canceled) || create.Complete || create.Project != nil {
		t.Fatal("pre-canceled create read performed work or hid cancellation")
	}
	edit, err := runEditInput(ctx, deps, command.Invocation{Args: []string{"KEY-1"}}, command.SelectorFlags{}, 1)
	if !errors.Is(err, context.Canceled) || len(edit.Results) != 1 || edit.Results[0].Err == nil || edit.Results[0].Err.Code != cli.CodeCanceled {
		t.Fatal("pre-canceled edit lost the named target or cancellation")
	}
}

func TestEditInputTotalFailureRanksProtocolAboveMissingIssue(t *testing.T) {
	t.Parallel()
	client := &inputMetadataClient{edit: func(_ context.Context, key string) (core.IssueEditMetadataRead, error) {
		if key == "KEY-1" {
			return core.IssueEditMetadataRead{}, &adapterjira.APIError{StatusCode: 404}
		}
		return core.IssueEditMetadataRead{Native: json.RawMessage(`{"fields":null}`)}, nil
	}}
	out, err := runEditInput(t.Context(), inputMetadataDeps(client), command.Invocation{Args: []string{"KEY-1", "KEY-2"}}, command.SelectorFlags{}, 1)
	if cli.MapError(err).Code != cli.CodeIssueMetadataResponseInvalid || len(out.Results) != 2 || out.Results[0].Err.Code != "jira_not_found" || out.Results[1].Err.Code != cli.CodeIssueMetadataResponseInvalid {
		t.Fatalf("protocol failure was demoted or total outcomes lost: %+v %v", out, err)
	}
}
