//go:build outputaudit

package audit

import (
	"encoding/json"
	"testing"
)

func TestIssueInputPreparationAuditMeasuresEveryNewCell(t *testing.T) {
	members, problems := liveMembers()
	if len(problems) != 0 {
		t.Fatalf("live inventory is invalid: %v", problems)
	}
	bin, err := jiraBinary()
	if err != nil {
		t.Fatal("build actual audit CLI", err)
	}
	captures := map[string]Capture{}
	count := 0
	for _, cell := range expectedCells(members) {
		if cell.Op != "issue.input.prepare" {
			continue
		}
		member, ok := memberFor(members, cell.Op, cell.Variant)
		if !ok {
			t.Fatal("prepared-input cell has no real member")
		}
		key := string(cell.Category) + "|" + string(cell.Mode)
		capture, present := captures[key]
		if !present {
			found := false
			for _, scenario := range scenarios {
				if scenario.Op == cell.Op && scenario.Variant == cell.Variant && scenario.Category == cell.Category {
					capture, err = runScenario(bin, scenario, cell.Mode, "")
					if err != nil {
						t.Fatal("actual recipe failed", err)
					}
					found = true
					break
				}
			}
			if !found {
				t.Fatal("prepared-input cell has no actual recipe")
			}
			captures[key] = capture
		}
		t.Run(cell.Key(), func(t *testing.T) {
			check := axisChecks[cell.Category][cell.Axis]
			if check == nil {
				t.Fatal("cell has no implemented guard")
			}
			ok, detail := check(capture, member.Meta)
			if !ok {
				t.Fatal(detail)
			}
			row, err := json.Marshal(cell.evaluated("pass", ""))
			if err != nil {
				t.Fatal(err)
			}
			t.Log("AUDIT_PREPARATION_CELL=" + string(row))
		})
		count++
	}
	if count == 0 {
		t.Fatal("no new prepared-input cells derived")
	}
}
