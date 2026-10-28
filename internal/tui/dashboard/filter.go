package dashboard

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/filter"
	"github.com/gechr/primer/input"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

type issueFacet struct{ field, value, label string }

func (f issueFacet) matches(issue *envelope.IssueView) bool {
	switch f.field {
	case "status":
		return issue.Fields.Status.Set && !issue.Fields.Status.Null && issue.Fields.Status.Value.Name.Value == f.value
	case "assignee":
		a := issue.Fields.Assignee
		if f.value == "" {
			return a.Set && a.Null
		}
		return a.Set && !a.Null && a.Value.AccountID.Value == f.value
	case "label":
		return issue.Fields.Labels.Set && !issue.Fields.Labels.Null && slices.Contains(issue.Fields.Labels.Value, f.value)
	}
	return true
}

func (m *Model) rebuildVisible() {
	m.visible = m.visible[:0]
	term := filter.Parse(m.localFilter)
	for i := range m.issues {
		issue := &m.issues[i]
		text := issue.Key + " " + issue.Fields.Summary.Value + " " + issue.Fields.Status.Value.Name.Value + " " + issue.Fields.Assignee.Value.DisplayName.Value + " " + strings.Join(issue.Fields.Labels.Value, " ")
		if term.Match(text) && m.facet.matches(issue) {
			m.visible = append(m.visible, i)
		}
	}
}

func (m *Model) restoreCursor(key string) {
	for i, source := range m.visible {
		if m.issues[source].Key == key {
			m.rows.SetCursor(i)
			return
		}
	}
}

func (m *Model) applyLocalFilter() tea.Cmd {
	keep := m.selectedKey()
	m.rebuildRows()
	m.restoreCursor(keep)
	return m.selectDetail()
}

func (m *Model) openFilter() {
	if m.detailFocused || m.previewFocused {
		return
	}
	m.filtering = true
	m.filterInput = input.NewLine("Filter > ", "Loaded issues only")
	m.filterInput.SetValue(m.localFilter)
	m.filterInput.SetWidth(max(1, m.pageWidth()-9))
}

func (m *Model) updateFilter(msg tea.Msg) tea.Cmd {
	if press, ok := msg.(tea.KeyPressMsg); ok {
		switch press.String() {
		case "enter":
			m.filtering = false
			m.filterInput.Blur()
			return nil
		case "esc":
			m.filtering = false
			m.localFilter = ""
			m.filterInput.Blur()
			return m.applyLocalFilter()
		}
	}
	cmd := m.filterInput.Update(msg)
	value := m.filterInput.Value()
	if value != m.localFilter {
		m.localFilter = value
		return tea.Batch(cmd, m.applyLocalFilter())
	}
	return cmd
}

func (m *Model) openFacets() {
	if m.detailFocused || m.previewFocused {
		return
	}
	counts := make(map[issueFacet]int)
	for _, issue := range m.issues {
		if s := issue.Fields.Status; s.Set && !s.Null && s.Value.Name.Set && !s.Value.Name.Null {
			counts[issueFacet{"status", s.Value.Name.Value, s.Value.Name.Value}]++
		}
		if a := issue.Fields.Assignee; a.Set {
			if a.Null {
				counts[issueFacet{"assignee", "", "Unassigned"}]++
			} else if a.Value.AccountID.Set && a.Value.AccountID.Value != "" {
				counts[issueFacet{"assignee", a.Value.AccountID.Value, a.Value.DisplayName.Value + " · " + a.Value.AccountID.Value}]++
			}
		}
		labels := make(map[string]bool)
		for _, label := range issue.Fields.Labels.Value {
			if !labels[label] {
				counts[issueFacet{"label", label, label}]++
				labels[label] = true
			}
		}
	}
	facets := make([]issueFacet, 0, len(counts))
	for f := range counts {
		facets = append(facets, f)
	}
	slices.SortFunc(facets, func(a, b issueFacet) int {
		if cmp := strings.Compare(a.field, b.field); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.label, b.label)
	})
	choices := []choice{{label: "Clear facet", value: "clear"}}
	mapping := map[string]issueFacet{"clear": {}}
	for i, f := range facets {
		id := strconv.Itoa(i)
		mapping[id] = f
		choices = append(choices, choice{label: fmt.Sprintf("%s: %s (%d)", f.field, cli.Text(f.label), counts[f]), value: id})
	}
	m.pick("Filter loaded issues", "facet", "", choices)
	m.dialogs.Top().(*pickerDialog).facets = mapping
}
