//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	xos "github.com/gechr/x/os"
)

func TestParityActualBinaryCloneFieldFlags(t *testing.T) {
	lane := parityLane{Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")}
	if lane.Binary == "" {
		t.Skip("requires the admitted current binary")
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	var transcripts []parityCreateWireCase
	t.Cleanup(func() {
		saveParityLocal(t, "clone-field-flags-fixture", evidence)
		if base := os.Getenv("JIRA_PARITY_LOCAL_EVIDENCE_DIR"); base != "" {
			// Cloning enters the same native create writer. Retain its
			// synthetic transcript through the existing secret-free DTO.
			raw, err := json.MarshalIndent(transcripts, "", "  ")
			if err == nil {
				err = xos.AtomicWrite(filepath.Join(base, "clone-field-flags.http"), raw, 0o600)
			}
			if err != nil {
				t.Error(err)
			}
		}
	})
	for _, mode := range []string{"json", "compact"} {
		for _, scenario := range []string{"scalars", "collections", "copied-summary-replacements", "copied-summary-replacements-reversed", "from-file", "jql", "filter"} {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
					t.Fatal(err)
				}
				// Native source and expected candidate are authored separately.
				// Expanded references, excluded server fields, null description
				// and an exact unknown numeric field expose accidental copying.
				const source = `{"id":"101","key":"PAIR-1","fields":{"summary":"Source v1","project":{"id":"10","key":"PAIR","name":"Source project"},"issuetype":{"id":"20","name":"Task","description":"Source type"},"priority":{"id":"30","name":"Low"},"assignee":{"accountId":"source-account","displayName":"Source user"},"reporter":{"accountId":"source-reporter"},"status":{"name":"Open"},"timetracking":{"originalEstimate":"20m"},"labels":["old"],"components":[{"id":"99","name":"Old component"}],"fixVersions":[{"id":"88","name":"Old fix"}],"versions":[{"id":"77","name":"Old affected"}],"description":null,"native_extra":900719925474099312345}}`
				fields := map[string]any{
					"summary": "Source v1", "project": map[string]any{"key": "PAIR"}, "issuetype": map[string]any{"name": "Task"}, "priority": map[string]any{"name": "Low"}, "assignee": map[string]any{"accountId": "source-account"},
					"labels": []string{"old"}, "components": []any{map[string]any{"id": "99", "name": "Old component"}}, "fixVersions": []any{map[string]any{"id": "88", "name": "Old fix"}}, "versions": []any{map[string]any{"id": "77", "name": "Old affected"}}, "description": nil, "native_extra": json.Number("900719925474099312345"),
				}
				args := []string{"issue", "clone", "PAIR-1"}
				var selectors []map[string]any
				switch scenario {
				case "scalars":
					args = append(args, "--project=OTHER", "--type=Bug", "--summary=Authored v1", "--assignee=opaque-account", "--reporter=opaque-reporter", "--parent=PAIR-2", "--priority=High", "--due=2026-12-31", "--original-estimate=1h", "--replace=v1=v2")
					fields["project"], fields["issuetype"], fields["summary"] = map[string]any{"key": "OTHER"}, map[string]any{"name": "Bug"}, "Authored v2"
					fields["assignee"], fields["reporter"], fields["parent"], fields["priority"] = map[string]any{"accountId": "opaque-account"}, map[string]any{"accountId": "opaque-reporter"}, map[string]any{"key": "PAIR-2"}, map[string]any{"name": "High"}
					fields["duedate"], fields["timetracking"] = "2026-12-31", map[string]any{"originalEstimate": "1h"}
				case "collections":
					args = append(args, `--label="alpha,beta"`, "--label=duplicate", "--label=duplicate", "--component=123", "--component=ui", "--fix-version=123", "--affects-version=456")
					fields["labels"] = []string{"alpha,beta", "duplicate", "duplicate"}
					fields["components"] = []any{map[string]any{"name": "123"}, map[string]any{"name": "ui"}}
					fields["fixVersions"], fields["versions"] = []any{map[string]any{"name": "123"}}, []any{map[string]any{"name": "456"}}
				case "copied-summary-replacements":
					args = append(args, "--replace=Source=Copied", "--replace=Copied=Final")
					fields["summary"] = "Final v1"
				case "copied-summary-replacements-reversed":
					args = append(args, "--replace=Copied=Final", "--replace=Source=Copied")
					fields["summary"] = "Copied v1"
				case "from-file", "jql", "filter":
					args = []string{"issue", "clone", "--force"}
					query := "key = PAIR-1"
					if scenario == "filter" {
						query = "73"
					}
					summary := map[string]any{"kind": scenario, "query": query, "resolved": true}
					if scenario == "filter" {
						summary["jql"] = "key = PAIR-1"
					}
					selectors = append(selectors, summary)
					if scenario != "from-file" {
						args = append(args, "--"+scenario, query)
					}
				}
				payload, err := json.Marshal(map[string]any{"fields": fields})
				if err != nil {
					t.Fatal(err)
				}
				var mu sync.Mutex
				var requests []string
				var wire []parityCreateWireRequest
				var submitted []byte
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					body, err := io.ReadAll(io.LimitReader(r.Body, 65_537))
					user, token, authenticated := r.BasicAuth()
					requests = append(requests, r.Method+" "+r.URL.RequestURI())
					wire = append(wire, parityCreateWireRequest{Request: r.Method + " " + r.URL.RequestURI(), Body: slices.Clone(body), Authenticated: authenticated && user == "fixture@example.invalid" && token == "synthetic-fixture-token", ContentType: r.Header.Get("Content-Type")})
					if err != nil || len(body) > 65_536 || !authenticated || user != "fixture@example.invalid" || token != "synthetic-fixture-token" || r.Header.Get("Cookie") != "" {
						t.Error("clone changed its bounded authenticated request")
						http.Error(w, "invalid owned clone request", http.StatusBadRequest)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					switch r.Method + " " + r.URL.Path {
					case "GET /rest/api/3/filter/73":
						if scenario != "filter" || r.URL.RawQuery != "" || len(body) != 0 {
							t.Error("clone selected an unauthored filter read")
							http.Error(w, "invalid filter read", http.StatusBadRequest)
							return
						}
						_, _ = io.WriteString(w, `{"id":"73","name":"Owned filter","jql":"key = PAIR-1"}`)
					case "POST /rest/api/3/search/jql":
						if scenario != "jql" && scenario != "filter" || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" || !parityAuthoringJSON(body, []byte(`{"jql":"key = PAIR-1","fields":["key"],"maxResults":101}`)) {
							t.Error("clone changed its exact selector search")
							http.Error(w, "invalid selector search", http.StatusBadRequest)
							return
						}
						_, _ = io.WriteString(w, `{"isLast":true,"issues":[{"id":"101","key":"PAIR-1"}]}`)
					case "GET /rest/api/3/issue/PAIR-1":
						query, queryErr := url.ParseQuery(r.URL.RawQuery)
						if queryErr != nil || query.Encode() != "fields=%2Aall" || len(body) != 0 {
							t.Error("clone changed its exact native source read")
							http.Error(w, "invalid source read", http.StatusBadRequest)
							return
						}
						_, _ = io.WriteString(w, source)
					case "POST /rest/api/3/issue":
						if submitted != nil || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" || !parityAuthoringJSON(body, payload) {
							t.Error("clone changed independent native overrides, copied values or replayed creation")
							http.Error(w, "invalid clone candidate", http.StatusBadRequest)
							return
						}
						submitted = slices.Clone(body)
						w.WriteHeader(http.StatusCreated)
						_, _ = fmt.Fprintf(w, `{"id":"901","key":"PAIR-90","self":"http://%s/rest/api/3/issue/901"}`, r.Host)
					default:
						t.Error("clone entered an unauthored request")
						http.Error(w, "unexpected clone request", http.StatusBadRequest)
					}
				}))
				defer server.Close()
				home := t.TempDir()
				config := parityCachePublicConfig(t, home, lane.Name, server.URL)
				if scenario == "from-file" {
					path := filepath.Join(home, "owned sources 古い '$;&.txt")
					if err := os.WriteFile(path, []byte("PAIR-1\n"), 0o600); err != nil {
						t.Fatal(err)
					}
					selectors[0]["query"] = path
					args = append(args, "--from-file", path)
				}
				before := parityLocalTree(t, home)
				began := time.Now()
				result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, nil, args...)
				evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
				mu.Lock()
				observed, captured := slices.Clone(requests), slices.Clone(submitted)
				transcripts = append(transcripts, parityCreateWireCase{Lane: lane.Name, Output: mode, Source: scenario, Requests: slices.Clone(wire)})
				mu.Unlock()
				wantRequests := []string{"GET /rest/api/3/issue/PAIR-1?fields=%2Aall", "POST /rest/api/3/issue"}
				if scenario == "jql" || scenario == "filter" {
					wantRequests = append([]string{"POST /rest/api/3/search/jql"}, wantRequests...)
				}
				if scenario == "filter" {
					wantRequests = append([]string{"GET /rest/api/3/filter/73"}, wantRequests...)
				}
				if result.Exit != 0 || len(result.Stderr) != 0 || !slices.Equal(observed, wantRequests) || !parityAuthoringJSON(captured, payload) {
					t.Fatalf("clone writer failed or changed native requests: exit %d; requests %v; stdout %s; stderr %s", result.Exit, observed, result.Stdout, result.Stderr)
				}
				raw := result.Stdout
				if mode == "json" {
					parityAuthoringCleanData(t, raw, lane.Name, "issue.clone")
					raw = parityOriginalFrameData(raw)
				}
				clone := liveIssue{ID: "901", Key: "PAIR-90", Self: server.URL + "/rest/api/3/issue/901"}
				if !parityCloneAcknowledged(raw, lane.Name, "PAIR-1", clone, fields, selectors...) {
					t.Fatal("clone lost its complete source-bound acknowledgement or native receipt")
				}
				history := parityLocalHistory(t, home, lane.Name, server.URL, []string{"PAIR-1", "PAIR-90"}, began)
				parityOpenOnlyHistory(t, home, history, before)
				if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
					t.Fatal(err)
				}
				evidence.Checks["I005.clone.field-flags."+scenario+"."+mode] = []string{lane.Name}
			})
		}
	}
}

func TestParityActualBinaryCloneRemoteSelectorPreview(t *testing.T) {
	lane := parityLane{Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")}
	if lane.Binary == "" {
		t.Skip("requires the admitted current binary")
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	t.Cleanup(func() { saveParityLocal(t, "clone-selector-previews-fixture", evidence) })
	for _, kind := range []string{"jql", "filter"} {
		for _, mode := range []string{"json", "compact"} {
			for _, remote := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/remote=%t", kind, mode, remote), func(t *testing.T) {
					if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
						t.Fatal(err)
					}
					var mu sync.Mutex
					var requests []string
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mu.Lock()
						defer mu.Unlock()
						requests = append(requests, r.Method+" "+r.URL.RequestURI())
						body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
						user, token, ok := r.BasicAuth()
						if !remote || err != nil || len(body) > 4096 || !ok || user != "fixture@example.invalid" || token != "synthetic-fixture-token" || r.Header.Get("Cookie") != "" || r.URL.RawQuery != "" {
							t.Error("clone preview entered an unauthored authenticated request")
							http.Error(w, "invalid owned preview request", http.StatusBadRequest)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						switch r.Method + " " + r.URL.Path {
						case "GET /rest/api/3/filter/73":
							if kind != "filter" || len(body) != 0 {
								t.Error("clone preview changed its filter read")
								http.Error(w, "invalid filter read", http.StatusBadRequest)
								return
							}
							_, _ = io.WriteString(w, `{"id":"73","name":"Owned filter","jql":"key = PAIR-1"}`)
						case "POST /rest/api/3/search/jql":
							if r.Header.Get("Content-Type") != "application/json" || !parityAuthoringJSON(body, []byte(`{"jql":"key = PAIR-1","fields":["key"],"maxResults":101}`)) {
								t.Error("clone preview changed its exact key-only search")
								http.Error(w, "invalid selector search", http.StatusBadRequest)
								return
							}
							_, _ = io.WriteString(w, `{"isLast":true,"issues":[{"id":"101","key":"PAIR-1"}]}`)
						default:
							t.Error("clone preview entered a source read or writer")
							http.Error(w, "unexpected preview request", http.StatusBadRequest)
						}
					}))
					defer server.Close()
					home := t.TempDir()
					config := parityCachePublicConfig(t, home, lane.Name, server.URL)
					query := "key = PAIR-1"
					if kind == "filter" {
						query = "73"
					}
					args := []string{"issue", "clone", "--" + kind, query, "--dry-run"}
					extra := []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE="}
					var wantRequests []string
					summary := map[string]any{"kind": kind, "query": query, "resolved": remote}
					rows := []any{}
					warnings := []map[string]any{}
					if remote {
						args = append(args, "--validate-remote")
						extra = nil
						wantRequests = []string{"POST /rest/api/3/search/jql"}
						if kind == "filter" {
							summary["jql"] = "key = PAIR-1"
							wantRequests = append([]string{"GET /rest/api/3/filter/73"}, wantRequests...)
						}
						rows = append(rows, map[string]any{"issue": map[string]any{"key": "PAIR-1"}, "ok": true, "data": map[string]any{"applied": false}})
					} else {
						warnings = append(warnings, map[string]any{"type": "selector_unresolved", "message": "--" + kind + " was not resolved in this preview: finding which issues it matches is a read from the site, which --dry-run never does, so nothing is listed below. A live run resolves it; add --validate-remote to resolve it here and list the issues without writing anything.", "lossy": true})
					}
					before := parityLocalTree(t, home)
					result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, append(extra, "JIRA_READ_ONLY=true"), args...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					mu.Lock()
					observed := slices.Clone(requests)
					mu.Unlock()
					if result.Exit != 0 || len(result.Stderr) != 0 || !slices.Equal(observed, wantRequests) || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
						t.Fatalf("clone selector preview failed or published state: exit %d; requests %v; stdout %s; stderr %s", result.Exit, observed, result.Stdout, result.Stderr)
					}
					raw := result.Stdout
					if mode == "json" {
						parityAuthoringData(t, raw, lane.Name, "issue.clone", 0)
						var frame map[string]json.RawMessage
						expectedWarnings, err := json.Marshal(warnings)
						if err != nil || json.Unmarshal(raw, &frame) != nil || !parityAuthoringJSON(frame["warnings"], expectedWarnings) {
							t.Fatal("clone selector preview changed its exact unresolved-selector warning")
						}
						raw = parityOriginalFrameData(raw)
					}
					expected, err := json.Marshal(map[string]any{"dry_run": true, "results": rows, "selector": summary})
					if err != nil || !parityAuthoringJSON(raw, expected) {
						t.Fatal("clone preview fabricated source facts or lost exact selector/target facts")
					}
					if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
						t.Fatal(err)
					}
					id := fmt.Sprintf("I005.clone.selector-preview.%s.%s.remote=%t", kind, mode, remote)
					evidence.Checks[id] = []string{lane.Name}
				})
			}
		}
	}
}

func TestParityCloneSelectorReceiptRequiresExactScope(t *testing.T) {
	const valid = `{"dry_run":false,"selector":{"kind":"jql","query":"key = PAIR-1","resolved":true},"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"applied":true,"clone":{"id":"901","key":"PAIR-90","self":"https://example.invalid/rest/api/3/issue/901"},"receipt":{"id":"901","key":"PAIR-90","self":"https://example.invalid/rest/api/3/issue/901"}}}]}`
	clone := liveIssue{ID: "901", Key: "PAIR-90", Self: "https://example.invalid/rest/api/3/issue/901"}
	selector := map[string]any{"kind": "jql", "query": "key = PAIR-1", "resolved": true}
	if !parityCloneAcknowledged([]byte(valid), "after", "PAIR-1", clone, nil, selector) {
		t.Fatal("independent selector-bound clone receipt was refused")
	}
	for _, tc := range []struct{ name, original, replacement string }{
		{"wrong-kind", `"kind":"jql"`, `"kind":"filter"`},
		{"wrong-query", `key = PAIR-1`, `key = PAIR-2`},
		{"unresolved", `"resolved":true`, `"resolved":false`},
		{"missing-selector", `"selector":`, `"unrelated":`},
		{"invented-filter-jql", `"kind":"jql"`, `"jql":"key = PAIR-1","kind":"jql"`},
		{"duplicate-kind", `"kind":"jql"`, `"kind":"filter","kind":"jql"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(valid, tc.original, tc.replacement, 1)
			if changed == valid || parityCloneAcknowledged([]byte(changed), "after", "PAIR-1", clone, nil, selector) {
				t.Fatal("missing, changed or ambiguous selector facts earned acknowledgement credit")
			}
		})
	}
}
