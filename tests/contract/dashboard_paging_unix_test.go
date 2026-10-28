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
	"sync/atomic"
	"testing"
)

func TestDashboardNativeAutomaticAndManualPageLoading(t *testing.T) {
	for _, automatic := range []bool{true, false} {
		t.Run(fmt.Sprint(automatic), func(t *testing.T) {
			var searches atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search/jql" {
					searches.Add(1)
					var body struct {
						Cursor string `json:"nextPageToken"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					start, count := 1, 50
					if body.Cursor == "next" {
						start, count = 51, 2
					} else if body.Cursor != "" {
						t.Error("unexpected page token")
					}
					rows := make([]string, count)
					for i := range rows {
						rows[i] = fmt.Sprintf(`{"key":"DEMO-%d","fields":{"summary":"Paged issue"}}`, start+i)
					}
					if body.Cursor == "" {
						_, _ = fmt.Fprintf(w, `{"issues":[%s],"isLast":false,"nextPageToken":"next"}`, strings.Join(rows, ","))
					} else {
						_, _ = fmt.Fprintf(w, `{"issues":[%s],"isLast":true}`, strings.Join(rows, ","))
					}
					return
				}
				if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/DEMO-") {
					key := strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/")
					summary := "Initial issue detail"
					if key == "DEMO-50" {
						summary = "Selected boundary issue"
					}
					_, _ = fmt.Fprintf(
						w,
						`{"key":%q,"fields":{"summary":%q,"description":null}}`,
						key,
						summary,
					)
					return
				}
				t.Errorf("unexpected paging request: %s %s", r.Method, r.URL.Path)
				http.Error(w, "unexpected request", http.StatusBadRequest)
			}))
			t.Cleanup(server.Close)
			dir := t.TempDir()
			path := filepath.Join(dir, "settings.toml")
			contents := fmt.Sprintf(
				"base_url=%q\nemail='person@example.invalid'\nsecret_backend='env'\ntui_auto_page=%t\n",
				server.URL,
				automatic,
			)
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			terminal := startSettingsAuditTerminal(t, dir, path, "")
			terminal.wait("50 loaded")
			if searches.Load() != 1 {
				t.Fatal("initial view drained additional pages")
			}
			if automatic {
				terminal.sendWait("G", "Loaded 2 more issues.")
			} else {
				terminal.sendWait("G", "Selected boundary issue")
				if searches.Load() != 1 {
					t.Fatal("disabled automatic loading fetched another page")
				}
				terminal.sendWait("m", "Loaded 2 more issues.")
			}
			if searches.Load() != 2 {
				t.Fatalf("page request was missing or repeated: %d", searches.Load())
			}
			terminal.stop()
		})
	}
}
