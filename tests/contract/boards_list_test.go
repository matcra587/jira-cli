package contract

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func parseBoards(t *testing.T, stdout, mode string, exit int) envelope.BoardsListOutput {
	t.Helper()
	var out envelope.BoardsListOutput
	data := adfData(t, stdout, mode, "boards.list", exit == 0)
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func boardCacheEnv(t *testing.T, site string) []string {
	t.Helper()
	home := t.TempDir()
	return append(editReceiptEnv(site), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, "config"), "XDG_CACHE_HOME="+filepath.Join(home, "cache"), "JIRA_READ_ONLY=true")
}

func TestBoardsListBinaryAccountIsolationAndCacheWindows(t *testing.T) {
	t.Parallel()
	var boardCalls, projectCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		account, _, _ := r.BasicAuth()
		if r.Method != "GET" || r.URL.Query().Get("startAt") != "0" || r.URL.Query().Get("maxResults") != "50" {
			t.Errorf("wrong board request: %s %s", r.Method, r.URL)
		}
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			boardCalls.Add(1)
			_, _ = fmt.Fprintf(w, `{"isLast":true,"startAt":0,"maxResults":50,"values":[{"id":99,"name":"Later"},{"id":42,"name":%q,"type":null}]}`, account)
		case "/rest/agile/1.0/board/42/project", "/rest/agile/1.0/board/99/project":
			projectCalls.Add(1)
			_, _ = fmt.Fprint(w, `{"isLast":true,"startAt":0,"maxResults":50,"values":[{"id":"2","key":"KEY10"},{"id":"1","key":"KEY2"}]}`)
		default:
			t.Errorf("unexpected route: %s", r.URL)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	env := boardCacheEnv(t, server.URL)
	for _, step := range []struct {
		email string
		flags []string
		cache bool
		count int
		calls int32
	}{{"first@example.com", []string{"--limit=1"}, false, 1, 1}, {"first@example.com", []string{"--all", "--limit=1"}, true, 2, 1}, {"other@example.com", []string{"--all"}, false, 2, 2}, {"first@example.com", []string{"--all"}, true, 2, 2}, {"first@example.com", []string{"--refresh", "--all"}, false, 2, 3}} {
		stdout, stderr, exit := runBinaryEnv(t, append(env, "JIRA_EMAIL="+step.email), append([]string{"boards", "list", "--output=json"}, step.flags...)...)
		out := parseBoards(t, stdout, "json", exit)
		if exit != 0 || stderr != "" || out.FromCache != step.cache || out.Count != step.count || out.Pagination.Total != 2 || out.Pagination.IsLast != (step.count == 2) || !out.Pagination.Complete || boardCalls.Load() != step.calls || projectCalls.Load() != 2*step.calls || out.Boards[0].ID != 42 || out.Boards[0].Name != step.email || !out.Boards[0].Type.Null || !reflect.DeepEqual(out.Boards[0].ProjectKeys, []string{"KEY2", "KEY10"}) {
			t.Fatalf("scope/cache/window changed: %+v exit=%d stderr=%q calls=%d/%d", out, exit, stderr, boardCalls.Load(), projectCalls.Load())
		}
	}
}

func TestBoardsListBinaryPartialProjectWarningAndFailureFrame(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/rest/agile/1.0/board" {
					_, _ = fmt.Fprint(w, `{"isLast":true,"startAt":0,"maxResults":50,"values":[{"id":42,"name":"Sprint"}]}`)
					return
				}
				w.WriteHeader(403)
			}))
			defer server.Close()
			stdout, _, exit := runBinaryEnv(t, boardCacheEnv(t, server.URL), "boards", "list", "--output="+mode)
			out := parseBoards(t, stdout, mode, exit)
			if exit != 0 || out.Count != 1 || out.Boards[0].ProjectsComplete || out.Boards[0].ProjectKeys == nil || len(out.ProjectFailures) != 1 || out.ProjectFailures[0].Error.Code != "jira_forbidden" {
				t.Fatalf("enrichment failure hidden: %s", stdout)
			}
			if mode == "json" {
				var f frame
				if json.Unmarshal([]byte(stdout), &f) != nil {
					t.Fatal("bad envelope")
				}
				var e struct {
					Warnings []envelope.Warning `json:"warnings"`
				}
				if json.Unmarshal([]byte(stdout), &e) != nil || len(e.Warnings) != 1 || !e.Warnings[0].Lossy {
					t.Fatal("project failure lacks envelope warning")
				}
			}
		})
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("startAt") == "0" {
			_, _ = fmt.Fprint(w, `{"isLast":false,"startAt":0,"maxResults":50,"values":[{"id":42,"name":"Sprint"}]}`)
			return
		}
		w.WriteHeader(403)
	}))
	defer server.Close()
	stdout, _, exit := runBinaryEnv(t, boardCacheEnv(t, server.URL), "boards", "list", "--output=compact", "--jq=.boards[].id")
	out := parseBoards(t, stdout, "compact", exit)
	if exit != 1 || out.Count != 1 || out.Boards[0].ID != 42 || out.NextStart == nil || *out.NextStart != 1 || out.CacheSaved || calls.Load() != 2 || !strings.Contains(stdout, "jira_forbidden") {
		t.Fatalf("partial failure was filtered/discarded/cached: %d %s", exit, stdout)
	}
}

func TestBoardsListBinaryDefaultCapsAndUnbounded(t *testing.T) {
	t.Parallel()
	for _, unbounded := range []bool{false, true} {
		t.Run(strconv.FormatBool(unbounded), func(t *testing.T) {
			t.Parallel()
			var pages atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/rest/agile/1.0/board" {
					_, _ = fmt.Fprint(w, `{"isLast":true,"startAt":0,"maxResults":50,"values":[]}`)
					return
				}
				start, err := strconv.Atoi(r.URL.Query().Get("startAt"))
				if err != nil {
					t.Error(err)
				}
				pages.Add(1)
				_, _ = fmt.Fprintf(w, `{"isLast":%t,"startAt":%d,"maxResults":50,"values":[{"id":%d,"name":"Sprint"}]}`, start == 100, start, start+1)
			}))
			defer server.Close()
			args := []string{"boards", "list", "--all", "--output=json"}
			if unbounded {
				args = append(args, "--unbounded")
			}
			stdout, _, exit := runBinaryEnv(t, boardCacheEnv(t, server.URL), args...)
			out := parseBoards(t, stdout, "json", exit)
			want := 100
			if unbounded {
				want = 101
			}
			if exit != 0 || out.Count != want || int(pages.Load()) != want || out.Truncated == unbounded || out.Pagination.Complete != unbounded {
				t.Fatalf("fetch cap ignored: pages=%d output=%+v", pages.Load(), out)
			}
			if !unbounded && (out.TruncatedReason != "max_pages" || out.NextStart == nil || *out.NextStart != 100) {
				t.Fatal("cap lost truthful resume metadata")
			}
		})
	}
}

func TestBoardsListValidatesTTLBeforeIOAndDiscoverableWithoutClient(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "boards", "list", "--ttl-minutes="+strconv.Itoa(math.MaxInt), "--output=json")
	if exit != 3 || calls.Load() != 0 || !strings.Contains(stdout, "flag_value_invalid") {
		t.Fatalf("overflow reached I/O: %d %s", exit, stdout)
	}
	for _, args := range [][]string{{"boards", "list", "--help", "--output=json"}, {"agent", "schema", "--path=boards list", "--output=json"}, {"completion", "bash"}} {
		stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_EMAIL=", "JIRA_API_TOKEN="), args...)
		if exit != 0 || stderr != "" || stdout == "" || calls.Load() != 0 {
			t.Fatalf("local discovery constructed client: %v %d %q", args, exit, stderr)
		}
	}
}
