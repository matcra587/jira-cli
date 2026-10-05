//go:build outputaudit

package audit

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

// Each new baseline cell is measured through the actual command; the existing
// matrix is extended from these results without rewriting historical claims.
func TestProfileOutputAuditMeasuresEveryNewCell(t *testing.T) {
	members, problems := liveMembers()
	if len(problems) != 0 {
		t.Fatalf("invalid inventory: %v", problems)
	}
	bin, err := jiraBinary()
	if err != nil {
		t.Fatal(err)
	}
	captures := map[string]Capture{}
	for _, scenario := range scenarios {
		if !strings.HasPrefix(scenario.Op, "config.profile.") {
			continue
		}
		for _, mode := range modesFor(scenario.Category) {
			capture, err := runScenario(bin, scenario, mode, "")
			if err != nil {
				t.Fatal(err)
			}
			captures[scenario.ID+"|"+string(mode)] = capture
			if scenario.Category != CategoryHelp && scenario.Category != CategoryFailure {
				wantWrite := scenario.Category == CategorySuccess && scenario.Op != "config.profile.list"
				if capture.WroteFiles != wantWrite {
					t.Fatalf("%s/%s: configuration effect does not match recipe", scenario.ID, mode)
				}
			}
			if scenario.Category == CategoryFailure && capture.WroteFiles {
				t.Fatalf("%s/%s: refused operation changed configuration", scenario.ID, mode)
			}
		}
	}
	count := 0
	for _, cell := range expectedCells(members) {
		if !strings.HasPrefix(cell.Op, "config.profile.") {
			continue
		}
		member, ok := memberFor(members, cell.Op, cell.Variant)
		if !ok {
			t.Fatal("profile cell has no declared member")
		}
		var capture Capture
		found := false
		for _, candidate := range captures {
			if candidate.Scenario.Op == cell.Op && candidate.Scenario.Category == cell.Category && candidate.Mode == cell.Mode {
				capture, found = candidate, true
				break
			}
		}
		if !found {
			t.Fatalf("profile cell has no executed capture: %s", cell.Key())
		}
		t.Run(cell.Key(), func(t *testing.T) {
			check := axisChecks[cell.Category][cell.Axis]
			if check == nil {
				t.Fatal("profile cell has no guard")
			}
			if cell.Axis == AxisM12 {
				check = func(cap Capture, _ command.Meta) (bool, string) {
					siblingMode := ModeJSON
					if cap.Mode == ModeJSON {
						siblingMode = ModeCompact
					}
					sibling, ok := captures[cap.Scenario.ID+"|"+string(siblingMode)]
					if !ok {
						return false, "no sibling machine capture"
					}
					mine, err := successData(cap)
					if err != nil {
						return false, err.Error()
					}
					theirs, err := successData(sibling)
					if err != nil {
						return false, "sibling capture: " + err.Error()
					}
					if !reflect.DeepEqual(mine, theirs) {
						return false, fmt.Sprintf("data differs across modes: %v vs %v", mine, theirs)
					}
					return true, ""
				}
			}
			if ok, detail := check(capture, member.Meta); !ok {
				t.Fatal(detail)
			}
			row, err := json.Marshal(cell.evaluated("pass", ""))
			if err != nil {
				t.Fatal(err)
			}
			t.Log("AUDIT_PROFILE_CELL=" + string(row))
		})
		count++
	}
	if count == 0 {
		t.Fatal("no profile cells derived")
	}
}
