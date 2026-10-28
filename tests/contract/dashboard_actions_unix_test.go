//go:build linux || darwin

package contract

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/tests/internal/nativepty"
)

func TestDashboardNativeViewsSwitcherDetailsAndReviewedActions(t *testing.T) {
	const initialQuery = "project = DEMO ORDER BY updated DESC"
	var mu sync.Mutex
	writes := make(map[string]int)
	writeTargets := make(map[string]map[string]int)
	record := func(kind, target string) {
		mu.Lock()
		defer mu.Unlock()
		writes[kind]++
		if writeTargets[kind] == nil {
			writeTargets[kind] = make(map[string]int)
		}
		writeTargets[kind][target]++
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/project/search":
			_, _ = fmt.Fprint(w, `{"startAt":0,"maxResults":100,"total":1,"isLast":true,"values":[{"id":"10402","key":"DEMO","name":"Fixture project"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/project/10402":
			_, _ = fmt.Fprint(w, `{"id":"10402","key":"DEMO","name":"Fixture project"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/createmeta/10402/issuetypes":
			_, _ = fmt.Fprint(w, `{"startAt":0,"maxResults":50,"total":1,"issueTypes":[{"id":"10002","name":"Task"}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/createmeta/10402/issuetypes/10002":
			fields := []string{}
			for _, name := range []string{"summary", "description", "labels", "assignee"} {
				fields = append(fields, fmt.Sprintf(`{"fieldId":%q,"key":%q,"name":%q,"required":false,"operations":["set"],"schema":{"type":"string"}}`, name, name, name))
			}
			_, _ = fmt.Fprintf(w, `{"startAt":0,"maxResults":50,"total":4,"fields":[%s]}`, strings.Join(fields, ","))
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/issue":
			var body struct {
				Fields map[string]json.RawMessage `json:"fields"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				http.Error(w, "bad creation", 400)
				return
			}
			if len(body.Fields) != 6 || string(body.Fields["project"]) != `{"id":"10402"}` || string(body.Fields["issuetype"]) != `{"id":"10002"}` || string(body.Fields["summary"]) != `"Created native fixture"` || string(body.Fields["labels"]) != `["native,label"]` || string(body.Fields["assignee"]) != `{"accountId":"account-7"}` || !bytes.Contains(body.Fields["description"], []byte(`"type":"doc"`)) || !bytes.Contains(body.Fields["description"], []byte("native")) {
				t.Errorf("creation lost native reviewed fields: %v", body.Fields)
			}
			record("create", "DEMO-4")
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprint(w, `{"id":"404","key":"DEMO-4"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/102/editmeta":
			_, _ = fmt.Fprint(w, `{"fields":{"summary":{"key":"summary","name":"Summary","required":true,"schema":{"type":"string"},"operations":["set"]},"labels":{"key":"labels","name":"Labels","required":false,"schema":{"type":"array","items":"string"},"operations":["set"]}}}`)
		case r.Method == http.MethodPut && r.URL.Path == "/rest/api/3/issue/102":
			var body struct {
				Fields map[string]json.RawMessage `json:"fields"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				http.Error(w, "bad body", 400)
				return
			}
			if len(body.Fields) != 1 {
				t.Error("field editor changed unrelated native fields")
			}
			if summary, exists := body.Fields["summary"]; exists {
				if string(summary) != `"Edited native summary"` {
					t.Error("summary changed before native submission")
				}
				record("summary", "DEMO-2")
			} else {
				if string(body.Fields["labels"]) != `["base,label","new,\"label"]` {
					t.Errorf("label edit lost literal values: %s", body.Fields["labels"])
				}
				record("labels", "DEMO-2")
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search/jql":
			var body struct {
				JQL string `json:"jql"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.JQL != initialQuery {
				t.Errorf("saved view changed authored JQL: %+v %v", body, err)
			}
			_, _ = fmt.Fprint(w, `{"isLast":true,"issues":[{"key":"DEMO-1","fields":{"summary":"Fixture issue"}},{"key":"DEMO-2","fields":{"summary":"Second fixture issue"}}]}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/transitions"):
			id := "31"
			if strings.Contains(r.URL.Path, "DEMO-2/") {
				id = "41"
			}
			_, _ = fmt.Fprintf(w, `{"transitions":[{"id":%q,"name":"Finish","hasScreen":false,"to":{"name":"Done"}}]}`, id)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/transitions"):
			var body struct {
				Transition struct {
					ID string `json:"id"`
				} `json:"transition"`
			}
			target := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/"), "/transitions")
			expected := "31"
			if target == "DEMO-2" {
				expected = "41"
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Transition.ID != expected {
				t.Errorf("wrong transition payload: %+v %v", body, err)
			}
			record("transition", target)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/user":
			if r.URL.Query().Get("accountId") != "account-7" {
				t.Error("assignment looked up a different account")
			}
			_, _ = fmt.Fprint(w, `{"accountId":"account-7","displayName":"Fixture Person","active":true}`)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/DEMO-"):
			var body struct {
				Fields struct {
					Assignee struct {
						ID string `json:"accountId"`
					} `json:"assignee"`
				} `json:"fields"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Fields.Assignee.ID != "account-7" {
				t.Errorf("wrong assignment payload: %+v %v", body, err)
			}
			target := strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/")
			record("assign", target)
			if target == "DEMO-2" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprint(w, `{"errorMessages":["Fixture refuses assignment"]}`)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comment"):
			var body struct {
				Body json.RawMessage `json:"body"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !bytes.Contains(body.Body, []byte("Native fixture comment")) && !bytes.Contains(body.Body, []byte("Shared bulk fixture")) {
				t.Errorf("new comment did not reach native ADF: %s %v", body.Body, err)
			}
			record("comment", strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/"), "/comment"))
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprint(w, `{"id":"comment-7"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/DEMO-1/comment":
			_, _ = fmt.Fprint(w, `{"startAt":0,"maxResults":20,"total":1,"comments":[{"id":"comment-1","author":{"displayName":"Fixture Person"},"body":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Existing fixture comment"}]}]}}]}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/DEMO-"):
			key := strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/")
			summary := "Detail " + key
			mu.Lock()
			switch {
			case writes["labels"] > 0:
				summary = "Labels fixture refreshed"
			case writes["summary"] > 0:
				summary = "Edited native summary"
			case writes["comment"] > 0:
				summary = "Comment fixture refreshed"
			case writes["assign"] > 0:
				summary = "Assignment fixture refreshed"
			case writes["transition"] > 0:
				summary = "Transition fixture refreshed"
			}
			mu.Unlock()
			id := "101"
			if key == "DEMO-2" {
				id = "102"
			}
			if key == "DEMO-3" {
				id = "103"
			}
			_, _ = fmt.Fprintf(w, `{"id":%q,"key":%q,"fields":{"summary":%q,"labels":["base,label"],"description":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":%q}]}]},"issuelinks":[{"type":{"name":"Blocks","outward":"blocks"},"outwardIssue":{"key":"DEMO-2"}}]},"changelog":{"total":1,"histories":[{"created":"2026-10-01T00:00:00Z","author":{"displayName":"Fixture Person"},"items":[{"field":"status","fromString":"To Do","toString":"In Progress"}]}]}}`, id, key, summary, "Description for "+key)
		default:
			t.Errorf("unexpected dashboard request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.toml")
	if err := os.WriteFile(settings, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	queries := filepath.Join(dir, "queries")
	master, slave, err := nativepty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = master.Close(); _ = slave.Close() }()
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 35, Col: 140}); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, jiraBinary(t), "tui", "--config="+settings, "--jql="+initialQuery, "--output=human", "--color=never")
	process.Dir = dir
	process.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "XDG_CONFIG_HOME=" + filepath.Join(dir, "config"), "XDG_CACHE_HOME=" + filepath.Join(dir, "cache"), "TERM=xterm-256color", "JIRA_KEYRING=false", "JIRA_BASE_URL=" + server.URL, "JIRA_EMAIL=person@example.com", "JIRA_API_TOKEN=synthetic-dashboard-fixture", "JIRA_QUERIES_PATH=" + queries}
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
			if err := <-finished; err != nil {
				t.Logf("interrupted native invocation joined: %v", err)
			}
		}
	}()
	steps := []struct{ wait, input string }{
		{"Description for DEMO-1", "\x13"},
		{"Choose a new saved-search name", "Fixture view\r"},
		{"Saved view Fixture view", "v"},
		{"Saved views and lenses", "Fixture view\r"},
		{"Description for DEMO-1", "\x0f"},
		{"Switch issue", "DEMO-3\r"},
		{"Description for DEMO-3", "\x1b"},
		{"Description for DEMO-1", "]"},
		{"Existing fixture comment", "]"},
		{"returned history page", "]"},
		{"blocks · DEMO-2", "]"},
		{"Description for DEMO-1", "t"},
		{"Choose transition", "\r"},
		{"Move DEMO-1 using transition 31?", "\r"},
		{"Transition fixture refreshed", "a"},
		{"Assign · DEMO-1", "account ID\r"},
		{"Enter an exact Jira account ID", "account-7\r"},
		{"Assign DEMO-1 to account account-7?", "\r"},
		{"Assignment fixture refreshed", "c"},
		{"Write a new Markdown comment", "\x1b[200~Native fixture comment\x1b[201~\x13"},
		{"Post this new comment to DEMO-1?", "\r"},
		{"Comment fixture refreshed", "\x06\x1b[200~DEMO-2\x1b[201~\r "},
		{"1 / 2 loaded", "\x06"},
		{"Filter >", "\x1b"},
		{"JQL", "\x01B"},
		{"Bulk actions", "Comment\r"},
		{"Bulk comment", "\x1b[200~Shared bulk fixture\x1b[201~\x13"},
		{"Apply comment to these 2 issues?", "y"},
		{"comment: 2 acknowledged", "\x01t"},
		{"Choose action", "\r"},
		{"Apply transition to these 2 issues?", "y"},
		{"transition: 2 acknowledged", "\x01a"},
		{"Assign · 2 issues", "account ID\r"},
		{"Enter an exact Jira account ID for all marked issues.", "account-7\r"},
		{"Apply assign to these 2 issues?", "y"},
		{"assign: 1 acknowledged", "L"},
		{"Operation history", "b"},
		{"DEMO-2 · unconfirmed", "\x1b"},
		{"q quit", "e"},
		{"Edit · DEMO-2", "\r"},
		{"Edit this issue's summary", "\x15Edited native summary\r"},
		{"Replace summary on DEMO-2?", "y"},
		{"Edited native summary", "l"},
		{"Labels · DEMO-2", "\x1b[200~new,\"label\x1b[201~\r\r"},
		{"Confirm labels · DEMO-2", "\t\r"},
		{"abels fixture refreshed", "Y"},
		{"\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(server.URL+"/browse/DEMO-2")), "n"},
		{"DEMO · 10402", "DEMO\r"},
		{"Task · 10002", "\r"},
		{"Title", "Created native fixture\t\x1b[200~Created **native** body\x1b[201~\x1bOQ"},
		{"Labels · New issue", "\x1b[200~native,label\x1b[201~\r\r\x1bOR"},
		{"New issue · assignee", "account ID\r"},
		{"Use an exact account ID. Jira decides assignability.", "account-7\r\x13"},
		{"Review new issue", "y"},
		{"Description for DEMO-4", ","},
		{"Show issue preview", "/default_project\r\r"},
		{"keep in draft", "FIXTURE\r\x13"},
		{"Runtime reloaded.", "\x1b"},
		{"q quit", ","},
		{"Show issue preview", "/theme\r\r"},
		{"ctrl+n new", "\x0e"},
		{"New theme name", "Native palette\r"},
		{"Edit theme · Native palette", "\x13"},
		{"Native palette", "\r\x13"},
		{"Runtime reloaded.", "\x1b"},
		{"q quit", "q"},
	}
	var transcript bytes.Buffer
	stage := 0
	for {
		buffer := make([]byte, 8192)
		n, readErr := unix.Read(int(master.Fd()), buffer)
		if n > 0 {
			transcript.Write(buffer[:n])
		}
		if transcript.Len() > 1<<20 {
			t.Fatal("dashboard transcript exceeded bound")
		}
		if readErr != nil && !errors.Is(readErr, unix.EAGAIN) && !errors.Is(readErr, unix.EIO) {
			t.Fatal(readErr)
		}
		if stage < len(steps) && bytes.Contains(transcript.Bytes(), []byte(steps[stage].wait)) {
			input := steps[stage].input
			stage++
			transcript.Reset()
			if _, err := unix.Write(int(master.Fd()), []byte(input)); err != nil {
				t.Fatal(err)
			}
		}
		select {
		case err := <-finished:
			joined = true
			if err != nil || stage != len(steps) {
				t.Fatalf("dashboard ended at stage %d: %v\n%s", stage, err, &transcript)
			}
			after, err := term.GetState(int(slave.Fd()))
			if err != nil || *before != *after {
				t.Fatal("dashboard did not restore the terminal")
			}
			saved, err := os.ReadFile(filepath.Join(queries, "Fixture view.jql"))
			if err != nil || strings.TrimSpace(string(saved)) != initialQuery {
				t.Fatal("saved view did not retain authored JQL")
			}
			configured, err := config.Load(config.Options{Path: settings, Environ: func() []string { return nil }})
			if err != nil || configured.Config.DefaultProject != "FIXTURE" || configured.Source("default_project") != config.LayerFile {
				t.Fatalf("settings menu did not save the authored project: %v", err)
			}
			themePath := filepath.Join(dir, "themes", "Native palette.toml")
			if configured.Config.ThemePath != themePath {
				t.Fatalf("named theme selection was not saved: %q", configured.Config.ThemePath)
			}
			definition, err := config.LoadThemeFile(t.Context(), themePath)
			if err != nil || definition.Name != "Native palette" {
				t.Fatalf("native theme creation missing: %+v %v", definition, err)
			}
			if configured.Config.BaseURL != "" || configured.Config.Email != "" || !configured.Config.Keyring {
				t.Fatal("settings save copied effective environment values into the file")
			}
			mu.Lock()
			defer mu.Unlock()
			if writes["create"] != 1 || writeTargets["create"]["DEMO-4"] != 1 {
				t.Fatal("native creation did not submit exactly once")
			}
			for _, field := range []string{"summary", "labels"} {
				if writes[field] != 1 || writeTargets[field]["DEMO-2"] != 1 {
					t.Fatalf("field %s was not submitted exactly once to the reviewed immutable target", field)
				}
			}
			for kind, expected := range map[string]int{"transition": 3, "assign": 3, "comment": 3} {
				if writes[kind] != expected {
					t.Fatalf("%s wrote %d times, want %d reviewed attempts", kind, writes[kind], expected)
				}
				wantFirst := 2
				if writeTargets[kind]["DEMO-1"] != wantFirst || writeTargets[kind]["DEMO-2"] != 1 {
					t.Fatalf("batch target binding changed: %s %v", kind, writeTargets[kind])
				}
			}
			return
		case <-ctx.Done():
			t.Fatalf("dashboard timed out at stage %d waiting for %q\n%s", stage, steps[min(stage, len(steps)-1)].wait, &transcript)
		case <-time.After(5 * time.Millisecond):
		}
	}
}
