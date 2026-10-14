package guardrails

import (
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
)

// When a keyed command's keys ALL fail, one of them has to represent the
// run, and command.FailureSeverity picks it by class — an explicit ladder
// over taxonomy codes, because exit codes are an enumeration rather than
// a scale.
//
// A map lookup returns 0 for a code it has never heard of. That is the
// failure this guard exists for: add a taxonomy code — a new Jira status
// mapping, a new local refusal — and it silently ranks at the bottom, so
// a mixed run that hit it and one not-found reports the not-found. No
// test fails, nothing warns, and the CLI exits on the less important of
// two failures. The ladder is complete precisely so that "unranked"
// cannot happen; this join is what keeps it complete.
//
// Rank 0 is a legitimate answer — most codes cannot arise per key at all
// (flag parsing, jq compilation, writing the result) — so this checks
// membership, never the value. What it refuses is silence: a code with
// no entry, or an entry for a code the taxonomy does not have.

func TestEveryTaxonomyCodeHasAKeyedFailureRank(t *testing.T) {
	t.Parallel()
	ranks := command.KeyFailureRanks()
	codes := cli.Codes()

	if len(codes) == 0 {
		t.Fatal("the taxonomy enumerated no codes — cli.Codes() lost its registry, and this join is checking nothing")
	}

	for _, code := range codes {
		if _, ranked := ranks[code]; !ranked {
			t.Errorf("taxonomy code %q has no rank in command.KeyFailureRanks — it would sort to the bottom of a keyed run's failures silently. "+
				"Add it: 0 if it cannot arise per key, higher if a caller should see it over a plain per-key failure", code)
		}
	}
	for code := range ranks {
		if !slices.Contains(codes, code) {
			t.Errorf("command.KeyFailureRanks ranks %q, which is not a taxonomy code — a stale entry (renamed or removed), so the rank it states applies to nothing", code)
		}
	}
}

// TestKeyedFailureLadderOrdersTheClassesItClaimsTo pins the ordering
// itself, not just the coverage: the doc comment argues auth outranks a
// missing issue and an interrupted run outranks everything, and a table
// whose numbers drifted from that argument would still pass the join
// above.
func TestKeyedFailureLadderOrdersTheClassesItClaimsTo(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ higher, lower string }{
		{"canceled", "jira_unauthorized"},
		{"timeout", "jira_unauthorized"},
		{"jira_unauthorized", "jira_rate_limited"},
		{cli.CodeAuthMissing, "jira_rate_limited"},
		{cli.CodeAuthStoreUnavailable, "jira_rate_limited"},
		{cli.CodeCanceled, cli.CodeAuthStoreUnavailable},
		{"timeout", cli.CodeAuthStoreUnavailable},
		{"jira_rate_limited", "jira_server_error"},
		{"jira_server_error", "jira_not_found"},
		{"jira_not_found", cli.CodeFlagValueInvalid},
		{cli.CodeIssueFieldPreflightFailed, cli.CodeFlagValueInvalid},
		{cli.CodeIssueMetadataResponseInvalid, cli.CodeIssueFieldPreflightFailed},
	} {
		if command.FailureSeverity(tc.higher) <= command.FailureSeverity(tc.lower) {
			t.Errorf("FailureSeverity(%q)=%d does not outrank %q=%d — the ladder's numbers no longer say what its doc comment argues",
				tc.higher, command.FailureSeverity(tc.higher), tc.lower, command.FailureSeverity(tc.lower))
		}
	}
}
