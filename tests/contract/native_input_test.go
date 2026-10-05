package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestNativeInputEmptySpellingsRefuseBeforeRequests(t *testing.T) {
	t.Parallel()
	routes := []struct {
		name string
		args []string
	}{
		{name: "create", args: []string{"issue", "create", "--project=FIX", "--type=Task", "--summary=fixture"}},
		{name: "edit", args: []string{"issue", "edit", "KEY-1", "--summary=fixture"}},
		{name: "clone", args: []string{"issue", "clone", "KEY-1"}},
		{name: "link", args: []string{"issue", "link", "create", "KEY-1", "--to=KEY-2", "--type=Blocks"}},
		{name: "comment_add", args: []string{"issue", "comment", "add", "KEY-1", "--markdown=fixture"}},
		{name: "comment_edit", args: []string{"issue", "comment", "edit", "KEY-1", "10001", "--markdown=fixture"}},
		{name: "transition", args: []string{"issue", "transition", "KEY-1", "Done", "--comment=fixture"}},
		{name: "worklog", args: []string{"worklog", "add", "KEY-1", "--time-spent=1m", "--started=2026-01-02"}},
		{name: "move", args: []string{"issue", "move", "KEY-1", "--force"}},
		{name: "move_fields", args: []string{"issue", "move", "fields", "KEY-1", "--force"}},
		{name: "weblink", args: []string{"issue", "weblink", "add", "KEY-1", "--url=https://example.com"}},
	}
	for _, route := range routes {
		for _, flag := range []string{"json-input", "from-json", "body-adf"} {
			for _, preview := range []bool{false, true} {
				name := route.name + "/" + flag + "/write"
				if preview {
					name = route.name + "/" + flag + "/preview"
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						calls.Add(1)
						w.WriteHeader(http.StatusBadRequest)
					}))
					t.Cleanup(server.Close)
					args := append([]string(nil), route.args...)
					args = append(args, "--"+flag+"=", "--no-input", "--output=json")
					if preview {
						args = append(args, "--dry-run")
					}
					stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
					var frame struct {
						OK     bool             `json:"ok"`
						Errors []envelope.Error `json:"errors"`
					}
					if calls.Load() != 0 {
						t.Errorf("empty native input reached HTTP: %d requests", calls.Load())
					}
					if exit != cli.ExitValidation || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeFlagValueInvalid || frame.Errors[0].Field != "json-input" {
						t.Fatalf("empty native input was not refused at its boundary: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
					}
				})
			}
		}
	}
}

func TestNativeInputOmittedRouteKeepsFlagOnlyPreviews(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"issue", "create", "--project=FIX", "--type=Task", "--summary=fixture"},
		{"issue", "edit", "KEY-1", "--summary=fixture"},
		{"issue", "clone", "KEY-1"},
		{"issue", "weblink", "add", "KEY-1", "--url=https://example.com"},
		{"worklog", "add", "KEY-1", "--time-spent=1m", "--started=2026-01-02"},
	} {
		t.Run(strings.Join(args[:2], "_"), func(t *testing.T) {
			t.Parallel()
			args = append(append([]string(nil), args...), "--dry-run", "--no-input", "--output=json")
			stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL="}, args...)
			var frame struct {
				OK bool `json:"ok"`
			}
			if exit != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || stderr != "" {
				t.Fatalf("omitted native input changed flag-only preview: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
		})
	}
}
