package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestBinarySearchFollowsEmptyPages(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			tokens := make(chan string, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
					http.Error(w, "unexpected request", http.StatusNotFound)
					return
				}
				var query struct {
					JQL           string `json:"jql"`
					NextPageToken string `json:"nextPageToken"`
					MaxResults    int    `json:"maxResults"`
				}
				if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
					t.Error(err)
					http.Error(w, "invalid query", http.StatusBadRequest)
					return
				}
				n := requests.Add(1)
				wantSize := 2
				if n > 2 {
					wantSize = 1
				}
				if query.JQL != "project = ABC" || query.MaxResults != wantSize {
					t.Errorf("request %d: query=%q size=%d, want declared query and remaining count %d", n, query.JQL, query.MaxResults, wantSize)
				}
				select {
				case tokens <- query.NextPageToken:
				default:
					t.Error("search requested an unexpected fifth page")
				}
				w.Header().Set("Content-Type", "application/json")
				switch query.NextPageToken {
				case "":
					_, _ = w.Write([]byte(`{"issues":[],"isLast":false,"nextPageToken":"cursor-a"}`))
				case "cursor-a":
					_, _ = w.Write([]byte(`{"issues":[{"key":"ABC-1","fields":{"summary":"First"}}],"isLast":false,"nextPageToken":"cursor-b"}`))
				case "cursor-b":
					_, _ = w.Write([]byte(`{"issues":[],"isLast":false,"nextPageToken":"cursor-c"}`))
				case "cursor-c":
					_, _ = w.Write([]byte(`{"issues":[{"key":"ABC-2","fields":{"summary":"Second"}}],"isLast":true}`))
				default:
					http.Error(w, "unknown cursor", http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, searchEnv(server.URL), "issue", "list", "--jql", "project = ABC", "--limit", "2", "--output="+mode, "--timeout=5s")
			if exit != 0 || requests.Load() != 4 {
				t.Fatalf("sparse search: exit=%d requests=%d, want four requests and success", exit, requests.Load())
			}
			for _, expected := range []string{"", "cursor-a", "cursor-b", "cursor-c"} {
				if got := <-tokens; got != expected {
					t.Fatalf("requested cursor=%q, want %q", got, expected)
				}
			}
			if mode == "human" {
				if !strings.Contains(stdout, "ABC-1") || !strings.Contains(stdout, "ABC-2") || strings.Contains(stdout, "No issues found") || strings.Contains(stdout+stderr, "cursor-") {
					t.Fatal("human sparse search lost its issues or exposed an internal cursor")
				}
				return
			}
			var out envelope.IssueListOutput
			raw := []byte(stdout)
			if mode == "json" {
				var f frame
				if err := json.Unmarshal(raw, &f); err != nil || !f.OK {
					t.Fatalf("search frame: err=%v ok=%t", err, f.OK)
				}
				raw = f.Data
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatal(err)
			}
			if len(out.Issues) != 2 || out.Issues[0].Key != "ABC-1" || out.Issues[1].Key != "ABC-2" || !out.Pagination.IsLast || out.Pagination.Returned != 2 || out.Truncated {
				t.Fatal("machine sparse search lost ordered results or final pagination facts")
			}
		})
	}
}

func TestBinarySelectorRefusesIncompleteSearch(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"missing cursor", "repeated cursor", "later overflow", "later HTTP failure", "duplicate key", "missing issue array", "null issue array", "missing key", "missing completion", "null completion", "too many empty pages"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			var searches, writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					writes.Add(1)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
					http.Error(w, "unexpected request", http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if searches.Add(1) == 1 {
					if failure == "missing cursor" {
						_, _ = w.Write([]byte(`{"issues":[{"key":"ABC-1"}],"isLast":false}`))
					} else {
						_, _ = w.Write([]byte(`{"issues":[{"key":"ABC-1"}],"isLast":false,"nextPageToken":"cursor-a"}`))
					}
					return
				}
				switch failure {
				case "repeated cursor":
					_, _ = w.Write([]byte(`{"issues":[{"key":"ABC-2"}],"isLast":false,"nextPageToken":"cursor-a"}`))
				case "later overflow":
					issues := make([]map[string]string, 100)
					for i := range issues {
						issues[i] = map[string]string{"key": fmt.Sprintf("ABC-%d", i+2)}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"issues": issues, "isLast": true})
				case "later HTTP failure":
					http.Error(w, "search failed", http.StatusInternalServerError)
				case "duplicate key":
					_, _ = w.Write([]byte(`{"issues":[{"key":"ABC-1"}],"isLast":true}`))
				case "missing issue array":
					_, _ = w.Write([]byte(`{"isLast":true}`))
				case "null issue array":
					_, _ = w.Write([]byte(`{"issues":null,"isLast":true}`))
				case "missing key":
					_, _ = w.Write([]byte(`{"issues":[{}],"isLast":true}`))
				case "missing completion":
					_, _ = w.Write([]byte(`{"issues":[{"key":"ABC-2"}]}`))
				case "null completion":
					_, _ = w.Write([]byte(`{"issues":[{"key":"ABC-2"}],"isLast":null}`))
				case "too many empty pages":
					_ = json.NewEncoder(w).Encode(map[string]any{"issues": []any{}, "isLast": false, "nextPageToken": fmt.Sprintf("cursor-%d", searches.Load())})
				default:
					http.Error(w, "unexpected continuation", http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			stdout, _, exit := runBinaryEnv(t, searchEnv(server.URL), "issue", "delete", "--jql", "project = ABC", "--force", "--output=json", "--no-input", "--timeout=5s")
			var f frame
			if err := json.Unmarshal([]byte(stdout), &f); err != nil {
				t.Fatal(err)
			}
			if exit == 0 || f.OK || writes.Load() != 0 || len(f.Errors) != 1 {
				t.Fatalf("incomplete selector: exit=%d ok=%t errors=%d writes=%d; no mutation may follow an incomplete target search", exit, f.OK, len(f.Errors), writes.Load())
			}
			var failureOutput struct {
				Code string `json:"code"`
				Hint string `json:"hint"`
			}
			if err := json.Unmarshal(f.Errors[0], &failureOutput); err != nil {
				t.Fatal(err)
			}
			wantCode, wantExit := cli.CodeSelectorIncomplete, cli.ExitServer
			switch failure {
			case "later overflow":
				wantCode, wantExit = cli.CodeFlagValueInvalid, cli.ExitValidation
			case "later HTTP failure":
				wantCode = "jira_server_error"
			case "missing issue array", "null issue array", "missing key", "missing completion", "null completion":
				wantCode = cli.CodeSearchResponseInvalid
			}
			if exit != wantExit || failureOutput.Code != wantCode || failureOutput.Hint == "" || f.Meta.ExitCode == nil || *f.Meta.ExitCode != wantExit {
				t.Fatalf("refusal: exit=%d code=%q, want %d/%q with matching metadata and a remedy", exit, failureOutput.Code, wantExit, wantCode)
			}
			if wantCode == cli.CodeSelectorIncomplete && (!strings.Contains(failureOutput.Hint, "nothing was written") || !strings.Contains(failureOutput.Hint, "--from-file")) {
				t.Fatal("selector refusal did not explain the write outcome and explicit-target remedy")
			}
		})
	}
}

func TestBinarySearchReportsIncompleteWalks(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{envelope.TruncatedReasonMissingCursor, envelope.TruncatedReasonRepeatedCursor, envelope.TruncatedReasonMaxEmptyPages} {
		for _, mode := range []string{"json", "compact", "human"} {
			t.Run(reason+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
						t.Error("incomplete search attempted an unexpected request or count estimate")
						http.Error(w, "unexpected request", http.StatusNotFound)
						return
					}
					n := requests.Add(1)
					w.Header().Set("Content-Type", "application/json")
					switch reason {
					case envelope.TruncatedReasonMissingCursor:
						_, _ = w.Write([]byte(`{"issues":[],"isLast":false}`))
					case envelope.TruncatedReasonRepeatedCursor:
						_, _ = fmt.Fprintf(w, `{"issues":[{"key":"ABC-%d","fields":{"summary":"Retained"}}],"isLast":false,"nextPageToken":"private-cursor"}`, n)
					case envelope.TruncatedReasonMaxEmptyPages:
						_ = json.NewEncoder(w).Encode(map[string]any{"issues": []any{}, "isLast": false, "nextPageToken": fmt.Sprintf("private-cursor-%d", n)})
					}
				}))
				t.Cleanup(server.Close)
				stdout, stderr, exit := runBinaryEnv(t, searchEnv(server.URL), "issue", "list", "--jql", "project = ABC", "--limit=3", "--output="+mode, "--timeout=5s")
				wantRequests := int32(1)
				wantReturned := 0
				if reason == envelope.TruncatedReasonRepeatedCursor {
					wantRequests = 2
					wantReturned = 2
				}
				if reason == envelope.TruncatedReasonMaxEmptyPages {
					wantRequests = command.MaxSearchEmptyPages
				}
				if exit != 0 || requests.Load() != wantRequests {
					t.Fatalf("incomplete walk: exit=%d requests=%d, want %d", exit, requests.Load(), wantRequests)
				}
				if mode == "human" {
					if !strings.Contains(stdout+stderr, "search incomplete") || strings.Contains(stdout+stderr, "No issues found") || strings.Contains(stdout+stderr, "private-cursor") {
						t.Fatal("human output hid incompleteness or exposed cursor bytes")
					}
					if reason != envelope.TruncatedReasonMaxEmptyPages && strings.Contains(stdout+stderr, "--unbounded") {
						t.Fatal("cursor fault incorrectly suggested lifting a safety cap")
					}
					return
				}
				var out envelope.IssueListOutput
				raw := []byte(stdout)
				if mode == "json" {
					var f frame
					if err := json.Unmarshal(raw, &f); err != nil || !f.OK {
						t.Fatalf("incomplete search frame: err=%v ok=%t", err, f.OK)
					}
					raw = f.Data
				}
				if err := json.Unmarshal(raw, &out); err != nil {
					t.Fatal(err)
				}
				if !out.Truncated || out.TruncatedReason != reason || out.Pagination.IsLast || out.Pagination.Returned != wantReturned || len(out.Issues) != wantReturned {
					t.Fatal("machine output lost incomplete state or fetched issues")
				}
				if reason == envelope.TruncatedReasonMissingCursor && out.Pagination.NextPageToken != "" {
					t.Fatal("missing cursor was invented")
				}
				if reason != envelope.TruncatedReasonMissingCursor && !strings.HasPrefix(out.Pagination.NextPageToken, "private-cursor") {
					t.Fatal("machine output lost Jira's reported cursor")
				}
			})
		}
	}
}

func TestBinarySearchUnboundedRemedyFinishesSparseWalk(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		n := requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n <= command.MaxSearchEmptyPages {
			_ = json.NewEncoder(w).Encode(map[string]any{"issues": []any{}, "isLast": false, "nextPageToken": fmt.Sprintf("cursor-%d", n)})
			return
		}
		_, _ = w.Write([]byte(`{"issues":[{"key":"ABC-1"}],"isLast":true}`))
	}))
	t.Cleanup(server.Close)
	stdout, _, exit := runBinaryEnv(t, searchEnv(server.URL), "issue", "list", "--jql", "project = ABC", "--all", "--unbounded", "--output=compact", "--timeout=5s")
	var out envelope.IssueListOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatal(err)
	}
	if exit != 0 || requests.Load() != command.MaxSearchEmptyPages+1 || out.Truncated || !out.Pagination.IsLast || len(out.Issues) != 1 || out.Issues[0].Key != "ABC-1" {
		t.Fatal("documented --all --unbounded remedy did not complete the sparse search")
	}
}

func TestBinarySelectorFinishesSearchBeforeWrites(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"write", "remote preview", "read only"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var searches, writes atomic.Int32
			keys := make(chan string, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					if searches.Load() != 4 {
						t.Error("write preceded complete target resolution")
					}
					writes.Add(1)
					select {
					case keys <- strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/"):
					default:
						t.Error("more than two target writes")
					}
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
					http.Error(w, "unexpected request", http.StatusNotFound)
					return
				}
				var query struct {
					NextPageToken string `json:"nextPageToken"`
					MaxResults    int    `json:"maxResults"`
				}
				if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
					t.Error(err)
					return
				}
				n := searches.Add(1)
				wantSize := command.MaxSelectorKeys + 1
				if n > 2 {
					wantSize--
				}
				if query.MaxResults != wantSize {
					t.Errorf("selector requested %d targets, want remaining %d", query.MaxResults, wantSize)
				}
				w.Header().Set("Content-Type", "application/json")
				switch query.NextPageToken {
				case "":
					_, _ = w.Write([]byte(`{"issues":[],"isLast":false,"nextPageToken":"a"}`))
				case "a":
					_, _ = w.Write([]byte(`{"issues":[{"key":"ABC-1"}],"isLast":false,"nextPageToken":"b"}`))
				case "b":
					_, _ = w.Write([]byte(`{"issues":[],"isLast":false,"nextPageToken":"c"}`))
				case "c":
					_, _ = w.Write([]byte(`{"issues":[{"key":"ABC-2"}],"isLast":true}`))
				default:
					http.Error(w, "unknown cursor", http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			args := []string{"issue", "delete", "--jql", "project = ABC", "--force", "--no-input", "--output=json", "--parallelism=1", "--timeout=5s"}
			if mode == "remote preview" {
				args = append(args, "--dry-run", "--validate-remote")
			}
			if mode == "read only" {
				args = append(args, "--read-only")
			}
			stdout, _, exit := runBinaryEnv(t, searchEnv(server.URL), args...)
			var f frame
			if err := json.Unmarshal([]byte(stdout), &f); err != nil {
				t.Fatal(err)
			}
			wantWrites := int32(0)
			if mode == "write" {
				wantWrites = 2
			}
			wantSearches := int32(4)
			if mode == "read only" {
				wantSearches = 0
			}
			if searches.Load() != wantSearches || writes.Load() != wantWrites {
				t.Fatalf("searches=%d writes=%d, want %d/%d", searches.Load(), writes.Load(), wantSearches, wantWrites)
			}
			if mode == "read only" {
				if exit != cli.ExitValidation || f.OK {
					t.Fatal("read-only selector succeeded")
				}
				return
			}
			if exit != 0 || !f.OK {
				t.Fatalf("complete selector failed: exit=%d ok=%t", exit, f.OK)
			}
			if mode == "write" {
				for _, want := range []string{"ABC-1", "ABC-2"} {
					if got := <-keys; got != want {
						t.Fatalf("written key=%q, want %q", got, want)
					}
				}
			}
		})
	}
}

func TestBinarySelectorDetectsOverflowAfterServerClampsPageSize(t *testing.T) {
	t.Parallel()
	for _, overflow := range []bool{false, true} {
		t.Run(fmt.Sprintf("overflow=%t", overflow), func(t *testing.T) {
			t.Parallel()
			var searches, writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					writes.Add(1)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
					http.Error(w, "unexpected request", http.StatusNotFound)
					return
				}
				var query struct {
					MaxResults    int    `json:"maxResults"`
					NextPageToken string `json:"nextPageToken"`
				}
				if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
					t.Error(err)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if searches.Add(1) == 1 {
					if query.MaxResults != command.MaxSelectorKeys+1 || query.NextPageToken != "" {
						t.Error("first request did not ask for cap+1 from the initial cursor")
					}
					issues := make([]map[string]string, command.MaxSelectorKeys)
					for i := range issues {
						issues[i] = map[string]string{"key": fmt.Sprintf("ABC-%d", i+1)}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"issues": issues, "isLast": false, "nextPageToken": "overflow-check"})
					return
				}
				if query.MaxResults != 1 || query.NextPageToken != "overflow-check" {
					t.Error("continuation did not request the one extra issue at the remaining cursor")
				}
				if overflow {
					_, _ = w.Write([]byte(`{"issues":[{"key":"ABC-101"}],"isLast":true}`))
				} else {
					_, _ = w.Write([]byte(`{"issues":[],"isLast":true}`))
				}
			}))
			t.Cleanup(server.Close)
			args := []string{"issue", "delete", "--jql", "project = ABC", "--force", "--no-input", "--output=json", "--timeout=5s"}
			if !overflow {
				args = append(args, "--dry-run", "--validate-remote")
			}
			stdout, _, exit := runBinaryEnv(t, searchEnv(server.URL), args...)
			var f frame
			if err := json.Unmarshal([]byte(stdout), &f); err != nil {
				t.Fatal(err)
			}
			if searches.Load() != 2 || writes.Load() != 0 {
				t.Fatalf("cap boundary: searches=%d writes=%d, want 2/0", searches.Load(), writes.Load())
			}
			if overflow {
				if exit != cli.ExitValidation || f.OK {
					t.Fatal("101-target selection was not refused")
				}
				return
			}
			var preview struct {
				Results []json.RawMessage `json:"results"`
				DryRun  bool              `json:"dry_run"`
			}
			if err := json.Unmarshal(f.Data, &preview); err != nil {
				t.Fatal(err)
			}
			if exit != 0 || !f.OK || !preview.DryRun || len(preview.Results) != command.MaxSelectorKeys {
				t.Fatal("exactly 100 targets with a final empty page were not completely previewed")
			}
		})
	}
}

func TestBinarySelectorTimeoutBeforeWrites(t *testing.T) {
	t.Parallel()
	var searches, writes atomic.Int32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			writes.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		var query struct {
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
			t.Error(err)
			return
		}
		if searches.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"issues":[{"key":"ABC-1"}],"isLast":false,"nextPageToken":"slow-page"}`))
			return
		}
		if query.NextPageToken != "slow-page" {
			t.Error("slow continuation lost its cursor")
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	stdout, _, exit := runBinaryEnv(t, searchEnv(server.URL), "issue", "delete", "--jql", "project = ABC", "--force", "--no-input", "--output=json", "--timeout=500ms")
	var f frame
	if err := json.Unmarshal([]byte(stdout), &f); err != nil {
		t.Fatal(err)
	}
	// --timeout bounds each HTTP attempt. Safe search retries may repeat the
	// slow continuation; no target may be written after those retries fail.
	wantSearches := int32(1 + core.DefaultRetryPolicy().MaxAttempts)
	if exit != cli.ExitTimeout || f.OK || len(f.Errors) != 1 || searches.Load() != wantSearches || writes.Load() != 0 {
		t.Fatalf("interrupted resolution: exit=%d ok=%t searches=%d writes=%d", exit, f.OK, searches.Load(), writes.Load())
	}
	var failure struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(f.Errors[0], &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Code != "timeout" {
		t.Fatalf("interrupted resolution code=%q, want timeout", failure.Code)
	}
}

func searchEnv(site string) []string {
	return []string{
		"JIRA_BASE_URL=" + site,
		"JIRA_EMAIL=person@example.com",
		"JIRA_API_TOKEN=example-fixture-token",
		"JIRA_KEYRING=false",
		"JIRA_READ_ONLY=false",
		"NO_PROXY=127.0.0.1,localhost",
	}
}
