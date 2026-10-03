//go:build flagmatrix

package matrix

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// The proof registry. A case joins it by RUNNING: Prove is called from
// inside the case after its assertions, so a case that is deleted,
// skipped, or filtered out never records, and its pair drops back into
// the missing set with no bookkeeping. The registry holds evidence
// strings, not booleans, so `go test -v` and the gate's census can show
// WHAT each case claims to have demonstrated — a case that proves
// nothing has to say so in its own words, where a reviewer sees it.
//
// Honest limit, stated rather than papered over: the registry cannot
// mechanically verify that a case would fail if its flag were ignored.
// That property is enforceable only the way the audit suite enforces
// it — plant the defect, watch the case fail by name — and by review
// of the evidence string. What IS mechanical: a proof must name a pair
// the live tree actually has, must carry a non-empty evidence
// sentence, and must run to be counted.
var (
	regMu  sync.Mutex
	proofs = map[string]string{} // owner pair key → evidence
)

// Prove records that the calling case demonstrated the flag at the
// given command path has its documented effect. evidence is one
// sentence naming the observable the case asserted (e.g. "stub saw
// maxResults=5 in the list request query").
//
// The pair may be named at any path the flag appears on; the proof is
// credited to the DECLARING command, because that is the single
// registration being exercised (see ClassInherited).
func Prove(t *testing.T, commandPath, flagName, evidence string) {
	t.Helper()
	if err := record(commandPath, flagName, evidence); err != nil {
		t.Fatalf("flagmatrix: %v", err)
	}
}

// record is Prove's error-returning core, split out so the self-tests
// can assert its refusals without failing themselves.
func record(commandPath, flagName, evidence string) error {
	if strings.TrimSpace(evidence) == "" {
		return fmt.Errorf("proof for %s carries no evidence — say what the case asserted, or the registry is a boolean in disguise", pairKey(commandPath, flagName))
	}
	pair, ok := findPair(commandPath, flagName)
	if !ok {
		return fmt.Errorf("proof names %s, which the live tree does not have — the flag moved, was renamed, or the path is wrong", pairKey(commandPath, flagName))
	}
	regMu.Lock()
	defer regMu.Unlock()
	proofs[pair.OwnerKey()] = evidence
	return nil
}

func findPair(commandPath, flagName string) (Pair, bool) {
	for _, p := range Enumerate() {
		if p.Path == commandPath && p.Flag == flagName {
			return p, true
		}
	}
	return Pair{}, false
}

// provedEvidence snapshots the registry.
func provedEvidence() map[string]string {
	regMu.Lock()
	defer regMu.Unlock()
	snapshot := make(map[string]string, len(proofs))
	for k, v := range proofs {
		snapshot[k] = v
	}
	return snapshot
}

// missingPairs is the gate's core join: every required pair no ran case
// proved, by name.
func missingPairs(pairs []Pair, proved map[string]string) []Pair {
	var missing []Pair
	for _, p := range requiredPairs(pairs) {
		if _, ok := proved[p.OwnerKey()]; !ok {
			missing = append(missing, p)
		}
	}
	return missing
}
