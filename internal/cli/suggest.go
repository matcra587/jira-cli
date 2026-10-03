package cli

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/agnivade/levenshtein"
)

// suggestMaxDistance is the largest Levenshtein edit distance at which a
// candidate is still offered as a "did you mean" suggestion. Fixed at 2
// to match cobra's command-suggestion default, so flag and command
// suggestions sit on one contract; wider thresholds offer genuinely
// distinct names and teach the reader to distrust the hint.
const suggestMaxDistance = 2

// suggestMinNameLen is the shortest candidate eligible for a suggestion
// from an OPEN set (flag and command names). Names below this length sit
// within distance 2 of one another, and the candidate pool grows with the
// CLI, so offering them is noise rather than help.
//
// A closed enumerated set is the opposite case — the whole vocabulary is
// known, tiny, and authored together — so SuggestValues waives the floor.
// Without that split, `zsh` and `nu` could never be suggested at all, and
// an agent typing `zshh` would get an error carrying no route forward.
const suggestMinNameLen = 4

// suggestLimit caps how many candidates one failure offers.
const suggestLimit = 2

// Suggest returns up to suggestLimit candidates within suggestMaxDistance
// of input, closest first. The returned names are the raw candidate
// strings — the caller adds any "--" prefix.
//
// This deliberately is not x/strings.Closest: cobra's SuggestionsFor
// feeds the unknown-command path with plain-Levenshtein distance-2
// multi-candidate results, and this keeps unknown-flag suggestions on the
// same contract. Closest returns one best match under a
// length-proportional Damerau threshold — a different payload shape.
func Suggest(input string, candidates []string) []string {
	return suggest(input, candidates, suggestMinNameLen)
}

// SuggestValues is Suggest for a CLOSED enumerated set — a flag's or
// argument's declared values, where the vocabulary is fixed and fully
// known. It waives the minimum-length floor, which exists to keep an
// open, growing pool of identifiers quiet and only suppresses real
// answers here.
func SuggestValues(input string, candidates []string) []string {
	return suggest(input, candidates, 0)
}

func suggest(input string, candidates []string, minNameLen int) []string {
	if input == "" {
		return nil
	}
	type scored struct {
		name string
		dist int
	}
	var matches []scored
	for _, c := range candidates {
		if c == input || utf8.RuneCountInString(c) < minNameLen {
			continue
		}
		if d := levenshtein.ComputeDistance(input, c); d <= suggestMaxDistance {
			matches = append(matches, scored{name: c, dist: d})
		}
	}
	if len(matches) == 0 {
		return nil
	}
	// Stable: candidates at equal distance keep the caller's order
	// (visit order for flags, declaration order for values).
	slices.SortStableFunc(matches, func(a, b scored) int { return a.dist - b.dist })
	out := make([]string, 0, suggestLimit)
	for _, m := range matches {
		out = append(out, m.name)
		if len(out) == suggestLimit {
			break
		}
	}
	return out
}

// foreignFlagEquivalents maps a flag borrowed from another Jira CLI onto
// this CLI's equivalents, offered as suggestions when the unknown flag
// has no near-miss. Only flags with NO equivalent anywhere in this CLI
// belong here: a flag that exists on some command would produce a
// misleading suggestion, and a flag the failing command accepts never
// reaches the unknown-flag path at all (which is why the hidden --plain
// alias is absent — it parses). Entries whose 0.15 equivalents have not
// been rebuilt yet (--jq, --limit, --all, --cursor) join alongside those
// flags; a table row must never point at a flag the binary does not
// carry, and the root test holds every row to that.
var foreignFlagEquivalents = map[string][]string{
	"template":    {"--output=json"},
	"no-headers":  {"--output=json"},
	"no-truncate": {"--output=json"},
	// Another Jira CLI spells the jq filter --gjq. Ours is --jq, close
	// enough that the row only earns its place because the two names are
	// three edits apart, so the near-miss matcher never offers it.
	"gjq": {"--jq"},
}

// ForeignFlagSuggestions resolves a flag name against the foreign-CLI
// table, tolerating leading dashes and case drift in how the parser
// reported it. It returns a fresh slice, or nil for a flag with no known
// equivalent.
func ForeignFlagSuggestions(flag string) []string {
	return slices.Clone(foreignFlagEquivalents[normalizeFlagName(flag)])
}

// foreignFlagTable exposes the table's suggestion values for the root
// test that pins every entry to a flag the live tree actually carries.
func foreignFlagTable() map[string][]string {
	out := make(map[string][]string, len(foreignFlagEquivalents))
	for k, v := range foreignFlagEquivalents {
		out[k] = slices.Clone(v)
	}
	return out
}

// normalizeFlagName strips leading dashes and case drift from a flag
// name as the parser reported it.
func normalizeFlagName(flag string) string {
	return strings.ToLower(strings.TrimLeft(strings.TrimSpace(flag), "-"))
}
