//go:build linux || darwin

package contract

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDashboardNativeAllSettingsCompletesRedraw(t *testing.T) {
	_ = jiraBinary(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"isLast":true,"issues":[]}`)
	}))
	t.Cleanup(server.Close)
	for _, window := range []unix.Winsize{{Col: 80, Row: 24}, {Col: 159, Row: 45}} {
		t.Run(fmt.Sprintf("%dx%d", window.Col, window.Row), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "settings.toml")
			if err := os.WriteFile(path, fmt.Appendf(nil, "base_url=%q\nemail='person@example.invalid'\nsecret_backend='env'\nread_only=true\n", server.URL), 0o600); err != nil {
				t.Fatal(err)
			}
			terminal := startSettingsAuditTerminalSize(t, dir, path, "", window)
			terminal.wait("Ready")
			terminal.sendWait(",", "ctrl+s")
			terminal.sendWait("\t", "Glyph set")
			terminal.sendWait("\x1b[Z", "Show status")
			terminal.sendWait("j", "Automatic places")
			terminal.stop()
		})
	}
}
