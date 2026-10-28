//go:build linux || darwin

package contract

import (
	"bytes"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/config"
)

func TestDashboardNativePreviewLayoutChoiceSizeAndZoom(t *testing.T) {
	for _, window := range []unix.Winsize{{Col: 40, Row: 12}, {Col: 80, Row: 24}, {Col: 159, Row: 45}} {
		t.Run(fmt.Sprintf("%dx%d", window.Col, window.Row), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search/jql":
					_, _ = fmt.Fprint(w, `{"isLast":true,"issues":[{"key":"DEMO-1","fields":{"summary":"Layout fixture"}}]}`)
				case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/DEMO-1":
					_, _ = fmt.Fprint(w, `{"key":"DEMO-1","fields":{"summary":"Layout fixture","status":{"name":"In progress"},"assignee":{"displayName":"Fixture person"},"priority":{"name":"High"},"issuetype":{"name":"Task"},"labels":["layout-label"],"description":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Dock fixture body"}]}]}}}`)
				default:
					t.Errorf("unexpected layout fixture request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected request", http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			dir := t.TempDir()
			path := filepath.Join(dir, "settings.toml")
			contents := []byte(fmt.Sprintf("base_url=%q\nemail='person@example.invalid'\nsecret_backend='env'\n", server.URL))
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			terminal := startSettingsAuditTerminalSize(t, dir, path, "", window)
			if window.Col == 40 {
				terminal.wait("DEMO-1")
				terminal.sendWait("\r", "esc back")
				terminal.sendWait("\x1b", "/ search")
				terminal.stop()
				return
			}
			terminal.wait("Dock fixture body")
			terminal.sendWait("z", "List zoom")
			if current, err := os.ReadFile(path); err != nil || !bytes.Equal(current, contents) {
				t.Fatal("temporary zoom changed saved settings", err)
			}
			terminal.sendWait("z", "Preview layout restored")
			terminal.sendWait(",", "Show issue preview")
			terminal.selectSetting("tui_preview_dock")
			terminal.sendWait("\r", "keep in draft")
			terminal.sendWait("\x1b[200~bottom\x1b[201~\r", "unsaved")
			terminal.sendWait("/\x15^tui_preview_size \r", "Preferred preview percentage")
			terminal.sendWait("\r", "cancel edit")
			terminal.sendWait("\x15\x1b[200~55\x1b[201~\r", "unsaved")
			terminal.sendWait("\x13", "Saved and active")
			load := func() *config.Resolved {
				t.Helper()
				resolved, err := config.Load(config.Options{Path: path, Environ: func() []string { return nil }})
				if err != nil {
					t.Fatal(err)
				}
				return resolved
			}
			resolved := load()
			if resolved.Config.TUIPreviewDock != config.PreviewDockBottom || resolved.Config.TUIPreviewSize != 55 {
				t.Fatal("saved layout lost its typed values")
			}
			published := maps.Clone(resolved.Config.Values())
			if raw, err := os.ReadFile(path); err != nil || strings.Contains(string(raw), "api_token") {
				t.Fatal("layout save published a credential", err)
			}
			terminal.sendWait("\x1b", "JQL")
			terminal.sendWait("z", "List zoom")
			terminal.sendWait("z", "Preview layout restored")
			if !maps.Equal(published, load().Config.Values()) {
				t.Fatal("zoom changed the saved layout after reload")
			}
			terminal.stop()
			reopened := startSettingsAuditTerminalSize(t, dir, path, "", window)
			reopened.wait("Dock fixture body")
			reopened.sendWait(",", "Show issue preview")
			reopened.selectSetting("tui_preview_dock")
			reopened.sendWait("\r", "Bottom · current")
			reopened.stop()
		})
	}
}

func TestDashboardNativeLeftDockAndMouseTabs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search/jql" {
			_, _ = fmt.Fprint(w, `{"isLast":true,"issues":[{"key":"DEMO-1","fields":{"summary":"Pointer fixture"}}]}`)
			return
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/") {
			_, _ = fmt.Fprint(w, `{"key":"DEMO-1","fields":{"summary":"Pointer fixture","description":null}}`)
			return
		}
		t.Errorf("unexpected pointer request: %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected request", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	for _, window := range []unix.Winsize{{Col: 40, Row: 12}, {Col: 80, Row: 24}, {Col: 159, Row: 45}} {
		t.Run(fmt.Sprintf("%dx%d", window.Col, window.Row), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "settings.toml")
			if err := os.WriteFile(path, fmt.Appendf(nil, "base_url=%q\nemail='person@example.invalid'\nsecret_backend='env'\nread_only=true\ntui_preview_dock='left'\ntui_refresh_interval=0\n", server.URL), 0o600); err != nil {
				t.Fatal(err)
			}
			terminal := startSettingsAuditTerminalSize(t, dir, path, "", window)
			terminal.wait("DEMO-1")
			terminal.sendWait("\x1b[<0;13;2M\x1b[<0;13;2m", "/ or Enter to write JQL")
			terminal.sendWait("\x1b[<0;4;2M\x1b[<0;4;2m", "DEMO-1")
			terminal.sendWait("\r", "esc back")
			if window.Col == 40 {
				terminal.sendWait("\x1b[<0;4;6M\x1b[<0;4;6m", "Issue sections")
				terminal.sendWait("\x1b[200~Activity\x1b[201~\r", "Activity")
			} else {
				terminal.sendWait("\x1b[<0;30;6M\x1b[<0;30;6m", "Activity not returned.")
			}
			terminal.stop()
		})
	}
}
