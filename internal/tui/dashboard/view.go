package dashboard

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/table"
	"github.com/gechr/x/ansi"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/jira"
)

// A split needs enough space for useful issue summaries and readable prose.
func (m *Model) pageWidth() int { return max(1, m.width-4) }

func (m *Model) split() bool {
	return m.enabled("tui_preview") && m.pageWidth() >= 110 && m.height >= 24 && !m.detailFocused
}

func (m *Model) rowStride() int {
	if m.enabled("tui_compact") {
		return 1
	}
	return 2
}

func (m *Model) listWidth() int {
	if m.split() {
		return (m.pageWidth() - 3) * 3 / 5
	}
	return m.pageWidth()
}

func (m *Model) previewWidth() int {
	if m.split() {
		return m.pageWidth() - m.listWidth() - 3
	}
	return m.pageWidth()
}
func (m *Model) bodyHeight() int { return max(1, m.height-7) }

func (m *Model) applyStyles() {
	if !cli.StyleEnabled() {
		m.tabs.ActiveStyle = lipgloss.NewStyle()
		m.rows.Styles.Cursor = lipgloss.NewStyle()
		return
	}
	theme := cli.ResolvedTheme()
	m.tabs.ActiveStyle = theme.Blue.Bold(true).Underline(true)
	m.tabs.InactiveStyle = *theme.Dim
	m.rows.Styles.Cursor = lipgloss.NewStyle().Reverse(true)
	m.rows.Styles.Dim = *theme.Dim
}

func (m *Model) resize() {
	m.input.SetWidth(max(1, m.width-9))
	m.rows.SetSize(m.listWidth(), max(1, (m.bodyHeight()-3)/m.rowStride()))
	m.preview.SetWidth(max(1, m.previewWidth()-2))
	m.preview.SetHeight(max(1, m.bodyHeight()-2))
	m.rebuildRows()
	m.syncPreview()
}

func (m *Model) rebuildRows() {
	headers := []string{"ISSUE", "SUMMARY"}
	if m.enabled("tui_status") && m.listWidth() >= 64 {
		headers = append(headers, "STATUS")
	}
	showAssignee := m.enabled("tui_assignee") && m.listWidth() >= 92 && m.tabs.ActiveItem() != "My work"
	if showAssignee {
		headers = append(headers, "ASSIGNEE")
	}
	showUpdated := m.enabled("tui_updated") && m.listWidth() >= 110
	if showUpdated {
		headers = append(headers, "UPDATED")
	}
	rows := make([][]string, 0, len(m.issues)+1)
	rows = append(rows, headers)
	statusWidth := 0
	for _, issue := range m.issues {
		statusWidth = max(statusWidth, ansi.StringWidth(cli.Text(issue.Fields.Status.Value.Name.Value)))
	}
	for _, issue := range m.issues {
		f := issue.Fields
		cells := []string{cli.Text(issue.Key), textValue(f.Summary)}
		if m.enabled("tui_status") && m.listWidth() >= 64 {
			cells = append(cells, statusPadded(f.Status, statusWidth))
		}
		if showAssignee {
			cells = append(cells, personValue(f.Assignee))
		}
		if showUpdated {
			cells = append(cells, textValue(f.Updated))
		}
		rows = append(rows, cells)
	}
	grid := table.NewGrid(rows)
	grid.MaxWidth, grid.FlexCols = max(1, m.listWidth()-7), []int{1}
	// The selected Primer table uses wcwidth, matching layout.NormalizeLine.
	aligned, _ := grid.AlignColumns()
	m.rowHeader = dim("      " + aligned[0])
	m.paintedRows = aligned[1:]
	m.rows.SetRows(m.paintedRows)
	m.rows.SetSize(m.listWidth(), max(1, (m.bodyHeight()-3)/m.rowStride()))
}

// list.View strips SGR from the selected row, which removes status-pill colors.
// Keep Primer's selection and viewport math, and paint its public visible range
// with the ordinary value sinks so selected badges retain their semantics.
func (m *Model) rowsView() string {
	start, end := m.rows.VisibleRange()
	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		marker := "  "
		if i == m.rows.Cursor() {
			marker = accent("› ")
		}
		mark := dim("[ ] ")
		if m.marks[m.issues[i].Key] {
			mark = accent("[x] ")
		}
		lines = append(lines, marker+mark+m.paintedRows[i])
		if m.rowStride() == 2 {
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n")
}

func dim(text string) string {
	if cli.StyleEnabled() {
		return cli.ResolvedTheme().Dim.Render(text)
	}
	return text
}

func accent(text string) string {
	if cli.StyleEnabled() {
		return cli.ResolvedTheme().Blue.Render(text)
	}
	return text
}

func textValue(value jira.Nullable[string]) string {
	if !value.Set {
		return "not returned"
	}
	if value.Null || value.Value == "" {
		return "-"
	}
	return cli.Text(value.Value)
}

func personValue(value jira.Nullable[jira.UserDetails]) string {
	if !value.Set {
		return "not returned"
	}
	if value.Null {
		return "Unassigned"
	}
	return textValue(value.Value.DisplayName)
}

func statusValue(value jira.Nullable[jira.StatusDetails]) string {
	return statusPadded(value, 0)
}

func statusPadded(value jira.Nullable[jira.StatusDetails], width int) string {
	if !value.Set {
		return "not returned"
	}
	if value.Null {
		return "-"
	}
	status := value.Value
	return cli.StatusPadded(textValue(status.Name), cli.StatusCategory(status.StatusCategory.Value.Key.Value), width)
}

func (m *Model) syncPreview() {
	w := max(1, m.previewWidth()-2)
	var content string
	switch {
	case m.detailKey == "":
		content = "Select an issue to see its details."
	case m.detailErr != nil:
		content = "Could not load this issue.\n\n" + m.errorText(m.detailErr) + "\n\nPress r to retry."
	case m.detailLoading:
		content = "Loading issue…"
	case m.detailIssue != nil:
		if m.detailSection != 0 {
			content = m.sectionContent(w)
			break
		}
		f := m.detailIssue.Fields.Value
		lines := []string{cli.MarkupStrong(textValue(f.Summary)), ""}
		if f.Status.Set {
			lines = append(lines, "Status     "+statusValue(f.Status))
		}
		if f.Assignee.Set {
			lines = append(lines, "Assignee   "+personValue(f.Assignee))
		}
		if m.enabled("tui_priority") && f.Priority.Set {
			lines = append(lines, "Priority   "+textValue(f.Priority.Value.Name))
		}
		if m.enabled("tui_issue_type") && f.Issuetype.Set {
			lines = append(lines, "Type       "+textValue(f.Issuetype.Value.Name))
		}
		if m.enabled("tui_labels") && f.Labels.Set && !f.Labels.Null && len(f.Labels.Value) > 0 {
			lines = append(lines, "Labels     "+cli.Text(strings.Join(f.Labels.Value, ", ")))
		}
		lines = append(lines, "", cli.MarkupStrong("Description"), "")
		switch {
		case !f.Description.Set:
			lines = append(lines, "Description not returned.")
		case f.Description.Null || len(f.Description.Value) == 0:
			lines = append(lines, "No description.")
		default:
			lines = append(lines, command.RenderADFWidth(f.Description.Value, w, "open the issue in Jira to inspect unsupported content"))
		}
		content = strings.Join(lines, "\n")
	default:
		content = "Loading issue…"
	}
	// Keep the viewport offset across resize while wrapping through the shared
	// ANSI-aware layout owner, including CJK and multiline Jira content.
	m.preview.SetContent(strings.Join(layout.WrapLines(content, w), "\n"))
}

func frame(content string, width, height int) string {
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(layout.NormalizeLines(lines, width), "\n")
}

func (m *Model) listView() string {
	width, height := m.listWidth(), m.bodyHeight()
	viewName := m.tabs.ActiveItem()
	if viewName == "Search" && m.viewName != "" {
		viewName = m.viewName
	}
	focus := ""
	if !m.detailFocused && !m.previewFocused {
		focus = accent("› ")
	}
	title := focus + cli.MarkupStrong(viewName) + dim(fmt.Sprintf("   %d loaded", len(m.issues)))
	if selected := len(m.markedKeys()); selected > 0 {
		title += accent(fmt.Sprintf(" · %d selected", selected))
	}
	if !m.isLast && len(m.issues) > 0 {
		title += " · more available"
	}
	if m.loading {
		title += " · loading…"
	}
	body := m.rowsView()
	if len(m.issues) == 0 {
		switch {
		case m.loading:
			body = "\n  Loading your issues…"
		case m.err != nil:
			body = "\n  Could not load issues. Press r to retry."
		default:
			body = "\n  No issues match this view.\n\n  Press / to change the JQL query."
		}
	}
	return frame(title+"\n\n"+m.rowHeader+"\n"+body, width, height)
}

func (m *Model) detailView() string {
	width, height := m.previewWidth(), m.bodyHeight()
	title := dim("Issue preview")
	if m.detailFocused || m.previewFocused {
		title = accent("› Issue preview")
	}
	if m.detailKey != "" {
		title = accent(cli.Text(m.detailKey))
		if m.detailFocused || m.previewFocused {
			title = accent("› ") + title
		}
	}
	if m.detailFocused {
		title += dim("   esc back")
	} else {
		title += dim("   enter to focus")
	}
	sections := make([]string, len(detailSections))
	for i, name := range detailSections {
		if i == m.detailSection {
			sections[i] = accent(name)
		} else {
			sections[i] = dim(name)
		}
	}
	sectionBar := strings.Join(sections, " · ")
	if width < 65 {
		sectionBar = accent(detailSections[m.detailSection]) + dim(" · [ ] sections")
	}
	return frame(title+"\n"+sectionBar+"\n"+m.preview.View(), width, height)
}

func (m *Model) helpView() string {
	text := "DASHBOARD HELP\n\n" +
		"Views     Tab / Shift+Tab switch views · v saved views and lenses\n" +
		"Search    / edit JQL · Ctrl+S save the current search\n" +
		"Issues    Ctrl+O switch issue · ↑↓ or j/k select\n" +
		"Focus     Enter full details · Ctrl+W list/preview · Esc back\n" +
		"Details   [ / ] switch Description, Comments, Activity and Links\n" +
		"Links     o choose a linked issue\n" +
		"Select    Space mark · Ctrl+A all/none · X invert · x range\n" +
		"Actions   t transition · a assign · c comment · B marked actions\n" +
		"History   L outcomes · b opens latest batch in history\n" +
		"Settings  , customize display · Ctrl+S save · p preview · Esc cancel\n" +
		"Refresh   r retry/refresh · m more issues or comments in focused details\n" +
		"Menus     Ctrl+P / Ctrl+K find a command · / searches inside Settings\n" +
		"Exit      q / Ctrl+C close dashboard\n\n" +
		"Comment drafts and recent issues stay in this session.\n" +
		"Press any key to return."
	return frame(strings.Join(layout.WrapLines(text, m.pageWidth()), "\n"), m.pageWidth(), m.bodyHeight())
}

func (m *Model) hints() string {
	hints := []key.Hint{
		{Key: "v", Desc: "views"},
		{Key: "ctrl+o", Desc: "switch"},
		{Key: "↑↓", Desc: "select"},
		{Key: "/", Desc: "search"},
		{Key: "enter", Desc: "details"},
		{Key: "ctrl+p", Desc: "commands"},
		{Key: ",", Desc: "settings"},
		{Key: "q", Desc: "quit"},
	}
	if m.pageWidth() < 100 {
		hints = hints[2:]
	}
	if m.pageWidth() < 65 {
		hints = []key.Hint{{Key: "/", Desc: "search"}, {Key: "ctrl+p", Desc: "menu"}, {Key: "q", Desc: "quit"}}
	}
	if m.detailFocused || m.previewFocused {
		hints = []key.Hint{{Key: "↑↓", Desc: "scroll"}, {Key: "[ ]", Desc: "sections"}, {Key: "esc", Desc: "back"}, {Key: "ctrl+p", Desc: "commands"}, {Key: "q", Desc: "quit"}}
	}
	if m.editing {
		hints = []key.Hint{{Key: "enter", Desc: "run query"}, {Key: "esc", Desc: "cancel"}}
	}
	if m.dialogs.Active() {
		hints = []key.Hint{{Key: "esc", Desc: "back to issues"}}
	}
	if m.settings != nil && m.settings.preview {
		hints = []key.Hint{{Key: "esc", Desc: "back to settings"}}
	}
	prefix := ""
	renderer := key.Renderer{Prefix: &prefix, Gap: dim(" · ")}
	if cli.StyleEnabled() {
		renderer.Styles = key.Styles{Key: *cli.ResolvedTheme().Blue, Text: *cli.ResolvedTheme().Dim}
	}
	return renderer.Render(hints)
}

// View is pure: data loading, row rebuilding and ADF rendering happen on
// accepted updates, never during repeated terminal repaints.
func (m *Model) View() tea.View {
	if m.width == 0 || m.height == 0 {
		return tea.NewView("")
	}
	if m.width < 40 || m.height < 12 {
		v := tea.NewView(frame("Terminal too small. Resize to at least 40 × 12.\nPress q to quit.", m.width, m.height))
		v.AltScreen = true
		return v
	}
	identity := cli.Text(m.site)
	if m.options.Project != "" {
		identity += " · " + cli.Text(m.options.Project)
	}
	brand := accent("JIRA") + "  " + cli.MarkupStrong("Issues")
	gap := max(2, m.pageWidth()-xansi.WcWidth.StringWidth(brand)-xansi.WcWidth.StringWidth(identity))
	header := brand + strings.Repeat(" ", gap) + dim(identity)
	query := dim("JQL  " + cli.Text(m.query))
	if m.editing {
		query = m.input.View()
	}
	body := m.listView()
	switch {
	case m.help:
		body = m.helpView()
	case m.detailFocused:
		body = m.detailView()
	case m.split():
		left, right := strings.Split(body, "\n"), strings.Split(m.detailView(), "\n")
		for i := range left {
			left[i] += dim(" │ ") + right[i]
		}
		body = strings.Join(left, "\n")
	}
	status := dim("Ready")
	if m.notice != "" {
		status = dim(m.notice)
	}
	if m.loading {
		status = dim("Loading issues…")
	}
	if m.err != nil {
		status = m.errorText(m.err)
		if cli.StyleEnabled() {
			status = cli.ResolvedTheme().Red.Render(status)
		}
	}
	if len(m.issues) >= command.MaxSearchIssues && !m.isLast {
		status = dim("Loaded 5,000 issues. Narrow the JQL to continue.")
	}
	if m.settings != nil && m.settings.preview {
		status = dim("Previewing display settings · changes are saved only with Ctrl+S in Settings")
	}
	content := strings.Join([]string{header, m.tabs.View(), query, "", body, status, "", m.hints()}, "\n")
	content = frame(content, m.pageWidth(), m.height)
	if m.settings != nil && !m.settings.preview {
		content = m.settingsView()
	}
	if m.dialogs.Active() {
		content = m.dialogs.View(content, m.pageWidth(), m.height)
	}
	lines := strings.Split(content, "\n")
	for i := range lines {
		lines[i] = "  " + lines[i] + "  "
	}
	v := tea.NewView(strings.Join(lines, "\n"))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}
