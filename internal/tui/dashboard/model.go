package dashboard

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/carousel"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/input"
	"github.com/gechr/primer/list"
	"github.com/gechr/primer/task"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/config"
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
	ThemeLabel, ThemeNotice      string
	Settings                     map[string]bool
	SettingSources               map[string]string
	SettingValues                map[string]string
	RuntimeScope                 core.JiraScope
	StopRuntime                  func()
	ApplyRuntime                 func()
	ReloadSettings               func() (*Model, error)
	SaveSettings                 func(map[string]string) error
	BindContext                  func(context.Context)
	LoadViews                    func() ([]SavedView, error)
	SaveView                     func(string, string) error
	LoadThemes                   func(string) ([]*config.SavedTheme, error)
	ReadTheme                    func(string) (*config.ThemeFile, error)
	WriteTheme                   func(string, *config.ThemeFile, *config.SavedTheme) (*config.SavedTheme, bool, error)
	ExportTheme                  func(string, *config.ThemeFile) (bool, error)
	Actions                      Actions
}

// Model owns UI state on Tea's update loop. Fetch closures capture the shared
// session context, so screen shutdown cancels admitted HTTP reads.
type Model struct {
	refresh                      refreshState
	clock                        refreshClock
	hierarchy                    hierarchyWorkspace
	hierarchyRead                func(core.IssueHierarchyReference) (core.IssueHierarchyRead, error)
	hierarchySearch              func(string, string, int) (envelope.IssueListOutput, error)
	autoPageCursor               string
	descriptionDrafts            map[descriptionIdentity]*descriptionDraft
	descriptionReadRequest       uint64
	creation                     *creationDraft
	creationEditor               *creationEditor
	options                      Options
	preferences                  map[string]bool
	themeName                    string
	reloadFailure                string
	localSaveBusy                bool
	workspaces                   map[core.JiraScope]runtimeWorkspace
	interaction                  uint64
	bulkNoticeRequest            uint64
	editReadRequest              uint64
	fieldDrafts                  map[string]string
	labelDrafts                  map[string]labelDraft
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
	search                       func(string, string, int) (envelope.IssueListOutput, error)
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
	viewName, notice             string
	drafts                       map[string]string
	previewFocused, actionBusy   bool
	listZoom                     bool
	detailSection                int
	commentsKey                  string
	comments                     []jira.Comment
	commentsTotal, commentsNext  int
	commentsLoading              bool
	commentsErr                  error
	standalone                   bool
	visible                      []int
	localFilter                  string
	facet                        issueFacet
	filtering                    bool
	filterInput                  input.Line
	marks                        map[string]bool
	markAnchor                   string
	operations                   []operation
	nextOperation                uint64
	lastBatch                    *bulkResult
	bulkDraft                    string
	bulkDraftTargets             map[string]bool
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
	labels = append(labels, "Search", hierarchyTab)
	m := &Model{
		refresh: refreshState{markers: make(map[string]rowChange)}, clock: newRefreshClock(ctx),
		options: options, themeName: options.Theme, site: client.BaseURL(), tabs: carousel.New(labels...),
		drafts: make(map[string]string), rows: list.New(), tasks: task.New(), preview: viewport.New(),
		query: myWorkQuery, customQuery: options.Query,
		search:          dashboardSearcher(ctx, client, false),
		hierarchySearch: dashboardSearcher(ctx, client, true),
		hierarchyRead: func(reference core.IssueHierarchyReference) (core.IssueHierarchyRead, error) {
			return core.ReadIssueHierarchy(ctx, client, reference)
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
	m.fieldDrafts = make(map[string]string)
	m.descriptionDrafts = make(map[descriptionIdentity]*descriptionDraft)
	m.labelDrafts = make(map[string]labelDraft)
	defaults := config.Defaults()
	for _, s := range dashboardSettings {
		if value, ok := defaults[s.key].(bool); ok {
			m.preferences[s.key] = value
		}
	}
	maps.Copy(m.preferences, options.Settings)
	m.input = textinput.New()
	m.input.Prompt = "JQL > "
	m.input.Placeholder = "Enter a Jira query"
	m.input.CharLimit = 8192
	if options.Query != "" {
		m.tabs.SetActive(slices.Index(labels, "Search"))
		m.query = options.Query
	}
	m.applyStyles()
	m.dialogs = newCommandStack()
	return m
}

// Init starts the bounded first page. Loading is set before the command runs.
func (m *Model) Init() tea.Cmd { return m.load(false) }

func (m *Model) errorText(err error) string {
	if err == nil {
		return ""
	}
	classified := cli.MapError(err)
	return cli.Text(classified.Message)
}
