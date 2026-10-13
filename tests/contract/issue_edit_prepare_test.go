package contract

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestIssueEditPrepareActualBinaryRetainsBoundNativeArtifacts(t *testing.T) {
	t.Parallel()
	const native = `{"type":"doc","version":1,"future":{"large":9007199254740993,"text":"\u003c\/"},"content":[{"type":"paragraph","content":[{"type":"text","text":"authored description"}]},{"type":"futureNode","attrs":{"raw":"<>&"}}]}`
	for _, mode := range []string{"json", "compact", "human"} {
		for _, description := range []string{native, strings.ReplaceAll(native, `,"content"`, ",\n  \"content\""), "null"} {
			t.Run(mode+"/"+description, func(t *testing.T) {
				t.Parallel()
				var reads, writes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						writes.Add(1)
						w.WriteHeader(http.StatusMethodNotAllowed)
						return
					}
					if r.URL.Path != "/rest/api/3/issue/KEY-1" || r.URL.Query().Get("fields") != "description,updated" {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					reads.Add(1)
					if _, err := w.Write([]byte(`{"id":"123456","key":"KEY-1","fields":{"updated":null,"description":` + description + `}}`)); err != nil {
						t.Error(err)
					}
				}))
				defer server.Close()
				env := []string{"JIRA_BASE_URL=" + server.URL, "JIRA_EMAIL=reader@example.test", "JIRA_API_TOKEN=synthetic-token", "JIRA_KEYRING=false", "JIRA_SECRET_BACKEND=env", "JIRA_READ_ONLY=true", "JIRA_NO_UPDATE_CHECK=1"}
				stdout, stderr, exit := runBinaryEnv(t, env, "issue", "edit", "prepare", "KEY-1", "--output="+mode, "--no-input")
				if exit != 0 || stderr != "" || reads.Load() != 1 || writes.Load() != 0 {
					t.Fatalf("read-only preparation failed: exit=%d reads=%d writes=%d stdout=%s stderr=%s", exit, reads.Load(), writes.Load(), stdout, stderr)
				}
				if mode == "human" {
					if !strings.Contains(stdout, "KEY-1") || !strings.Contains(stdout, "has not changed Jira") || description != "null" && !strings.Contains(stdout, "protected") {
						t.Fatal("human artifact lost identity, protection or read-only outcome", stdout)
					}
					return
				}
				var out envelope.IssueEditPrepareOutput
				if err := json.Unmarshal(adfData(t, stdout, mode, "issue.edit.prepare", true), &out); err != nil {
					t.Fatal(err)
				}
				if out.Issue.ID != "123456" || out.Issue.Key != "KEY-1" || out.Snapshot.Site != server.URL || out.Snapshot.Account != "reader@example.test" || string(out.Snapshot.DescriptionBytes) != description || !out.Snapshot.Updated.Set || !out.Snapshot.Updated.Null {
					t.Fatal("native source, identity, null timestamp or client binding changed", stdout)
				}
				session, err := core.OpenIssueDescriptionSession(out.Snapshot)
				if err != nil || session.Markdown() != out.Markdown {
					t.Fatal("actual binary artifact could not reopen", err)
				}
				candidate, err := session.Apply(out.Markdown)
				if err != nil || !bytes.Equal(candidate, out.Snapshot.DescriptionBytes) {
					t.Fatal("actual binary artifact changed native bytes", err)
				}
				if description == "null" && (out.Markdown != "" || len(out.Regions) != 0 || !strings.Contains(stdout, `"regions":[]`)) {
					t.Fatal("null source invented content or null region inventory", stdout)
				}
			})
		}
	}
}

func TestIssueEditPrepareActualBinaryRefusesBeforeClient(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"issue", "edit", "prepare"},
		{"issue", "edit", "prepare", "KEY-1", "KEY-2"},
		{"issue", "edit", "prepare", "KEY-1", "--web"},
		{"issue", "edit", "prepare", "KEY-1", "--dry-run"},
		{"issue", "edit", "--summary=ignored", "prepare", "KEY-1"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_BASE_URL=" + server.URL, "JIRA_EMAIL=reader@example.test", "JIRA_API_TOKEN=synthetic-token", "JIRA_KEYRING=false", "JIRA_SECRET_BACKEND=env", "JIRA_NO_UPDATE_CHECK=1"}, append(args, "--output=json", "--no-input")...)
			if exit != 3 || calls.Load() != 0 {
				t.Fatalf("pure refusal reached client: exit=%d calls=%d stdout=%s stderr=%s", exit, calls.Load(), stdout, stderr)
			}
		})
	}
}

func TestIssueEditPrepareActualBinaryRefusesForeignAndMalformedReplies(t *testing.T) {
	t.Parallel()
	const body = `{"id":"123456","key":"KEY-1","fields":{"description":null}}`
	for _, reply := range []string{"foreign redirect", "malformed JSON", "wrong field type", "trailing document", "duplicate fields"} {
		t.Run(reply, func(t *testing.T) {
			t.Parallel()
			foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("foreign redirect retained credentials")
				}
				_, _ = w.Write([]byte(body))
			}))
			defer foreign.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var native string
				switch reply {
				case "foreign redirect":
					http.Redirect(w, r, foreign.URL+"/issue", http.StatusFound)
					return
				case "malformed JSON":
					native = `{"id":`
				case "wrong field type":
					native = `{"id":12,"key":"KEY-1","fields":{"description":null}}`
				case "trailing document":
					native = body + ` {}`
				case "duplicate fields":
					native = `{"id":"123456","key":"KEY-1","fields":{},"fields":{"description":null}}`
				}
				_, _ = w.Write([]byte(native))
			}))
			defer server.Close()
			stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_BASE_URL=" + server.URL, "JIRA_EMAIL=reader@example.test", "JIRA_API_TOKEN=synthetic-token", "JIRA_KEYRING=false", "JIRA_SECRET_BACKEND=env", "JIRA_NO_UPDATE_CHECK=1"}, "issue", "edit", "prepare", "KEY-1", "--output=json", "--no-input")
			var frame struct {
				OK     bool            `json:"ok"`
				Data   json.RawMessage `json:"data"`
				Errors []struct {
					Code string `json:"code"`
				} `json:"errors"`
			}
			if json.Unmarshal([]byte(stdout), &frame) != nil || exit != 5 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "issue_description_response_invalid" || len(frame.Data) != 0 && string(frame.Data) != "null" {
				t.Fatalf("foreign or malformed source became usable or lost taxonomy: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
			}
		})
	}
}
