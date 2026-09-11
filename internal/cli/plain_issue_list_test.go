package cli

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/gechr/x/ansi"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestIssueListPlainTableUsesPrimerFlexLinksAndStyles(t *testing.T) {
	t.Parallel()

	data := map[string]any{
		"detail": false,
		"issues": []map[string]any{
			{
				"key":      "SAM1-7",
				"summary":  "Create wallet integration with a long enough summary to exercise flex columns",
				"status":   "In Progress",
				"assignee": "Riley Chen",
				"priority": "High",
			},
		},
	}

	var buf bytes.Buffer
	err := WriteCommandPlain(
		&buf,
		"issue.list",
		data,
		WithPlainBaseURL("https://acme.atlassian.net/"),
		WithPlainTermWidth(72),
		WithPlainTTY(true),
	)
	if err != nil {
		t.Fatalf("WriteCommandPlain() error = %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "\x1b[8m  \x1b[28m") {
		t.Fatalf("terminal table spacing is not protected from tab conversion: %q", got)
	}
	stripped := ansi.Strip(got)
	for _, want := range []string{"INF", "Listed issues", "KEY", "SUMMARY", "STATUS", "ASSIGNEE", "PRIORITY", "SAM1-7", "In Progress", "Riley Chen", "High"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("plain issue list missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "\x1b]8;;https://acme.atlassian.net/browse/SAM1-7") {
		t.Fatalf("issue key is not an OSC 8 Jira hyperlink:\n%q", got)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("status/priority cells were not styled with ANSI colors:\n%q", got)
	}
	if !regexp.MustCompile("\x1b\\[38;[^m]*mRiley Chen").MatchString(got) {
		t.Fatalf("assignee cell was not color-styled by hash:\n%q", got)
	}

	for line := range strings.SplitSeq(stripped, "\n") {
		if strings.Contains(line, "SAM1-7") && ansi.StringWidth(line) > 72 {
			t.Fatalf("issue table row exceeded terminal width: width=%d line=%q", ansi.StringWidth(line), line)
		}
	}
}

func TestIssueListPlainDetailRendersFullIssuesAsTable(t *testing.T) {
	t.Parallel()

	data := map[string]any{
		"detail": true,
		"issues": []*jira.Issue{
			{
				Key: new("SAM1-7"),
				Fields: &jira.IssueFields{
					Summary:  new("Create wallet integration"),
					Status:   &jira.Status{Name: new("In Progress")},
					Assignee: &jira.User{DisplayName: new("Riley Chen")},
					Priority: &jira.Priority{Name: new("High")},
				},
			},
		},
	}

	var buf bytes.Buffer
	err := WriteCommandPlain(&buf, "issue.list", data, WithPlainTTY(false), WithPlainTermWidth(100))
	if err != nil {
		t.Fatalf("WriteCommandPlain() error = %v", err)
	}

	got := buf.String()
	for _, want := range []string{"Listed issues", "KEY", "SUMMARY", "ASSIGNEE", "SAM1-7", "Create wallet integration", "Riley Chen"} {
		if !strings.Contains(got, want) {
			t.Fatalf("detail issue list missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b[8m") {
		t.Fatalf("piped table contains terminal padding escapes: %q", got)
	}
	for _, notWant := range []string{"issues=\"", "\"fields\"", "\"comments\"", "value="} {
		if strings.Contains(got, notWant) {
			t.Fatalf("detail issue list fell back to raw struct output %q:\n%s", notWant, got)
		}
	}
}

func TestIssueListPlainShowsParallelWhenNonDefault(t *testing.T) {
	t.Parallel()

	data := map[string]any{
		"detail": false,
		"issues": []map[string]any{},
	}

	var buf bytes.Buffer
	err := WriteCommandPlain(&buf, "issue.list", data, WithPlainThreads(4))
	if err != nil {
		t.Fatalf("WriteCommandPlain() error = %v", err)
	}
	got := buf.String()
	for _, want := range []string{"Listed issues", "count=0", "threads=4"} {
		if !strings.Contains(got, want) {
			t.Fatalf("issue list output missing %q:\n%s", want, got)
		}
	}

	buf.Reset()
	err = WriteCommandPlain(&buf, "issue.list", data)
	if err != nil {
		t.Fatalf("WriteCommandPlain(default) error = %v", err)
	}
	if strings.Contains(buf.String(), "parallel=") {
		t.Fatalf("default issue list output should omit parallel:\n%s", buf.String())
	}
}
