package issues

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xstrings "github.com/gechr/x/strings"

	"github.com/matcra587/jira-cli/internal/tui/components/form"
	"github.com/matcra587/jira-cli/internal/tui/components/input"
	"github.com/matcra587/jira-cli/internal/tui/core"
)

// ID is the section identifier.
const ID core.SectionID = "issues"

// Lens is a saved quick-filter: a named JQL the user can toggle to slice the
// queue. Config ([[tui.lenses]]) can replace the built-in set.
type Lens = core.Lens

// Lenses are the built-in quick filters, ordered. "Mine" is the triage default:
// my issues that are not done, most-recently-updated first — the working queue.
func Lenses() []Lens {
	return []Lens{
		{Name: "Mine", JQL: "assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC"},
		{Name: "Updated", JQL: "assignee = currentUser() AND updated >= -7d ORDER BY updated DESC"},
		{Name: "Reported", JQL: "reporter = currentUser() AND statusCategory != Done ORDER BY updated DESC"},
	}
}

// DefaultLens returns the triage landing lens.
func DefaultLens() Lens { return Lenses()[0] }

// lensesFor returns the configured lens set, or the built-ins when none are
// configured. Shared by the issues chips and the search section's presets.
func lensesFor(ctx *core.ProgramContext) []Lens {
	if len(ctx.Lenses) > 0 {
		return ctx.Lenses
	}
	return Lenses()
}

// defaultLensIndex resolves tui.default_lens (by title, case-insensitive)
// against a lens set; absent or unmatched lands on the first lens. The set
// must be non-empty — every caller goes through lensesFor, whose built-in
// fallback guarantees that; index 0 of an empty slice would panic downstream.
func defaultLensIndex(lenses []Lens, name string) int {
	for i, l := range lenses {
		if strings.EqualFold(l.Name, name) {
			return i
		}
	}
	return 0
}

// QueryID returns the section ID for the i-th configured query section. The
// index keeps IDs stable and collision-free regardless of user titles.
func QueryID(i int) core.SectionID { return core.SectionID(fmt.Sprintf("jql:%d", i)) }

var _ core.Section = (*QueryModel)(nil)

// QueryModel is a config-defined section: the shared
// results view bound to one saved JQL query from tui.sections. It has no query
// editing — Search is for that — so the whole surface is the shared results
// behavior plus a fixed fetch.
type QueryModel struct {
	results
	title string
	jql   string
}

// NewQuery returns a registry factory for a configured query section.
func NewQuery(id core.SectionID, title, jql string) func(*core.ProgramContext) core.Section {
	return func(ctx *core.ProgramContext) core.Section {
		m := &QueryModel{results: newResults(ctx, id), title: title, jql: jql}
		m.refetch = m.fetch
		return m
	}
}

// ID returns the section's configured identifier.
func (m *QueryModel) ID() core.SectionID { return m.id }

// Title returns the tab-bar label.
func (m *QueryModel) Title() string { return m.title }

// Init sizes the list (one row reserved for the JQL header) and runs the query.
func (m *QueryModel) Init(ctx *core.ProgramContext) tea.Cmd {
	m.ctx = ctx
	m.refetch = m.fetch
	m.applySize(1)
	return m.fetch()
}

func (m *QueryModel) fetch() tea.Cmd { return m.runFetch(m.jql) }

// Update delegates shared behavior to results and handles refresh.
func (m *QueryModel) Update(msg tea.Msg) (core.Section, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.applySize(1)
		return m, nil
	case core.TaskFinishedMsg:
		cmd, _ := m.handleTask(msg)
		return m, cmd
	case core.RestyleMsg:
		m.restyle()
		return m, nil
	case core.RefreshTickMsg:
		return m, m.autoRefresh()
	case tea.MouseWheelMsg:
		return m, m.handleWheel(msg)
	case tea.MouseClickMsg:
		return m, m.handleClick(msg)
	case input.EditorFinishedMsg:
		return m, m.handleEditor(msg)
	case form.SuggestionsMsg:
		// Autocomplete fetches resolve as commands; their results must find
		// their way back into the open form dialog or the seam silently drops them.
		cmd, _, _ := m.dialogs.Update(msg)
		return m, cmd
	case formSubmitMsg:
		return m, m.handleFormSubmit(msg)
	case formProjectChangedMsg:
		// The create form's project pill moved: refetch that project's issue
		// types and swap them into the open form.
		return m, m.loadIssueTypes(msg.project, true)
	case spinner.TickMsg:
		return m, m.handleSpinner(msg)
	case flashClearMsg:
		m.handleFlashClear(msg)
		return m, nil
	case tea.PasteMsg:
		cmd, _ := m.handlePaste(msg)
		return m, cmd
	case tea.KeyPressMsg:
		if cmd, handled := m.handleKey(msg); handled {
			return m, cmd
		}
		if key.Matches(msg, m.ctx.Keys.Refresh) {
			return m, m.fetch()
		}
	}
	return m, nil
}

// View renders the section's JQL as a faint one-line header above the results,
// so the running query is always visible.
func (m *QueryModel) View() string {
	w := max(m.ctx.MainWidth-1, 1)
	return m.view(lipgloss.NewStyle().Faint(true).Render(xstrings.Truncate(m.jql, w, "…")))
}

// CapturesInput reports filter/action focus.
func (m *QueryModel) CapturesInput() bool { return m.capturing() }

// HelpBindings lists the section's contextual bindings — the triage verbs and
// shared list controls, with no lens or query-edit keys.
func (m *QueryModel) HelpBindings() []key.Binding {
	k := m.ctx.Keys
	return []key.Binding{
		k.Open, k.Create, k.Transition, k.AssignMe, k.Comment, k.Edit, k.Labels, k.OpenBrowse,
		k.Select, k.SelectAll, k.SelectInvert, k.SelectRange,
		k.Filter, k.Facet, k.Jumplist, k.TogglePreview, k.Refresh,
	}
}
