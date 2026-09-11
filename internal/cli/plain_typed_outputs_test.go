package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// The renderer integration closed a regression class: a bespoke plain
// renderer that asserted data.(map[string]any) fell through to the generic
// field dump once its builder emitted a typed envelope Output struct. Each
// test below feeds WriteCommandPlain the SAME struct the builder emits and
// asserts the restored table/preview rendering — the case no test caught,
// because every existing test fed a hand-built map. Helper strings assume the
// default no-TTY config (bare text, no ANSI), so substring checks are stable.

func renderCommand(t *testing.T, command string, data any) string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteCommandPlain(&buf, command, data); err != nil {
		t.Fatalf("WriteCommandPlain(%q) error = %v", command, err)
	}
	return buf.String()
}

func requireContains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered output missing %q:\n%s", want, got)
		}
	}
}

func requireNotContains(t *testing.T, got, unwanted string) {
	t.Helper()
	if strings.Contains(got, unwanted) {
		t.Fatalf("rendered output should not contain %q:\n%s", unwanted, got)
	}
}

func TestBoardListPlainRendersTypedStruct(t *testing.T) {
	got := renderCommand(t, "boards.list", envelope.BoardsListOutput{
		Boards: []envelope.BoardRow{
			{ID: 42, Name: "Sprint Board", Type: "scrum", ProjectKeys: []string{"PROJ"}},
		},
		FromCache: true,
		FetchedAt: "2026-07-01T00:00:00Z",
	})
	requireContains(t, got, "Boards", "Sprint Board", "42", "scrum", "PROJ")
	requireNotContains(t, got, "No boards visible")
}

func TestLinkListPlainRendersTypedStruct(t *testing.T) {
	got := renderCommand(t, "issue.link.list", envelope.IssueLinkListOutput{
		Issue: envelope.IssueRef{Key: "PROJ-1"},
		Links: []jira.IssueLinkView{{
			ID:         "9001",
			Direction:  "outward",
			Type:       jira.IssueLinkType{Name: "Blocks", Outward: "blocks", Inward: "is blocked by"},
			OtherIssue: jira.IssueRef{Key: "PROJ-2", Summary: "dependent work", Status: "In Progress"},
		}},
		Count: 1,
	})
	requireContains(t, got, "Links on PROJ-1", "blocks", "PROJ-2", "dependent work")
	requireNotContains(t, got, "(no links)")
}

func TestLinkTypesPlainRendersTypedStruct(t *testing.T) {
	got := renderCommand(t, "issue.link.types", envelope.IssueLinkTypesOutput{
		LinkTypes: []jira.IssueLinkType{{ID: "10000", Name: "Blocks", Outward: "blocks", Inward: "is blocked by"}},
		Count:     1,
		FromCache: true,
	})
	requireContains(t, got, "Link types", "Blocks", "blocks", "is blocked by")
	requireNotContains(t, got, "(no link types configured)")
}

func TestAttachmentListPlainRendersTypedStruct(t *testing.T) {
	got := renderCommand(t, "issue.attachment.list", envelope.IssueAttachmentListOutput{
		Issue: envelope.IssueRef{Key: "PROJ-1"},
		Attachments: []envelope.AttachmentItem{{
			ID:       "10",
			Filename: "design.pdf",
			Size:     2048,
			Created:  "2026-07-01T00:00:00.000+0000",
			Author:   envelope.AttachmentAuthor{DisplayName: "Alice"},
		}},
	})
	requireContains(t, got, "Attachments on PROJ-1", "design.pdf", "Alice")
	requireNotContains(t, got, "(no attachments)")
}

func TestAttachmentListPlainUsesIssueHeadingAcrossTerminalModes(t *testing.T) {
	data := map[string]any{
		"issue":       map[string]any{"key": "PROJ-1"},
		"attachments": []any{},
	}
	for _, tty := range []bool{false, true} {
		var buf bytes.Buffer
		if err := WriteAttachmentListPlain(&buf, "issue.attachment.list", data, WithPlainTTY(tty)); err != nil {
			t.Fatalf("WriteAttachmentListPlain(tty=%v) error = %v", tty, err)
		}
		requireContains(t, buf.String(), "Attachments on PROJ-1")
	}
}

func TestCommentListPlainRendersTypedStructWithNativeADF(t *testing.T) {
	body := adf.Document{Type: "doc", Version: 1, Content: []adf.Node{{
		Type:    "paragraph",
		Content: []adf.Node{{Type: "text", Text: "native adf preview survives"}},
	}}}
	got := renderCommand(t, "issue.comment.list", envelope.IssueCommentListOutput{
		Issue: envelope.IssueRef{Key: "PROJ-1"},
		Comments: []envelope.CommentItem{{
			ID:      "100",
			Body:    body,
			Author:  &envelope.CommentUser{DisplayName: "Alice"},
			Created: "2026-07-01T10:00:00.000+0000",
			Updated: "2026-07-01T10:00:00.000+0000",
		}},
	})
	// The body preview proves the native adf.Document reached commentBodyText
	// intact — a mapFromAny round-trip at the top would have dumped fields.
	requireContains(t, got, "Comments on PROJ-1", "#100", "Alice", "native adf preview survives")
}

func TestCommentListPlainUsesIssueHeadingAcrossTerminalModes(t *testing.T) {
	data := map[string]any{
		"issue":    map[string]any{"key": "PROJ-1"},
		"comments": []any{},
	}
	for _, tty := range []bool{false, true} {
		var buf bytes.Buffer
		if err := WriteCommentListPlain(&buf, "issue.comment.list", data, WithPlainTTY(tty)); err != nil {
			t.Fatalf("WriteCommentListPlain(tty=%v) error = %v", tty, err)
		}
		requireContains(t, buf.String(), "Comments on PROJ-1")
	}
}

func TestCommentListPlainResultKeyTakesHeadingPrecedence(t *testing.T) {
	data := map[string]any{
		"issue":    map[string]any{"key": "WRONG-1"},
		"comments": []any{},
	}
	var buf bytes.Buffer
	if err := WriteCommentListPlain(
		&buf,
		"issue.comment.list",
		data,
		WithPlainResultKey("PROJ-2"),
	); err != nil {
		t.Fatalf("WriteCommentListPlain() error = %v", err)
	}
	requireContains(t, buf.String(), "Comments on PROJ-2")
	requireNotContains(t, buf.String(), "WRONG-1")
}

func TestWatcherListPlainRendersTypedStruct(t *testing.T) {
	got := renderCommand(t, "issue.watchers.list", envelope.IssueWatchersListOutput{
		Issue: envelope.IssueRef{Key: "PROJ-1"},
		Watchers: []envelope.WatcherItem{{
			DisplayName:  "Alice",
			AccountID:    "5e0000000000000000000001",
			EmailAddress: new("alice@example.com"),
		}},
		IsWatching: true,
		WatchCount: 1,
	})
	requireContains(t, got, "Watchers on PROJ-1", "Alice", "(you are watching)")
	requireNotContains(t, got, "(no watchers visible)")
}

func TestWatcherListPlainUsesIssueHeadingAcrossTerminalModes(t *testing.T) {
	data := map[string]any{
		"issue":       map[string]any{"key": "PROJ-1"},
		"watchers":    []any{},
		"is_watching": false,
		"watch_count": 0,
	}
	for _, tty := range []bool{false, true} {
		var buf bytes.Buffer
		if err := WriteWatcherListPlain(&buf, "issue.watchers.list", data, WithPlainTTY(tty)); err != nil {
			t.Fatalf("WriteWatcherListPlain(tty=%v) error = %v", tty, err)
		}
		requireContains(t, buf.String(), "Watchers on PROJ-1")
	}
}

func TestIssueTransitionsPlainRendersTypedStruct(t *testing.T) {
	got := renderCommand(t, "issue.transitions", envelope.IssueTransitionsOutput{
		Issue: envelope.IssueRef{Key: "PROJ-1"},
		Transitions: []*jira.Transition{
			{ID: new("21"), Name: new("In Review")},
		},
	})
	requireContains(t, got, "Transitions on PROJ-1", "21", "In Review")
	requireNotContains(t, got, "(no transitions available)")
}

func TestJQLBuildPlainRendersTypedStruct(t *testing.T) {
	got := renderCommand(t, "jql.build", envelope.JQLBuildOutput{
		JQL: "project = PROJ ORDER BY updated DESC",
		URL: "https://example.atlassian.net/issues/?jql=project%20%3D%20PROJ",
	})
	requireContains(t, got, "project = PROJ ORDER BY updated DESC")
}

func TestSearchCountPlainRendersTypedStruct(t *testing.T) {
	got := renderCommand(t, "search.count", envelope.SearchCountOutput{
		Source: "jira",
		JQL:    "project = PROJ",
		Count:  42,
	})
	requireContains(t, got, "42")
}

// issue.list.count now emits envelope.IssueListOutput (Jql + Count), sharing
// the struct with the list/preview variants. Its renderer is writeCountPlain,
// so this pins the count-variant struct through the count code path.
func TestIssueListCountPlainRendersTypedStruct(t *testing.T) {
	count := 42
	got := renderCommand(t, "issue.list.count", envelope.IssueListOutput{
		Jql:   "project = PROJ",
		Count: &count,
	})
	requireContains(t, got, "42")
}

func TestJQLValidatePlainRendersTypedStruct(t *testing.T) {
	got := renderCommand(t, "jql.validate", envelope.JQLValidateOutput{
		Queries: []envelope.JQLValidateEntry{
			{Query: "project = PROJ", Valid: true},
			{Query: "bad = = =", Valid: false, Errors: []string{"syntax error"}},
		},
	})
	requireContains(t, got, "OK  project = PROJ", "INVALID  bad = = =", "syntax error")
}

func TestJQLReferencePlainRendersTypedStruct(t *testing.T) {
	got := renderCommand(t, "jql.reference", envelope.JQLReferenceOutput{
		Fields: []envelope.JQLReferenceField{
			{Value: "status", DisplayName: "Status"},
		},
	})
	requireContains(t, got, "status", "Status")
}

func TestUserSearchPlainRendersTypedStruct(t *testing.T) {
	got := renderCommand(t, "user.search", envelope.UserSearchOutput{
		Query: "alice",
		Users: []envelope.UserSearchMatch{
			{AccountID: "5e0000000000000000000001", DisplayName: "Alice", EmailAddress: "alice@example.com"},
		},
		Count: 1,
	})
	requireContains(t, got, "Alice", "alice@example.com")
	requireNotContains(t, got, "No users matched")
}
