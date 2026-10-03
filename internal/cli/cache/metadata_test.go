package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type metadataReaderStub struct {
	core.JiraClient
	api    func(context.Context, core.APIRequest) (*core.APIResponse, error)
	search func(context.Context, string, []string, string, int) (core.IssueSearchRead, error)
}

func (s metadataReaderStub) DoAPI(ctx context.Context, request core.APIRequest) (*core.APIResponse, error) {
	return s.api(ctx, request)
}

func (s metadataReaderStub) SearchIssues(ctx context.Context, query string, fields []string, token string, size int, expand ...string) (core.IssueSearchRead, error) {
	return s.search(ctx, query, fields, token, size)
}

func metadataResponse(body string) *core.APIResponse {
	return &core.APIResponse{StatusCode: http.StatusOK, Body: []byte(body)}
}

func TestMetadataCatalogPreservesTenResourcesTTLAndConsumerKeys(t *testing.T) {
	t.Parallel()
	want := map[string]time.Duration{
		"labels": time.Hour, "projects": 7 * 24 * time.Hour,
		"epics": 4 * time.Hour, "fields": 14 * 24 * time.Hour,
		"issuetypes": 30 * 24 * time.Hour, "linktypes": 90 * 24 * time.Hour,
		"boards": 28 * 24 * time.Hour, "statuses": 30 * 24 * time.Hour,
		"priorities": 90 * 24 * time.Hour, "resolutions": 90 * 24 * time.Hour,
	}
	keys := map[string]bool{}
	for _, resource := range MetadataResources() {
		if ttl, ok := want[resource.Name]; !ok || resource.DefaultTTL != ttl {
			t.Fatalf("unexpected or repeated resource: %+v", resource)
		}
		delete(want, resource.Name)
		key := resource.Key("https://example.com")
		if keys[key] {
			t.Fatalf("resources share key %q", key)
		}
		keys[key] = true
		if resource.Name == "boards" && key != BoardListKey || resource.Name == "linktypes" && key != LinkTypesPrefix+"https://example.com" {
			t.Fatalf("consumer interoperability key changed: %+v", resource)
		}
	}
	if len(want) != 0 {
		t.Fatalf("required resources missing: %v", want)
	}
}

func TestMetadataEveryRESTResourceUsesBoundedTransportAndNativeValues(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, endpoint, body string
		paged                bool
	}{
		{name: "labels", endpoint: "/rest/api/3/label", paged: true, body: `{"startAt":0,"maxResults":100,"isLast":true,"values":["two words"]}`},
		{name: "projects", endpoint: "/rest/api/3/project/search", paged: true, body: `{"startAt":0,"maxResults":100,"total":1,"values":[{"id":"1","name":"Project","key":"EX","unknown":null}]}`},
		{name: "fields", endpoint: "/rest/api/3/field", body: `[{"id":"customfield_10000","name":"Date","schema":{"type":"date"},"unknown":null}]`},
		{name: "issuetypes", endpoint: "/rest/api/3/issuetype", body: `[{"id":"1","name":"Task","subtask":false,"unknown":null}]`},
		{name: "linktypes", endpoint: "/rest/api/3/issueLinkType", body: `{"issueLinkTypes":[{"id":"1","name":"Blocks","inward":"is blocked by","outward":"blocks","unknown":null}]}`},
		{name: "statuses", endpoint: "/rest/api/3/status", body: `[{"id":"1","name":"Open","unknown":null}]`},
		{name: "priorities", endpoint: "/rest/api/3/priority/search", paged: true, body: `{"startAt":0,"maxResults":100,"isLast":true,"total":1,"values":[{"id":"1","name":"High","unknown":null}]}`},
		{name: "resolutions", endpoint: "/rest/api/3/resolution/search", paged: true, body: `{"startAt":0,"maxResults":100,"isLast":true,"total":1,"values":[{"id":"1","name":"Done","unknown":null}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			reader := metadataReaderStub{api: func(ctx context.Context, request core.APIRequest) (*core.APIResponse, error) {
				calls++
				if ctx != t.Context() || request.Method != http.MethodGet || request.Endpoint != tc.endpoint || request.MaxResponseBytes != core.DefaultMaxAPIResponseBytes || request.Body != nil {
					t.Fatalf("unsafe or misrouted metadata request: %+v", request)
				}
				if tc.paged && (request.Query.Get("startAt") != "0" || request.Query.Get("maxResults") != "100") || !tc.paged && len(request.Query) != 0 {
					t.Fatalf("metadata query changed: %v", request.Query)
				}
				return metadataResponse(tc.body), nil
			}}
			out, err := FetchMetadata(t.Context(), reader, nil, tc.name, false)
			if err != nil || !out.Complete || out.Count != 1 || len(out.Items) != 1 || out.Pages != 1 || out.NextStart != nil || out.TruncatedReason != "" || calls != 1 {
				t.Fatalf("native metadata = %+v, %v", out, err)
			}
			if tc.name != "labels" {
				var item map[string]json.RawMessage
				if err := json.Unmarshal(out.Items[0], &item); err != nil || string(item["unknown"]) != "null" {
					t.Fatalf("unknown native null dropped: %s, %v", out.Items[0], err)
				}
			}
			if tc.name == "linktypes" {
				raw, err := jira.MarshalNoEscape(out.Items)
				if err != nil {
					t.Fatal(err)
				}
				var types []jira.IssueLinkType
				if err := json.Unmarshal(raw, &types); err != nil || len(types) != 1 || types[0].Name.Value != "Blocks" || string(types[0].Extra["unknown"]) != "null" {
					t.Fatalf("existing link consumer cannot read primed payload: %s, %v", raw, err)
				}
			}
		})
	}
}

func TestMetadataPreflightAndCompleteHTTPReceipts(t *testing.T) {
	t.Parallel()
	cause := errors.New("body read interrupted")
	for _, tc := range []struct {
		name     string
		resource string
		response *core.APIResponse
		err      error
		cancel   bool
		code     string
		calls    int
	}{
		{name: "unknown", resource: "fieldz", code: cli.CodeArgValueInvalid},
		{name: "canceled before read", resource: "fields", cancel: true, code: cli.CodeCanceled},
		{name: "nil receipt", resource: "fields", code: cli.CodeCacheResponseInvalid, calls: 1},
		{name: "wrong status", resource: "fields", response: &core.APIResponse{StatusCode: 204, Body: []byte(`[]`)}, code: cli.CodeCacheResponseInvalid, calls: 1},
		{name: "partial capture", resource: "fields", response: &core.APIResponse{StatusCode: 200, Body: []byte(`[]`), Truncated: true}, code: cli.CodeCacheResponseInvalid, calls: 1},
		{name: "body error", resource: "fields", response: metadataResponse(`[]`), err: cause, code: "validation_failed", calls: 1},
		{name: "null array", resource: "fields", response: metadataResponse(`null`), code: cli.CodeCacheResponseInvalid, calls: 1},
		{name: "missing link array", resource: "linktypes", response: metadataResponse(`{}`), code: cli.CodeCacheResponseInvalid, calls: 1},
		{name: "trailing data", resource: "fields", response: metadataResponse(`[] {}`), code: cli.CodeCacheResponseInvalid, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			calls := 0
			reader := metadataReaderStub{api: func(context.Context, core.APIRequest) (*core.APIResponse, error) { calls++; return tc.response, tc.err }}
			out, err := FetchMetadata(ctx, reader, nil, tc.resource, false)
			if err == nil || cli.MapError(err).Code != tc.code || out.Complete || out.Count != 0 || len(out.Items) != 0 || calls != tc.calls {
				t.Fatalf("invalid read accepted: %+v, %v, calls=%d", out, err, calls)
			}
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatal("original reader cause lost")
			}
		})
	}
}

func TestMetadataNativeArrayIdentityValidationAndEmptyResults(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, resource, body string
		valid                bool
		count                int
	}{
		{name: "empty", resource: "fields", body: `[]`, valid: true},
		{name: "same status name distinct ids", resource: "statuses", body: `[{"id":"1","name":"Open"},{"id":"2","name":"Open"}]`, valid: true, count: 2},
		{name: "null element", resource: "fields", body: `[null]`},
		{name: "missing id", resource: "fields", body: `[{"name":"Text"}]`},
		{name: "blank name", resource: "fields", body: `[{"id":"1","name":" "}]`},
		{name: "numeric id", resource: "issuetypes", body: `[{"id":1,"name":"Task"}]`},
		{name: "duplicate identity", resource: "statuses", body: `[{"id":"1","name":"Open"},{"id":"1","name":"Different"}]`},
		{name: "missing direction", resource: "linktypes", body: `{"issueLinkTypes":[{"id":"1","name":"Blocks","outward":"blocks"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reader := metadataReaderStub{api: func(context.Context, core.APIRequest) (*core.APIResponse, error) {
				return metadataResponse(tc.body), nil
			}}
			out, err := FetchMetadata(t.Context(), reader, nil, tc.resource, false)
			if tc.valid {
				if err != nil || !out.Complete || out.Items == nil || out.Count != tc.count {
					t.Fatalf("valid metadata rejected: %+v, %v", out, err)
				}
			} else if err == nil || cli.MapError(err).Code != cli.CodeCacheResponseInvalid || out.Count != 0 || out.Complete {
				t.Fatalf("bad identities accepted: %+v, %v", out, err)
			}
		})
	}
}

func TestMetadataPagedDrainUsesActualOffsetsAndIgnoresResponseURLs(t *testing.T) {
	t.Parallel()
	var offsets []int
	reader := metadataReaderStub{api: func(_ context.Context, request core.APIRequest) (*core.APIResponse, error) {
		start, err := strconv.Atoi(request.Query.Get("startAt"))
		if err != nil {
			t.Fatal(err)
		}
		offsets = append(offsets, start)
		switch start {
		case 0:
			return metadataResponse(`{"startAt":0,"maxResults":1,"total":2,"isLast":false,"nextPage":"https://unrelated.example/steal","values":["first"]}`), nil
		case 1:
			return metadataResponse(`{"startAt":1,"maxResults":1,"total":2,"isLast":true,"values":["second"]}`), nil
		default:
			t.Fatal("unexpected page offset")
			return nil, errors.New("unexpected page offset")
		}
	}}
	out, err := FetchMetadata(t.Context(), reader, nil, "labels", false)
	if err != nil || !out.Complete || out.Count != 2 || out.Pages != 2 || out.NextStart != nil || !slices.Equal(offsets, []int{0, 1}) || string(out.Items[1]) != `"second"` {
		t.Fatalf("short-page drain lost native values or offset: %+v, %v, offsets=%v", out, err, offsets)
	}
}

func TestMetadataRejectsAmbiguousAndCaseVariantNativeJSONFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, resource, body string
	}{
		{name: "duplicate id", resource: "fields", body: `[{"id":"1","id":"1","name":"Text"}]`},
		{name: "duplicate unknown nested member", resource: "fields", body: `[{"id":"1","name":"Text","schema":{"type":"string","type":"string"}}]`},
		{name: "case variant id", resource: "fields", body: `[{"ID":"1","name":"Text"}]`},
		{name: "case variant name", resource: "fields", body: `[{"id":"1","NAME":"Text"}]`},
		{name: "duplicate link container", resource: "linktypes", body: `{"issueLinkTypes":[],"issueLinkTypes":[]}`},
		{name: "case variant link container", resource: "linktypes", body: `{"ISSUELINKTYPES":[]}`},
		{name: "duplicate offset", resource: "labels", body: `{"startAt":0,"startAt":0,"maxResults":100,"isLast":true,"values":[]}`},
		{name: "case variant offset", resource: "labels", body: `{"STARTAT":0,"maxResults":100,"isLast":true,"values":[]}`},
		{name: "duplicate completion", resource: "labels", body: `{"startAt":0,"maxResults":100,"isLast":false,"isLast":true,"values":[]}`},
		{name: "case variant completion", resource: "labels", body: `{"startAt":0,"maxResults":100,"ISLAST":true,"values":[]}`},
		{name: "duplicate values", resource: "labels", body: `{"startAt":0,"maxResults":100,"isLast":true,"values":[],"values":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reader := metadataReaderStub{api: func(context.Context, core.APIRequest) (*core.APIResponse, error) {
				return metadataResponse(tc.body), nil
			}}
			out, err := FetchMetadata(t.Context(), reader, nil, tc.resource, false)
			if err == nil || cli.MapError(err).Code != cli.CodeCacheResponseInvalid || out.Complete || out.Count != 0 || len(out.Items) != 0 {
				t.Fatalf("ambiguous native JSON accepted: %+v, %v", out, err)
			}
		})
	}
}

func TestMetadataPagedFailureRetainsOnlyValidatedEarlierPages(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"startAt":0,"maxResults":100,"isLast":true,"values":["second"]}`,
		`{"startAt":1,"isLast":true,"values":["second"]}`,
		`{"startAt":1,"maxResults":0,"isLast":true,"values":["second"]}`,
		`{"startAt":1,"maxResults":1,"isLast":true,"values":["second","third"]}`,
		`{"startAt":1,"maxResults":100,"values":["second"]}`,
		`{"startAt":1,"maxResults":100,"total":1,"values":["second"]}`,
		`{"startAt":1,"maxResults":100,"total":3,"isLast":true,"values":["second"]}`,
		`{"startAt":1,"maxResults":100,"total":2,"isLast":false,"values":["second"]}`,
		`{"startAt":1,"maxResults":100,"isLast":false,"values":[]}`,
		`{"startAt":1,"maxResults":100,"isLast":true,"values":null}`,
		`{"startAt":1,"maxResults":100,"isLast":true,"values":[" "]}`,
		`{"startAt":1,"maxResults":100,"isLast":true,"values":[3]}`,
		`{"startAt":1,"maxResults":100,"isLast":true,"values":["second","second"]}`,
		`{"startAt":1,"maxResults":100,"isLast":true,"values":["second","first"]}`,
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			calls := 0
			reader := metadataReaderStub{api: func(context.Context, core.APIRequest) (*core.APIResponse, error) {
				calls++
				if calls > 2 {
					return nil, errors.New("invalid page was followed by another request")
				}
				if calls == 1 {
					return metadataResponse(`{"startAt":0,"maxResults":100,"isLast":false,"values":["first"]}`), nil
				}
				return metadataResponse(body), nil
			}}
			out, err := FetchMetadata(t.Context(), reader, nil, "labels", false)
			if err == nil || cli.MapError(err).Code != cli.CodeCacheResponseInvalid || out.Complete || out.Count != 1 || out.Pages != 1 || len(out.Items) != 1 || string(out.Items[0]) != `"first"` || out.NextStart == nil || *out.NextStart != 1 || calls != 2 {
				t.Fatalf("bad page contaminated prior receipt: %+v, %v, calls=%d", out, err, calls)
			}
		})
	}
}

func TestMetadataPageRejectsRequestOverflowAndOffsetOverflow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                    string
		start, requested, count int
	}{
		{name: "request size", requested: 1, count: 2},
		{name: "offset overflow", start: math.MaxInt, requested: 1, count: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			page := metadataPage{StartAt: new(tc.start), MaxResults: new(100), IsLast: new(true)}
			_, _, err := validateMetadataPage(page, tc.start, tc.requested, tc.count)
			if err == nil || cli.MapError(err).Code != cli.CodeCacheResponseInvalid {
				t.Fatal("unsafe page bounds accepted")
			}
		})
	}
}

func TestMetadataFetchCapsAndExplicitUnboundedWalk(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		width     int
		unbounded bool
		pages     int
		complete  bool
		reason    string
	}{
		{name: "page cap", width: 1, pages: 100, reason: "max_pages"},
		{name: "item cap", width: 100, pages: 100, reason: "max_results"},
		{name: "unbounded", width: 1, unbounded: true, pages: 101, complete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			reader := metadataReaderStub{api: func(_ context.Context, request core.APIRequest) (*core.APIResponse, error) {
				if request.Query.Get("startAt") != strconv.Itoa(calls*tc.width) || request.Query.Get("maxResults") != "100" {
					t.Fatalf("bounded collector offset or size changed: %+v", request)
				}
				items := make([]string, tc.width)
				for i := range items {
					items[i] = strconv.Itoa(calls*tc.width + i)
				}
				values, err := json.Marshal(items)
				if err != nil {
					t.Fatal(err)
				}
				calls++
				return metadataResponse(fmt.Sprintf(`{"startAt":%d,"maxResults":100,"isLast":%t,"values":%s}`, (calls-1)*tc.width, calls == 101, values)), nil
			}}
			out, err := FetchMetadata(t.Context(), reader, nil, "labels", tc.unbounded)
			if err != nil || out.Complete != tc.complete || out.TruncatedReason != tc.reason || out.Pages != tc.pages || out.Count != tc.pages*tc.width || calls != tc.pages {
				t.Fatalf("metadata fetch bound changed: %+v, %v, calls=%d", out, err, calls)
			}
			if !out.Complete && (out.NextStart == nil || *out.NextStart != out.Count) || out.Complete && out.NextStart != nil {
				t.Fatal("bounded walk lost resume or invented one on completion")
			}
		})
	}
}

func TestMetadataCancellationAndReaderErrorsPreservePriorReceipts(t *testing.T) {
	t.Parallel()
	for _, cancelRead := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelRead), func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cause := errors.New("second metadata request failed")
			calls := 0
			reader := metadataReaderStub{api: func(received context.Context, _ core.APIRequest) (*core.APIResponse, error) {
				if received != ctx {
					t.Fatal("collector replaced command context")
				}
				calls++
				if calls == 1 {
					return metadataResponse(`{"startAt":0,"maxResults":100,"isLast":false,"values":["first"]}`), nil
				}
				if cancelRead {
					cause = context.Canceled
					cancel()
					return metadataResponse(`{"startAt":1,"maxResults":100,"isLast":true,"values":["second"]}`), nil
				}
				return nil, cause
			}}
			out, err := FetchMetadata(ctx, reader, nil, "labels", false)
			if !errors.Is(err, cause) || out.Complete || out.Count != 1 || len(out.Items) != 1 || out.Pages != 1 || out.NextStart == nil || *out.NextStart != 1 || calls != 2 {
				t.Fatalf("interrupted metadata lost validated receipt: %+v, %v", out, err)
			}
		})
	}
}

func TestMetadataEpicsReuseSharedSearchWalkAndKeepNativeFields(t *testing.T) {
	t.Parallel()
	var tokens []string
	reader := metadataReaderStub{search: func(ctx context.Context, query string, fields []string, token string, size int) (core.IssueSearchRead, error) {
		if ctx != t.Context() || query != "issuetype = Epic" || !slices.Equal(fields, []string{"summary", "status"}) || size != 100 {
			t.Fatal("epic primer diverged from shared search policy")
		}
		tokens = append(tokens, token)
		index := len(tokens)
		bean := jira.IssueBean{
			ID: jira.NullableOf(strconv.Itoa(index)), Key: jira.NullableOf("EX-" + strconv.Itoa(index)),
			Fields: jira.NullableOf(jira.IssueFields{Summary: jira.NullableOf("<literal>"), Extra: map[string]json.RawMessage{"native_null": json.RawMessage(`null`)}}),
		}
		page := &jira.SearchResults{Issues: jira.NullableOf([]jira.IssueBean{bean}), IsLast: jira.NullableOf(index == 2)}
		if index == 1 {
			page.NextPageToken = jira.NullableOf("next")
		}
		return core.IssueSearchRead{Results: page}, nil
	}}
	out, err := FetchMetadata(t.Context(), reader, nil, "epics", false)
	if err != nil || !out.Complete || out.Count != 2 || out.Pages != 2 || out.NextPageToken != "" || !reflect.DeepEqual(tokens, []string{"", "next"}) {
		t.Fatalf("epic primer failed shared cursor drain: %+v, %v", out, err)
	}
	var issue struct {
		Key    string                     `json:"key"`
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(out.Items[0], &issue); err != nil || issue.Key != "EX-1" || string(issue.Fields["summary"]) != `"<literal>"` || string(issue.Fields["native_null"]) != "null" {
		t.Fatalf("native epic values narrowed: %s, %v", out.Items[0], err)
	}
}

func TestMetadataBoardsDelegateWithCommandContextAndBounds(t *testing.T) {
	t.Parallel()
	cause := errors.New("board project enrichment failed")
	want := MetadataValue{Items: []json.RawMessage{}, Count: 1, Pages: 1, Payload: json.RawMessage(`{"boards":[]}`)}
	calls := 0
	out, err := FetchMetadata(t.Context(), nil, func(ctx context.Context, unbounded bool) (MetadataValue, error) {
		calls++
		if ctx != t.Context() || !unbounded {
			t.Fatal("board owner lost context or unbounded policy")
		}
		return want, cause
	}, "boards", true)
	if !errors.Is(err, cause) || !reflect.DeepEqual(out, want) || calls != 1 {
		t.Fatalf("board owner's payload or error lost: %+v, %v", out, err)
	}
}
