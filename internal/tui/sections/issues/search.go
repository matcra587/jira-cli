package issues

import (
	"errors"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/tui/components/form"
	"github.com/matcra587/jira-cli/internal/tui/components/input"
	"github.com/matcra587/jira-cli/internal/tui/components/picker"
	"github.com/matcra587/jira-cli/internal/tui/core"
	"github.com/matcra587/jira-cli/internal/tui/theme"
)

// searchBoxRows is the height of the JQL box (rounded border top+bottom plus the
// query line); applySize reserves it so the results list is sized correctly.
const searchBoxRows = 3

var _ core.Section = (*SearchModel)(nil)

// SearchID is the search section identifier.
const SearchID core.SectionID = "search"

// SearchModel is the JQL explore section: the same results view as Issues, but
// its query source is a free-form JQL input plus saved queries. It never
// auto-runs a partial query — the user commits with enter.
type SearchModel struct {
	results
	jql      string     // last committed query
	jqlInput input.Line // in-progress edit
	editing  bool
	savedIdx int // last ]-cycled position in the saved queries

	// JQL autocomplete state: the instance's reference data (fields,
	// operators, functions) fetched once on first edit, plus the live value
	// suggestions for the field currently being valued. sugField/sugPrefix
	// record the last fetch fired so identical contexts don't refire.
	jqlRef       jira.JQLReference
	jqlRefLoaded bool
	valueSugs    []string
	valueSugsFor string // field the cached values belong to
	sugField     string
	sugPrefix    string
}

// jqlRefScope/jqlSuggScope are the async scopes for the autocomplete fetches;
// the suggestions scope supersedes per keystroke via the task generation.
func (s *SearchModel) jqlRefScope() core.TaskScope {
	return core.TaskScope(string(s.id) + ".jqlref")
}

func (s *SearchModel) jqlSuggScope() core.TaskScope {
	return core.TaskScope(string(s.id) + ".jqlsugg")
}

// jqlRefResult and jqlSuggResult are delivered as TaskFinishedMsg.Result.
type (
	jqlRefResult  struct{ ref jira.JQLReference }
	jqlSuggResult struct {
		field, prefix string
		values        []string
	}
)

// NewSearch builds the search section. It opens in edit mode with no results,
// since there is no default query to run.
func NewSearch(ctx *core.ProgramContext) core.Section {
	s := &SearchModel{results: newResults(ctx, SearchID)}
	s.refetch = s.fetch
	return s
}

// saved returns the quick queries — the lens set, configured lenses when
// present, built-ins otherwise. Read per call so a config hot-reload
// refreshes the ctrl+p dropdown, ] cycling, and the editor's completions.
func (s *SearchModel) saved() []Lens { return lensesFor(s.ctx) }

// ID returns the search section's identifier.
func (s *SearchModel) ID() core.SectionID { return SearchID }

// Title returns the tab-bar label.
func (s *SearchModel) Title() string { return "Search" }

// Init sizes the list and lands in browse mode (not editing), so the section
// can be tabbed away from immediately. The user presses enter (or the search
// key) to start writing a JQL query. It runs nothing until a query is submitted.
func (s *SearchModel) Init(ctx *core.ProgramContext) tea.Cmd {
	s.ctx = ctx
	s.refetch = s.fetch
	s.applySize(searchBoxRows)
	return nil
}

// fetch validates the committed JQL via Jira's parser and only then runs the
// search, so an invalid query surfaces an inline error instead of a failed
// search request. Empty queries run nothing.
func (s *SearchModel) fetch() tea.Cmd {
	if s.jql == "" {
		// Clear stale results and supersede any in-flight fetch (an empty-result
		// task bumps the generation) so a late response can't repopulate.
		s.all = nil
		s.applyFilter()
		s.loading = false
		return s.ctx.StartTask(core.TaskSpec{
			Scope: s.fetchScope(),
			Run:   func() (any, error) { return fetchResult{}, nil },
		})
	}
	if s.jql != s.lastJQL {
		// A different query is a new world, not a refresh of the old one.
		s.seen, s.changed = nil, nil
	}
	s.loading = true
	s.loadingMore = false // a new query supersedes any in-flight page fetch
	s.lastJQL = s.jql     // fetch-more repeats the committed query verbatim
	jql := s.jql
	base := s.ctx.Base
	svc := s.ctx.Services
	return tea.Batch(s.spin.Tick, s.ctx.StartTask(core.TaskSpec{
		Scope: s.fetchScope(),
		Run: func() (any, error) {
			if svc == nil {
				return fetchResult{}, nil
			}
			parsed, _, err := svc.JQL().Parse(base, []string{jql}, "")
			if err != nil {
				return nil, err
			}
			if len(parsed) > 0 && len(parsed[0].Errors) > 0 {
				return nil, errors.New(parsed[0].Errors[0])
			}
			issues, next, err := jira.ListIssuesPage(base, svc.Issues(), &jira.IssueListOptions{
				JQL:        jql,
				Fields:     fetchFields,
				MaxResults: 50,
			}, jira.PageCursor{})
			if err != nil {
				return nil, err
			}
			return fetchResult{issues: issues, cursor: next}, nil
		},
	}))
}

// jqlStarters are common query openings offered as ghost-text completions in
// the JQL editor, alongside the saved queries themselves.
var jqlStarters = []string{
	"assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC",
	"assignee = currentUser() ORDER BY updated DESC",
	"project = ",
	"reporter = currentUser() ORDER BY updated DESC",
	"status = ",
	"sprint IN openSprints() ORDER BY updated DESC",
	"text ~ ",
	"updated >= -7d ORDER BY updated DESC",
}

// openEdit starts (or restarts) the JQL editor prefilled with the committed
// query, arms the suggestion machinery, and fetches the instance's JQL
// reference data on first use.
func (s *SearchModel) openEdit() tea.Cmd {
	s.editing = true
	s.jqlInput = input.NewLine("", "project = … ORDER BY updated DESC")
	s.jqlInput.SetWidth(s.ctx.MainWidth - 10)
	s.jqlInput.SetValue(s.jql)
	cmd := s.refreshSuggestions()
	if s.jqlRefLoaded {
		return cmd
	}
	return tea.Batch(cmd, s.fetchJQLRef())
}

// fallbackSuggestions are the whole-query completions available without
// reference data: the saved queries plus common JQL openings.
func (s *SearchModel) fallbackSuggestions() []string {
	saved := s.saved()
	out := make([]string, 0, len(saved)+len(jqlStarters))
	for _, l := range saved {
		out = append(out, l.JQL)
	}
	return append(out, jqlStarters...)
}

// refreshSuggestions recomputes the editor's ghost completions for the
// current input: token-aware candidates from the reference data (fields,
// the active field's operators, functions, keywords, cached live values),
// or the whole-query fallbacks while the reference data hasn't landed. At a
// value position on a suggestable field it also fires the live-values fetch,
// superseded per keystroke by the task generation.
func (s *SearchModel) refreshSuggestions() tea.Cmd {
	if !s.editing {
		return nil
	}
	q := s.jqlInput.Value()
	if q == "" || !s.jqlRefLoaded {
		// An empty editor offers the saved queries and common openings, not
		// the instance's field list — its alphabetically-first entry is
		// usually some plugin's custom field, a baffling first ghost.
		s.jqlInput.SetSuggestions(s.fallbackSuggestions())
		return nil
	}
	c := jqlComplete(q)
	var cands []string
	if c.kind == wantValue && strings.EqualFold(s.valueSugsFor, c.field) {
		// The field's live values lead — for a status they're what the user
		// means; the JQL functions follow as the generic option.
		cands = append(cands, s.valueSugs...)
	}
	cands = append(cands, candidatesFor(s.jqlRef, c)...)
	lines := completionLines(q, c, cands)
	if c.start == 0 {
		// At the very start the saved queries complete as whole lines too.
		lines = append(lines, s.fallbackSuggestions()...)
	}
	s.jqlInput.SetSuggestions(lines)
	if f := valueField(s.jqlRef, c); f != "" && (f != s.sugField || c.prefix != s.sugPrefix) {
		s.sugField, s.sugPrefix = f, c.prefix
		return s.fetchValueSuggestions(f, c.prefix)
	}
	return nil
}

// fetchJQLRef loads the instance's JQL autocomplete reference data once.
func (s *SearchModel) fetchJQLRef() tea.Cmd {
	base := s.ctx.Base
	svc := s.ctx.Services
	return s.ctx.StartTask(core.TaskSpec{
		Scope: s.jqlRefScope(),
		Run: func() (any, error) {
			if svc == nil || svc.JQL() == nil {
				return jqlRefResult{}, nil
			}
			ref, _, err := svc.JQL().AutocompleteData(base)
			if err != nil {
				return nil, err
			}
			return jqlRefResult{ref: ref}, nil
		},
	})
}

// fetchValueSuggestions loads live values for a field (e.g. status names)
// narrowed by the typed prefix.
func (s *SearchModel) fetchValueSuggestions(field, prefix string) tea.Cmd {
	base := s.ctx.Base
	svc := s.ctx.Services
	return s.ctx.StartTask(core.TaskSpec{
		Scope: s.jqlSuggScope(),
		Run: func() (any, error) {
			if svc == nil || svc.JQL() == nil {
				return jqlSuggResult{field: field, prefix: prefix}, nil
			}
			sugs, _, err := svc.JQL().AutocompleteSuggestions(base, field, prefix)
			if err != nil {
				return nil, err
			}
			values := make([]string, 0, len(sugs))
			for _, sg := range sugs {
				values = append(values, sg.Value)
			}
			return jqlSuggResult{field: field, prefix: prefix, values: values}, nil
		},
	})
}

// openPresets opens the saved-query dropdown as a section dialog. Labels carry
// the name and the query so type-to-filter matches either; the value is the
// JQL itself, applied by the pick's bound action — so however the pick's
// messages reach the shared updateDialog dispatcher, an accepted preset always
// commits and runs its query.
func (s *SearchModel) openPresets() {
	saved := s.saved()
	items := make([]picker.Item, len(saved))
	for i, l := range saved {
		items[i] = picker.Item{Label: l.Name + " — " + l.JQL, Value: l.JQL}
	}
	s.pushPick("Run saved query:", items, func(sel picker.Item) tea.Cmd {
		s.editing = false
		s.jql = sel.Value
		return s.fetch()
	})
}

// loadSaved cycles the saved queries into the committed query and runs it.
func (s *SearchModel) loadSaved(delta int) tea.Cmd {
	saved := s.saved()
	if len(saved) == 0 {
		return nil
	}
	n := len(saved)
	s.savedIdx = ((s.savedIdx+delta)%n + n) % n
	s.jql = saved[s.savedIdx].JQL
	s.editing = false
	return s.fetch()
}

// Update edits/commits the JQL, then delegates shared behavior to results.
func (s *SearchModel) Update(msg tea.Msg) (core.Section, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.applySize(searchBoxRows)
		return s, nil
	case core.TaskFinishedMsg:
		switch msg.Scope {
		case s.jqlRefScope():
			// Reference data is best-effort: on error the fallback
			// suggestions simply stand.
			if res, ok := msg.Result.(jqlRefResult); ok && msg.Err == nil {
				s.jqlRef = res.ref
				s.jqlRefLoaded = true
			}
			return s, s.refreshSuggestions()
		case s.jqlSuggScope():
			res, ok := msg.Result.(jqlSuggResult)
			if !ok || msg.Err != nil {
				// Forget the attempted field/prefix so the next keystroke at
				// the same position retries — but don't refresh here: that
				// would refetch immediately and loop while the API is down.
				s.sugField, s.sugPrefix = "", ""
				return s, nil
			}
			s.valueSugs = res.values
			s.valueSugsFor = res.field
			return s, s.refreshSuggestions()
		}
		cmd, _ := s.handleTask(msg)
		return s, cmd
	case core.RestyleMsg:
		s.restyle()
		return s, nil
	case core.RefreshTickMsg:
		// Nothing to refresh until a query has been committed (an empty-JQL
		// refetch would just churn tasks), and never mid-edit — the embedded
		// results can't see s.editing, so gate it here.
		if s.jql == "" || s.editing {
			return s, nil
		}
		return s, s.autoRefresh()
	case tea.MouseWheelMsg:
		return s, s.handleWheel(msg)
	case tea.MouseClickMsg:
		// A click on the JQL box starts editing; the rest is shared routing.
		// The x bound keeps preview-pane clicks (which share these rows under
		// a side dock) from opening the editor.
		if _, inMain := s.mainX(msg.X); inMain &&
			msg.Button == tea.MouseLeft && !s.editing && !s.capturing() &&
			msg.Y >= core.TopChromeRows && msg.Y < core.TopChromeRows+searchBoxRows {
			return s, s.openEdit()
		}
		return s, s.handleClick(msg)
	case input.EditorFinishedMsg:
		return s, s.handleEditor(msg)
	case form.SuggestionsMsg:
		// Autocomplete fetches resolve as commands; their results must find
		// their way back into the open form dialog or the seam silently drops them.
		cmd, _, _ := s.dialogs.Update(msg)
		return s, cmd
	case formSubmitMsg:
		return s, s.handleFormSubmit(msg)
	case spinner.TickMsg:
		return s, s.handleSpinner(msg)
	case flashClearMsg:
		s.handleFlashClear(msg)
		return s, nil
	case tea.PasteMsg:
		// An open dialog outranks the editor: the preset dropdown opens from
		// edit mode and covers the box, so a paste must filter it, not the
		// hidden input. (When not editing, the shared paste routing already
		// drives an open dialog first.)
		if s.editing && s.dialogs.Active() {
			return s, s.updateDialog(msg)
		}
		if s.editing {
			cmd := s.jqlInput.Update(msg)
			return s, tea.Batch(cmd, s.refreshSuggestions())
		}
		cmd, _ := s.handlePaste(msg)
		return s, cmd
	case tea.KeyPressMsg:
		// An open dialog outranks the editor: the preset dropdown opens from
		// edit mode and covers the box, so its keys must never leak into the
		// hidden input. (When not editing, handleKey already routes an open
		// dialog first.)
		if s.editing && s.dialogs.Active() {
			return s, s.updateDialog(msg)
		}
		if s.editing {
			if key.Matches(msg, s.ctx.Keys.Presets) {
				s.openPresets()
				return s, nil
			}
			return s.updateEdit(msg)
		}
		if key.Matches(msg, s.ctx.Keys.Presets) && !s.capturing() {
			s.openPresets()
			return s, nil
		}
		if cmd, handled := s.handleKey(msg); handled {
			return s, cmd
		}
		switch {
		case key.Matches(msg, s.ctx.Keys.Search), key.Matches(msg, s.ctx.Keys.Open):
			return s, s.openEdit()
		case key.Matches(msg, s.ctx.Keys.Refresh):
			return s, s.fetch()
		case key.Matches(msg, s.ctx.Keys.NextLens):
			return s, s.loadSaved(1)
		case key.Matches(msg, s.ctx.Keys.PrevLens):
			return s, s.loadSaved(-1)
		}
	}
	return s, nil
}

// updateEdit handles the JQL editor through the shared input: enter commits
// and runs; esc cancels; everything else (cursor movement, paste) edits and
// recomputes the token-aware completions.
func (s *SearchModel) updateEdit(msg tea.KeyPressMsg) (core.Section, tea.Cmd) {
	switch msg.String() {
	case "esc":
		s.editing = false
	case "enter":
		s.editing = false
		s.jql = strings.TrimSpace(s.jqlInput.Value())
		return s, s.fetch()
	default:
		cmd := s.jqlInput.Update(msg)
		return s, tea.Batch(cmd, s.refreshSuggestions())
	}
	return s, nil
}

// View renders the JQL query box above the shared results body. The box border
// brightens while editing so focus is obvious.
func (s *SearchModel) View() string {
	var body string
	switch {
	case s.editing:
		body = s.jqlInput.View()
	case s.jql != "":
		body = s.jql
	default:
		body = lipgloss.NewStyle().Faint(true).Render("enter to write a JQL query · ctrl+p saved queries · ] cycle")
	}

	border := theme.Theme.Dim.GetForeground()
	if s.editing {
		border = theme.Theme.Blue.GetForeground()
	}
	w := max(
		// rounded border (2) + horizontal padding (2)
		s.ctx.MainWidth-4, 1)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 1).
		Width(w).
		Render(lipgloss.NewStyle().Bold(true).Render("JQL ") + body)
	return s.view(box)
}

// CapturesInput reports JQL-edit focus on top of the shared filter/action focus.
func (s *SearchModel) CapturesInput() bool { return s.editing || s.capturing() }

// HelpBindings lists the section's contextual bindings.
func (s *SearchModel) HelpBindings() []key.Binding {
	k := s.ctx.Keys
	return []key.Binding{
		k.Search, k.Presets, k.Open, k.Create, k.Transition, k.AssignMe, k.Comment, k.Labels, k.OpenBrowse,
		k.Filter, k.Facet, k.Jumplist, k.TogglePreview,
	}
}
