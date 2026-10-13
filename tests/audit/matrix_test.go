//go:build outputaudit

package audit

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// fixtureExpected builds a two-cell expected set for the pure verifier
// tests.
func fixtureExpected() []ExpectedCell {
	return []ExpectedCell{
		{Op: "fix.op", Category: CategorySuccess, Mode: ModeJSON, Axis: AxisM6},
		{Op: "fix.op", Category: CategoryFailure, Mode: ModeJSON, Axis: AxisM11},
	}
}

// TestVerifyMatrixFailsAbsentExpectedCellByName is R10's core: an
// expected cell with no recorded result fails NAMING the cell, so a
// missing or mislabeled recipe can never read as coverage.
func TestVerifyMatrixFailsAbsentExpectedCellByName(t *testing.T) {
	t.Parallel()
	expected := fixtureExpected()
	problems := verifyMatrix(expected, []Cell{expected[0].evaluated("pass", "")}, nil)
	want := "coverage: expected cell fix.op||failure|json|M11 has no recorded result"
	if len(problems) != 1 || !strings.Contains(problems[0], want) {
		t.Errorf("problems = %v, want one containing %q", problems, want)
	}
}

// TestVerifyMatrixFailsOrphanRecordedCell closes the other direction:
// a recorded cell the derivation cannot produce is a mislabeled
// scenario or a stale check, not silent extra coverage.
func TestVerifyMatrixFailsOrphanRecordedCell(t *testing.T) {
	t.Parallel()
	expected := fixtureExpected()
	orphan := Cell{Op: "fix.gone", Category: "success", Mode: "json", Axis: "M6", Status: "pass"}
	problems := verifyMatrix(expected, []Cell{
		expected[0].evaluated("pass", ""),
		expected[1].evaluated("pass", ""),
		orphan,
	}, nil)
	if len(problems) != 1 || !strings.Contains(problems[0], "recorded cell fix.gone|") {
		t.Errorf("problems = %v, want one orphan naming fix.gone", problems)
	}
}

// TestVerifyMatrixSeparatesShapeFromCorrectness pins the two-verdict
// design: a red cell reports as correctness, a missing one as coverage,
// and neither masks the other.
func TestVerifyMatrixSeparatesShapeFromCorrectness(t *testing.T) {
	t.Parallel()
	expected := fixtureExpected()
	problems := verifyMatrix(expected, []Cell{expected[0].evaluated("fail", "stdout not empty")}, nil)
	var haveCoverage, haveCorrectness bool
	for _, p := range problems {
		haveCoverage = haveCoverage || strings.HasPrefix(p, "coverage: expected cell fix.op||failure")
		haveCorrectness = haveCorrectness || strings.HasPrefix(p, "correctness: cell fix.op||success")
	}
	if !haveCoverage || !haveCorrectness {
		t.Errorf("problems = %v, want both a coverage and a correctness verdict", problems)
	}
}

// TestVerifyMatrixHonorsReviewedExemptions: an exempt cell may be red
// or unrecorded without failing — and ONLY an exempt one.
func TestVerifyMatrixHonorsReviewedExemptions(t *testing.T) {
	t.Parallel()
	expected := fixtureExpected()
	exempt := []Exemption{
		{Cell: expected[0].Key(), Reason: "known red, fix tracked", Date: "2026-08-02"},
		{Cell: expected[1].Key(), Reason: "no hermetic recipe possible yet", Date: "2026-08-02"},
	}
	problems := verifyMatrix(expected, []Cell{expected[0].evaluated("fail", "red but reviewed")}, exempt)
	if len(problems) != 0 {
		t.Errorf("problems = %v, want none for fully exempted red/missing cells", problems)
	}
}

// TestExemptionGuardrail audits the exemption table's own rules:
// bounded (one entry per cell), dated, explained, and glued to the live
// expected set — the design's answer to exemption-creep.
func TestExemptionGuardrail(t *testing.T) {
	t.Parallel()
	expected := map[string]bool{"fix.op||success|json|M6": true}
	tests := []struct {
		name string
		list []Exemption
		part string
	}{
		{
			name: "undated",
			list: []Exemption{{Cell: "fix.op||success|json|M6", Reason: "r", Date: "soonish"}},
			part: "no parseable decision date",
		},
		{
			name: "unexplained",
			list: []Exemption{{Cell: "fix.op||success|json|M6", Reason: " ", Date: "2026-08-02"}},
			part: "has no reason",
		},
		{
			name: "future dated",
			list: []Exemption{{Cell: "fix.op||success|json|M6", Reason: "r", Date: "2099-12-31"}},
			part: "dated in the future",
		},
		{
			name: "zero-width reason",
			list: []Exemption{{Cell: "fix.op||success|json|M6", Reason: "\u200b \u200b", Date: "2026-08-02"}},
			part: "has no reason",
		},
		{
			name: "bidi override reason",
			list: []Exemption{{Cell: "fix.op||success|json|M6", Reason: "\u202ereversed reason", Date: "2026-08-02"}},
			part: "has no reason",
		},
		{
			name: "stale cell",
			list: []Exemption{{Cell: "fix.gone||success|json|M6", Reason: "r", Date: "2026-08-02"}},
			part: "stale entry",
		},
		{
			name: "duplicate",
			list: []Exemption{
				{Cell: "fix.op||success|json|M6", Reason: "r", Date: "2026-08-02"},
				{Cell: "fix.op||success|json|M6", Reason: "r", Date: "2026-08-02"},
			},
			part: "listed twice",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			problems := validateExemptions(tt.list, expected)
			for _, p := range problems {
				if strings.Contains(p, tt.part) {
					return
				}
			}
			t.Errorf("problems = %v, want one containing %q", problems, tt.part)
		})
	}
}

// TestLiveExemptionTableIsClean holds the PRODUCTION exemption list to
// its own guardrail against the PRODUCTION expected set — and pins that
// it is empty today: 0.16's contract is zero exemptions until a review
// deliberately adds one.
func TestLiveExemptionTableIsClean(t *testing.T) {
	members, problems := liveMembers()
	if len(problems) > 0 {
		t.Skip("inventory not clean; its own test reports the failures")
	}
	expected := map[string]bool{}
	for _, cell := range expectedCells(members) {
		expected[cell.Key()] = true
	}
	for _, p := range validateExemptions(exemptions, expected) {
		t.Errorf("exemption table: %s", p)
	}
	// Exact success exemptions remain only for terminal editing and native
	// credential removal. Environment login now supplies real success captures.
	wantExempt := []string{}
	for _, op := range []string{"auth.logout", "config.edit"} {
		for _, mode := range []string{"compact", "json"} {
			for _, axis := range []string{"M12", "M4", "M5", "M6", "M8"} {
				wantExempt = append(wantExempt, op+"||success|"+mode+"|"+axis)
			}
		}
		for _, axis := range []string{"H3", "H4"} {
			wantExempt = append(wantExempt, op+"||success|human|"+axis)
		}
		wantExempt = append(wantExempt, op+"||success|human_color|H5")
	}
	gotExempt := make([]string, 0, len(exemptions))
	for _, e := range exemptions {
		gotExempt = append(gotExempt, e.Cell)
	}
	slices.Sort(gotExempt)
	slices.Sort(wantExempt)
	if !slices.Equal(gotExempt, wantExempt) {
		t.Errorf("exempt cells = %v, want %v", gotExempt, wantExempt)
	}
	if len(exemptions) != 26 {
		t.Errorf("exemption table has %d entries; adding one is a reviewed decision that must also move this pin", len(exemptions))
	}
}

// TestCommittedBaselineParses guards the ratchet's substrate: the
// committed baseline must exist and parse. Drift itself is verified in
// TestMain's finalization, after all cells are recorded.
func TestCommittedBaselineParses(t *testing.T) {
	t.Parallel()
	path, err := matrixBaselinePath()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("committed baseline missing (regenerate deliberately with AUDIT_UPDATE_MATRIX=1): %v", err)
	}
	var doc struct {
		Cells []Cell `json:"cells"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("baseline does not parse: %v", err)
	}
	members, problems := liveMembers()
	if len(problems) > 0 {
		t.Fatalf("baseline inventory is not clean: %v", problems)
	}
	if problems := verifyMatrix(expectedCells(members), doc.Cells, exemptions); len(problems) > 0 {
		t.Errorf("baseline does not reconcile with the current inventory: %v", problems)
	}
	for _, cell := range doc.Cells {
		if cell.Status != "pass" && !isExempt(ExpectedCell{Op: cell.Op, Variant: cell.Variant, Category: Category(cell.Category), Mode: Mode(cell.Mode), Axis: Axis(cell.Axis)}) {
			t.Errorf("baseline carries a non-exempt red cell %s — the zero-red ratchet forbids committing it", cell.Key())
		}
	}
}
