//go:build linux || darwin

package contract

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDashboardNativeBindingCaptureSaveAndReopen(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search/jql" {
			_, _ = fmt.Fprint(w, `{"isLast":true,"issues":[{"key":"DEMO-1","fields":{"summary":"Binding fixture"}}]}`)
			return
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/") {
			_, _ = fmt.Fprint(w, `{"key":"DEMO-1","fields":{"summary":"Binding fixture","description":null}}`)
			return
		}
		t.Errorf("unexpected binding request: %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected request", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	for _, window := range []unix.Winsize{{Col: 40, Row: 12}, {Col: 80, Row: 24}, {Col: 159, Row: 45}} {
		t.Run(fmt.Sprintf("%dx%d", window.Col, window.Row), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "settings.toml")
			initial := fmt.Appendf(nil, "base_url=%q\nemail='person@example.invalid'\nsecret_backend='env'\nread_only=true\ntui_refresh_interval=0\n", server.URL)
			if err := os.WriteFile(path, initial, 0o600); err != nil {
				t.Fatal(err)
			}
			terminal := startSettingsAuditTerminalSize(t, dir, path, "", window)
			terminal.wait("DEMO-1")
			terminal.sendWait(",", "Settings")
			terminal.selectSetting("tui_workspace")
			terminal.sendWait("\r", "Workspace")
			terminal.sendWait("\x1b[200~Keybindings\x1b[201~\r", "Create issue")
			terminal.sendWait("\x1b[200~Next view\x1b[201~\r", "Replace shortcut")
			terminal.sendWait("\r", "press a key")
			// A narrow viewport wraps the conflict sentence between words.
			terminal.sendWait("e", "shortcut \"e\"")
			stored, err := os.ReadFile(path)
			if err != nil || string(stored) != string(initial) {
				t.Fatal("conflicting capture published settings", err)
			}
			terminal.sendWait("\x1b[17~", "f6")
			terminal.sendWait("\x1b", "Keybindings")
			terminal.sendWait("\x1b", "Workspace")
			// The dirty status may already be visible behind a tall overlay.
			// Observe its dismissal repaint before sending the save control.
			terminal.sendWait("\x1b", "\x1b[K")
			terminal.sendWait("\x13", "Runtime reloaded.")
			terminal.sendWait("\x1b", "JQL")
			terminal.sendWait("\t?", "shortcuts")
			terminal.sendWait("\x1b[200~Next view\x1b[201~", "f6")
			terminal.sendWait("\x1b", "q quit")
			terminal.sendWait("\x1b[17~", "/ or Enter to write JQL")
			terminal.stop()
			reopened := startSettingsAuditTerminalSize(t, dir, path, "", window)
			reopened.wait("DEMO-1")
			reopened.sendWait("\x1b[17~", "/ or Enter to write JQL")
			reopened.stop()
		})
	}
}
