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
	"testing"

	"golang.org/x/sys/unix"
)

func TestDashboardNativeTabsDoNotFocusAnEmptySearch(t *testing.T) {
	for _, window := range []unix.Winsize{{Col: 40, Row: 12}, {Col: 80, Row: 24}, {Col: 159, Row: 45}} {
		t.Run(fmt.Sprintf("%dx%d", window.Col, window.Row), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				key := "DEMO-1"
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search/jql":
					var body struct {
						JQL string `json:"jql"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						http.Error(w, "bad body", http.StatusBadRequest)
						return
					}
					if body.JQL == "" {
						t.Error("idle Search issued empty JQL")
						http.Error(w, "empty JQL", http.StatusBadRequest)
						return
					}
					if strings.HasPrefix(body.JQL, "hierarchyLevel") {
						key = "EPIC-2"
					}
					_, _ = fmt.Fprintf(w, `{"isLast":true,"issues":[{"id":"1","key":%q,"fields":{"summary":"Navigation fixture"}}]}`, key)
				case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/"):
					key = strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/")
					_, _ = fmt.Fprintf(w, `{"id":"1","key":%q,"fields":{"summary":"Navigation fixture","description":null}}`, key)
				default:
					t.Errorf("unexpected tab request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected request", http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			dir := t.TempDir()
			path := filepath.Join(dir, "settings.toml")
			if err := os.WriteFile(path, fmt.Appendf(nil, "base_url=%q\nemail='person@example.invalid'\nsecret_backend='env'\nread_only=true\n", server.URL), 0o600); err != nil {
				t.Fatal(err)
			}
			terminal := startSettingsAuditTerminalSize(t, dir, path, "", window)
			terminal.wait("DEMO-1")
			terminal.sendWait("\t", "/ or Enter to write JQL")
			terminal.sendWait("\t", "EPIC-2")
			terminal.wait("EPIC-2")
			terminal.sendWait("\t", "DEMO-1")
			terminal.sendWait("\x1b[Z", "EPIC-2")
			terminal.sendWait("\x1b[Z", "/ or Enter to write JQL")
			terminal.sendWait("\r", "JQL >")
			terminal.sendWait("\x1b", "/ or Enter to write JQL")
			terminal.sendWait("\x1b[Z", "DEMO-1")
			terminal.stop()
		})
	}
}
