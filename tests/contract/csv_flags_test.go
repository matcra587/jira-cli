package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestCSVFlagsRefuseChangedInputsBeforeSideEffects(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		args  []string
		field string
	}{
		{name: "attachment", args: []string{"issue", "attachment", "add", "KEY-1", "--file"}, field: "file"},
		{name: "clone replacement", args: []string{"issue", "clone", "KEY-1", "--replace"}, field: "replace"},
		{name: "field selector", args: []string{"issue", "view", "KEY-1", "--fields"}, field: "fields"},
		{name: "field mutation", args: []string{"issue", "create", "--summary=ready", "--label"}, field: "label"},
		{name: "hidden alias", args: []string{"issue", "create", "--summary=ready", "--labels"}, field: "labels"},
	} {
		for _, mode := range []string{"json", "compact", "human"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusBadRequest) }))
				t.Cleanup(server.Close)
				value := "first\nignored"
				switch tc.field {
				case "file":
					first := filepath.Join(t.TempDir(), "first.txt")
					if err := os.WriteFile(first, []byte("first"), 0o600); err != nil {
						t.Fatal(err)
					}
					value = first + "\nignored"
				case "replace":
					value = "old=new\nignored=discarded"
				}
				args := append(append([]string{}, tc.args...), value, "--dry-run", "--no-input", "--output="+mode, "--color=never")
				// issue view is a read and does not accept dry-run.
				if tc.field == "fields" {
					args = append(append([]string{}, tc.args...), value, "--no-input", "--output="+mode, "--color=never")
				}
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				if exit != cli.ExitValidation || calls.Load() != 0 {
					t.Fatalf("changed CSV reached command execution: exit=%d requests=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
				}
				if mode == "human" {
					if !strings.Contains(stdout+stderr, "discard or normalize line endings") {
						t.Fatal("human CSV error did not explain the refused data loss")
					}
					return
				}
				var frame struct {
					OK     bool             `json:"ok"`
					Data   json.RawMessage  `json:"data"`
					Errors []envelope.Error `json:"errors"`
				}
				if err := json.Unmarshal([]byte(stdout), &frame); err != nil {
					t.Fatal(err)
				}
				if frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "flag_value_invalid" || frame.Errors[0].Field != tc.field || string(frame.Data) != "null" {
					t.Fatalf("CSV refusal lost typed field or invented operation data: %+v", frame)
				}
			})
		}
	}
}

func TestAttachmentCSVQuotedNewlinePathIsPreserved(t *testing.T) {
	if xos.IsWindows() {
		t.Skip("Windows filenames cannot contain literal newlines")
	}
	t.Parallel()
	path := filepath.Join(t.TempDir(), "first\nsecond.txt")
	if err := os.WriteFile(path, []byte("authored"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_BASE_URL=", "AGENT=1"}, "issue", "attachment", "add", "KEY-1", "--file", "\""+path+"\"", "--dry-run", "--no-input", "--output=json")
	var frame struct {
		OK   bool                              `json:"ok"`
		Data envelope.IssueAttachmentAddOutput `json:"data"`
	}
	if exit != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || len(frame.Data.Files) != 1 || frame.Data.Files[0] != path || len(frame.Data.FileDetails) != 1 || frame.Data.FileDetails[0].Path != path {
		t.Fatalf("quoted filename changed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
}
