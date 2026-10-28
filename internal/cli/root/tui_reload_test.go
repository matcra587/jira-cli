package root

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestTUIReloadReopensPinnedProfileAndRebuildsTransport(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"101","key":"DEMO-1","fields":{"summary":"Fresh client"}}`)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "settings.toml")
	original := fmt.Sprintf("format_version=1\ndefault_profile='work'\n[profiles.work]\nbase_url=%q\nemail='work@example.invalid'\nsecret_backend='env'\ntimeout='1s'\n[profiles.other]\nbase_url='https://other.invalid'\nemail='other@example.invalid'\nsecret_backend='env'\n", server.URL)
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	captured, err := config.CaptureOptions(t.Context(), config.Options{Path: path, Environ: func() []string { return []string{"JIRA_API_TOKEN=synthetic-reload-token", "JIRA_KEYRING=false"} }})
	if err != nil {
		t.Fatal(err)
	}
	settings := &invocationSettings{capture: func(context.Context) (config.Options, error) { return captured, nil }}
	changed := strings.Replace(original, "default_profile='work'", "default_profile='other'", 1)
	changed = strings.Replace(changed, "timeout='1s'", "timeout='30s'\nread_only=true\ndefault_project='NEW'", 1)
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, client, err := settings.reloadTUI(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Profile != "work" || resolved.Config.DefaultProject != "NEW" || resolved.Config.Timeout != 30*time.Second || !resolved.Config.ReadOnly {
		t.Fatal("reload retained frozen bytes or changed the selected profile")
	}
	if client.(core.JiraScopedClient).JiraScope().Account != "work@example.invalid" {
		t.Fatal("fresh client did not retain captured account selection")
	}
	if _, err := client.EditIssue(t.Context(), "101", jira.IssueWriteRequest{Fields: map[string]json.RawMessage{"summary": json.RawMessage(`"blocked"`)}}, false); !errors.Is(err, adapterjira.ErrReadOnly) || requests.Load() != 0 {
		t.Fatal("saved read-only mode did not reach the actual transport")
	}
	if _, err := client.GetIssue(t.Context(), "DEMO-1", nil); err != nil || requests.Load() != 1 {
		t.Fatalf("fresh read-only transport refused reads: %v", err)
	}
}
