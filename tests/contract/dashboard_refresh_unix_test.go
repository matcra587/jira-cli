//go:build linux || darwin

package contract

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDashboardNativeRefreshFocusPauseAndSettings(t *testing.T) {
	for _, window := range []unix.Winsize{{Col: 40, Row: 12}, {Col: 80, Row: 24}, {Col: 159, Row: 45}} {
		t.Run(fmt.Sprintf("%dx%d", window.Col, window.Row), func(t *testing.T) {
			var version, searches atomic.Int32
			version.Store(1)
			summary := func() string {
				return []string{"", "Initial snapshot", "Focused snapshot", "Manual snapshot"}[version.Load()]
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search/jql":
					searches.Add(1)
					_, _ = fmt.Fprintf(w, `{"isLast":true,"issues":[{"id":"1","key":"DEMO-1","fields":{"summary":%q}}]}`, summary())
				case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/"):
					_, _ = fmt.Fprintf(w, `{"id":"1","key":"DEMO-1","fields":{"summary":%q,"description":null}}`, summary())
				default:
					t.Errorf("unexpected refresh request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected request", http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			dir := t.TempDir()
			path := filepath.Join(dir, "settings.toml")
			if err := os.WriteFile(path, fmt.Appendf(nil, "base_url=%q\nemail='person@example.invalid'\nsecret_backend='env'\nread_only=true\ntui_refresh_interval=0\n", server.URL), 0o600); err != nil {
				t.Fatal(err)
			}
			terminal := startSettingsAuditTerminalSize(t, dir, path, "", window)
			terminal.wait("Initial")
			version.Store(2)
			terminal.sendWait("\x1b[O\x1b[I", "Focused")
			terminal.sendWait("R", "paused")
			version.Store(3)
			terminal.sendWait("/", "JQL >")
			// Focus alone need not repaint an unchanged editor. A following paste
			// makes the ordered input observable before checking the request count.
			terminal.sendWait("\x1b[O\x1b[I\x15\x1b[200~DRAFT\x1b[201~", "DRAF")
			if searches.Load() != 2 {
				t.Fatal("paused focus issued another search")
			}
			terminal.sendWait("\x1b", "JQL  ")
			terminal.sendWait("r", "Manual")
			terminal.sendWait(",", "Settings")
			terminal.selectSetting("tui_refresh_interval")
			terminal.sendWait("\r", "cancel edit")
			terminal.sendWait("\x15\x1b[200~10\x1b[201~\r", "unsaved")
			terminal.sendWait("\x13", "Runtime reloaded.")
			terminal.stop()
		})
	}
}
