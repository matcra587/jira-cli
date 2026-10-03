//go:build flagmatrix

package matrix

import (
	"slices"
	"strings"
	"testing"
)

// These are the harness's own self-tests: they defend the enumerator
// and the registry, not any flag. They run under m.Run() like any
// case, and the gate judges the registry afterwards — which is why the
// one test that records a real proof must also erase it (see
// TestRecordCreditsDeclaringCommand).

func TestEnumerationIsDeterministic(t *testing.T) {
	first := enumerate(liveTree())
	second := enumerate(liveTree())
	if !slices.Equal(first, second) {
		t.Fatal("two walks of the same tree enumerated different pair sets — an unstable enumeration makes every coverage number unrepeatable")
	}
}

// TestInheritedPairsHaveOwnedDeclarations pins the classification
// invariant the proof-crediting rests on: every inherited pair's owner
// path carries the same flag as an OWNED pair, so crediting a proof to
// the owner can never credit a pair the gate does not require.
func TestInheritedPairsHaveOwnedDeclarations(t *testing.T) {
	pairs := Enumerate()
	for _, p := range pairs {
		if p.Class != ClassInherited {
			continue
		}
		owner := Pair{Path: p.OwnerPath, Flag: p.Flag, Class: ClassOwned, Hidden: p.Hidden, OwnerPath: p.OwnerPath}
		if !hasPair(pairs, owner) {
			t.Errorf("inherited pair %s claims owner %q, but %s is not an owned pair there", p.Key(), p.OwnerPath, owner.Key())
		}
	}
}

// TestHiddenAliasesAreEnumerated pins the reason this package uses
// pflag's VisitAll rather than clib's FlagMeta: the hidden
// compatibility aliases are raw pflag registrations with no clib
// metadata, and a metadata-driven walk would silently drop all of
// them. The transition --status alias stands anchor for the class.
func TestHiddenAliasesAreEnumerated(t *testing.T) {
	pair, ok := findPair("jira issue transition", "status")
	if !ok {
		t.Fatal("the enumeration does not carry jira issue transition --status — either the hidden alias table changed or the walk lost hidden flags entirely")
	}
	if !pair.Hidden || pair.Class != ClassOwned {
		t.Fatalf("jira issue transition --status enumerated as (class %s, hidden %t), want an owned hidden alias", pair.Class, pair.Hidden)
	}
}

func TestRecordRefusesUnknownPair(t *testing.T) {
	err := record("jira issue list", "definitely-not-a-flag", "some evidence")
	if err == nil {
		t.Fatal("recording a proof for a pair the tree does not have was accepted — a typoed path or flag would silently prove nothing")
	}
	if !strings.Contains(err.Error(), "jira issue list --definitely-not-a-flag") {
		t.Errorf("refusal %q does not name the pair — the case author needs the name to fix it", err)
	}
}

func TestRecordRefusesEmptyEvidence(t *testing.T) {
	if err := record("jira", "output", "   "); err == nil {
		t.Fatal("recording a proof with blank evidence was accepted — the registry would be a boolean in disguise")
	}
	if _, proved := provedEvidence()["jira --output"]; proved {
		t.Fatal("the refused proof still landed in the registry")
	}
}

// TestRecordCreditsDeclaringCommand pins the inherited-crediting rule:
// a proof named at a leaf lands on the declaring command's pair,
// because that is the single registration being exercised.
func TestRecordCreditsDeclaringCommand(t *testing.T) {
	// This records a REAL pair into the live registry, which the gate
	// judges after m.Run() — left in place it would mark jira --output
	// proved without any case proving it, so it is erased on cleanup.
	t.Cleanup(func() {
		regMu.Lock()
		defer regMu.Unlock()
		delete(proofs, "jira --output")
	})
	if err := record("jira issue list", "output", "self-test fixture, erased on cleanup"); err != nil {
		t.Fatalf("recording a proof through an inherited appearance failed: %v", err)
	}
	if _, ok := provedEvidence()["jira --output"]; !ok {
		t.Fatal("a proof recorded at jira issue list --output did not credit the declaring pair jira --output")
	}
	if _, ok := provedEvidence()["jira issue list --output"]; ok {
		t.Fatal("the proof landed on the inherited appearance itself — the required set is owner-keyed, so this proof would satisfy nothing")
	}
}
