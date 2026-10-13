//go:build outputaudit

package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Cell is one evaluated matrix point with its verdict. The committed
// matrix-result.json serializes these: the audit's reviewable coverage
// claim and its regression ratchet in one file.
type Cell struct {
	Op       string `json:"op"`
	Variant  string `json:"variant,omitempty"`
	Category string `json:"category"`
	Mode     string `json:"mode"`
	Axis     string `json:"axis"`
	Status   string `json:"status"` // pass | fail
	Detail   string `json:"detail,omitempty"`
}

// cellKey is the one identity format shared by expected cells, recorded
// cells, and the exemption table: "op|variant|category|mode|axis".
func cellKey(op, variant, category, mode, axis string) string {
	return strings.Join([]string{op, variant, category, mode, axis}, "|")
}

func (c Cell) Key() string {
	return cellKey(c.Op, c.Variant, c.Category, c.Mode, c.Axis)
}

var (
	recordedMu sync.Mutex
	recorded   []Cell
)

// record adds one evaluated cell. The corpus tests ALSO fail the Go
// test for a non-exempt red cell — recording is the matrix's book, not
// a substitute for failing (design §10: zero non-exempt red, unlike
// 0.15's file-a-finding posture).
func record(cell ExpectedCell, ok bool, detail string) {
	recordedMu.Lock()
	defer recordedMu.Unlock()
	status := "pass"
	if !ok {
		status = "fail"
	}
	recorded = append(recorded, cell.evaluated(status, detail))
}

// recordedCells returns a sorted snapshot.
func recordedCells() []Cell {
	recordedMu.Lock()
	defer recordedMu.Unlock()
	out := slices.Clone(recorded)
	slices.SortFunc(out, func(a, b Cell) int { return strings.Compare(a.Key(), b.Key()) })
	return out
}

// Exemption is one reviewed hole in the matrix: a cell allowed to be
// red or unrecorded. The list is the erosion path the design warns
// about, so it is bounded by construction — every entry names exactly
// one expected cell, states why, and carries the decision date — and
// audited by its own guardrail test.
type Exemption struct {
	// Cell is the ExpectedCell key ("op|variant|category|mode|axis").
	Cell string
	// Reason states why the cell is exempt, for the reviewer.
	Reason string
	// Date is the decision date, YYYY-MM-DD.
	Date string
}

// exemptions is the live exemption table. Every addition is a reviewed
// decision, never a baseline bump — and each entry names exactly one
// cell, so the cost of an uncoverable surface is visible in review
// rather than hidden behind a pattern.
//
// authWriteNeedsRealKeyring explains logout's native-store success exemptions.
// Environment login publishes only public metadata and now has safe live corpus
// coverage. Logout still needs a real provider to remove a stored credential.
const authWriteNeedsRealKeyring = "auth.logout succeeds by removing credentials from a persistent provider, which the real-binary corpus must not open. Its preview and failure paths are recorded here; isolated native keyring and injected SDK tests cover actual removal."

var exemptions = []Exemption{
	{
		Cell:   "auth.logout||success|json|M4",
		Reason: authWriteNeedsRealKeyring,
		Date:   "2026-08-03",
	},
	{
		Cell:   "auth.logout||success|json|M5",
		Reason: authWriteNeedsRealKeyring,
		Date:   "2026-08-03",
	},
	{
		Cell:   "auth.logout||success|json|M6",
		Reason: authWriteNeedsRealKeyring,
		Date:   "2026-08-03",
	},
	{
		Cell:   "auth.logout||success|json|M8",
		Reason: authWriteNeedsRealKeyring,
		Date:   "2026-08-03",
	},
	{
		Cell:   "auth.logout||success|json|M12",
		Reason: authWriteNeedsRealKeyring,
		Date:   "2026-08-03",
	},
	{
		Cell:   "auth.logout||success|compact|M4",
		Reason: authWriteNeedsRealKeyring,
		Date:   "2026-08-03",
	},
	{
		Cell:   "auth.logout||success|compact|M5",
		Reason: authWriteNeedsRealKeyring,
		Date:   "2026-08-03",
	},
	{
		Cell:   "auth.logout||success|compact|M6",
		Reason: authWriteNeedsRealKeyring,
		Date:   "2026-08-03",
	},
	{
		Cell:   "auth.logout||success|compact|M8",
		Reason: authWriteNeedsRealKeyring,
		Date:   "2026-08-03",
	},
	{
		Cell:   "auth.logout||success|compact|M12",
		Reason: authWriteNeedsRealKeyring,
		Date:   "2026-08-03",
	},
	// Logout's native-store restriction also applies to human success.
	// Config editing now has real terminal captures and no success exemption.
	// Logout's reachable human capture is the dry-run preview; H3/H4 are
	// evaluated there — see cellAxes's CategoryDryRun+ModeHuman branch
	// — so this is not new unevaluated surface, just the same known gap
	// restated for the two axes this slice adds.
	{
		Cell:   "auth.logout||success|human|H3",
		Reason: authWriteNeedsRealKeyring,
		Date:   "2026-08-05",
	},
	{
		Cell:   "auth.logout||success|human|H4",
		Reason: authWriteNeedsRealKeyring,
		Date:   "2026-08-05",
	},
	// Logout's same native-store restriction covers human_color success.
	// Its dry-run capture still exercises H5 through the shared branch.
	{
		Cell:   "auth.logout||success|human_color|H5",
		Reason: authWriteNeedsRealKeyring,
		Date:   "2026-08-06",
	},
}

// validateExemptions audits the exemption table itself: undated,
// unexplained, duplicated, or stale entries (naming a cell the expected
// set no longer derives) are failures — the bounded/dated/audited rule
// made mechanical.
func validateExemptions(list []Exemption, expected map[string]bool) []string {
	var problems []string
	seen := map[string]bool{}
	for _, e := range list {
		switch {
		case e.Cell == "":
			problems = append(problems, "exemption with an empty cell key")
		case seen[e.Cell]:
			problems = append(problems, fmt.Sprintf("exemption %q listed twice", e.Cell))
		case !expected[e.Cell]:
			problems = append(problems, fmt.Sprintf("exemption %q names a cell the expected matrix does not derive — stale entry", e.Cell))
		}
		seen[e.Cell] = true
		// Substance, not just non-whitespace: zero-width and other format
		// runes are invisible in review yet defeat a TrimSpace check.
		if !hasSubstance(e.Reason) {
			problems = append(problems, fmt.Sprintf("exemption %q has no reason", e.Cell))
		}
		switch d, err := time.Parse(time.DateOnly, e.Date); {
		case err != nil:
			problems = append(problems, fmt.Sprintf("exemption %q has no parseable decision date (want YYYY-MM-DD): %q", e.Cell, e.Date))
		case d.After(time.Now()):
			// The date records a review that HAPPENED; a future date is a
			// review that has not.
			problems = append(problems, fmt.Sprintf("exemption %q is dated in the future (%s) — a decision date records a review that already happened", e.Cell, e.Date))
		}
	}
	return problems
}

// verifyMatrix reconciles the expected cell set against the recorded
// results. Coverage-shape and correctness are separate verdicts on
// purpose (design §10): a hole in coverage must never hide behind green
// checks, and a red check must never look like a coverage hole.
func verifyMatrix(expected []ExpectedCell, results []Cell, list []Exemption) []string {
	expectedKeys := map[string]bool{}
	for _, cell := range expected {
		expectedKeys[cell.Key()] = true
	}
	problems := validateExemptions(list, expectedKeys)
	exempt := map[string]bool{}
	for _, e := range list {
		exempt[e.Cell] = true
	}

	resultKeys := map[string]bool{}
	for _, cell := range results {
		if resultKeys[cell.Key()] {
			problems = append(problems, "coverage: cell "+cell.Key()+" recorded twice — one coordinate, one verdict")
		}
		resultKeys[cell.Key()] = true
	}
	for _, cell := range expected {
		if !resultKeys[cell.Key()] && !exempt[cell.Key()] {
			problems = append(problems, "coverage: expected cell "+cell.Key()+" has no recorded result — write its scenario recipe or exempt it with review")
		}
	}
	for _, cell := range results {
		if !expectedKeys[cell.Key()] {
			problems = append(problems, "coverage: recorded cell "+cell.Key()+" is not derivable from metadata + applicability — mislabeled scenario or stale check")
		}
		if cell.Status != "pass" && !exempt[cell.Key()] {
			problems = append(problems, "correctness: cell "+cell.Key()+" is red: "+cell.Detail)
		}
	}
	slices.Sort(problems)
	return problems
}

// matrixBaselinePath locates the committed baseline.
func matrixBaselinePath() (string, error) {
	dir, err := repoRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tests", "audit", "matrix-result.json"), nil
}

// finalizeMatrix runs after every test has recorded its cells: verify
// the Cartesian product, then ratchet against the committed baseline.
// A cell changing state in EITHER direction fails until the baseline is
// regenerated deliberately (AUDIT_UPDATE_MATRIX=1) in the same change.
func finalizeMatrix() string {
	members, problems := liveMembers()
	if len(problems) > 0 {
		// The inventory tests already failed the run by name; piling
		// matrix noise on top would bury them.
		return ""
	}
	results := recordedCells()
	if issues := verifyMatrix(expectedCells(members), results, exemptions); len(issues) > 0 {
		return strings.Join(issues, "\n  ")
	}

	path, err := matrixBaselinePath()
	if err != nil {
		return err.Error()
	}
	current, err := json.MarshalIndent(struct {
		Cells []Cell `json:"cells"`
	}{results}, "", "  ")
	if err != nil {
		return "marshal matrix: " + err.Error()
	}
	current = append(current, '\n')
	if os.Getenv("AUDIT_UPDATE_MATRIX") == "1" {
		// Loud on purpose: this path disables the regression ratchet for
		// the run — a lingering exported var must never do that silently.
		fmt.Fprintf(os.Stderr, "audit: AUDIT_UPDATE_MATRIX=1 — rewriting %s, ratchet not enforced this run\n", path)
		if err := os.WriteFile(path, current, 0o600); err != nil {
			return "write matrix baseline: " + err.Error()
		}
		return ""
	}
	baseline, err := os.ReadFile(path)
	if err != nil {
		return "no committed matrix baseline (regenerate deliberately with AUDIT_UPDATE_MATRIX=1): " + err.Error()
	}
	if string(baseline) != string(current) {
		return "matrix drifted from the committed baseline " + path + " — a cell changed state; if intended, regenerate with AUDIT_UPDATE_MATRIX=1 in the same change"
	}
	return ""
}

// hasSubstance reports whether s carries at least one letter or digit and
// no bidi control characters — the bar a human-readable reason must clear:
// immune to invisible-rune padding, and to bidi overrides that render a
// reason as something other than its bytes.
func hasSubstance(s string) bool {
	substantive := false
	for _, r := range s {
		if (r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069') {
			return false
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			substantive = true
		}
	}
	return substantive
}
