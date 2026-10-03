//go:build flagmatrix

package matrix

import (
	"flag"
	"fmt"
	"io"
	"os"
	"testing"
)

// The coverage gate lives in TestMain, AFTER m.Run(): coverage is
// recorded by cases running, so the join can only be computed once
// every case has had its chance to record. A gate written as an
// ordinary test would depend on test ordering — one -shuffle away from
// judging an empty registry.
func TestMain(m *testing.M) {
	flag.Parse()
	code := m.Run()
	if code != 0 {
		os.Exit(code)
	}
	// A filtered run executes a subset of cases by design; judging the
	// registry after one would report every unselected pair as missing.
	// The gate only counts on full runs — which is what CI does.
	if filterActive() {
		_, _ = fmt.Fprintln(os.Stderr, "flagmatrix: coverage gate skipped — filtered run (-run/-skip selects a subset, so the registry is knowingly partial)")
		os.Exit(0)
	}
	os.Exit(gate(os.Stdout))
}

func filterActive() bool {
	for _, name := range []string{"test.run", "test.skip"} {
		if f := flag.Lookup(name); f != nil && f.Value.String() != "" {
			return true
		}
	}
	return false
}

// Non-vacuity bounds on the REQUIRED (owned) pair count. A walker bug
// that silently enumerates nothing must read as failure, never as
// total coverage — this codebase has been bitten repeatedly by empty
// results that look like success. The band is generous on purpose: it
// exists to catch collapse (a broken walk) and explosion (a walk
// suddenly counting inherited pairs as owned), not to be maintained on
// every flag addition.
const (
	minRequiredPairs = 150
	maxRequiredPairs = 1200
)

// gateAnchors are pairs the tree is known to carry, one per class the
// enumeration must not silently lose: a visible root persistent flag,
// a HIDDEN compatibility alias (the rot-prone surface this matrix
// exists for), and an inherited appearance of a root flag at a leaf.
// If one moves, the walk found a different tree than the CLI ships —
// update the anchor only after confirming the surface really changed.
var gateAnchors = []Pair{
	{Path: "jira", Flag: "output", Class: ClassOwned, Hidden: false, OwnerPath: "jira"},
	{Path: "jira issue transition", Flag: "status", Class: ClassOwned, Hidden: true, OwnerPath: "jira issue transition"},
	{Path: "jira issue list", Flag: "output", Class: ClassInherited, Hidden: false, OwnerPath: "jira"},
}

// gate computes the join and prints the census. Returns the process
// exit code: 0 only when the enumeration is sane AND no required pair
// is unproved.
func gate(w io.Writer) int {
	pairs := Enumerate()
	required := requiredPairs(pairs)
	proved := provedEvidence()
	missing := missingPairs(pairs, proved)

	problems := sanityProblems(pairs, required)
	printCensus(w, pairs, required, proved)

	for _, p := range problems {
		_, _ = fmt.Fprintf(w, "SANITY: %s\n", p)
	}
	if len(problems) > 0 {
		_, _ = fmt.Fprintln(w, "flagmatrix: FAIL — the enumeration itself is not trustworthy; fix the walk before reading the coverage number")
		return 1
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintf(w, "flagmatrix: FAIL — %d of %d required pairs have no proving case (each listed above by name)\n", len(missing), len(required))
		return 1
	}
	_, _ = fmt.Fprintf(w, "flagmatrix: PASS — all %d required pairs proved by a ran case\n", len(required))
	return 0
}

// sanityProblems is the vacuity guard on the enumeration.
func sanityProblems(pairs, required []Pair) []string {
	var problems []string
	if len(pairs) == 0 {
		return []string{"enumeration is EMPTY — the walk found no tree; nothing below can be trusted"}
	}
	if len(required) < minRequiredPairs || len(required) > maxRequiredPairs {
		problems = append(problems, fmt.Sprintf("required pair count %d is outside the sane band [%d, %d] — the walk collapsed or exploded", len(required), minRequiredPairs, maxRequiredPairs))
	}
	hidden := 0
	for _, p := range pairs {
		if p.Hidden && p.Class == ClassOwned {
			hidden++
		}
	}
	if hidden == 0 {
		problems = append(problems, "enumeration carries zero hidden owned flags — the compatibility aliases vanished from the walk, which is the exact rot this matrix exists to catch")
	}
	for _, want := range gateAnchors {
		if !hasPair(pairs, want) {
			problems = append(problems, fmt.Sprintf("anchor %s (%s, hidden=%t, owner %q) is not in the enumeration — the walk is not seeing the real tree", want.Key(), want.Class, want.Hidden, want.OwnerPath))
		}
	}
	return problems
}

func hasPair(pairs []Pair, want Pair) bool {
	for _, p := range pairs {
		if p == want {
			return true
		}
	}
	return false
}

// printCensus reports the numbers phase 2 is planned from: the split
// pair counts, the corpus cross-reference, and the missing set BY NAME
// — a gate that says "312/400" and stops is useless to the person who
// has to close it.
func printCensus(w io.Writer, pairs, required []Pair, proved map[string]string) {
	counts := map[Class]int{}
	hiddenOwned := 0
	distinct := map[string]bool{}
	for _, p := range pairs {
		counts[p.Class]++
		distinct[p.Flag] = true
		if p.Hidden && p.Class == ClassOwned {
			hiddenOwned++
		}
	}
	_, _ = fmt.Fprintf(w, "flagmatrix census: %d pairs total — %d owned (%d hidden), %d inherited, %d framework (--help); %d distinct flag names; %d required\n",
		len(pairs), counts[ClassOwned], hiddenOwned, counts[ClassInherited], counts[ClassFramework], len(distinct), len(required))

	exercised, err := corpusExercised()
	if err != nil {
		_, _ = fmt.Fprintf(w, "corpus cross-reference unavailable: %v\n", err)
	}

	_, _ = fmt.Fprintf(w, "proved by a ran case: %d of %d required\n", len(proved), len(required))
	for _, p := range required {
		key := p.OwnerKey()
		switch evidence, ok := proved[key]; {
		case ok:
			_, _ = fmt.Fprintf(w, "  PROVED  %-46s %s\n", key, evidence)
		case len(exercised[key]) > 0:
			_, _ = fmt.Fprintf(w, "  MISSING %-46s (exercised, unproved: corpus %v runs it without asserting its effect)\n", key, exercised[key])
		default:
			_, _ = fmt.Fprintf(w, "  MISSING %-46s (never exercised anywhere mechanical)\n", key)
		}
	}
}
