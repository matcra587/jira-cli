package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

type metadataFixtureClient struct {
	types  func(context.Context, string, int, int) (IssueMetadataRead, error)
	fields func(context.Context, string, string, int, int) (IssueMetadataRead, error)
}

func (c metadataFixtureClient) GetCreateIssueTypes(ctx context.Context, project string, startAt, maxResults int) (IssueMetadataRead, error) {
	return c.types(ctx, project, startAt, maxResults)
}

func (c metadataFixtureClient) GetCreateIssueFields(ctx context.Context, project, typeID string, startAt, maxResults int) (IssueMetadataRead, error) {
	return c.fields(ctx, project, typeID, startAt, maxResults)
}

func (metadataFixtureClient) GetEditIssueFields(context.Context, string) (IssueEditMetadataRead, error) {
	return IssueEditMetadataRead{}, errors.New("unexpected edit metadata request")
}

func metadataTestRead(native string) IssueMetadataRead {
	// A seam may supply decoded facts that disagree with its native source.
	// Discovery must establish its contract from the actual document instead.
	return IssueMetadataRead{Page: &IssueMetadataPage{Native: json.RawMessage(native)}}
}

func TestIssueMetadataDiscoveryWalksShortPagesAndResolvesNames(t *testing.T) {
	t.Parallel()
	var typeStarts, fieldStarts []int
	const selected = `{"id":"10002","name":"Task","future":{"big":9007199254740993}}`
	const definition = `{"fieldId":"customfield_10123","key":"customfield_10123","name":"Future field","operations":[],"required":true,"schema":{"type":"future"},"allowedValues":[],"defaultValue":null,"future":{"spelling":"\u003c"}}`
	client := metadataFixtureClient{
		types: func(ctx context.Context, project string, start, size int) (IssueMetadataRead, error) {
			if ctx != t.Context() || project != "ENG" || size != 50 {
				t.Error("type discovery changed context/project/page bound")
			}
			typeStarts = append(typeStarts, start)
			switch start {
			case 0:
				return metadataTestRead(`{"startAt":0,"maxResults":2,"total":2,"issueTypes":[{"id":"10001","name":"Bug"}],"future":null}`), nil
			case 1:
				return metadataTestRead(`{"startAt":1,"maxResults":2,"total":2,"issueTypes":[` + selected + `]}`), nil
			default:
				t.Errorf("wrong type continuation: %d", start)
				return IssueMetadataRead{}, errors.New("unexpected type offset")
			}
		},
		fields: func(ctx context.Context, project, typeID string, start, size int) (IssueMetadataRead, error) {
			if ctx != t.Context() || project != "ENG" || typeID != "10002" || size != 50 {
				t.Error("field discovery used a name, wrong project or lost context")
			}
			fieldStarts = append(fieldStarts, start)
			switch start {
			case 0:
				return metadataTestRead(`{"startAt":0,"maxResults":3,"total":3,"fields":[{"fieldId":"summary","key":"summary","name":"Summary","required":true,"operations":["set"],"schema":{"type":"string"}}]}`), nil
			case 1:
				return metadataTestRead(`{"startAt":1,"maxResults":3,"total":3,"fields":[` + definition + `,{"fieldId":"description","key":"description","name":"Description","required":false,"operations":["set"],"schema":{"type":"string"},"defaultValue":null}],"future":[]}`), nil
			default:
				t.Errorf("wrong field continuation: %d", start)
				return IssueMetadataRead{}, errors.New("unexpected field offset")
			}
		},
	}
	result, err := DiscoverCreateIssueMetadata(t.Context(), client, "ENG", "task")
	if err != nil || result == nil {
		t.Fatalf("discovery: %v", err)
	}
	if len(typeStarts) != 2 || typeStarts[0] != 0 || typeStarts[1] != 1 || len(fieldStarts) != 2 || fieldStarts[0] != 0 || fieldStarts[1] != 1 {
		t.Fatalf("incomplete walk: types=%v fields=%v", typeStarts, fieldStarts)
	}
	if result.Project != "ENG" || result.IssueTypeID != "10002" || string(result.IssueType) != selected || len(result.Fields) != 3 || string(result.Fields["customfield_10123"]) != definition || len(result.TypePages) != 2 || len(result.FieldPages) != 2 {
		t.Fatalf("context/native metadata lost: %+v", result)
	}
}

func TestIssueMetadataDiscoveryRefusesAmbiguousAndUnknownTypes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, request string
		wantAmbiguous bool
		wantIDs       []string
	}{
		{name: "ambiguous-across-pages", request: "Task", wantAmbiguous: true, wantIDs: []string{"10001", "10002"}},
		{name: "unknown-shows-all-types", request: "Story", wantAmbiguous: false, wantIDs: []string{"10001", "10002"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fieldCalls, typeCalls := 0, 0
			client := metadataFixtureClient{
				types: func(_ context.Context, _ string, start, _ int) (IssueMetadataRead, error) {
					typeCalls++
					if start == 0 {
						return metadataTestRead(`{"startAt":0,"maxResults":1,"total":2,"issueTypes":[{"id":"10001","name":"Task"}]}`), nil
					}
					return metadataTestRead(`{"startAt":1,"maxResults":1,"total":2,"issueTypes":[{"id":"10002","name":"TASK"}]}`), nil
				},
				fields: func(context.Context, string, string, int, int) (IssueMetadataRead, error) {
					fieldCalls++
					return IssueMetadataRead{}, errors.New("unexpected fields request")
				},
			}
			result, err := DiscoverCreateIssueMetadata(t.Context(), client, "ENG", tc.request)
			var selection *IssueTypeResolutionError
			if result != nil || !errors.As(err, &selection) || selection.Ambiguous != tc.wantAmbiguous || selection.Requested != tc.request || len(selection.Candidates) != len(tc.wantIDs) || typeCalls != 2 || fieldCalls != 0 {
				t.Fatalf("unsafe/incomplete selection: result=%+v err=%v types=%d fields=%d", result, err, typeCalls, fieldCalls)
			}
			for i, raw := range selection.Candidates {
				var candidate struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(raw, &candidate) != nil || candidate.ID != tc.wantIDs[i] {
					t.Fatalf("candidate identities/order changed: %s", raw)
				}
			}
		})
	}
}

func TestIssueMetadataDiscoveryExplicitIDWinsOverDisplayName(t *testing.T) {
	t.Parallel()
	client := metadataFixtureClient{
		types: func(context.Context, string, int, int) (IssueMetadataRead, error) {
			return metadataTestRead(`{"startAt":0,"maxResults":50,"total":2,"issueTypes":[{"id":"10001","name":"Task"},{"id":"10002","name":"10001"}]}`), nil
		},
		fields: func(_ context.Context, _, id string, _, _ int) (IssueMetadataRead, error) {
			if id != "10001" {
				t.Fatalf("explicit ID was reinterpreted as a display name: %s", id)
			}
			return metadataTestRead(`{"startAt":0,"maxResults":50,"total":0,"fields":[]}`), nil
		},
	}
	result, err := DiscoverCreateIssueMetadata(t.Context(), client, "ENG", "10001")
	if err != nil || result == nil || result.IssueTypeID != "10001" || result.Fields == nil || len(result.Fields) != 0 || len(result.FieldPages) != 1 {
		t.Fatalf("explicit ID or empty complete metadata lost: %+v %v", result, err)
	}
}

func TestIssueMetadataDiscoveryRejectsContradictoryWalks(t *testing.T) {
	t.Parallel()
	for _, domain := range []string{"types", "fields"} {
		t.Run(domain, func(t *testing.T) {
			t.Parallel()
			member, identity := "issueTypes", "id"
			if domain == "fields" {
				member, identity = "fields", "fieldId"
			}
			for name, second := range map[string]string{
				"changed-total":      fmt.Sprintf(`{"startAt":1,"maxResults":50,"total":3,"%s":[{"%s":"10002","name":"Bug"}]}`, member, identity),
				"repeated-identity":  fmt.Sprintf(`{"startAt":1,"maxResults":50,"total":2,"%s":[{"%s":"10001","name":"Task"}]}`, member, identity),
				"wrong-continuation": fmt.Sprintf(`{"startAt":0,"maxResults":50,"total":2,"%s":[{"%s":"10002","name":"Bug"}]}`, member, identity),
				"empty-nonfinal":     fmt.Sprintf(`{"startAt":1,"maxResults":50,"total":2,"%s":[]}`, member),
				"no-native-document": ``,
			} {
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					walkCalls := 0
					if domain == "fields" {
						switch name {
						case "changed-total":
							second = `{"startAt":1,"maxResults":50,"total":3,"fields":[` + metadataCompleteField("10002") + `]}`
						case "repeated-identity":
							second = `{"startAt":1,"maxResults":50,"total":2,"fields":[` + metadataCompleteField("10001") + `]}`
						case "wrong-continuation":
							second = `{"startAt":0,"maxResults":50,"total":2,"fields":[` + metadataCompleteField("10002") + `]}`
						}
					}
					walk := func(_ context.Context, _ string, start, _ int) (IssueMetadataRead, error) {
						walkCalls++
						if start == 0 {
							first := fmt.Sprintf(`{"startAt":0,"maxResults":50,"total":2,"%s":[{"%s":"10001","name":"Task"}]}`, member, identity)
							if domain == "fields" {
								first = `{"startAt":0,"maxResults":50,"total":2,"fields":[` + metadataCompleteField("10001") + `]}`
							}
							return metadataTestRead(first), nil
						}
						return metadataTestRead(second), nil
					}
					client := metadataFixtureClient{
						types: func(context.Context, string, int, int) (IssueMetadataRead, error) {
							return metadataTestRead(`{"startAt":0,"maxResults":50,"total":1,"issueTypes":[{"id":"10001","name":"Task"}]}`), nil
						},
						fields: func(ctx context.Context, project, _ string, start, size int) (IssueMetadataRead, error) {
							return walk(ctx, project, start, size)
						},
					}
					if domain == "types" {
						client.types = walk
						client.fields = func(context.Context, string, string, int, int) (IssueMetadataRead, error) {
							t.Error("contradictory type discovery dispatched a field request")
							return metadataTestRead(`{"startAt":0,"maxResults":50,"total":0,"fields":[]}`), nil
						}
					}
					result, err := DiscoverCreateIssueMetadata(t.Context(), client, "ENG", "Task")
					if result != nil || !errors.Is(err, ErrIssueMetadataInvalid) {
						t.Fatalf("incomplete walk became a snapshot: %+v %v", result, err)
					}
					if walkCalls != 2 {
						t.Fatalf("contradictory metadata did not stop at its failing page: %d reads", walkCalls)
					}
				})
			}
		})
	}
}

func TestIssueMetadataDiscoveryStopsOnFailureAndCancellation(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"initial-cancel", "blank-project", "blank-type", "type-transport", "nil-type-page", "field-transport", "nil-field-page", "cancel-after-last-field"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			transport := errors.New("controlled metadata transport failure")
			calls := 0
			client := metadataFixtureClient{
				types: func(context.Context, string, int, int) (IssueMetadataRead, error) {
					calls++
					if mode == "type-transport" {
						return IssueMetadataRead{}, transport
					}
					if mode == "nil-type-page" {
						return IssueMetadataRead{}, nil
					}
					return metadataTestRead(`{"startAt":0,"maxResults":50,"total":1,"issueTypes":[{"id":"10001","name":"Task"}]}`), nil
				},
				fields: func(context.Context, string, string, int, int) (IssueMetadataRead, error) {
					calls++
					if mode == "field-transport" {
						return IssueMetadataRead{}, transport
					}
					if mode == "nil-field-page" {
						return IssueMetadataRead{}, nil
					}
					if mode == "cancel-after-last-field" {
						cancel()
					}
					return metadataTestRead(`{"startAt":0,"maxResults":50,"total":0,"fields":[]}`), nil
				},
			}
			project, requested := "ENG", "Task"
			if mode == "initial-cancel" {
				cancel()
			}
			if mode == "blank-project" {
				project = " "
			}
			if mode == "blank-type" {
				requested = " "
			}
			result, err := DiscoverCreateIssueMetadata(ctx, client, project, requested)
			if result != nil || err == nil {
				t.Fatalf("failed discovery returned a snapshot: %+v %v", result, err)
			}
			switch mode {
			case "initial-cancel", "cancel-after-last-field":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation cause lost: %v", err)
				}
			case "type-transport", "field-transport":
				if !errors.Is(err, transport) {
					t.Fatalf("transport cause lost: %v", err)
				}
			case "nil-type-page", "nil-field-page":
				if !errors.Is(err, ErrIssueMetadataInvalid) {
					t.Fatalf("nil metadata lacks invalid-response type: %v", err)
				}
			}
			if (mode == "initial-cancel" || mode == "blank-project" || mode == "blank-type") && calls != 0 {
				t.Fatalf("invalid input performed reads: %d", calls)
			}
		})
	}
}
