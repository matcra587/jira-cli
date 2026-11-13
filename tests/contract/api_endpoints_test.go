package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestAPIEndpointsAreOfflineAndKeepProvenance(t *testing.T) {
	t.Parallel()
	cache := filepath.Join(t.TempDir(), "unused-cache")
	env := []string{"XDG_CACHE_HOME=" + cache, "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null"}
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			stdout, stderr, exit := runBinaryEnv(t, env, "api", "endpoints", "--method=get", "--query=getCurrentUser", "--output="+mode)
			if exit != 0 || stderr != "" {
				t.Fatalf("offline discovery exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "GET  /rest/api/3/myself") || !strings.Contains(stdout, "bundled catalog") {
					t.Fatal("human catalog omitted its operation or local source")
				}
				return
			}
			data := apiData(t, stdout, mode, true, "api.endpoints")
			var out envelope.APIEndpointsOutput
			if err := json.Unmarshal(data, &out); err != nil {
				t.Fatal(err)
			}
			if out.CatalogVersion != 1 || len(out.Sources) != 2 || len(out.Operations) != 1 {
				t.Fatal("catalog lost format, source provenance or selected operation", string(data))
			}
			row := out.Operations[0]
			if row.Method != "GET" || row.Path != "/rest/api/3/myself" || row.OperationID != "getCurrentUser" || row.Family != "platform" {
				t.Fatal("catalog changed the documented operation", row)
			}
			for _, source := range out.Sources {
				if len(source.SHA256) != 64 || source.Version == "" || !strings.HasPrefix(source.URL, "https://developer.atlassian.com/") {
					t.Fatal("catalog omitted pinned source identity", source)
				}
			}
		})
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatal("offline catalog created cache storage", err)
	}
}

func TestAPIEndpointsEmptyResultsAndMethodRefusal(t *testing.T) {
	t.Parallel()
	stdout, stderr, exit := runBinary(t, "api", "endpoints", "--method=get", "--query=no-such-catalog-operation", "--output=compact")
	if exit != 0 || stderr != "" {
		t.Fatal("empty catalog selection failed", exit, stderr)
	}
	var out struct {
		Operations []json.RawMessage `json:"operations"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil || out.Operations == nil || len(out.Operations) != 0 {
		t.Fatal("empty selection was not an explicit array", stdout, err)
	}
	for _, method := range []string{"trace", ""} {
		stdout, _, exit := runBinary(t, "api", "endpoints", "--method="+method, "--output=json")
		var frame apiFrame
		if err := json.Unmarshal([]byte(stdout), &frame); err != nil || exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "flag_value_invalid" {
			t.Fatal("invalid method did not receive a typed refusal", stdout, exit, err)
		}
	}
}

func TestAPIEndpointCompletionUsesGeneratedBashOffline(t *testing.T) {
	script, stderr, exit := runBinary(t, "completion", "bash")
	if exit != 0 || stderr != "" {
		t.Fatal("Bash generation failed", exit, stderr)
	}
	env := []string{"JIRA_API_TOKEN=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "XDG_CACHE_HOME=" + filepath.Join(t.TempDir(), "unused-cache")}
	for _, method := range []string{"get", "GET"} {
		got := completionArgumentReplies(t, script, env, []string{"jira", "api", method, "/rest/api/3/my"})
		if !slices.Contains(got, "/rest/api/3/myself") {
			t.Fatal("generated shell did not route to GET endpoint completion", method, got)
		}
	}
	got := completionArgumentReplies(t, script, env, []string{"jira", "api", "post", "/rest/api/3/my"})
	if slices.Contains(got, "/rest/api/3/myself") {
		t.Fatal("POST completion offered a GET-only operation", got)
	}
	got = completionArgumentReplies(t, script, env, []string{"jira", "api", "get", "/rest/api/3/issue/"})
	if !slices.Contains(got, "/rest/api/3/issue/{issueIdOrKey}") {
		t.Fatal("completion lost the literal path template", got)
	}
}
