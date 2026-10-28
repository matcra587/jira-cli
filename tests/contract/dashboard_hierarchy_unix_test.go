//go:build linux || darwin

package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
)

func TestDashboardNativeHierarchyAndRootTypeSettings(t *testing.T) {
	beans := map[string]map[string]any{}
	for _, item := range []struct{ id, key, kind, parent string }{
		{id: "1", key: "PLAN-1", kind: "Initiative"},
		{id: "2", key: "EPIC-2", kind: "Epic", parent: "1"},
		{id: "3", key: "WORK-3", kind: "Story", parent: "2"},
		{id: "4", key: "SUB-4", kind: "Sub-task", parent: "3"},
	} {
		fields := map[string]any{
			"summary": "Hierarchy " + item.key, "issuetype": map[string]string{"name": item.kind}, "parent": nil,
			"description": map[string]any{"type": "doc", "version": 1, "content": []any{}},
		}
		if item.parent != "" {
			fields["parent"] = map[string]string{"id": item.parent}
		}
		bean := map[string]any{"id": item.id, "key": item.key, "fields": fields}
		beans[item.id], beans[item.key] = bean, bean
	}
	var mu sync.Mutex
	queries := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issuetype":
			_ = json.NewEncoder(w).Encode([]map[string]string{{"id": "100", "name": "Initiative"}, {"id": "200", "name": "Epic"}})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/"):
			bean, ok := beans[strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/")]
			if !ok {
				t.Errorf("unknown hierarchy selector %s", r.URL.Path)
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(bean)
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search/jql":
			var input struct {
				JQL    string   `json:"jql"`
				Fields []string `json:"fields"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
				http.Error(w, "bad fixture body", http.StatusBadRequest)
				return
			}
			mu.Lock()
			queries = append(queries, input.JQL)
			mu.Unlock()
			id := "3"
			switch input.JQL {
			case config.DefaultTUIHierarchyQuery:
				id = "2"
			case `issuetype = "100" ORDER BY updated DESC`:
				id = "1"
			case `parent = "1" ORDER BY updated DESC`:
				id = "2"
			case `parent = "2" ORDER BY updated DESC`:
				id = "3"
			case `parent = "3" ORDER BY updated DESC`:
				id = "4"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"isLast": true, "issues": []any{beans[id]}})
		default:
			t.Errorf("unexpected hierarchy request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "writes and other requests disabled", http.StatusForbidden)
		}
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.toml")
	if err := os.WriteFile(path, fmt.Appendf(nil, "base_url=%q\nemail='person@example.invalid'\nsecret_backend='env'\nread_only=true\n", server.URL), 0o600); err != nil {
		t.Fatal(err)
	}
	terminal := startSettingsAuditTerminal(t, dir, path, "")
	terminal.wait("WORK-3")
	terminal.sendWait("H", "EPIC-2")
	terminal.sendWait("\x1b[C", "Epic EPIC-2")
	terminal.wait("WORK-3")
	terminal.sendWait("\x1b[C", "Story WORK-3")
	terminal.wait("SUB-4")
	terminal.sendWait("\x7f", "Epic EPIC-2")
	terminal.sendWait("\x7f", "Configured roots")
	terminal.sendWait("\x7f", "My work")
	terminal.sendWait("\x07", "Epic EPIC-2")
	terminal.sendWait("\x1b[D", "Initiative PLAN-1")
	terminal.wait("EPIC-2")
	terminal.sendWait(",", "Show issue preview")
	terminal.selectSetting("tui_hierarchy_query")
	terminal.sendWait("\r", "f2 issue type")
	terminal.sendWait("\x1bOQ", "Hierarchy root type")
	terminal.sendWait("\x1b[200~Initiative\x1b[201~\r", "unsaved")
	terminal.sendWait("\x13", "Saved and active")
	resolved, err := config.Load(config.Options{Path: path, Environ: func() []string { return nil }})
	if err != nil || string(resolved.Config.TUIHierarchyQuery) != `issuetype = "100" ORDER BY updated DESC` {
		t.Fatal("native root type did not persist", err)
	}
	terminal.sendWait("\x1b", "JQL")
	terminal.sendWait("H", "Configured roots")
	terminal.wait("PLAN-1")
	terminal.stop()
	reopened := startSettingsAuditTerminal(t, dir, path, "")
	reopened.wait("WORK-3")
	reopened.sendWait("H", "PLAN-1")
	reopened.stop()
	mu.Lock()
	defer mu.Unlock()
	for _, expected := range []string{config.DefaultTUIHierarchyQuery, `parent = "1" ORDER BY updated DESC`, `parent = "2" ORDER BY updated DESC`, `parent = "3" ORDER BY updated DESC`, `issuetype = "100" ORDER BY updated DESC`} {
		found := false
		for _, q := range queries {
			found = found || q == expected
		}
		if !found {
			t.Errorf("native hierarchy missed query %q", expected)
		}
	}
}
