package core

import "testing"

func TestNarrowJQLKeepsOrderingAndQuotedOrderText(t *testing.T) {
	query, err := NarrowJQL(`project = DEMO OR summary ~ "order by rank" ORDER BY updated DESC`, `assignee = currentUser() OR reporter = currentUser()`)
	want := `(project = DEMO OR summary ~ "order by rank") AND (assignee = currentUser() OR reporter = currentUser()) ORDER BY updated DESC`
	if err != nil || query != want {
		t.Fatalf("scope/order changed: %q %v", query, err)
	}
	for _, lens := range []string{"", "ORDER BY key", "status = Open ORDER BY key"} {
		t.Run(lens, func(t *testing.T) {
			if _, err := NarrowJQL("project = DEMO", lens); err == nil {
				t.Fatal("invalid lens accepted")
			}
		})
	}
}
