// Package dashboard owns the issue home screen. Widgets come from Primer;
// Jira reads and public configuration come from the ordinary CLI owners.
package dashboard

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/carousel"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/list"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

const myWorkQuery = "assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC"

const (
	listScope   task.Scope = "issues"
	detailScope task.Scope = "detail"
	pageSize               = 50
)

// Client is the read-only capability needed by this screen.
type Client interface {
	command.IssueSearcher
	GetIssue(context.Context, string, []string, ...string) (core.IssueRead, error)
	BaseURL() string
}

// Options carries resolved public settings; it never selects a profile itself.
type Options struct {
	Project, ProjectQuery, Query string
	Theme                        string
	Settings                     map[string]bool
	SettingSources               map[string]string
	SaveSettings                 func(map[string]string) error
	BindContext                  func(context.Context)
	LoadViews                    func() ([]SavedView, error)
	SaveView                     func(string, string) error
	Actions                      Actions
}

type detailResult struct {
	key   string
	issue *jira.IssueBean
}

type detailDue struct{ key string }

// Model owns UI state on Tea's update loop. Fetch closures capture the shared
// session context, so screen shutdown cancels admitted HTTP reads.
type Model struct {
	options                      Options
	preferences                  map[string]bool
	themeName                    string
	interaction                  uint64
	settings                     *settingsPage
	site                         string
	tabs                         *carousel.Model
	rows                         *list.Model
	rowHeader                    string
	paintedRows                  []string
	dialogs                      *dialog.Stack
	tasks                        *task.Manager
	input                        textinput.Model
	preview                      viewport.Model
	search                       func(string, string) (envelope.IssueListOutput, error)
	detail                       func(string) (detailResult, error)
	issues                       []envelope.IssueView
	progress                     *command.SearchProgress
	next                         string
	isLast, truncated            bool
	query                        string
	loading, loadingMore         bool
	detailLoading                bool
	detailIssue                  *jira.IssueBean
	detailKey                    string
	err, detailErr               error
	width, height                int
	editing, detailFocused, help bool
	customQuery                  string
	recent                       []recentIssue
	views                        []SavedView
	viewName, notice             string
	drafts                       map[string]string
	previewFocused, actionBusy   bool
	detailSection                int
	commentsKey                  string
	comments                     []jira.Comment
	commentsTotal, commentsNext  int
	commentsLoading              bool
	commentsErr                  error
	standalone                   bool
}

// New creates the main page without performing I/O. Init starts its first read.
func New(ctx context.Context, client Client, options Options) *Model {
	if options.BindContext != nil {
		options.BindContext(ctx)
	}
	labels := []string{"My work"}
	if options.ProjectQuery != "" {
		labels = append(labels, "Project")
	}
	labels = append(labels, "Search")
	m := &Model{
		options: options, themeName: options.Theme, site: client.BaseURL(), tabs: carousel.New(labels...),
		drafts: make(map[string]string), rows: list.New(), tasks: task.New(), preview: viewport.New(),
		query: myWorkQuery, customQuery: options.Query,
		search: func(query, cursor string) (envelope.IssueListOutput, error) {
			return command.WalkIssues(ctx, client, query,
				[]string{"summary", "status", "issuetype", "priority", "assignee", "updated"},
				cursor, command.SearchWalk{Limit: pageSize})
		},
		detail: func(key string) (detailResult, error) {
			read, err := client.GetIssue(ctx, key, []string{"summary", "status", "issuetype", "priority", "assignee", "reporter", "updated", "labels", "description", "issuelinks"}, "changelog")
			if err == nil && (read.Issue == nil || !read.Issue.Key.Set || read.Issue.Key.Value != key) {
				err = fmt.Errorf("issue response did not identify the selected issue")
			}
			return detailResult{key: key, issue: read.Issue}, err
		},
	}
	m.preferences = make(map[string]bool)
	for _, s := range dashboardSettings {
		if s.key != "theme" {
			m.preferences[s.key] = true
		}
	}
	maps.Copy(m.preferences, options.Settings)
	m.input = textinput.New()
	m.input.Prompt = "JQL > "
	m.input.Placeholder = "Enter a Jira query"
	m.input.CharLimit = 8192
	if options.Query != "" {
		m.tabs.SetActive(len(labels) - 1)
		m.query = options.Query
	}
	m.applyStyles()
	m.dialogs = newCommandStack()
	return m
}

// Init starts the bounded first page. Loading is set before the command runs.
func (m *Model) Init() tea.Cmd { return m.load(false) }

func (m *Model) load(more bool) tea.Cmd {
	if m.loading {
		return nil
	}
	if more && (m.isLast || m.truncated || m.next == "" || len(m.issues) >= command.MaxSearchIssues) {
		return nil
	}
	m.loading, m.loadingMore, m.err = true, more, nil
	cursor := ""
	if more {
		cursor = m.next
	}
	query := m.query
	return m.tasks.Start(task.Spec{Scope: listScope, Run: func() (any, error) { return m.search(query, cursor) }})
}

func (m *Model) selected() *envelope.IssueView {
	i, ok := m.rows.Selected()
	if !ok || i >= len(m.issues) {
		return nil
	}
	return &m.issues[i]
}

func (m *Model) selectedKey() string {
	if issue := m.selected(); issue != nil {
		return issue.Key
	}
	return ""
}

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

func (m *Model) switchView() tea.Cmd {
	switch m.tabs.ActiveItem() {
	case "My work":
		m.query = myWorkQuery
	case "Project":
		m.query = m.options.ProjectQuery
	case "Search":
		if m.customQuery == "" {
			return m.editQuery()
		}
		m.query = m.customQuery
	}
	return m.newQuery()
}

func (m *Model) newQuery() tea.Cmd {
	// A new generation supersedes the old query even while its HTTP read runs.
	m.loading = false
	m.issues, m.next, m.err = nil, "", nil
	m.detailKey, m.detailIssue, m.detailErr = "", nil, nil
	m.standalone = false
	m.detailLoading, m.detailFocused = false, false
	m.isLast, m.truncated = false, false
	m.progress = command.NewSearchProgress("", false)
	m.rebuildRows()
	m.syncPreview()
	return m.load(false)
}

func (m *Model) editQuery() tea.Cmd {
	m.editing = true
	m.input.SetValue(m.query)
	m.input.CursorEnd()
	return m.input.Focus()
}

// Update routes input to one focus owner and commits only current fetches.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(0, msg.Width), max(0, msg.Height)
		m.resize()
		m.resizeSettings()
		return m, nil
	case detailDue:
		if msg.key != m.detailKey || msg.key == "" {
			return m, nil
		}
		return m, m.tasks.Start(task.Spec{Scope: detailScope, Run: func() (any, error) { return m.detail(msg.key) }})
	case task.FinishedMsg:
		if !m.tasks.Accept(msg.Scope, msg.Gen) {
			return m, nil
		}
		switch msg.Scope {
		case "comments":
			m.acceptComments(msg)
		case "views", "save-view":
			return m, m.navigationResult(msg)
		case "action-choices", "write":
			return m, m.actionFinished(msg)
		case settingsScope:
			m.acceptSettings(msg)
		case listScope:
			return m, m.acceptList(msg)
		case detailScope:
			result, ok := msg.Result.(detailResult)
			if !ok || result.key != m.detailKey {
				return m, nil
			}
			m.detailLoading, m.detailErr = false, msg.Err
			if msg.Err == nil {
				m.detailIssue = result.issue
				m.rememberIssue(result.key, result.issue.Fields.Value.Summary.Value)
			}
			m.syncPreview()
		}
		return m, nil
	case tea.KeyPressMsg:
		m.interaction++
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if msg.String() == "q" && m.actionBusy && !m.dialogs.Active() {
			m.notice = "A Jira action is running; wait for its outcome before quitting."
			return m, nil
		}
		if m.dialogs.Active() {
			return m, m.updateCommands(msg)
		}
		if m.settings != nil {
			return m, m.updateSettings(msg)
		}
		if m.editing {
			return m, m.updateQuery(msg)
		}
		if m.help {
			m.help = false
			return m, nil
		}
		return m, m.updateKey(msg)
	case tea.PasteMsg:
		if m.dialogs.Active() {
			return m, m.updateCommands(msg)
		}
		if m.settings != nil {
			return m, m.updateSettings(msg)
		}
		if m.editing {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
	case tea.MouseWheelMsg:
		if m.settings != nil {
			return m, m.updateSettings(msg)
		}
		if m.dialogs.Active() {
			switch msg.Button {
			case tea.MouseWheelUp:
				m.dialogs.ScrollBy(-3)
			case tea.MouseWheelDown:
				m.dialogs.ScrollBy(3)
			}
			cmd, _, _ := m.dialogs.Update(msg)
			return m, cmd
		}
		if m.detailFocused || m.previewFocused || (m.split() && msg.X > m.listWidth()) {
			var cmd tea.Cmd
			m.preview, cmd = m.preview.Update(msg)
			return m, cmd
		}
		before := m.selectedKey()
		switch msg.Button {
		case tea.MouseWheelUp:
			m.rows.MoveUp(3)
		case tea.MouseWheelDown:
			m.rows.MoveDown(3)
		}
		if before != m.selectedKey() {
			return m, m.selectDetail()
		}
	}
	return m, nil
}

func (m *Model) acceptList(msg task.FinishedMsg) tea.Cmd {
	more := m.loadingMore
	m.loading, m.loadingMore, m.err = false, false, msg.Err
	if msg.Err != nil {
		return nil
	}
	page, ok := msg.Result.(envelope.IssueListOutput)
	if !ok {
		m.err = fmt.Errorf("unexpected dashboard search result")
		return nil
	}
	keep := m.selectedKey()
	if !more {
		m.issues = nil
		m.progress = command.NewSearchProgress("", false)
	}
	seen := make(map[string]bool, len(m.issues))
	for _, issue := range m.issues {
		seen[issue.Key] = true
	}
	for _, issue := range page.Issues {
		if seen[issue.Key] {
			m.err = fmt.Errorf("search repeated an issue; refresh to restart")
			m.truncated = true
			return nil
		}
		seen[issue.Key] = true
	}
	m.issues = append(m.issues, page.Issues...)
	m.next, m.isLast, m.truncated = page.Pagination.NextPageToken, page.Pagination.IsLast, page.Truncated
	if m.truncated {
		m.err = fmt.Errorf("search incomplete (%s); refresh to restart", page.TruncatedReason)
	}
	if !m.isLast && !m.truncated {
		if reason := m.progress.Advance(m.next, len(page.Issues)); reason != "" {
			m.truncated = true
			m.err = fmt.Errorf("search incomplete (%s); refresh to restart", reason)
		}
	}
	m.rebuildRows()
	for i, issue := range m.issues {
		if issue.Key == keep {
			m.rows.SetCursor(i)
			break
		}
	}
	if m.standalone && m.detailFocused {
		return nil
	}
	// An explicit refresh fetches the selected detail again, even if its key stayed.
	if !more {
		m.detailKey = ""
	}
	return m.selectDetail()
}

func (m *Model) updateQuery(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.editing = false
		m.input.Blur()
		return nil
	case "enter":
		query := strings.TrimSpace(m.input.Value())
		if query == "" {
			return nil
		}
		m.query, m.customQuery, m.editing = query, query, false
		m.viewName = ""
		m.input.Blur()
		m.tabs.SetActive(len(m.tabs.Items()) - 1)
		return m.newQuery()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

func (m *Model) updateKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	switch key {
	case "[":
		return m.changeDetailSection(-1)
	case "]":
		return m.changeDetailSection(1)
	case "o":
		m.openLinkedIssues()
		return nil
	case "ctrl+o":
		m.openIssues()
		return nil
	case "v":
		return m.openViews()
	case "ctrl+s":
		m.text("Save current view", "save-view", m.query, "Choose a new saved-search name. Existing files will not be replaced.")
		return nil
	case "t":
		return m.openTransitions()
	case "a":
		m.openAssign()
		return nil
	case "c":
		m.openComment()
		return nil
	case "ctrl+w":
		if m.split() {
			m.previewFocused = !m.previewFocused
		} else {
			m.detailFocused = !m.detailFocused
			m.resize()
			if !m.detailFocused && m.standalone {
				m.standalone = false
				return m.selectDetail()
			}
		}
		return nil
	case "q":
		return tea.Quit
	case ",":
		m.openSettings()
		return nil
	case "?":
		m.help = true
		return nil
	case "ctrl+p", "ctrl+k":
		m.openCommands()
		return nil
	case "1":
		m.tabs.SetActive(0)
		return m.switchView()
	case "2":
		if m.options.ProjectQuery != "" {
			m.tabs.SetActive(1)
			return m.switchView()
		}
		return m.editQuery()
	case "/":
		return m.editQuery()
	case "tab", "shift+tab":
		if key == "tab" {
			m.tabs.Next()
		} else {
			m.tabs.Prev()
		}
		return m.switchView()
	case "r":
		if m.detailSection == 1 && (m.detailFocused || m.previewFocused) {
			return m.loadComments(false)
		}
		if m.loading {
			return nil
		}
		m.detailErr = nil
		return m.load(false)
	case "m":
		if m.detailSection == 1 && (m.detailFocused || m.previewFocused) {
			return m.loadComments(true)
		}
		return m.load(true)
	case "enter", "l":
		if m.selected() != nil {
			m.detailFocused = true
			m.resize()
		}
		return nil
	case "esc", "h":
		m.detailFocused, m.previewFocused = false, false
		m.resize()
		if m.standalone {
			m.standalone = false
			return m.selectDetail()
		}
		return nil
	}
	if m.detailFocused || m.previewFocused {
		var cmd tea.Cmd
		m.preview, cmd = m.preview.Update(msg)
		return cmd
	}
	before := m.selectedKey()
	switch key {
	case "j", "down":
		m.rows.MoveDown(1)
	case "k", "up":
		m.rows.MoveUp(1)
	case "pgdown", "ctrl+d":
		m.rows.PageDown()
	case "pgup", "ctrl+u":
		m.rows.PageUp()
	case "g", "home":
		m.rows.Top()
	case "G", "end":
		m.rows.Bottom()
	}
	if before != m.selectedKey() {
		return m.selectDetail()
	}
	return nil
}

func (m *Model) errorText(err error) string {
	if err == nil {
		return ""
	}
	classified := cli.MapError(err)
	return cli.Text(classified.Message)
}
