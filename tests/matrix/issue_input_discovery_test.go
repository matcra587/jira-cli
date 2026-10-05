//go:build flagmatrix

package matrix

import (
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestIssueInputDiscoveryFlagsSelectAuthenticatedReads(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"create", "edit"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			const field = `{"fieldId":"customfield_14002","key":"displayKey","name":"Choice","schema":{"type":"plugin","custom":"fixture:choice"},"required":true,"operations":["set"],"defaultValue":{"id":"7001"},"allowedValues":[{"id":"7001"}],"future":{"preserve":null}}`
			url := mx3Stub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet {
					t.Error("input generation attempted a mutation")
				}
				switch r.URL.Path {
				case "/rest/api/3/project/KEY":
					_, _ = io.WriteString(w, `{"id":"10402","key":"KEY"}`)
				case "/rest/api/3/issue/createmeta/10402/issuetypes":
					_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"total":1,"issueTypes":[{"id":"10044","name":"Task"}]}`)
				case "/rest/api/3/issue/createmeta/10402/issuetypes/10044":
					_, _ = fmt.Fprintf(w, `{"startAt":0,"maxResults":50,"total":1,"fields":[%s]}`, field)
				case "/rest/api/3/issue/KEY-1/editmeta", "/rest/api/3/issue/KEY-2/editmeta":
					_, _ = fmt.Fprintf(w, `{"fields":{"customfield_14002":%s}}`, field)
				default:
					t.Error("input discovery changed its explicit context")
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			args := []string{"issue", verb}
			wantCalls := int32(3)
			if verb == "create" {
				args = append(args, "--project=KEY", "--type=Task")
			} else {
				args = append(args, "KEY-1", "KEY-2", "--parallelism=1")
				wantCalls = 2
			}
			readArgs := append(append([]string(nil), args...), "--generate-json", "--output=json")
			readEnv := []string{"JIRA_READ_ONLY=true"}
			result := fwExecEnv(t, url, nil, "", readEnv, readArgs...)
			out := fwParse(t, result)
			if result.Exit != 0 || !out.OK || calls.Load() != wantCalls || out.Data["dry_run"] != nil {
				t.Fatalf("generation flag failed to select a read: exit=%d calls=%d stdout=%s", result.Exit, calls.Load(), result.Stdout)
			}
			if verb == "create" {
				if out.Data["complete"] != true || fwAt(t, out.Data, "payload", "fields", "project", "id") != "10402" || fwAt(t, out.Data, "payload", "fields", "issuetype", "id") != "10044" || fwAt(t, out.Data, "payload", "fields", "customfield_14002") != nil || fwAt(t, out.Data, "fields", "customfield_14002", "required") != true {
					t.Fatal("create metadata skeleton lost actual context or guessed plugin values")
				}
			} else {
				for i := range 2 {
					if fwAt(t, out.Data, "results", i, "issue", "key") != fmt.Sprintf("KEY-%d", i+1) || fwAt(t, out.Data, "results", i, "ok") != true || fwAt(t, out.Data, "results", i, "data", "payload", "fields", "customfield_14002") != nil || fwAt(t, out.Data, "results", i, "data", "fields", "customfield_14002", "required") != true {
						t.Fatal("edit metadata skeleton lost an independent issue context")
					}
				}
			}
			blockedArgs := append(append([]string(nil), args...), "--summary=authored", "--output=json")
			blocked := fwExecEnv(t, url, nil, "", readEnv, blockedArgs...)
			refusal := fwParse(t, blocked)
			if blocked.Exit != 3 || refusal.OK || len(refusal.Errs) != 1 || refusal.Errs[0].Code != "read_only_blocked" || calls.Load() != wantCalls {
				t.Fatal("read-only control failed to refuse the corresponding mutation before requests")
			}
			// Explicit false retains ordinary local composition, with no metadata
			// reads and no authenticated client needed by a mutation preview.
			writeArgs := append(append([]string(nil), args...), "--generate-json=false", "--summary=authored", "--dry-run", "--output=json")
			result = fwExec(t, url, nil, "", writeArgs...)
			out = fwParse(t, result)
			if result.Exit != 0 || !out.OK || out.Data["dry_run"] != true || fwAt(t, out.Data, "payload", "fields", "summary") != "authored" || calls.Load() != wantCalls {
				t.Fatal("false read selector changed default mutation composition or performed live reads")
			}
			Prove(t, "jira issue "+verb, "generate-json", "true selects native context-specific GET metadata and null skeletons in read-only mode; false retains local authored mutation composition without requests")
		})
	}
}
