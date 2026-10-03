package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

const commentReadBody = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Retained comment <&>","future":9007199254740993}]}]}`

func commentReadPage(start, total int, id string) string {
	return fmt.Sprintf(`{"startAt":%d,"maxResults":100,"total":%d,"comments":[{"id":%q,"created":"2026-01-02T03:04:05.000+0000","author":{"displayName":"Fixture author"},"body":%s}]}`, start, total, id, commentReadBody)
}

type commentReadFrame struct {
	OK   bool `json:"ok"`
	Meta struct {
		ExitCode *int `json:"exit_code"`
	} `json:"meta"`
	Data   *envelope.IssueCommentListOutput `json:"data"`
	Errors []envelope.Error                 `json:"errors"`
}

func TestCommentWalkFailureRetainsObservedPage(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"forbidden", "interrupt"} {
		for _, mode := range []string{"json", "compact", "human", "agent"} {
			t.Run(failure+"/"+mode, func(t *testing.T) {
				if failure == "interrupt" && runtime.GOOS == "windows" {
					t.Skip("os.Interrupt is not supported for subprocesses on Windows")
				}
				t.Parallel()
				started := make(chan struct{})
				var once sync.Once
				var mu sync.Mutex
				var offsets []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if req.Method != http.MethodGet || req.URL.Path != "/rest/api/3/issue/KEY-1/comment" || req.URL.Query().Get("maxResults") != "100" || req.URL.Query().Get("orderBy") != "+created" {
						t.Error("comment walk changed its paginated read or order")
					}
					offset := req.URL.Query().Get("startAt")
					mu.Lock()
					offsets = append(offsets, offset)
					mu.Unlock()
					if offset == "0" {
						_, _ = w.Write([]byte(commentReadPage(0, 2, "10042")))
						return
					}
					if failure == "interrupt" {
						once.Do(func() { close(started) })
						<-req.Context().Done()
						return
					}
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"errorMessages":["fixture denied\u001b[31m\u0007"]}`))
				}))
				t.Cleanup(server.Close)
				env := editReceiptEnv(server.URL)
				args := []string{"issue", "comment", "list", "KEY-1", "--all", "--order=oldest", "--no-input"}
				if mode == "agent" {
					env = append(env, "AGENT=1")
				} else {
					args = append(args, "--output="+mode)
				}
				if mode == "json" || mode == "compact" {
					args = append(args, "--jq", `error("failed comment reads bypass jq")`)
				}
				var onStart func(*os.Process)
				wantExit, wantCode := cli.ExitAuth, "jira_forbidden"
				if failure == "interrupt" {
					wantExit, wantCode = cli.ExitCanceled, cli.CodeCanceled
					onStart = func(process *os.Process) {
						select {
						case <-started:
						case <-time.After(5 * time.Second):
							t.Fatal("comment read did not reach the independent cancellation stimulus")
						}
						if err := process.Signal(os.Interrupt); err != nil {
							t.Fatal(err)
						}
					}
				}
				stdout, stderr, exit := runBinaryEnvStarted(t, env, onStart, args...)
				mu.Lock()
				got := append([]string(nil), offsets...)
				mu.Unlock()
				if exit != wantExit || len(got) != 2 || got[0] != "0" || got[1] != "1" || strings.ContainsAny(stderr, "\x1b\a") {
					t.Fatalf("comment read changed failure classification or restarted the walk: exit=%d offsets=%v stderr=%q", exit, got, stderr)
				}
				if mode == "human" {
					if !strings.Contains(stdout, "Retained comment") || !strings.Contains(stdout, "incomplete") || strings.Contains(stdout, "No comments.") || strings.ContainsAny(stdout, "\x1b\a") {
						t.Fatal("failed comment read discarded its observed body or claimed a complete thread")
					}
					return
				}
				var frame commentReadFrame
				if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != wantExit || len(frame.Errors) != 1 || frame.Errors[0].Code != wantCode || frame.Data == nil {
					t.Fatal("failed comment read discarded its page or full failure frame")
				}
				out := frame.Data
				if out.Issue.Key != "KEY-1" || len(out.Comments) != 1 || out.Comments[0].ID != "10042" || string(out.Comments[0].Body) != commentReadBody || out.Pagination.StartAt != 0 || out.Pagination.Returned != 1 || out.Pagination.Total != 2 || !out.Truncated || out.TruncatedReason != "read_failed" {
					t.Fatal("failed comment read changed observed native data or omitted its incomplete state")
				}
			})
		}
	}
}

func TestCommentReadRejectsMalformedPages(t *testing.T) {
	t.Parallel()
	for name, response := range map[string]string{
		"null": "null", "missing metadata": `{"comments":[]}`, "null metadata": `{"startAt":null,"maxResults":50,"total":0,"comments":[]}`,
		"missing comments": `{"startAt":0,"maxResults":50,"total":0}`, "null comments": `{"startAt":0,"maxResults":50,"total":0,"comments":null}`,
		"negative offset": `{"startAt":-1,"maxResults":50,"total":0,"comments":[]}`, "negative limit": `{"startAt":0,"maxResults":-1,"total":0,"comments":[]}`,
		"negative total": `{"startAt":0,"maxResults":50,"total":-1,"comments":[]}`, "impossible extent": `{"startAt":0,"maxResults":50,"total":0,"comments":[{"id":"10042"}]}`,
		"exceeds page limit": `{"startAt":0,"maxResults":0,"total":1,"comments":[{"id":"10042"}]}`,
		"missing identity":   `{"startAt":0,"maxResults":50,"total":1,"comments":[{}]}`, "blank identity": `{"startAt":0,"maxResults":50,"total":1,"comments":[{"id":" \t"}]}`,
		"repeated identity":  `{"startAt":0,"maxResults":50,"total":2,"comments":[{"id":"10042"},{"id":"10042"}]}`,
		"trailing document":  `{"startAt":0,"maxResults":50,"total":0,"comments":[]} {}`,
		"empty before total": `{"startAt":0,"maxResults":50,"total":1,"comments":[]}`,
		"wrong offset":       `{"startAt":1,"maxResults":50,"total":2,"comments":[{"id":"10042"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				mu.Lock()
				calls++
				mu.Unlock()
				if req.Method != http.MethodGet || req.URL.Path != "/rest/api/3/issue/KEY-1/comment" {
					t.Error("comment list changed its read boundary")
				}
				_, _ = w.Write([]byte(response))
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "comment", "list", "KEY-1", "--output=json", "--no-input")
			var frame commentReadFrame
			mu.Lock()
			got := calls
			mu.Unlock()
			if exit != cli.ExitServer || got != 1 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Data != nil || len(frame.Errors) != 1 || frame.Errors[0].Code != "comment_response_invalid" {
				t.Fatalf("malformed comment page became a usable thread: exit=%d calls=%d stdout=%q stderr=%q", exit, got, stdout, stderr)
			}
		})
	}
}

func TestCommentWalkRejectsInconsistentPages(t *testing.T) {
	t.Parallel()
	for name, second := range map[string]string{
		"wrong offset":       commentReadPage(0, 2, "10043"),
		"repeated identity":  commentReadPage(1, 2, "10042"),
		"empty before total": `{"startAt":1,"maxResults":100,"total":2,"comments":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				mu.Lock()
				calls++
				mu.Unlock()
				response := second
				if req.URL.Query().Get("startAt") == "0" {
					response = commentReadPage(0, 2, "10042")
				}
				_, _ = w.Write([]byte(response))
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "comment", "list", "KEY-1", "--all", "--output=json", "--no-input")
			var frame commentReadFrame
			mu.Lock()
			got := calls
			mu.Unlock()
			if exit != cli.ExitServer || got != 2 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "comment_response_invalid" || frame.Data == nil || len(frame.Data.Comments) != 1 || frame.Data.Comments[0].ID != "10042" || frame.Data.Pagination.Returned != 1 || frame.Data.Pagination.Total != 2 || !frame.Data.Truncated || frame.Data.TruncatedReason != "response_invalid" {
				t.Fatalf("inconsistent comment walk published suspect data or discarded the earlier page: exit=%d calls=%d stdout=%q stderr=%q", exit, got, stdout, stderr)
			}
		})
	}
}

func TestCommentListInvalidWindowRefusedBeforeHTTP(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"--start=-1", "--limit=0", "--limit=-1"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				mu.Lock()
				calls++
				mu.Unlock()
				w.WriteHeader(http.StatusBadRequest)
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "comment", "list", "KEY-1", flag, "--output=json", "--no-input")
			var frame commentReadFrame
			mu.Lock()
			got := calls
			mu.Unlock()
			if exit != cli.ExitValidation || got != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Data != nil || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeFlagValueInvalid {
				t.Fatalf("invalid comment window reached Jira or changed input taxonomy: exit=%d calls=%d stdout=%q stderr=%q", exit, got, stdout, stderr)
			}
		})
	}
}

func TestCommentReadPreservesValidNativePages(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, response string
		start, limit   int
		wantID         string
		wantPagination envelope.OffsetPagination
	}{
		{name: "short native page", response: commentReadPage(2, 9, "0"), start: 2, limit: 100, wantID: "0", wantPagination: envelope.OffsetPagination{StartAt: 2, MaxResults: 100, Total: 9, Returned: 1}},
		{name: "opaque identity", response: commentReadPage(0, 1, "opaque-comment"), limit: 100, wantID: "opaque-comment", wantPagination: envelope.OffsetPagination{MaxResults: 100, Total: 1, Returned: 1}},
		{name: "empty", response: `{"startAt":0,"maxResults":50,"total":0,"comments":[]}`, limit: 50, wantPagination: envelope.OffsetPagination{MaxResults: 50}},
		{name: "beyond end clamped", response: `{"startAt":4,"maxResults":50,"total":4,"comments":[]}`, start: 9, limit: 50, wantPagination: envelope.OffsetPagination{StartAt: 4, MaxResults: 50, Total: 4}},
		{name: "beyond end retained", response: `{"startAt":9,"maxResults":50,"total":4,"comments":[]}`, start: 9, limit: 50, wantPagination: envelope.OffsetPagination{StartAt: 9, MaxResults: 50, Total: 4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodGet || req.URL.Query().Get("startAt") != fmt.Sprint(tc.start) || req.URL.Query().Get("maxResults") != fmt.Sprint(tc.limit) || req.URL.Query().Get("orderBy") != "-created" {
					t.Error("native comment page changed the requested window or default order")
				}
				_, _ = w.Write([]byte(tc.response))
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "comment", "list", "KEY-1", "--start="+fmt.Sprint(tc.start), "--limit="+fmt.Sprint(tc.limit), "--output=json", "--no-input")
			var frame commentReadFrame
			if exit != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || frame.Data == nil || frame.Data.Truncated || frame.Data.Comments == nil {
				t.Fatalf("valid comment page was lost: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			if len(frame.Data.Comments) != tc.wantPagination.Returned || frame.Data.Pagination != tc.wantPagination {
				t.Fatal("valid comment page changed native pagination or fabricated comments")
			}
			if tc.wantID != "" && (frame.Data.Comments[0].ID != tc.wantID || string(frame.Data.Comments[0].Body) != commentReadBody) {
				t.Fatal("valid comment page changed its opaque identity, native body or offset")
			}
		})
	}
}
