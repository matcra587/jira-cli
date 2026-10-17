package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gechr/x/shell"
	"github.com/itchyny/gojq"

	"github.com/matcra587/jira-cli/internal/agentguides"
)

func TestGuideBatchOutcomeRecipesExecute(t *testing.T) {
	t.Parallel()
	guides, err := agentguides.Load()
	if err != nil {
		t.Fatal(err)
	}
	guide, _ := guides.Get("machine-output")
	section, _ := guide.Section("Save")
	var expressions []*gojq.Query
	for line := range strings.SplitSeq(section.Body, "\n") {
		if !strings.HasPrefix(line, "jq ") {
			continue
		}
		args, err := shell.Split(line)
		if err != nil || len(args) != 3 || args[0] != "jq" || args[2] != "batch.json" {
			t.Fatalf("invalid saved-receipt recipe: %q", line)
		}
		query, err := gojq.Parse(args[1])
		if err != nil {
			t.Fatalf("published jq expression does not parse: %v", err)
		}
		expressions = append(expressions, query)
	}
	if len(expressions) != 2 {
		t.Fatal("guide must publish all-outcome and failed-outcome receipt inspections")
	}
	cases := []struct {
		name         string
		failed       []bool
		exit         int
		clone, cache bool
	}{
		{name: "all-success", failed: []bool{false, false}},
		{name: "mixed-success", failed: []bool{false, true}},
		{name: "total-failure", failed: []bool{true, true}, exit: 3},
		{name: "acknowledged-followup-failure", clone: true, failed: []bool{true}, exit: 5},
		{name: "mixed-resource-refresh", cache: true, failed: []bool{false, true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			identities := []string{"KEY-123", "KEY-124"}
			args := []string{"issue", "edit", "KEY-123", "KEY-124", "--summary=Guide batch change", "--parallelism=1"}
			if tc.clone {
				identities = []string{"KEY-123"}
				args = []string{"issue", "clone", "KEY-123", "--force", "--parallelism=1"}
			} else if tc.cache {
				identities = []string{"fields", "labels"}
				args = []string{"cache", "refresh", "fields", "labels", "--force", "--parallelism=1"}
			}
			var mu sync.Mutex
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests = append(requests, r.Method+" "+r.URL.Path)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch {
				case tc.clone && r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/KEY-123":
					_, _ = io.WriteString(w, `{"id":"101","key":"KEY-123","fields":{"summary":"Guide batch clone","project":{"key":"KEY"},"issuetype":{"name":"Task"}}}`)
				case tc.clone && r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/issue":
					w.WriteHeader(http.StatusCreated)
					_, _ = io.WriteString(w, `{"id":"9001","key":"NEW-1","transition":{"status":400}}`)
				case tc.cache && r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/field":
					_, _ = io.WriteString(w, `[{"id":"summary","name":"Summary"}]`)
				case tc.cache && r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/label":
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"errorMessages":["Guide resource refused"]}`)
				case !tc.clone && !tc.cache && r.Method == http.MethodPut:
					index := slices.Index(identities, strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/"))
					var payload struct {
						Fields struct{ Summary string } `json:"fields"`
					}
					if index < 0 || json.NewDecoder(r.Body).Decode(&payload) != nil || payload.Fields.Summary != "Guide batch change" {
						t.Error("batch edit changed its native target or authored payload")
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					if tc.failed[index] {
						w.WriteHeader(http.StatusBadRequest)
						_, _ = io.WriteString(w, `{"errorMessages":["Guide target refused"]}`)
					} else {
						w.WriteHeader(http.StatusNoContent)
					}
				default:
					t.Errorf("unexpected guide batch request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusInternalServerError)
				}
			}))
			defer server.Close()
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), append(args, "--no-input", "--output=json")...)
			if exit != tc.exit {
				t.Fatalf("batch exit=%d want=%d stdout=%s stderr=%s", exit, tc.exit, stdout, stderr)
			}
			var document map[string]any
			if err := json.Unmarshal([]byte(stdout), &document); err != nil {
				t.Fatalf("batch did not emit a complete saved JSON receipt: %v", err)
			}
			if document["ok"] != (tc.exit == 0) || (tc.exit == 0 && !reflect.DeepEqual(document["errors"], []any{})) {
				t.Fatal("batch envelope no longer establishes the documented mixed-success case")
			}
			mu.Lock()
			gotRequests := slices.Clone(requests)
			mu.Unlock()
			wantRequests := []string{"PUT /rest/api/3/issue/KEY-123", "PUT /rest/api/3/issue/KEY-124"}
			if tc.clone {
				wantRequests = []string{"GET /rest/api/3/issue/KEY-123", "POST /rest/api/3/issue"}
			} else if tc.cache {
				wantRequests = []string{"GET /rest/api/3/field", "GET /rest/api/3/label"}
			}
			if !slices.Equal(gotRequests, wantRequests) {
				t.Fatalf("batch requests changed or replayed: %v", gotRequests)
			}
			for index, expression := range expressions {
				var got []string
				iterator := expression.Run(document)
				for value, more := iterator.Next(); more; value, more = iterator.Next() {
					row, ok := value.(map[string]any)
					if !ok {
						t.Fatalf("published receipt expression returned a non-row: %v", value)
					}
					identity, _ := row["resource"].(string)
					if !tc.cache {
						issue, _ := row["issue"].(map[string]any)
						identity, _ = issue["key"].(string)
					}
					position := slices.Index(identities, identity)
					if position < 0 || row["ok"] != !tc.failed[position] {
						t.Fatal("published inspection lost target identity or its exact verdict")
					}
					if tc.failed[position] {
						failure, _ := row["error"].(map[string]any)
						code := "jira_bad_request"
						if tc.clone {
							code = "issue_create_followup_failed"
							data, _ := row["data"].(map[string]any)
							clone, _ := data["clone"].(map[string]any)
							if data["applied"] != true || clone["key"] != "NEW-1" || data["receipt"] == nil {
								t.Fatal("failed-row inspection discarded an acknowledged creation")
							}
						}
						if failure["code"] != code {
							t.Fatal("failed-row inspection lost its structured error")
						}
					} else if row["data"] == nil || row["error"] != nil {
						t.Fatal("successful-row inspection lost its receipt")
					}
					got = append(got, identity)
				}
				var want []string
				for position, identity := range identities {
					if index == 0 || tc.failed[position] {
						want = append(want, identity)
					}
				}
				if !slices.Equal(got, want) {
					t.Fatalf("receipt inspection %d retained %v want %v", index, got, want)
				}
			}
		})
	}
}
