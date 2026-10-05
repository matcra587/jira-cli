//go:build live

package live

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestLiveOpenIssueURLMatchesReadFixture(t *testing.T) {
	r := newLiveRun(t)
	key := requiredEnv(t, "JIRA_LIVE_READ_KEY")
	if !strings.HasPrefix(key, r.manifest.Project+"-") {
		t.Fatal("open fixture is outside the dedicated project")
	}
	native := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=project")
	var issue struct {
		Key    string `json:"key"`
		Fields struct {
			Project struct {
				Key string `json:"key"`
			} `json:"project"`
		} `json:"fields"`
	}
	if json.Unmarshal(native.Response.JSON, &issue) != nil || issue.Key != key || issue.Fields.Project.Key != r.manifest.Project {
		t.Fatal("native fixture has no independently confirmed scoped identity")
	}
	frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true", "JIRA_EMAIL=", "JIRA_KEYRING=false", "BROWSER=/does/not/exist"}, "open", key)
	var out struct {
		Data envelope.IssueOpenOutput `json:"data"`
		Meta struct {
			Command string `json:"command"`
		} `json:"meta"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &out) != nil || out.Meta.Command != "open" || out.Data.Issue.Key != key || out.Data.Issue.ID != "" || out.Data.Issue.Self != "" || out.Data.Opened || out.Data.WebURL != strings.TrimRight(r.manifest.BaseURL, "/")+"/browse/"+key {
		t.Fatal("local URL receipt differs from the independently confirmed native identity; inspect local evidence")
	}
}
