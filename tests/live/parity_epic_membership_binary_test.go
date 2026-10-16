//go:build live

package live

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func parityEpicReadRequest(r *http.Request, fields string) bool {
	query, err := url.ParseQuery(r.URL.RawQuery)
	body, bodyErr := io.ReadAll(io.LimitReader(r.Body, 1))
	return r.Method == http.MethodGet && err == nil && len(query) == 1 && slices.Equal(query["fields"], []string{fields}) && bodyErr == nil && len(body) == 0
}

func TestParityEpicReadRequestRejectsAmbiguousQueries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, query, body string
		want              bool
	}{
		{"valid", "fields=issuetype", "", true},
		{"duplicate", "fields=issuetype&fields=other", "", false},
		{"extra", "fields=issuetype&expand=names", "", false},
		{"body", "fields=issuetype", "{}", false},
		{"wrong-fields", "fields=summary", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://fixture.invalid/issue/PAIR-9?"+tc.query, strings.NewReader(tc.body))
			if parityEpicReadRequest(r, "issuetype") != tc.want {
				t.Fatal("epic preflight oracle admitted an ambiguous query or body")
			}
		})
	}
}

func TestParityActualBinaryEpicMembership(t *testing.T) {
	lanes := []parityLane{{Name: "before", Binary: os.Getenv("JIRA_PARITY_BEFORE_BINARY"), Digest: os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")}}
	for _, lane := range lanes {
		if lane.Binary == "" {
			t.Skip("requires both selected actual binaries")
		}
		if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
			t.Fatal(err)
		}
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range lanes {
		for _, verb := range []string{"add", "remove"} {
			for _, count := range []int{1, 2} {
				for _, step := range []string{"preview", "read_only", "native"} {
					for _, mode := range []string{"json", "compact"} {
						if step == "read_only" && mode == "compact" {
							continue
						}
						t.Run(fmt.Sprintf("%s/%s/%d/%s/%s", lane.Name, verb, count, step, mode), func(t *testing.T) {
							keys := []string{"PAIR-1", "PAIR-2"}[:count]
							var mu sync.Mutex
							var requests []string
							var writes []string
							bad := false
							server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								mu.Lock()
								defer mu.Unlock()
								requests = append(requests, r.Method+" "+r.URL.Path)
								w.Header().Set("Content-Type", "application/json")
								user, password, auth := r.BasicAuth()
								if !auth || user != "fixture@example.invalid" || password != "synthetic-fixture-token" {
									bad = true
									http.Error(w, "bad binding", http.StatusUnauthorized)
									return
								}
								if r.Method == http.MethodGet && lane.Name == "after" {
									if r.URL.Path == "/rest/api/3/issue/PAIR-9" {
										if !parityEpicReadRequest(r, "issuetype") {
											bad = true
										}
										_, _ = io.WriteString(w, `{"id":"109","key":"PAIR-9","fields":{"issuetype":{"id":"9","name":"Epic","subtask":false,"hierarchyLevel":1}}}`)
										return
									}
									for i, key := range keys {
										if verb == "remove" && r.URL.Path == "/rest/api/3/issue/"+key && parityEpicReadRequest(r, "parent,issuetype") {
											_, _ = fmt.Fprintf(w, `{"id":%q,"key":%q,"fields":{"parent":{"id":"109","key":"PAIR-9"},"issuetype":{"id":"1","name":"Task","subtask":false,"hierarchyLevel":0}}}`, strconv.Itoa(101+i), key)
											return
										}
									}
								}
								if r.Method == http.MethodPut {
									for _, key := range keys {
										if r.URL.Path != "/rest/api/3/issue/"+key {
											continue
										}
										body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
										want := `{"fields":{"parent":null}}`
										if verb == "add" {
											want = `{"fields":{"parent":{"key":"PAIR-9"}}}`
										}
										if err != nil || len(body) > 4096 || !parityAuthoringJSON(body, []byte(want)) || r.URL.RawQuery != "" {
											bad = true
											http.Error(w, "wrong native edit", http.StatusBadRequest)
											return
										}
										writes = append(writes, key)
										w.WriteHeader(http.StatusNoContent)
										return
									}
								}
								bad = true
								http.Error(w, "unexpected membership request", http.StatusBadRequest)
							}))
							defer server.Close()
							home := t.TempDir()
							config := parityCachePublicConfig(t, home, lane.Name, server.URL)
							args := append([]string{"epic", verb}, keys...)
							parent := ""
							if verb == "add" {
								parent = "PAIR-9"
								args = append(args, parent)
							}
							args = append(args, "--parallelism=1")
							var extra []string
							if step == "preview" {
								args = append(args, "--dry-run")
								extra = []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_KEYRING=false"}
							}
							if step == "read_only" {
								extra = []string{"JIRA_READ_ONLY=true"}
							}
							result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, extra, args...)
							evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
							data := result.Stdout
							if mode == "json" {
								data = parityOriginalFrameData(result.Stdout)
							}
							if err := parityValidateFrame(result.Stdout, lane.Name, "epic."+verb, result.Exit); (mode == "json" || step == "read_only") && err != nil {
								t.Fatal(err)
							}
							if step == "read_only" {
								if result.Exit != 3 || !parityEpicRefusal(result.Stdout, lane.Name, verb, keys, parent) {
									t.Fatalf("read-only epic operation failed classification: exit%d stdout%s stderr%s", result.Exit, result.Stdout, result.Stderr)
								}
							} else if result.Exit != 0 || len(result.Stderr) != 0 || !parityEpicReceipt(data, lane.Name, verb, keys, parent, step == "preview") {
								t.Fatalf("epic receipt mismatch: exit%d stdout%s stderr%s", result.Exit, result.Stdout, result.Stderr)
							}
							mu.Lock()
							observed := slices.Clone(requests)
							written := slices.Clone(writes)
							invalid := bad
							mu.Unlock()
							if invalid {
								t.Fatal("epic invocation sent an unauthenticated, malformed or unexpected request")
							}
							if step != "native" && len(written) != 0 {
								t.Fatal("epic preview or refusal entered native PUT")
							}
							if step == "preview" && len(observed) != 0 {
								t.Fatal("bare epic preview contacted Jira")
							}
							if step == "native" && !slices.Equal(written, keys) {
								t.Fatal("epic invocation changed native write count, target order or payload")
							}
							var expected []string
							if step != "preview" {
								if lane.Name == "after" && verb == "add" {
									expected = append(expected, "GET /rest/api/3/issue/PAIR-9")
								}
								for _, key := range keys {
									if lane.Name == "after" && verb == "remove" {
										expected = append(expected, "GET /rest/api/3/issue/"+key, "GET /rest/api/3/issue/PAIR-9")
									}
									if step == "native" {
										expected = append(expected, "PUT /rest/api/3/issue/"+key)
									}
								}
							}
							if !slices.Equal(observed, expected) {
								t.Fatalf("epic validation/write transcript changed: got%v want%v", observed, expected)
							}
							if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
								t.Fatal(err)
							}
							id := "C048"
							if verb == "remove" {
								id = "C051"
							}
							check := fmt.Sprintf("%s.epic.wire.%s_%d.%s.%s", id, verb, count, step, mode)
							evidence.Checks[check] = append(evidence.Checks[check], lane.Name)
						})
					}
				}
			}
		}
	}
	saveParityLocal(t, "epic-membership-fixture", evidence)
}
