//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

const (
	paritySearchAssigned = `{"id":"10001","key":"PAIR-1","fields":{"summary":"Owned café <&>","labels":["run"],"assignee":{"accountId":"self","displayName":"Fixture"}}}`
	paritySearchControl  = `{"id":"10002","key":"PAIR-2","fields":{"summary":"Control","labels":["run"],"assignee":null}}`
)

func TestParityActualBinarySearch(t *testing.T) {
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
	const query = "key in (PAIR-1,PAIR-2) ORDER BY key ASC"
	for _, lane := range lanes {
		for _, family := range []string{"list", "mine", "inline", "saved-plain", "saved-yaml", "saved-toml"} {
			for _, mode := range []string{"json", "compact"} {
				t.Run(lane.Name+"/"+family+"/"+mode, func(t *testing.T) {
					home := t.TempDir()
					queryDir := filepath.Join(home, "queries")
					if err := os.Mkdir(queryDir, 0o700); err != nil {
						t.Fatal(err)
					}
					body := query
					if family == "saved-yaml" {
						body = "---\nname: Owned query\ndescription: Independent search\nproject: metadata-only\n---\n" + query
					}
					if family == "saved-toml" {
						body = "+++\nname = 'Owned query'\ndescription = 'Independent search'\nproject = 'metadata-only'\n+++\n" + query
					}
					queryFile := filepath.Join(queryDir, "owned.jql")
					if err := os.WriteFile(queryFile, []byte(body), 0o600); err != nil {
						t.Fatal(err)
					}
					var mu sync.Mutex
					var requests [][]byte
					var badRequest bool
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mu.Lock()
						defer mu.Unlock()
						w.Header().Set("Content-Type", "application/json")
						user, password, auth := r.BasicAuth()
						if !auth || user != "fixture@example.invalid" || password != "synthetic-fixture-token" {
							badRequest = true
							http.Error(w, "bad binding", http.StatusUnauthorized)
							return
						}
						raw, err := io.ReadAll(io.LimitReader(r.Body, 8193))
						if err != nil || len(raw) > 8192 || r.Method != "POST" || r.URL.Path != "/rest/api/3/search/jql" || r.URL.RawQuery != "" || !jsontext.Value(raw).IsValid() {
							badRequest = true
							http.Error(w, "bad search entry", 400)
							return
						}
						requests = append(requests, slices.Clone(raw))
						issues := paritySearchAssigned + "," + paritySearchControl
						if family == "mine" {
							issues = paritySearchAssigned
						}
						_, _ = fmt.Fprintf(w, `{"issues":[%s],"isLast":true}`, issues)
					}))
					defer server.Close()
					config := parityCachePublicConfig(t, home, lane.Name, server.URL)
					public, err := os.ReadFile(config)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(config, append(fmt.Appendf(nil, "queries_path=%q\n", queryDir), public...), 0o600); err != nil {
						t.Fatal(err)
					}
					args := []string{"issue", family, "--jql=" + query, "--detail", "--all"}
					operation, id := "issue."+family, "I018"
					if family == "mine" {
						id = "I019"
						if lane.Name == "before" {
							operation = "issue.list"
						}
					}
					if family == "inline" || strings.HasPrefix(family, "saved-") {
						args = []string{"issue", "list", "--jql=" + query, "--detail", "--expand=", "--all"}
						operation = "issue.list"
						id = "C064"
						if lane.Name == "before" {
							args = []string{"search", "jql", query, "--full", "--all"}
							operation = "search.jql"
						}
						if strings.HasPrefix(family, "saved-") {
							id = "C065"
							args = []string{"issue", "list", "--saved=owned", "--detail", "--expand=", "--all"}
							if lane.Name == "before" {
								args = []string{"search", "saved", "owned", "--full"}
								operation = "search.saved"
							}
						}
					}
					result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_READ_ONLY=true", "JIRA_QUERIES_PATH=" + queryDir}, args...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					if result.Exit != 0 || len(result.Stderr) != 0 {
						t.Fatalf("search invocation failed: exit%d stdout%s stderr%s", result.Exit, result.Stdout, result.Stderr)
					}
					raw := result.Stdout
					if mode == "json" {
						parityAuthoringData(t, raw, lane.Name, operation, 0)
						raw = parityOriginalFrameData(raw)
					}
					rows, err := paritySearchRows(raw, lane.Name)
					want := []paritySearchRow{{"10001", "PAIR-1", "Owned café <&>", "self", []string{"run"}}, {"10002", "PAIR-2", "Control", "", []string{"run"}}}
					if family == "mine" {
						want = want[:1]
					}
					if err != nil || !reflect.DeepEqual(rows, want) {
						t.Fatalf("search native projection mismatch: %v; %s", err, raw)
					}
					if strings.HasPrefix(family, "saved-") {
						display, description, project := "owned", "", ""
						if family != "saved-plain" {
							display, description, project = "Owned query", "Independent search", "metadata-only"
						}
						if !paritySearchSavedMetadata(raw, lane.Name, "owned", display, query, description, project) {
							t.Fatalf("saved-query metadata changed: %s", raw)
						}
					}
					drained := lane.Name != "before" || !strings.HasPrefix(family, "saved-")
					if !paritySearchPage(result.Stdout, lane.Name, mode, len(want), 50, true, "", drained) {
						t.Fatalf("search pagination mismatch: %s", result.Stdout)
					}
					mu.Lock()
					observed := slices.Clone(requests)
					invalid := badRequest
					mu.Unlock()
					if invalid || len(observed) != 1 {
						t.Fatal("search made an unauthenticated, duplicate or unexpected request")
					}
					expectedJQL := query
					if family == "mine" {
						expectedJQL = "assignee = currentUser() AND (key in (PAIR-1,PAIR-2)) ORDER BY key ASC"
					}
					if !strings.HasPrefix(family, "saved-") && !paritySearchMetadata(raw, lane.Name, family, expectedJQL) {
						t.Fatalf("search metadata changed: %s", raw)
					}
					pageSize := 50
					if lane.Name == "after" {
						pageSize = 100
					}
					request := map[string]any{"jql": expectedJQL, "fields": []string{"*all"}, "maxResults": pageSize}
					if family == "list" || family == "mine" {
						request["expand"] = "renderedFields,names,schema,transitions,operations,changelog"
					}
					wantRequest, _ := json.Marshal(request)
					if !parityAuthoringJSON(observed[0], wantRequest) {
						t.Fatalf("search request differs: got%s want%s", observed[0], wantRequest)
					}
					retained, err := os.ReadFile(queryFile)
					if err != nil || !bytes.Equal(retained, []byte(body)) {
						t.Fatal("saved search changed its literal input")
					}
					if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
						t.Fatal(err)
					}
					check := id + ".search." + family + "." + mode
					evidence.Checks[check] = append(evidence.Checks[check], lane.Name)
				})
			}
		}
	}
	saveParityLocal(t, "search-fixture", evidence)
}
