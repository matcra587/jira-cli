package contract

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestJQLCompositionBinaryAliasesAndStatusPrecedence(t *testing.T) {
	t.Parallel()
	want := `project in ("ABC") AND (assignee = currentUser() OR assignee is EMPTY) AND (status in ("Open") OR statusCategory in ("In Progress", "Done")) AND status != "Abandoned" AND reporter = currentUser()`
	for _, format := range []string{"json", "compact", "human"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false", "AGENT=1", "JIRA_READ_ONLY=true"},
				"issue", "list", "--as-jql", "--project=ABC", "--assignee=me,none", "--reporter=currentUser()", "--status=Open,>=In Progress,!Abandoned", "--output="+format)
			if exit != 0 || stderr != "" {
				t.Fatalf("local composition failed: exit=%d stderr=%q stdout=%s", exit, stderr, stdout)
			}
			if format == "human" {
				if !strings.Contains(stdout, want) {
					t.Fatalf("human query changed precedence: %s", stdout)
				}
				return
			}
			var out envelope.IssueListOutput
			data := adfData(t, stdout, format, "issue.list", true)
			if json.Unmarshal(data, &out) != nil || out.JQL != want || out.Issues == nil || len(out.Issues) != 0 || out.Board != nil {
				t.Fatalf("local typed composition lost query or shape: %s", data)
			}
		})
	}
}

func TestJQLCompositionBinaryRefusesMalformedFiltersBeforeHTTP(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ flag, value string }{
		{"status", ""}, {"status", "!"}, {"status", ">Ready"}, {"status", "<To Do"}, {"status", "Done\n"}, {"assignee", "me\n"}, {"project", "ABC\n"},
	} {
		t.Run(test.flag+"/"+test.value, func(t *testing.T) {
			t.Parallel()
			stdout, _, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false", "AGENT=1"}, "issue", "list", "--as-jql", "--"+test.flag+"="+test.value, "--output=json")
			var out apiFrame
			if exit == 0 || json.Unmarshal([]byte(stdout), &out) != nil || out.OK || len(out.Errors) != 1 || out.Errors[0].Code != "flag_value_invalid" || out.Errors[0].Field != test.flag {
				t.Fatalf("malformed filter reached authentication or escaped input classification: exit=%d %s", exit, stdout)
			}
		})
	}
}
