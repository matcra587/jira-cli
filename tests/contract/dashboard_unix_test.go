//go:build linux || darwin

package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"github.com/matcra587/jira-cli/tests/internal/nativepty"
)

func TestDashboardNativeMainPageSearchSelectionAndSettings(t *testing.T) {
	const initialQuery = "project = DEMO ORDER BY updated DESC"
	const editedQuery = "project = DEMO AND summary ~ \"changed\""
	var mu sync.Mutex
	var queries []string
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search/jql":
			var body struct {
				JQL        string   `json:"jql"`
				MaxResults int      `json:"maxResults"`
				Fields     []string `json:"fields"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				http.Error(w, "bad body", 400)
				return
			}
			if body.MaxResults != 50 || !reflect.DeepEqual(body.Fields, []string{"summary", "status", "issuetype", "priority", "assignee", "updated", "labels"}) {
				t.Errorf("unexpected search shape: %+v", body)
			}
			queries = append(queries, body.JQL)
			switch body.JQL {
			case initialQuery:
				_, _ = fmt.Fprint(w, `{"isLast":true,"issues":[{"key":"DEMO-1","fields":{"summary":"First issue"}},{"key":"DEMO-2","fields":{"summary":"Second issue"}}]}`)
			case editedQuery:
				_, _ = fmt.Fprint(w, `{"isLast":true,"issues":[{"key":"DEMO-3","fields":{"summary":"Changed query result"}}]}`)
			default:
				t.Errorf("unexpected JQL: %q", body.JQL)
				http.Error(w, "wrong query", 400)
			}
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/DEMO-"):
			key := strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/")
			_, _ = fmt.Fprintf(w, `{"key":%q,"fields":{"summary":%q,"description":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":%q}]}]}}}`, key, "Detail "+key, "Description for "+key)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer server.Close()
	master, slave, err := nativepty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = master.Close(); _ = slave.Close() }()
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 30, Col: 120}); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.toml")
	if err := os.WriteFile(settingsPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, jiraBinary(t), "tui", "--config="+settingsPath, "--jql="+initialQuery, "--output=human", "--color=never")
	process.Dir = dir
	process.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "XDG_CONFIG_HOME=" + filepath.Join(dir, "config"), "XDG_CACHE_HOME=" + filepath.Join(dir, "cache"), "TERM=xterm-256color", "JIRA_KEYRING=false", "JIRA_BASE_URL=" + server.URL, "JIRA_EMAIL=person@example.com", "JIRA_API_TOKEN=synthetic-dashboard-fixture"}
	process.Stdin, process.Stdout, process.Stderr = slave, slave, slave
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- process.Wait() }()
	joined := false
	defer func() {
		if !joined {
			cancel()
			if waitErr := <-finished; waitErr != nil {
				t.Logf("failed invocation joined during cleanup: %v", waitErr)
			}
		}
	}()
	var transcript bytes.Buffer
	stage := 0
	for {
		buffer := make([]byte, 8192)
		n, readErr := unix.Read(int(master.Fd()), buffer)
		if n > 0 {
			transcript.Write(buffer[:n])
		}
		if transcript.Len() > 1<<20 {
			t.Fatal("dashboard transcript exceeded its byte bound")
		}
		if readErr != nil && !errors.Is(readErr, unix.EAGAIN) && !errors.Is(readErr, unix.EIO) {
			t.Fatal(readErr)
		}
		var input string
		switch stage {
		case 0:
			if bytes.Contains(transcript.Bytes(), []byte("Description for DEMO-1")) {
				input = "j"
				stage++
			}
		case 1:
			if bytes.Contains(transcript.Bytes(), []byte("Description for DEMO-2")) {
				input = "/"
				stage++
			}
		case 2:
			if bytes.Contains(transcript.Bytes(), []byte("JQL >")) {
				input = "\x15" + editedQuery + "\r"
				stage++
			}
		case 3:
			if bytes.Contains(transcript.Bytes(), []byte("Description for DEMO-3")) {
				input = ","
				stage++
			}
		case 4:
			if bytes.Contains(transcript.Bytes(), []byte("Show issue preview")) {
				input = "\r"
				stage++
			}
		case 5:
			if bytes.Contains(transcript.Bytes(), []byte("unsaved")) {
				input = "\x13"
				stage++
			}
		case 6:
			if bytes.Contains(transcript.Bytes(), []byte("Runtime reloaded.")) {
				input = "\x1b"
				stage++
				transcript.Reset()
			}
		case 7:
			if bytes.Contains(transcript.Bytes(), []byte("JQL")) {
				input = "q"
				stage++
			}
		}
		if input != "" {
			if _, err := unix.Write(int(master.Fd()), []byte(input)); err != nil {
				t.Fatal(err)
			}
		}
		select {
		case err := <-finished:
			joined = true
			if err != nil || stage != 8 {
				t.Fatalf("dashboard ended at stage %d: %v\n%s", stage, err, &transcript)
			}
			after, err := term.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			if *before != *after {
				t.Fatal("dashboard left the borrowed terminal in a changed mode")
			}
			settings, err := os.ReadFile(settingsPath)
			if err != nil || !strings.Contains(string(settings), "tui_preview = false") || strings.Contains(string(settings), "api_token") {
				t.Fatalf("dashboard failed to publish only its explicit display setting: %q %v", settings, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(queries, []string{initialQuery, editedQuery, editedQuery}) {
				t.Fatalf("queries=%q", queries)
			}
			if len(requests) != 6 {
				t.Fatalf("unexpected dashboard request count: %v", requests)
			}
			return
		case <-ctx.Done():
			t.Fatalf("dashboard timed out at stage %d\n%s", stage, &transcript)
		case <-time.After(5 * time.Millisecond):
		}
	}
}
