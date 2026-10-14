package contract

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestCurrentUserAmbiguousIdentityRefusesAccountWritesAndSaving(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"accountId":null,"accountId":"fixture"}`,
		`{"accountId":"other","account\u0049d":"fixture"}`,
		`{"accountId":"fixture","ACCOUNTID":"other"}`,
	} {
		for _, operation := range []struct {
			name string
			args []string
		}{
			{name: "create", args: []string{"issue", "create", "--project=KEY", "--type=Task", "--summary=fixture", "--assignee=@me", "--reporter=@me"}},
			{name: "edit", args: []string{"issue", "edit", "KEY-1", "--assignee=@me", "--reporter=@me"}},
			{name: "clone", args: []string{"issue", "clone", "KEY-1", "--assignee=@me", "--reporter=@me"}},
			{name: "watcher add", args: []string{"issue", "watchers", "add", "KEY-1", "--user=@me"}},
			{name: "watcher remove", args: []string{"issue", "watchers", "remove", "KEY-1", "--user=@me"}},
			{name: "remote preview", args: []string{"issue", "edit", "KEY-1", "--assignee=@me", "--dry-run", "--validate-remote"}},
			{name: "save", args: []string{"auth", "whoami", "--save"}},
			{name: "save preview", args: []string{"auth", "whoami", "--save", "--dry-run"}},
		} {
			t.Run(operation.name+"/"+body, func(t *testing.T) {
				t.Parallel()
				var identities, other atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/myself" {
						identities.Add(1)
						_, _ = io.WriteString(w, body)
						return
					}
					other.Add(1)
					w.WriteHeader(http.StatusNoContent)
				}))
				t.Cleanup(server.Close)
				args := append([]string(nil), operation.args...)
				path := filepath.Join(t.TempDir(), "absent", "settings.toml")
				if strings.HasPrefix(operation.name, "save") {
					args = append(args, "--config="+path)
				}
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), append(args, "--output=json", "--no-input")...)
				var frame struct {
					OK     bool             `json:"ok"`
					Errors []envelope.Error `json:"errors"`
				}
				if exit != cli.ExitServer || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "current_user_response_invalid" || identities.Load() != 1 || other.Load() != 0 || strings.Contains(stdout+stderr, "example-fixture-token") {
					t.Fatalf("ambiguous identity authorized work: exit=%d identities=%d other=%d stdout=%q stderr=%q", exit, identities.Load(), other.Load(), stdout, stderr)
				}
				if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("ambiguous identity published settings or directories: %v", err)
				}
			})
		}
	}
}
