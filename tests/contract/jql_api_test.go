package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestJQLParserRawPOSTNeedsNoForceAndHonorsReadOnly(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != `{"queries":["project = KEY","project = KEY"]}` || r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/jql/parse" || r.URL.Query().Get("validation") != "warn" {
					t.Error("raw parser request changed")
				}
				_, _ = io.WriteString(w, `{"queries":[{"query":"project = KEY","warnings":["unknown project"]},{"query":"project = KEY","errors":["invalid"]}]}`)
			}))
			defer server.Close()
			stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true"), "api", "post", "/rest/api/3/jql/parse", "--input", apiInput(t, []byte(`{"queries":["project = KEY","project = KEY"]}`)), "--query=validation=warn", "--no-input", "--output="+mode)
			if exit != 0 || hits.Load() != 1 {
				t.Fatalf("read POST demanded force or bypassed shared transport: exit=%d calls=%d stderr=%q stdout=%s", exit, hits.Load(), stderr, stdout)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "HTTP 200") {
					t.Fatal("human parser receipt lacks status")
				}
				return
			}
			data := apiData(t, stdout, mode, true, "api.post")
			var fields map[string]json.RawMessage
			var out envelope.APIOutput
			if json.Unmarshal(data, &fields) != nil || json.Unmarshal(data, &out) != nil || out.Response == nil || out.Response.StatusCode != 200 || out.Response.Attempts != 1 || len(out.Response.JSON) == 0 {
				t.Fatal("read POST lost response receipt")
			}
			// The generic POST command has one schema for reads and writes;
			// dry_run describes its preview flag, not the endpoint's effects.
			if raw, exists := fields["dry_run"]; !exists || string(raw) != "false" {
				t.Fatal("generic POST receipt lost its declared preview state")
			}
		})
	}
}

func TestJQLParserNearPathsRemainWrites(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/rest/api/3/jql/parse/write", "/rest/api/3/jql/Parse", "/rest/api/3/jql/autocompletedata"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); w.WriteHeader(200) }))
			defer server.Close()
			args := []string{"api", "post", path, "--no-input", "--output=json"}
			for _, forced := range []bool{false, true} {
				invocation := append([]string{}, args...)
				code := cli.CodeFlagRequired
				if forced {
					invocation = append(invocation, "--force")
					code = cli.CodeReadOnlyBlocked
				}
				stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true"), invocation...)
				var out apiFrame
				if exit != cli.ExitValidation || json.Unmarshal([]byte(stdout), &out) != nil || out.OK || len(out.Errors) != 1 || out.Errors[0].Code != code || hits.Load() != 0 {
					t.Fatalf("unlisted POST became a read: force=%t exit=%d calls=%d stdout=%s stderr=%q", forced, exit, hits.Load(), stdout, stderr)
				}
			}
		})
	}
}
