// The epics section: a carousel of open epics over the shared
// results engine, which lists the active epic's children. Cycling the
// carousel swaps the child query; everything below the strip is the same
// triage surface as every other list.

package issues

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	termansi "github.com/gechr/x/ansi"
	xslices "github.com/gechr/x/slices"
	xstrings "github.com/gechr/x/strings"

	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/tui/components/carousel"
	"github.com/matcra587/jira-cli/internal/tui/components/form"
	"github.com/matcra587/jira-cli/internal/tui/components/input"
	"github.com/matcra587/jira-cli/internal/tui/core"
	"github.com/matcra587/jira-cli/internal/tui/theme"
)

// EpicsID is the epics section's identifier — already named by the default
// tui.tabs, so registering the factory lights the tab up.
const EpicsID core.SectionID = "epics"

// BoardID is the default-board query tab's identifier; users opt in by
// naming "board" in tui.tabs. The wiring layer registers it only when the
// profile's default board resolves from the boards cache.
const BoardID core.SectionID = "board"

// epicFetchLimit bounds the carousel: a strip only reads well short, and an
// epic-heavy tenant still gets the most recently active ones.
const epicFetchLimit = 15

var _ core.Section = (*EpicsModel)(nil)

// EpicsModel is the epics section. The carousel is the query source: its
// active epic's children fill the embedded results list.
type EpicsModel struct {
	results
	strip       *carousel.Model
	epics       []*jira.Issue
	epicsLoaded bool
}

// NewEpics builds the epics section.
func NewEpics(ctx *core.ProgramContext) core.Section {
	m := &EpicsModel{results: newResults(ctx, EpicsID), strip: carousel.New()}
	m.strip.ActiveStyle = ctx.Styles.TabActive
	m.strip.InactiveStyle = ctx.Styles.TabInactive
	m.refetch = m.fetchChildren
	return m
}

// ID returns the section identifier.
func (m *EpicsModel) ID() core.SectionID { return EpicsID }

// Title returns the tab-bar label.
func (m *EpicsModel) Title() string { return "Epics" }

// Count overrides the embedded results.Count for the tab bar: the Epics tab
// counts epics, not the active epic's children. Without this the section would
// inherit results.Count (len(all) — the loaded child page of the selected
// epic), so "Epics (N)" showed a child total that shifted per selection and
// read like a stray issue number. ok stays false until the strip has loaded.
func (m *EpicsModel) Count() (int, bool) { return len(m.epics), m.epicsLoaded }

// epicsScope tracks the epic-list fetch, separate from the child fetch so a
// strip reload can never clobber a child page (or vice versa).
func (m *EpicsModel) epicsScope() core.TaskScope { return core.TaskScope(string(EpicsID) + ".epics") }

// Init sizes the list (two header rows: the strip and the active-epic line)
// and loads the epics.
func (m *EpicsModel) Init(ctx *core.ProgramContext) tea.Cmd {
	m.ctx = ctx
	m.refetch = m.fetchChildren
	m.applySize(2)
	return m.fetchEpics()
}

// epicsJQL lists open epics, project-scoped when the profile names one, most
// recently updated first so the strip leads with what is moving.
func (m *EpicsModel) epicsJQL() string {
	jql := "issuetype = Epic AND statusCategory != Done"
	if m.ctx.Project != "" {
		jql = "project = " + m.ctx.Project + " AND " + jql
	}
	return jql + " ORDER BY updated DESC"
}

func (m *EpicsModel) fetchEpics() tea.Cmd {
	m.loading = true
	base := m.ctx.Base
	svc := m.ctx.Services
	jql := m.epicsJQL()
	return tea.Batch(m.spin.Tick, m.ctx.StartTask(core.TaskSpec{
		Scope: m.epicsScope(),
		Run: func() (any, error) {
			if svc == nil {
				return epicsResult{}, nil
			}
			issues, _, err := jira.ListIssuesPage(base, svc.Issues(), &jira.IssueListOptions{
				JQL:        jql,
				Fields:     fetchFields,
				MaxResults: epicFetchLimit,
			}, jira.PageCursor{})
			if err != nil {
				return nil, err
			}
			return epicsResult{epics: issues}, nil
		},
	}))
}

type epicsResult struct {
	epics []*jira.Issue
}

// applyEpics installs the fetched epics, keeping the active selection on the
// same epic key across a refresh so a background reload never yanks the view.
func (m *EpicsModel) applyEpics(res epicsResult) tea.Cmd {
	prev := m.activeEpicKey()
	m.epics = res.epics
	m.epicsLoaded = true
	m.strip.SetItems(xslices.Map(res.epics, issueKey))
	if prev != "" {
		for i, e := range res.epics {
			if issueKey(e) == prev {
				m.strip.SetActive(i)
				break
			}
		}
	}
	if len(res.epics) == 0 {
		m.loading = false
		m.all = nil
		m.applyFilter()
		return nil
	}
	return m.fetchChildren()
}

// activeEpicKey returns the selected epic's key, or "" while none is loaded.
func (m *EpicsModel) activeEpicKey() string {
	i := m.strip.Active()
	if i < 0 || i >= len(m.epics) {
		return ""
	}
	return issueKey(m.epics[i])
}

// fetchChildren loads the active epic's children into the shared list.
func (m *EpicsModel) fetchChildren() tea.Cmd {
	epic := m.activeEpicKey()
	if epic == "" {
		return nil
	}
	return m.runFetch("parent = " + epic + " ORDER BY updated DESC")
}

// cycleEpic moves the carousel and swaps in the new epic's children.
func (m *EpicsModel) cycleEpic(delta int) tea.Cmd {
	if len(m.epics) == 0 {
		return nil
	}
	if delta > 0 {
		m.strip.Next()
	} else {
		m.strip.Prev()
	}
	return m.fetchChildren()
}

// Update handles the epic strip and refresh, then delegates the shared
// results behavior.
func (m *EpicsModel) Update(msg tea.Msg) (core.Section, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.applySize(2)
		return m, nil
	case core.TaskFinishedMsg:
		if msg.Scope == m.epicsScope() {
			if msg.Err != nil {
				m.loading = false
				m.err = msg.Err
				return m, nil
			}
			if res, ok := msg.Result.(epicsResult); ok {
				m.err = nil
				return m, m.applyEpics(res)
			}
			return m, nil
		}
		cmd, _ := m.handleTask(msg)
		return m, cmd
	case core.RestyleMsg:
		// The carousel captured its styles at construction; a theme preview
		// rebuilds ctx.Styles, so re-point them or the strip keeps the old
		// theme until the commit-path section rebuild.
		m.strip.ActiveStyle = m.ctx.Styles.TabActive
		m.strip.InactiveStyle = m.ctx.Styles.TabInactive
		m.restyle()
		return m, nil
	case core.RefreshTickMsg:
		// One fetch refreshes both layers: applyEpics chains the child fetch
		// once the strip lands, so fetching children here too would run the
		// same JQL twice per heartbeat. The gate matches autoRefresh's in
		// full — skipping while the user is mid-input, a fetch or page is in
		// flight, or a write is still reconciling — so a background tick can
		// never supersede a page fetch or stomp optimistic row state.
		if m.capturing() || m.loading || m.loadingMore || !m.canMutate() {
			return m, nil
		}
		return m, m.fetchEpics()
	case tea.MouseWheelMsg:
		return m, m.handleWheel(msg)
	case tea.MouseClickMsg:
		return m, m.handleClick(msg)
	case input.EditorFinishedMsg:
		return m, m.handleEditor(msg)
	case form.SuggestionsMsg:
		cmd, _, _ := m.dialogs.Update(msg)
		return m, cmd
	case formSubmitMsg:
		return m, m.handleFormSubmit(msg)
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
		switch {
		case key.Matches(msg, m.ctx.Keys.Refresh):
			return m, m.fetchEpics() // children follow via applyEpics
		case key.Matches(msg, m.ctx.Keys.NextLens):
			return m, m.cycleEpic(1)
		case key.Matches(msg, m.ctx.Keys.PrevLens):
			return m, m.cycleEpic(-1)
		}
	}
	return m, nil
}

// View renders the strip, the active epic's summary line, and the shared list.
func (m *EpicsModel) View() string {
	return m.view(m.header())
}

// header is the two epic rows above the list: the key strip and a faint line
// naming the active epic.
func (m *EpicsModel) header() string {
	w := max(m.ctx.MainWidth-1, 1)
	if !m.epicsLoaded {
		return theme.DetailDim.Render("loading epics…") + "\n"
	}
	if len(m.epics) == 0 {
		scope := "anywhere you can see"
		if m.ctx.Project != "" {
			scope = m.ctx.Project
		}
		return theme.DetailDim.Render(xstrings.Truncate("no open epics in "+scope, w, "…")) + "\n"
	}
	// The strip is styled by the carousel, so truncation must be ANSI-aware.
	strip := termansi.Truncate(m.strip.View(), w, "…")
	active := ""
	if i := m.strip.Active(); i >= 0 && i < len(m.epics) {
		// truncCells, not rune truncation: a CJK-heavy summary would otherwise
		// wrap and push the fixed two-row header down a line.
		active = theme.CodeSpans(truncCells(issueSummary(m.epics[i]), w))
	}
	return strip + "\n" + theme.DetailDim.Render(active)
}

// CapturesInput reports filter/action focus.
func (m *EpicsModel) CapturesInput() bool { return m.capturing() }

// HelpBindings lists the epic-cycling keys ahead of the shared list verbs.
// The lens-cycle keys are reused deliberately — "cycle the query source"
// keeps one muscle memory across sections — with epic-specific help text.
func (m *EpicsModel) HelpBindings() []key.Binding {
	k := m.ctx.Keys
	next := key.NewBinding(key.WithKeys(k.NextLens.Keys()...), key.WithHelp(k.NextLens.Help().Key, "next epic"))
	prev := key.NewBinding(key.WithKeys(k.PrevLens.Keys()...), key.WithHelp(k.PrevLens.Help().Key, "prev epic"))
	return []key.Binding{
		next, prev,
		k.Open, k.Create, k.Transition, k.AssignMe, k.Comment, k.Edit, k.Labels, k.OpenBrowse,
		k.Select, k.SelectAll, k.SelectInvert, k.SelectRange,
		k.Filter, k.Facet, k.Jumplist, k.TogglePreview, k.Refresh,
	}
}
