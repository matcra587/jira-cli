//go:build outputaudit

package audit

import (
	"testing"
)

func TestEpicSnakeCaseGuardKeepsFixedStateKeys(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		doc  any
		want string
	}{
		{name: "tenant statuses", doc: map[string]any{"totals": map[string]any{"In Progress": 1}, "epics": []any{map[string]any{"counts": map[string]any{"Review Now": 1}, "fields": map[string]any{"customUnknown": false}}}}},
		{name: "fixed top level", doc: map[string]any{"CountsComplete": false}, want: "$.CountsComplete"},
		{name: "fixed child state", doc: map[string]any{"epics": []any{map[string]any{"children": map[string]any{"UnknownStatus": 1}}}}, want: "$.epics.children.UnknownStatus"},
		{name: "map lookalike", doc: map[string]any{"totals_extra": map[string]any{"In Progress": 1}}, want: "$.totals_extra.In Progress"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exemptions := append([]string{"epics.fields"}, literalMapBoundaries["epic.board"]...)
			if got := firstNonSnakeKey("", tc.doc, exemptions...); got != tc.want {
				t.Fatalf("naming guard widened: got=%q want=%q", got, tc.want)
			}
		})
	}
}
