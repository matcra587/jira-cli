//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	xos "github.com/gechr/x/os"
)

type parityCreateWireRequest struct {
	Request       string `json:"request"`
	Body          []byte `json:"body_bytes"`
	Authenticated bool   `json:"authenticated"`
	ContentType   string `json:"content_type"`
}

type parityCreateWireCase struct {
	Lane     string                    `json:"lane"`
	Output   string                    `json:"output"`
	Source   string                    `json:"source"`
	Requests []parityCreateWireRequest `json:"requests"`
}

// A preview cannot prove that the native writer receives the same fields.
// This fixture admits one authenticated create and no replay per owned case.
func TestParityActualBinaryCreateConvenienceWrites(t *testing.T) {
	lanes := []parityLane{{Name: "before", Binary: os.Getenv("JIRA_PARITY_BEFORE_BINARY"), Digest: os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")}}
	if lanes[0].Binary == "" || lanes[1].Binary == "" {
		t.Skip("requires both selected actual binaries")
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	var transcripts []parityCreateWireCase
	t.Cleanup(func() {
		saveParityLocal(t, "create-flags-wire", evidence)
		if base := os.Getenv("JIRA_PARITY_LOCAL_EVIDENCE_DIR"); base != "" {
			raw, err := json.MarshalIndent(transcripts, "", "  ")
			if err == nil {
				err = xos.AtomicWrite(filepath.Join(base, "create-flags-wire.http"), raw, 0o600)
			}
			if err != nil {
				t.Error(err)
			}
		}
	})
	for _, lane := range lanes {
		for _, mode := range []string{"json", "compact"} {
			for _, source := range []string{"inline", "file"} {
				t.Run(lane.Name+"/"+mode+"/"+source, func(t *testing.T) {
					if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
						t.Fatal(err)
					}
					fields := json.RawMessage(`{"project":{"key":"KEY"},"issuetype":{"name":"Task"},"summary":"Owned café <&>","parent":{"key":"KEY-1"},"priority":{"name":"High"},"labels":["alpha,beta","duplicate","duplicate"],"assignee":{"accountId":"opaque-account"},"description":` + parityAuthoringDocument + `}`)
					payload := []byte(`{"fields":` + string(fields) + `}`)
					var mu sync.Mutex
					var requests []string
					var captured []parityCreateWireRequest
					var submitted []byte
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mu.Lock()
						defer mu.Unlock()
						body, err := io.ReadAll(io.LimitReader(r.Body, 65_537))
						user, token, ok := r.BasicAuth()
						requests = append(requests, r.Method+" "+r.URL.RequestURI())
						captured = append(captured, parityCreateWireRequest{Request: r.Method + " " + r.URL.RequestURI(), Body: slices.Clone(body), Authenticated: ok && user == "fixture@example.invalid" && token == "synthetic-fixture-token", ContentType: r.Header.Get("Content-Type")})
						if err != nil || len(body) > 65_536 || !ok || user != "fixture@example.invalid" || token != "synthetic-fixture-token" || r.Header.Get("Cookie") != "" {
							http.Error(w, "unexpected authenticated create request", http.StatusBadRequest)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						switch r.Method + " " + r.URL.Path {
						case "GET /rest/api/3/issue/createmeta/KEY/issuetypes":
							if lane.Name != "before" || len(body) != 0 {
								http.Error(w, "unexpected metadata read", http.StatusBadRequest)
								return
							}
							_, _ = w.Write([]byte(`{"startAt":0,"maxResults":50,"total":1,"issueTypes":[{"id":"20","name":"Task"}]}`))
						case "GET /rest/api/3/issue/createmeta/KEY/issuetypes/20":
							if lane.Name != "before" || len(body) != 0 {
								http.Error(w, "unexpected field metadata read", http.StatusBadRequest)
								return
							}
							_, _ = w.Write([]byte(`{"startAt":0,"maxResults":50,"total":6,"fields":[{"fieldId":"summary","schema":{"type":"string"}},{"fieldId":"description","schema":{"type":"string"}},{"fieldId":"parent","schema":{"type":"issuelink"}},{"fieldId":"priority","schema":{"type":"priority"}},{"fieldId":"labels","schema":{"type":"array","items":"string"}},{"fieldId":"assignee","schema":{"type":"user"}}]}`))
						case "POST /rest/api/3/issue":
							if r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" || submitted != nil || !parityAuthoringJSON(body, payload) {
								http.Error(w, "changed create payload or replay", http.StatusBadRequest)
								return
							}
							submitted = slices.Clone(body)
							w.WriteHeader(http.StatusCreated)
							_, _ = w.Write([]byte(`{"id":"10001","key":"KEY-2"}`))
						default:
							http.Error(w, "unexpected create request", http.StatusBadRequest)
						}
					}))
					defer server.Close()
					home := t.TempDir()
					config := parityCachePublicConfig(t, home, lane.Name, server.URL)
					markdown := filepath.Join(home, "description spaces 古い '$;&.md")
					if err := os.WriteFile(markdown, []byte("**Owned café**"), 0o600); err != nil {
						t.Fatal(err)
					}
					args := []string{"issue", "create", "--project=KEY", "--type=Task", "--summary=Owned café <&>", "--parent=KEY-1", "--priority=High", `--label="alpha,beta"`, "--label=duplicate", "--label=duplicate", "--assignee=opaque-account"}
					if source == "inline" {
						args = append(args, "--markdown=**Owned café**")
					} else {
						args = append(args, "--markdown-file", markdown)
					}
					result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, nil, args...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					mu.Lock()
					observed, wire := slices.Clone(requests), slices.Clone(submitted)
					transcripts = append(transcripts, parityCreateWireCase{Lane: lane.Name, Output: mode, Source: source, Requests: slices.Clone(captured)})
					mu.Unlock()
					wantRequests := []string{"POST /rest/api/3/issue"}
					if lane.Name == "before" {
						wantRequests = append([]string{"GET /rest/api/3/issue/createmeta/KEY/issuetypes?maxResults=50&startAt=0", "GET /rest/api/3/issue/createmeta/KEY/issuetypes/20?maxResults=50&startAt=0"}, wantRequests...)
					}
					if result.Exit != 0 || len(result.Stderr) != 0 || !slices.Equal(observed, wantRequests) || !parityAuthoringJSON(wire, payload) {
						t.Fatalf("actual field writer changed native payload, requests or outcome: exit %d; requests %v; stdout %s; stderr %s", result.Exit, observed, result.Stdout, result.Stderr)
					}
					raw := result.Stdout
					if mode == "json" {
						parityAuthoringCleanData(t, raw, lane.Name, "issue.create")
						raw = parityOriginalFrameData(raw)
					}
					var want map[string]any
					issue := map[string]any{"id": "10001", "key": "KEY-2"}
					if lane.Name == "before" {
						want = map[string]any{"issue": issue, "dry_run": false, "validated_remotely": true, "preview": map[string]any{"project_key": "KEY", "issue_type": "Task", "summary": "Owned café <&>", "parent": map[string]any{"key": "KEY-1"}, "priority": map[string]any{"name": "High"}, "labels": []string{"alpha,beta", "duplicate", "duplicate"}, "assignee_account_id": "opaque-account", "description_adf": json.RawMessage(parityAuthoringDocument)}}
					} else {
						want = map[string]any{"issue": issue, "dry_run": false, "attempted": true, "applied": true, "payload": json.RawMessage(payload), "description_route": "markdown", "receipt": issue}
					}
					expected, _ := json.Marshal(want)
					if !parityAuthoringJSON(raw, expected) {
						t.Fatal("field writer lost complete lane acknowledgement or requested native fields")
					}
					before, err := os.ReadFile(markdown)
					if err != nil || string(before) != "**Owned café**" {
						t.Fatal("writer changed its authored Markdown input")
					}
					if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
						t.Fatal(err)
					}
					id := fmt.Sprintf("I011.create.flags.wire.%s.%s", source, mode)
					evidence.Checks[id] = append(evidence.Checks[id], lane.Name)
				})
			}
		}
	}
}
