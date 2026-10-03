//go:build flagmatrix

package matrix

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const worklogNative = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"native <>&"}]}]}`

func TestWorklogAddFlagEffects(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"time-spent", "started", "markdown", "markdown-file", "json-input", "dry-run", "notify", "skip-notify", "parallelism", "body", "description", "body-file", "template", "description-file", "from-json", "body-adf", "comment-markdown"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			var calls, active, peak atomic.Int32
			seconds := 60
			wantNotify := ""
			if flag == "notify" || flag == "skip-notify" {
				wantNotify = "false"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(r.Body)
				var payload map[string]any
				if err != nil || json.Unmarshal(body, &payload) != nil || r.Method != "POST" || r.URL.Query().Get("notifyUsers") != wantNotify || payload["timeSpentSeconds"] != float64(seconds) || payload["started"] != "2026-01-02T12:30:00.000+0000" {
					t.Errorf("flag did not change request as expected: %s %s %s %v", r.Method, r.URL, body, err)
					w.WriteHeader(400)
					return
				}
				n := active.Add(1)
				for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
				}
				if flag == "parallelism" {
					time.Sleep(100 * time.Millisecond)
				}
				active.Add(-1)
				w.WriteHeader(201)
				_, _ = fmt.Fprint(w, `{"id":"100","timeSpentSeconds":60}`)
			}))
			defer server.Close()
			args := []string{"worklog", "add", "KEY-1", "--no-input"}
			files := map[string]string{"notes.md": "file comment", "native.json": `{"timeSpentSeconds":120,"started":"2026-01-02T12:30:00Z","comment":` + worklogNative + `,"future":false}`}
			stdin := "competing stdin comment"
			wantComment := ""
			wantRoute := ""
			switch flag {
			case "time-spent":
				seconds = 120
				args = append(args, "--time-spent=2m", "--json-input=-")
				stdin = `{"timeSpentSeconds":60,"started":"2026-01-02T12:30:00Z"}`
			case "started":
				args = append(args, "--started=2026-01-02T12:30:00Z", "--json-input=-")
				stdin = `{"timeSpentSeconds":60,"started":"1980-01-01"}`
			case "json-input", "from-json", "body-adf":
				seconds = 120
				args = append(args, "--"+flag+"=native.json")
				wantComment = "native <>&"
				wantRoute = "adf"
			default:
				args = append(args, "--time-spent=1m", "--started=2026-01-02T12:30:00Z")
				switch flag {
				case "markdown", "body", "description", "comment-markdown":
					args = append(args, "--"+flag+"=inline comment")
					wantComment = "inline comment"
					wantRoute = "markdown"
				case "markdown-file", "body-file", "template", "description-file":
					args = append(args, "--"+flag+"=notes.md")
					wantComment = "file comment"
					wantRoute = "markdown"
				case "notify":
					args = append(args, "--notify=false")
				case "parallelism":
					args = append(args, "KEY-2", "--parallelism=1")
				default:
					args = append(args, "--"+flag)
				}
			}
			res := fwExec(t, server.URL, files, stdin, args...)
			out := fwParse(t, res)
			wantCalls := int32(1)
			if flag == "dry-run" {
				wantCalls = 0
			}
			if flag == "parallelism" {
				wantCalls = 2
			}
			if res.Exit != 0 || calls.Load() != wantCalls || (flag == "parallelism" && peak.Load() != 1) || fwAt(t, out.Data, "payload", "timeSpentSeconds") != float64(seconds) || fwAt(t, out.Data, "payload", "started") != "2026-01-02T12:30:00.000+0000" || fwAt(t, out.Data, "notify") != (wantNotify == "") {
				t.Fatalf("worklog flag lacked effect: %s exit=%d calls=%d peak=%d output=%s", flag, res.Exit, calls.Load(), peak.Load(), res.Stdout)
			}
			if flag == "dry-run" && (fwAt(t, out.Data, "dry_run") != true || fwAt(t, out.Data, "results", 0, "data", "applied") != false) {
				t.Fatal("preview invented application")
			}
			if wantComment != "" && (fwAt(t, out.Data, "body_route") != wantRoute || fwAt(t, out.Data, "payload", "comment", "content", 0, "content", 0, "text") != wantComment) {
				t.Fatal("body route or authored comment changed")
			}
			if wantRoute == "adf" && fwAt(t, out.Data, "payload", "future") != false {
				t.Fatal("native extension lost")
			}
			Prove(t, "jira worklog add", flag, "request effort, start, authored comment, native extension, notification query, local preview or serial concurrency bound asserted through the binary")
		})
	}
}

func TestWorklogListFlagEffects(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"start", "limit", "all", "parallelism"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			var calls, active, peak atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				start, err := strconv.Atoi(r.URL.Query().Get("startAt"))
				size, sizeErr := strconv.Atoi(r.URL.Query().Get("maxResults"))
				if err != nil || sizeErr != nil || r.Method != "GET" {
					t.Error("bad worklog page request")
					w.WriteHeader(400)
					return
				}
				wantSize := 50
				if flag == "all" {
					wantSize = 100
				}
				if flag == "limit" {
					wantSize = 1
				}
				if size != wantSize || (flag == "start" && start != 1) {
					t.Error("paging flag ignored")
					w.WriteHeader(400)
					return
				}
				n := active.Add(1)
				for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
				}
				if flag == "parallelism" {
					time.Sleep(100 * time.Millisecond)
				}
				active.Add(-1)
				_, _ = fmt.Fprintf(w, `{"startAt":%d,"maxResults":%d,"total":2,"worklogs":[{"id":"%d"}]}`, start, size, start+1)
			}))
			defer server.Close()
			args := []string{"worklog", "list", "KEY-1"}
			switch flag {
			case "start":
				args = append(args, "--start=1")
			case "limit":
				args = append(args, "--limit=1")
			case "all":
				args = append(args, "--all")
			case "parallelism":
				args = append(args, "KEY-2", "--parallelism=1")
			}
			res := fwExec(t, server.URL, nil, "", args...)
			out := fwParse(t, res)
			wantCalls := int32(1)
			if flag == "all" || flag == "parallelism" {
				wantCalls = 2
			}
			if res.Exit != 0 || calls.Load() != wantCalls || (flag == "parallelism" && peak.Load() != 1) {
				t.Fatalf("worklog list flag ignored: %s exit=%d calls=%d peak=%d %s", flag, res.Exit, calls.Load(), peak.Load(), res.Stdout)
			}
			if flag == "all" && (fwAt(t, out.Data, "results", 0, "data", "pagination", "returned") != float64(2) || fwAt(t, out.Data, "results", 0, "data", "complete") != true) {
				t.Fatal("all failed to drain sparse pages")
			}
			if flag == "start" && (fwAt(t, out.Data, "results", 0, "data", "pagination", "start_at") != float64(1) || fwAt(t, out.Data, "results", 0, "data", "worklogs", 0, "id") != "2") {
				t.Fatal("offset ignored in output")
			}
			if flag == "limit" && fwAt(t, out.Data, "results", 0, "data", "pagination", "max_results") != float64(1) {
				t.Fatal("page size ignored in output")
			}
			Prove(t, "jira worklog list", flag, "request offsets and sizes, drained sparse records or serial request bound asserted through the binary")
		})
	}
}

func TestWorklogMarkdownStdin(t *testing.T) {
	t.Parallel()
	res := fwExec(t, "", nil, "piped worklog comment", "worklog", "add", "KEY-1", "--time-spent=1m", "--markdown-file=-", "--dry-run")
	out := fwParse(t, res)
	if res.Exit != 0 || !strings.Contains(res.Stdout, "piped worklog comment") || fwAt(t, out.Data, "body_route") != "markdown" {
		t.Fatalf("piped comment failed: %s", res.Stdout)
	}
}
