package issues

import (
	"fmt"
	"slices"
	"sort"
	"strconv"

	tea "charm.land/bubbletea/v2"

	xmaps "github.com/gechr/x/maps"
	"github.com/gechr/x/ptr"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/tui/components/picker"
)

// facet narrows the visible rows to issues matching one field value, on top
// of the text filter. The zero value means no facet.
type facet struct {
	field string // "status" | "assignee" | "label"
	value string
}

func (f facet) active() bool   { return f != facet{} }
func (f facet) String() string { return f.field + ": " + f.value }

// facetChoices pairs picker items with the facets they stand for, addressed
// by index — item values never embed Jira-controlled content, so a weird
// status or label string can't corrupt the selection.
type facetChoices struct {
	items  []picker.Item
	facets []facet // facets[i] belongs to items[i]; the zero facet clears
}

// facetsFor collects the distinct status/assignee/label values across the
// loaded issues, with occurrence counts, grouped in that order and sorted
// within each group.
func facetsFor(all []*jira.Issue) []picker.Item {
	return collectFacets(all).items
}

func collectFacets(all []*jira.Issue) facetChoices {
	count := map[facet]int{}
	for _, iss := range all {
		if s := issueStatus(iss); s != "" {
			count[facet{"status", s}]++
		}
		count[facet{"assignee", assigneeFacetValue(iss)}]++
		for _, l := range issueLabels(iss) {
			count[facet{"label", l}]++
		}
	}
	facets := xmaps.Keys(count)
	groupRank := map[string]int{"status": 0, "assignee": 1, "label": 2}
	sort.Slice(facets, func(i, j int) bool {
		if a, b := groupRank[facets[i].field], groupRank[facets[j].field]; a != b {
			return a < b
		}
		return facets[i].value < facets[j].value
	})
	items := make([]picker.Item, len(facets))
	for i, f := range facets {
		items[i] = picker.Item{
			Label: fmt.Sprintf("%s (%d)", f.String(), count[f]),
			Value: strconv.Itoa(i),
		}
	}
	return facetChoices{items: items, facets: facets}
}

// unassignedFacet is the assignee bucket for issues without one — faceting
// to unassigned work is the triage move, so it must be selectable.
const unassignedFacet = "Unassigned"

func assigneeFacetValue(i *jira.Issue) string {
	if i.Fields != nil && i.Fields.Assignee != nil {
		if a := ptr.Deref(i.Fields.Assignee.DisplayName); a != "" {
			return a
		}
	}
	return unassignedFacet
}

// matchesFacet reports whether an issue carries the facet's value.
func matchesFacet(i *jira.Issue, f facet) bool {
	switch f.field {
	case "status":
		return issueStatus(i) == f.value
	case "assignee":
		return assigneeFacetValue(i) == f.value
	case "label":
		if slices.Contains(issueLabels(i), f.value) {
			return true
		}
	}
	return false
}

// applyFacet narrows issues to the active facet (no-op when none).
func applyFacet(issues []*jira.Issue, f facet) []*jira.Issue {
	if !f.active() {
		return issues
	}
	out := make([]*jira.Issue, 0, len(issues))
	for _, iss := range issues {
		if matchesFacet(iss, f) {
			out = append(out, iss)
		}
	}
	return out
}

// openFacets opens the facet picker over the loaded set; with a facet
// already active the first item clears it (the zero facet in the choice
// table means "clear"). The choice table rides the pick's bound action, so
// nothing has to be stashed on the section for the pop.
func (r *results) openFacets() {
	choices := collectFacets(r.all)
	if r.facet.active() {
		choices.items = append([]picker.Item{{Label: "✕ clear (" + r.facet.String() + ")", Value: strconv.Itoa(len(choices.facets))}}, choices.items...)
		choices.facets = append(choices.facets, facet{})
	}
	r.pushPick("Filter by:", choices.items, func(sel picker.Item) tea.Cmd {
		r.applyFacetChoice(choices, sel)
		return nil
	})
}

// applyFacetChoice applies the facet chosen in the pick dialog. The
// selection's value is an index into choices; the zero facet at that index
// clears an active facet (see openFacets).
func (r *results) applyFacetChoice(choices facetChoices, sel picker.Item) {
	if idx, err := strconv.Atoi(sel.Value); err == nil && idx >= 0 && idx < len(choices.facets) {
		r.facet = choices.facets[idx]
	}
	r.applyFilter()
}
