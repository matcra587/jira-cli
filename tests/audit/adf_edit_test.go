//go:build outputaudit

package audit

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

// Exercise every independently derived axis for the local editing operations
// without constructing unrelated HTTP fixtures. The complete corpus still
// owns global matrix finalization; this test never records global cells.
func TestADFEditOutputAudit(t *testing.T) {
	members, problems := liveMembers()
	if len(problems) > 0 {
		t.Fatalf("inventory is not clean: %v", problems)
	}
	selected := []Member{}
	for _, member := range members {
		if strings.HasPrefix(member.Meta.Op, "adf.edit.") {
			selected = append(selected, member)
		}
	}
	if len(selected) != 3 {
		t.Fatalf("editing inventory has %d members, want 3", len(selected))
	}
	bin, err := jiraBinary()
	if err != nil {
		t.Fatal(err)
	}
	captures := map[string]Capture{}
	for _, scenario := range scenarios {
		if !strings.HasPrefix(scenario.Op, "adf.edit.") {
			continue
		}
		if scenario.Stub != "" {
			t.Fatal("local editing recipe gained a remote fixture")
		}
		for _, mode := range modesFor(scenario.Category) {
			capture, err := runScenario(bin, scenario, mode, "")
			if err != nil {
				t.Fatal(err)
			}
			if capture.ExitCode != scenario.ExpectExit || capture.WroteFiles {
				t.Fatalf("editing recipe %s/%s exit=%d writes=%v", scenario.ID, mode, capture.ExitCode, capture.WroteFiles)
			}
			captures[scenario.ID+"|"+string(mode)] = capture
		}
	}
	cells := []Cell{}
	for _, cell := range expectedCells(selected) {
		t.Run(cell.Key(), func(t *testing.T) {
			member, ok := memberFor(selected, cell.Op, cell.Variant)
			if !ok {
				t.Fatal("derived cell has no inventory member")
			}
			capture, ok := captureFor(captures, cell.Op, cell.Variant, cell.Category, cell.Mode)
			if !ok {
				t.Fatal("derived editing cell has no capture")
			}
			check := axisChecks[cell.Category][cell.Axis]
			if check == nil {
				t.Fatal("derived editing cell has no check")
			}
			if cell.Axis == AxisM12 {
				check = func(cap Capture, _ command.Meta) (bool, string) {
					sibling, found := siblingModeCaptureFrom(captures, cap)
					if !found {
						return false, "no sibling machine-mode capture to compare against"
					}
					return checkSuccessDeterminismPair(cap, sibling)
				}
			}
			ok, detail := check(capture, member.Meta)
			status := "pass"
			if !ok {
				status = "fail"
				t.Errorf("editing cell failed: %s", detail)
			}
			cells = append(cells, cell.evaluated(status, detail))
		})
	}
	if problems := verifyMatrix(expectedCells(selected), cells, nil); len(problems) > 0 {
		t.Fatalf("editing cell coverage: %v", problems)
	}
	encoded, err := json.Marshal(struct {
		Cells []Cell `json:"cells"`
	}{Cells: cells})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("anchored_edit_cells=%s", encoded)
}

func TestMachineModeDeterminismRejectsDivergence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		compact string
	}{
		{name: "changed-value", compact: `{"value":2,"optional":null}`},
		{name: "absent-versus-null", compact: `{"value":1}`},
		{name: "malformed-sibling", compact: `{"value":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cap := Capture{Scenario: Scenario{ID: "paired"}, Mode: ModeJSON, Stdout: `{"data":{"value":1,"optional":null}}`}
			other := Capture{Scenario: cap.Scenario, Mode: ModeCompact, Stdout: tc.compact}
			captures := map[string]Capture{"paired|json": cap, "paired|compact": other}
			sibling, ok := siblingModeCaptureFrom(captures, cap)
			if !ok || sibling.Mode != ModeCompact {
				t.Fatal("lookup did not select the other invocation")
			}
			if ok, reason := checkSuccessDeterminismPair(cap, sibling); ok || reason == "" {
				t.Fatalf("different machine outputs accepted: ok=%v reason=%q", ok, reason)
			}
		})
	}
}
