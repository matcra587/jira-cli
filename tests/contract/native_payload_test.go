package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const nativeTopLevel = `"update":{},"historyMetadata":{"activityDescription":"<>&\u003c\/","futureCount":9007199254740993},"properties":[{"key":"fixture.property","value":{"unknownValue":[null,[],false,0]}}],"transition":{"id":"31"},"futureMember":null`

func assertNativeTopLevel(t *testing.T, payload []byte) {
	t.Helper()
	var want, got map[string]json.RawMessage
	if err := json.Unmarshal([]byte("{"+nativeTopLevel+"}"), &want); err != nil {
		t.Error(err)
		return
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Error(err)
		return
	}
	for name, value := range want {
		if string(got[name]) != string(value) {
			t.Errorf("native payload lost or rewrote %s", name)
		}
	}
}

func TestNativeIssuePayloadTopLevelPreviewAndWire(t *testing.T) {
	t.Parallel()
	for _, route := range []string{"create", "edit", "clone"} {
		for _, mode := range []string{"json", "compact"} {
			t.Run(route+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var reads, writes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet && route == "clone" && r.URL.Path == "/rest/api/3/issue/KEY-123" {
						reads.Add(1)
						_, _ = io.WriteString(w, `{"id":"10123","key":"KEY-123","fields":{"summary":"source","project":{"key":"KEY"},"issuetype":{"name":"Task"}}}`)
						return
					}
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					assertNativeTopLevel(t, body)
					writes.Add(1)
					if route == "edit" {
						if r.Method != http.MethodPut || r.URL.Path != "/rest/api/3/issue/KEY-123" {
							t.Error("native edit used an unexpected route")
						}
						w.WriteHeader(http.StatusNoContent)
						return
					}
					if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/issue" {
						t.Error("native create/clone used an unexpected route")
					}
					w.WriteHeader(http.StatusCreated)
					_, _ = io.WriteString(w, `{"id":"10124","key":"KEY-124"}`)
				}))
				defer server.Close()
				input := apiInput(t, []byte(`{"fields":{"project":{"key":"KEY"},"issuetype":{"name":"Task"},"summary":"native"},`+nativeTopLevel+`}`))
				args := []string{"issue", route}
				if route != "create" {
					args = append(args, "KEY-123")
				}
				args = append(args, "--json-input", input, "--no-input", "--output="+mode)
				for _, preview := range []bool{true, false} {
					call := append([]string(nil), args...)
					if preview {
						call = append(call, "--dry-run")
					}
					stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), call...)
					if exit != 0 || stderr != "" {
						t.Fatalf("native request failed: preview=%t exit=%d stderr=%q", preview, exit, stderr)
					}
					data := adfData(t, stdout, mode, "issue."+route, true)
					var out struct {
						DryRun  bool            `json:"dry_run"`
						Payload json.RawMessage `json:"payload"`
					}
					if json.Unmarshal(data, &out) != nil || out.DryRun != preview {
						t.Fatal("native request lost its preview state")
					}
					if route != "clone" {
						assertNativeTopLevel(t, out.Payload)
					}
					if preview && (reads.Load() != 0 || writes.Load() != 0) {
						t.Fatal("native preview contacted Jira")
					}
				}
				if writes.Load() != 1 || (route == "clone" && reads.Load() != 1) {
					t.Fatal("native write was omitted/replayed or clone source was not read once")
				}
			})
		}
	}
}

func TestNativeIssuePropertyOnlyEdit(t *testing.T) {
	t.Parallel()
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != `{"properties":[{"key":"fixture.property","value":false}]}` || r.Method != http.MethodPut || r.URL.Path != "/rest/api/3/issue/KEY-123" {
			t.Error("property-only edit changed its native request")
		}
		writes.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	input := apiInput(t, []byte(`{"properties":[{"key":"fixture.property","value":false}]}`))
	for _, preview := range []bool{true, false} {
		args := []string{"issue", "edit", "KEY-123", "--json-input", input, "--output=json"}
		if preview {
			args = append(args, "--dry-run")
		}
		stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
		if exit != 0 || stderr != "" || !strings.Contains(stdout, `"fixture.property"`) {
			t.Fatal("property-only edit was refused or discarded its request")
		}
		if preview && writes.Load() != 0 {
			t.Fatal("property-only preview wrote to Jira")
		}
	}
	if writes.Load() != 1 {
		t.Fatal("property-only edit did not write exactly once")
	}
}

func TestNativeIssueMalformedContainersFailBeforeHTTP(t *testing.T) {
	t.Parallel()
	for _, route := range []string{"create", "edit", "clone"} {
		for _, mode := range []string{"json", "compact"} {
			t.Run(route+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.WriteHeader(http.StatusInternalServerError)
				}))
				defer server.Close()
				for _, body := range []string{`null`, `{"fields":null,"properties":[]}`, `{"update":null,"futureMember":null}`, `{"fields":[]}`} {
					input := apiInput(t, []byte(body))
					args := []string{"issue", route}
					if route != "create" {
						args = append(args, "KEY-123")
					}
					args = append(args, "--json-input", input, "--no-input", "--output="+mode)
					stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
					var out frame
					if err := json.Unmarshal([]byte(stdout), &out); err != nil {
						t.Fatal(err)
					}
					if exit != 3 || !strings.Contains(stderr, "--json-input") || out.OK || out.Meta.Command != "issue."+route || len(out.Errors) != 1 || requests.Load() != 0 {
						t.Fatalf("native %s(%s) refusal: exit=%d stderr=%q ok=%t errors=%d HTTP=%d, want input failure before HTTP", route, body, exit, stderr, out.OK, len(out.Errors), requests.Load())
					}
					var failure struct {
						Code  string `json:"code"`
						Field string `json:"field"`
					}
					if err := json.Unmarshal(out.Errors[0], &failure); err != nil {
						t.Fatal(err)
					}
					if failure.Code != "flag_value_invalid" || failure.Field != "json-input" {
						t.Errorf("native %s(%s) error = %+v, want flag_value_invalid for json-input", route, body, failure)
					}
				}
			})
		}
	}
}
