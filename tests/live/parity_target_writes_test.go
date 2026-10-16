//go:build live

package live

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

// These controls establish entry counts for the shared target repair. The
// separate per-operation fixtures retain full native receipt/value acceptance.
func TestParityActualBinarySharedTargetWrites(t *testing.T) {
	lane := parityLane{Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")}
	if lane.Binary == "" {
		t.Skip("requires the selected actual current binary")
	}
	if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
		t.Fatal(err)
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	t.Cleanup(func() { saveParityLocal(t, "shared-target-writes-fixture", evidence) })
	for _, operation := range []struct {
		id, name                 string
		command, flags, requests []string
	}{
		{"I007", "comment", []string{"issue", "comment", "add"}, []string{"--markdown=**Owned café**"}, []string{"POST /rest/api/3/issue/PAIR-1/comment"}},
		{"I005", "clone", []string{"issue", "clone"}, nil, []string{"GET /rest/api/3/issue/PAIR-1?fields=%2Aall", "POST /rest/api/3/issue"}},
		{"I012", "delete", []string{"issue", "delete"}, []string{"--force"}, []string{"DELETE /rest/api/3/issue/PAIR-1?deleteSubtasks=false"}},
		{"I013", "edit", []string{"issue", "edit"}, []string{"--summary=Owned target write"}, []string{"PUT /rest/api/3/issue/PAIR-1"}},
		{"I022", "transition", []string{"issue", "transition"}, []string{"Done"}, []string{"GET /rest/api/3/issue/PAIR-1/transitions?expand=transitions.fields", "POST /rest/api/3/issue/PAIR-1/transitions"}},
		{"I013", "input", []string{"issue", "edit"}, []string{"--generate-json"}, []string{"GET /rest/api/3/issue/PAIR-1/editmeta"}},
	} {
		for _, mode := range []string{"json", "compact"} {
			for _, expression := range []struct {
				name string
				args []string
			}{
				{"repeated", []string{"PAIR-1", "PAIR-1"}},
				{"csv", []string{"PAIR-1,PAIR-1"}},
				{"overlapping-range", []string{"PAIR-1:1", "PAIR-1..PAIR-1", "PAIR-0001"}},
				{"literal-file", nil},
			} {
				t.Run(operation.name+"/"+mode+"/"+expression.name, func(t *testing.T) {
					var mu sync.Mutex
					var requests []string
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mu.Lock()
						defer mu.Unlock()
						requests = append(requests, r.Method+" "+r.URL.RequestURI())
						body, err := io.ReadAll(io.LimitReader(r.Body, 8193))
						user, token, authenticated := r.BasicAuth()
						if err != nil || len(body) > 8192 || !authenticated || user != "fixture@example.invalid" || token != "synthetic-fixture-token" || r.Header.Get("Cookie") != "" {
							t.Error("target control changed authenticated native request")
							http.Error(w, "invalid request", http.StatusBadRequest)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						var want, reply string
						status := http.StatusOK
						switch r.Method + " " + r.URL.Path {
						case "GET /rest/api/3/issue/PAIR-1/editmeta":
							if operation.name != "input" {
								t.Error("unexpected metadata read")
							}
							reply = `{"fields":{"summary":{"key":"summary","name":"Summary","required":false,"schema":{"type":"string"},"operations":["set"]}}}`
						case "GET /rest/api/3/issue/PAIR-1":
							if operation.name != "clone" {
								t.Error("unexpected source read")
							}
							reply = `{"id":"101","key":"PAIR-1","fields":{"summary":"Owned source","project":{"key":"PAIR"},"issuetype":{"name":"Task"},"description":null}}`
						case "POST /rest/api/3/issue":
							if operation.name != "clone" {
								t.Error("unexpected clone write")
							}
							want = `{"fields":{"summary":"Owned source","project":{"key":"PAIR"},"issuetype":{"name":"Task"},"description":null}}`
							reply = fmt.Sprintf(`{"id":"901","key":"PAIR-90","self":"http://%s/rest/api/3/issue/901"}`, r.Host)
							status = http.StatusCreated
						case "POST /rest/api/3/issue/PAIR-1/comment":
							if operation.name != "comment" {
								t.Error("unexpected comment write")
							}
							want = `{"body":` + parityAuthoringDocument + `}`
							reply = `{"id":"501","body":` + parityAuthoringDocument + `}`
							status = http.StatusCreated
						case "DELETE /rest/api/3/issue/PAIR-1":
							if operation.name != "delete" {
								t.Error("unexpected delete")
							}
							status = http.StatusNoContent
						case "PUT /rest/api/3/issue/PAIR-1":
							if operation.name != "edit" {
								t.Error("unexpected edit")
							}
							want = `{"fields":{"summary":"Owned target write"}}`
							status = http.StatusNoContent
						case "GET /rest/api/3/issue/PAIR-1/transitions":
							if operation.name != "transition" {
								t.Error("unexpected transitions read")
							}
							reply = `{"transitions":[{"id":"31","name":"Finish","hasScreen":true,"to":{"name":"Done"}}]}`
						case "POST /rest/api/3/issue/PAIR-1/transitions":
							if operation.name != "transition" {
								t.Error("unexpected transition write")
							}
							want = `{"transition":{"id":"31"}}`
							status = http.StatusNoContent
						default:
							t.Error("target control entered unexpected HTTP route")
							status = http.StatusNotFound
						}
						if want != "" {
							if r.Header.Get("Content-Type") != "application/json" || !parityAuthoringJSON(body, []byte(want)) {
								t.Errorf("target control changed native %s body: %s", operation.name, body)
								status = http.StatusBadRequest
							}
						} else if len(body) != 0 {
							t.Error("bodyless target request gained content")
						}
						w.WriteHeader(status)
						if reply != "" {
							_, _ = io.WriteString(w, reply)
						}
					}))
					defer server.Close()
					home := t.TempDir()
					config := parityCachePublicConfig(t, home, lane.Name, server.URL)
					args := append(slices.Clone(operation.command), expression.args...)
					if expression.name == "literal-file" {
						path := filepath.Join(home, "owned target lines.txt")
						if err := os.WriteFile(path, []byte("PAIR-1\nPAIR-1 # duplicate\nPAIR-1\n"), 0o600); err != nil {
							t.Fatal(err)
						}
						args = append(args, "--from-file", path)
						if operation.name != "input" && operation.name != "delete" {
							args = append(args, "--force")
						}
					}
					args = append(args, operation.flags...)
					before, began := parityLocalTree(t, home), time.Now()
					result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_READ_ONLY=false"}, args...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					mu.Lock()
					observed := slices.Clone(requests)
					mu.Unlock()
					if result.Exit != 0 || len(result.Stderr) != 0 || !slices.Equal(observed, operation.requests) {
						t.Fatalf("target writer did not enter exactly once: requests=%v exit=%d stdout=%s stderr=%s", observed, result.Exit, result.Stdout, result.Stderr)
					}
					data := result.Stdout
					if mode == "json" {
						parityAuthoringCleanData(t, data, lane.Name, "issue."+map[string]string{"comment": "comment.add", "clone": "clone", "delete": "delete", "edit": "edit", "transition": "transition", "input": "edit.input"}[operation.name])
						data = parityOriginalFrameData(data)
					}
					if !parityTargetRows(data, []string{"PAIR-1"}, false) {
						t.Fatalf("target writer lost its unique acknowledged row: %s", data)
					}
					keys := []string{"PAIR-1"}
					if operation.name == "clone" {
						keys = append(keys, "PAIR-90")
					}
					history := parityLocalHistory(t, home, lane.Name, server.URL, keys, began)
					parityOpenOnlyHistory(t, home, history, before)
					evidence.Checks[operation.id+".target-writes."+operation.name+"."+expression.name+"."+mode] = []string{lane.Name}
				})
			}
		}
	}
}
