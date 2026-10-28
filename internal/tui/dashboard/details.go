package dashboard

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/jira"
)

var detailSections = []string{"Description", "Comments", "Activity", "Links"}

type commentsResult struct {
	key   string
	start int
	page  *jira.CommentPage
}

func (m *Model) changeDetailSection(delta int) tea.Cmd {
	m.acknowledgeRow()
	if !m.split() && m.detailKey != "" {
		m.detailFocused = true
		m.resize()
	}
	m.detailSection = (m.detailSection + delta + len(detailSections)) % len(detailSections)
	m.preview.GotoTop()
	m.syncPreview()
	if m.detailSection == 1 && m.commentsKey != m.detailKey {
		return m.loadComments(false)
	}
	return nil
}

func (m *Model) resetComments() {
	m.tasks.Start(task.Spec{Scope: "comments"})
	m.commentsKey = ""
	m.comments = nil
	m.commentsTotal = 0
	m.commentsNext = 0
	m.commentsLoading = false
	m.commentsErr = nil
}

func (m *Model) loadComments(more bool) tea.Cmd {
	if m.options.Actions.Comments == nil || m.detailKey == "" || m.commentsLoading {
		return nil
	}
	if more && (len(m.comments) >= m.commentsTotal || len(m.comments) >= 200) {
		return nil
	}
	target, start := m.detailKey, 0
	if more {
		start = m.commentsNext
	} else {
		m.comments = nil
	}
	m.commentsKey, m.commentsLoading, m.commentsErr = target, true, nil
	m.syncPreview()
	return m.tasks.Start(task.Spec{Scope: "comments", Run: func() (any, error) {
		page, err := m.options.Actions.Comments(target, start)
		return commentsResult{key: target, start: start, page: page}, err
	}})
}

func (m *Model) acceptComments(msg task.FinishedMsg) {
	result, ok := msg.Result.(commentsResult)
	if !ok || result.key != m.detailKey {
		return
	}
	m.commentsLoading, m.commentsErr = false, msg.Err
	if msg.Err == nil && result.page != nil {
		seen := make(map[string]bool)
		for _, c := range m.comments {
			seen[c.ID.Value] = true
		}
		for _, c := range result.page.Comments {
			if seen[c.ID.Value] {
				m.commentsErr = fmt.Errorf("comment page repeated an identity; refresh to restart")
				m.syncPreview()
				return
			}
			seen[c.ID.Value] = true
		}
		m.comments = append(m.comments, result.page.Comments...)
		m.commentsNext = result.start + len(result.page.Comments)
		m.commentsTotal = result.page.Total
	}
	m.syncPreview()
}

func (m *Model) sectionContent(width int) string {
	switch m.detailSection {
	case 1:
		if m.options.Actions.Comments == nil {
			return "Comments are unavailable in this session."
		}
		lines := []string{cli.MarkupStrong("Comments"), ""}
		if m.commentsErr != nil {
			lines = append(lines, m.errorText(m.commentsErr), "Press r to retry this comment page.")
		}
		if m.commentsLoading {
			lines = append(lines, "Loading comments…")
		}
		if !m.commentsLoading && m.commentsErr == nil && m.commentsTotal == 0 {
			lines = append(lines, "No comments.")
		}
		for _, c := range m.comments {
			lines = append(lines, cli.MarkupStrong(personValue(c.Author))+" · "+textValue(c.Created))
			if !c.Body.Set {
				lines = append(lines, "Body not returned.")
			} else if c.Body.Null {
				lines = append(lines, "No body.")
			} else {
				lines = append(lines, command.RenderADFWidth(c.Body.Value, width, "open the comment in Jira to inspect unsupported content"))
			}
			lines = append(lines, "")
		}
		lines = append(lines, fmt.Sprintf("%d of %d comments loaded · newest first", len(m.comments), m.commentsTotal))
		if len(m.comments) < m.commentsTotal {
			if len(m.comments) < 200 {
				lines = append(lines, "Press m to load the next page.")
			} else {
				lines = append(lines, "Showing 200 comments. Open Jira to read the remaining thread.")
			}
		}
		return strings.Join(lines, "\n")
	case 2:
		return activityContent(m.detailIssue)
	case 3:
		f := m.detailIssue.Fields.Value
		if !f.Issuelinks.Set {
			return "Linked issues not returned."
		}
		if f.Issuelinks.Null {
			return "Linked issues unavailable."
		}
		lines := []string{cli.MarkupStrong("Linked issues"), ""}
		for _, link := range f.Issuelinks.Value {
			if link.InwardIssue.Set && !link.InwardIssue.Null {
				lines = append(lines, textValue(link.Type.Value.Inward)+" · "+textValue(link.InwardIssue.Value.Key))
			}
			if link.OutwardIssue.Set && !link.OutwardIssue.Null {
				lines = append(lines, textValue(link.Type.Value.Outward)+" · "+textValue(link.OutwardIssue.Value.Key))
			}
		}
		if len(f.Issuelinks.Value) == 0 {
			lines = append(lines, "No linked issues.")
		} else {
			lines = append(lines, "", "Press o to choose a linked issue.")
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

func (m *Model) openLinkedIssues() {
	if m.detailIssue == nil {
		return
	}
	choices := []choice{}
	for _, link := range m.detailIssue.Fields.Value.Issuelinks.Value {
		for _, related := range []jira.Nullable[jira.LinkedIssue]{link.InwardIssue, link.OutwardIssue} {
			if related.Set && !related.Null && related.Value.Key.Set && !related.Value.Key.Null {
				choices = append(choices, choice{label: cli.Text(related.Value.Key.Value), description: cli.Text(link.Type.Value.Name.Value), value: related.Value.Key.Value})
			}
		}
	}
	if len(choices) == 0 {
		m.notice = "No returned linked issues to open."
		return
	}
	m.pick("Linked issues", "issue", "", choices)
}

// Activity is a read-only projection; the native changelog remains untouched.
// Expanded history is a server page, never an invented complete activity log.
func activityContent(issue *jira.IssueBean) string {
	if !issue.Changelog.Set {
		return "Activity not returned."
	}
	if issue.Changelog.Null {
		return "Activity unavailable."
	}
	var page struct {
		Total     jira.Nullable[int] `json:"total"`
		Histories jira.Nullable[[]struct {
			Author  jira.Nullable[jira.UserDetails] `json:"author"`
			Created jira.Nullable[string]           `json:"created"`
			Items   []struct {
				Field string                `json:"field"`
				From  jira.Nullable[string] `json:"fromString"`
				To    jira.Nullable[string] `json:"toString"`
			} `json:"items"`
		}] `json:"histories"`
	}
	if json.Unmarshal(issue.Changelog.Value, &page) != nil || !page.Histories.Set || page.Histories.Null {
		return "Activity could not be displayed. Inspect the native issue in Jira."
	}
	lines := []string{cli.MarkupStrong("Activity · returned history page"), ""}
	histories := page.Histories.Value[:min(100, len(page.Histories.Value))]
	for _, h := range histories {
		lines = append(lines, cli.MarkupStrong(personValue(h.Author))+" · "+textValue(h.Created))
		for _, item := range h.Items {
			lines = append(lines, cli.Text(item.Field)+": "+textValue(item.From)+" → "+textValue(item.To))
		}
		lines = append(lines, "")
	}
	if len(histories) == 0 {
		lines = append(lines, "No entries in the returned history page.")
	}
	if page.Total.Set && !page.Total.Null {
		lines = append(lines, fmt.Sprintf("%d of %d history entries shown.", len(histories), page.Total.Value))
	}
	lines = append(lines, "Open Jira for history outside this returned page.")
	return strings.Join(lines, "\n")
}

type detailResult struct {
	key   string
	issue *jira.IssueBean
}

type detailDue struct{ key string }

func (m *Model) selectDetail() tea.Cmd {
	key := m.selectedKey()
	if key == m.detailKey {
		return nil
	}
	m.detailKey, m.detailIssue, m.detailErr = key, nil, nil
	m.resetComments()
	m.detailLoading = key != ""
	m.preview.GotoTop()
	m.syncPreview()
	if key == "" {
		return nil
	}
	// Avoid a request for every key-repeat while moving through a long list.
	due := tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return detailDue{key: key} })
	if m.detailSection == 1 {
		return tea.Batch(due, m.loadComments(false))
	}
	return due
}

func (m *Model) startDetailRead(key string) tea.Cmd {
	if key != m.detailKey || key == "" {
		return nil
	}
	return m.tasks.Start(task.Spec{Scope: detailScope, Run: func() (any, error) { return m.detail(key) }})
}

func (m *Model) acceptDetail(msg task.FinishedMsg) {
	result, ok := msg.Result.(detailResult)
	if !ok || result.key != m.detailKey {
		return
	}
	m.detailLoading, m.detailErr = false, msg.Err
	if msg.Err == nil {
		m.detailIssue = result.issue
		m.rememberIssue(result.key, result.issue.Fields.Value.Summary.Value)
	}
	m.syncPreview()
}
