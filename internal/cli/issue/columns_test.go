package issue

import (
	"slices"
	"strings"
	"testing"
)

// issueFieldConstants is every field this package defines. Written out
// rather than derived from issueColumnTable's own keys, which would be
// checking the table against itself: the point is that a constant
// somebody adds without a spec fails here rather than at render time,
// inside a panic, in front of whoever ran the command.
var issueFieldConstants = []issueField{
	fieldKey, fieldStatus, fieldPriority, fieldType, fieldSummary, fieldAssignee, fieldUpdated,
}

func TestEveryIssueFieldHasAColumnSpec(t *testing.T) {
	t.Parallel()
	for _, field := range issueFieldConstants {
		spec, ok := issueColumnTable[field]
		if !ok {
			t.Errorf("field %q has no entry in issueColumnTable — issueColumnsFor would panic on it", field)
			continue
		}
		if spec.header == "" {
			t.Errorf("field %q has no header", field)
		}
		if spec.cell == nil {
			t.Errorf("field %q has no cell renderer — which sink draws it is exactly what the table exists to say", field)
		}
	}
	for field := range issueColumnTable {
		if !slices.Contains(issueFieldConstants, field) {
			t.Errorf("issueColumnTable carries %q, which is not one of this package's field constants", field)
		}
	}
}

// TestCanonicalOrderCoversEveryField keeps the order and the table
// closed over the same set: a field with a spec but no place in the
// order could never be drawn, and one in the order with no spec would
// panic at render.
func TestCanonicalOrderCoversEveryField(t *testing.T) {
	t.Parallel()
	for _, field := range issueFieldConstants {
		if !slices.Contains(issueFieldOrder, field) {
			t.Errorf("field %q has a column spec but no place in issueFieldOrder — no command could select it", field)
		}
	}
	for _, field := range issueFieldOrder {
		if !slices.Contains(issueFieldConstants, field) {
			t.Errorf("issueFieldOrder names %q, which is not one of this package's field constants", field)
		}
	}
}

// TestCommandFieldListsFollowTheCanonicalOrder is the mechanism behind
// issueFieldOrder's promise: a command may DROP a column, never move
// one. Two issue tables drawn one after another must not put STATUS in
// different places, and with four more keyed reads queued behind link
// list, "everyone picked their own order" is a drift that arrives four
// more times unless something says no.
//
// Subsequence, not equality: dropping is the per-command judgment this
// deliberately still allows.
func TestCommandFieldListsFollowTheCanonicalOrder(t *testing.T) {
	t.Parallel()
	for name, fields := range map[string][]issueField{
		"issue list":      issueListFields,
		"issue link list": issueLinkFields,
	} {
		at := 0
		for _, field := range fields {
			next := slices.Index(issueFieldOrder[at:], field)
			if next < 0 {
				t.Errorf("%s puts %q out of canonical order (%v) — a command selects from that order, it does not rearrange it; move the column in issueFieldOrder if the whole CLI should change",
					name, field, issueFieldOrder)
				break
			}
			at += next + 1
		}
	}
}

// TestEveryCommandsFieldListResolves is the other direction: a column
// set naming a field the table does not carry would panic on the first
// render, which is a failure a person running the command discovers.
func TestEveryCommandsFieldListResolves(t *testing.T) {
	t.Parallel()
	for name, fields := range map[string][]issueField{
		"issue list":      issueListFields,
		"issue link list": issueLinkFields,
	} {
		if len(fields) == 0 {
			t.Errorf("%s declares no columns", name)
		}
		for _, field := range fields {
			if _, ok := issueColumnTable[field]; !ok {
				t.Errorf("%s selects field %q, which the shared column table does not carry", name, field)
			}
		}
	}
}

// TestIssueLinkColumnsSpliceTheRelationAfterTheKey pins the one column
// that is not an issue field: it belongs second, so the table reads as
// the same family as `issue list`'s, and — more importantly — so the
// shared columns keep their own order around it.
func TestIssueLinkColumnsSpliceTheRelationAfterTheKey(t *testing.T) {
	t.Parallel()
	got := issueLinkColumns("", 0)

	want := []string{"key", "link"}
	for _, field := range issueLinkFields[1:] {
		want = append(want, string(field))
	}
	names := make([]string, len(got))
	for i, c := range got {
		names[i] = c.Name
	}
	if !slices.Equal(names, want) {
		t.Errorf("columns = %v, want %v", names, want)
	}
}

// TestIssueLinkColumnsDoNotDisturbTheListColumns guards the splice
// itself: it appends onto a slice the shared builder returned, and an
// append that reused that slice's own backing array would overwrite a
// neighboring column rather than insert beside it.
func TestIssueLinkColumnsDoNotDisturbTheListColumns(t *testing.T) {
	t.Parallel()
	issueLinkColumns("", 0)

	got := issueListColumns("", 0, nil)
	if len(got) != len(issueListFields) {
		t.Fatalf("issue list columns = %d, want %d", len(got), len(issueListFields))
	}
	for i, field := range issueListFields {
		if got[i].Name != string(field) {
			t.Errorf("issue list column %d = %q, want %q", i, got[i].Name, field)
		}
	}
}

// TestParseIssueFieldsKeepsTheCallerOrder pins the one place a field list
// is NOT the canonical order: --columns is the caller arranging this
// invocation's own table, and silently re-sorting it would defeat the
// flag (see parseIssueFields' own doc comment for why that departs from
// issueFieldOrder's rule for DECLARED lists).
func TestParseIssueFieldsKeepsTheCallerOrder(t *testing.T) {
	got, err := parseIssueFields([]string{"summary", "key", "status"})
	if err != nil {
		t.Fatalf("parseIssueFields: %v", err)
	}
	want := []issueField{fieldSummary, fieldKey, fieldStatus}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v — the caller's order was not preserved", got, want)
		}
	}
}

func TestParseIssueFieldsNormalizesCaseAndSpace(t *testing.T) {
	got, err := parseIssueFields([]string{" KEY ", "Status"})
	if err != nil {
		t.Fatalf("parseIssueFields: %v", err)
	}
	if len(got) != 2 || got[0] != fieldKey || got[1] != fieldStatus {
		t.Errorf("got %v, want [key status] — a caller retyping a header should not be refused", got)
	}
}

// TestParseIssueFieldsRefusesUnknownAndNamesTheSet: a refusal that does
// not say what IS accepted leaves the caller guessing, which is the whole
// difference between a usable error and an unusable one.
func TestParseIssueFieldsRefusesUnknownAndNamesTheSet(t *testing.T) {
	_, err := parseIssueFields([]string{"key", "bogus"})
	if err == nil {
		t.Fatal("an unknown column was accepted — issueColumnsFor would panic on it at render time")
	}
	for _, want := range []string{"bogus", "assignee", "updated"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal omits %q: %v", want, err)
		}
	}
}

func TestParseIssueFieldsRefusesDuplicate(t *testing.T) {
	if _, err := parseIssueFields([]string{"key", "key"}); err == nil {
		t.Fatal("a repeated column was accepted — a table with two KEY columns is a typo rendered faithfully")
	}
}

func TestParseIssueFieldsRefusesEmptySelection(t *testing.T) {
	if _, err := parseIssueFields([]string{"", "  "}); err == nil {
		t.Fatal("an empty selection was accepted — the table would have no columns at all")
	}
}

// TestRefuseUnfetchedColumnsRefusesABlankColumn is the check's whole
// reason for existing: a column of dashes reads as "nobody is assigned",
// a statement about the project, when the truth is "you did not ask Jira
// for that field".
func TestRefuseUnfetchedColumnsRefusesABlankColumn(t *testing.T) {
	err := refuseUnfetchedColumns([]issueField{fieldAssignee}, []string{"summary"})
	if err == nil {
		t.Fatal("--columns assignee was accepted alongside --fields summary — the column would be silently blank")
	}
	for _, want := range []string{"assignee", "--fields"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal omits %q: %v", want, err)
		}
	}
}

func TestRefuseUnfetchedColumnsAllowsAFetchedColumn(t *testing.T) {
	if err := refuseUnfetchedColumns([]issueField{fieldAssignee}, []string{"summary", "assignee"}); err != nil {
		t.Errorf("a fetched column was refused: %v", err)
	}
}

// TestRefuseUnfetchedColumnsNeverRefusesKey: a key is identity, carried
// outside the `fields` object entirely, so no --fields value can withhold
// it and no --fields value may make the KEY column unaskable.
func TestRefuseUnfetchedColumnsNeverRefusesKey(t *testing.T) {
	if err := refuseUnfetchedColumns([]issueField{fieldKey}, []string{"summary"}); err != nil {
		t.Errorf("--columns key was refused: %v", err)
	}
}

// TestRefuseUnfetchedColumnsSkipsServerResolvedFieldSets pins the
// deliberate hole: `*all` and `-description` name a set only the SERVER
// can enumerate, so a check that guessed its membership would refuse
// invocations that work.
func TestRefuseUnfetchedColumnsSkipsServerResolvedFieldSets(t *testing.T) {
	for _, fields := range [][]string{{"*all"}, {"*navigable"}, {"-description"}} {
		if err := refuseUnfetchedColumns([]issueField{fieldAssignee}, fields); err != nil {
			t.Errorf("--fields %v was treated as an enumerable set: %v", fields, err)
		}
	}
}

func TestRefuseUnfetchedColumnsSkipsWithoutExplicitFields(t *testing.T) {
	if err := refuseUnfetchedColumns(issueFieldOrder, nil); err != nil {
		t.Errorf("the default field set was treated as narrowing: %v", err)
	}
}

// TestEveryColumnsSelectableFieldIsCoveredByTheDefaultFetch closes the
// loop between the two halves of this feature: --columns can name any
// field in issueFieldOrder, and with no --fields the command asks Jira
// for defaultListFields. If those ever diverge, a caller selecting a
// perfectly valid column with no --fields at all would get a blank one —
// and refuseUnfetchedColumns, which only runs when --fields was given,
// would not catch it.
func TestEveryColumnsSelectableFieldIsCoveredByTheDefaultFetch(t *testing.T) {
	fetched := map[string]bool{}
	for _, f := range defaultListFields {
		fetched[f] = true
	}
	for _, field := range issueFieldOrder {
		need := issueColumnTable[field].requires
		if need != "" && !fetched[need] {
			t.Errorf("--columns %s draws from %q, which defaultListFields does not request — "+
				"selecting it without --fields would render a column of blanks", field, need)
		}
	}
}

// TestIssueListColumnsHonoursASelection pins that the selection reaches
// the rendered table, in the caller's order — the property every check
// above is in service of.
func TestIssueListColumnsHonoursASelection(t *testing.T) {
	got := issueListColumns("", 0, []issueField{fieldSummary, fieldKey})
	if len(got) != 2 {
		t.Fatalf("got %d columns, want 2", len(got))
	}
	if got[0].Header != "SUMMARY" || got[1].Header != "KEY" {
		t.Errorf("headers = %q, %q; want SUMMARY, KEY", got[0].Header, got[1].Header)
	}
}

// TestIssueListColumnsFallsBackToTheWholeSet: nil means "no --columns",
// and it must mean exactly what the command drew before the flag existed.
func TestIssueListColumnsFallsBackToTheWholeSet(t *testing.T) {
	if got := issueListColumns("", 0, nil); len(got) != len(issueListFields) {
		t.Errorf("got %d columns for a nil selection, want the whole set of %d", len(got), len(issueListFields))
	}
}
