package guardrails

import (
	"reflect"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

// Pagination state belongs INSIDE an operation's data, never in meta.
//
// Two shipped decisions make data the only place a machine consumer can
// reach: compact prints bare data on success, and --jq filters exactly
// the document the chosen mode would print. A pagination block in meta is
// therefore invisible to a compact caller — which is what agent detection
// selects — and unreachable by a --jq expression, by construction.
//
// The predecessor CLI put it at meta.pagination for single-target reads
// while keyed commands nested it in results[].data. That split was
// harmless when every consumer saw the whole frame; it stops being
// harmless the moment bare-data output exists. This guard exists because
// the convention is otherwise only a comment, and a convention nothing
// enforces is the defect this codebase reproduces most reliably.
//
// It is written before the first paginated command so the shape cannot
// arrive wrong: moving the block later is a breaking change to a
// published envelope.

// paginationTokens are the field-name fragments that mark a pagination
// concept. Matching on the CONCEPT rather than on one exact name is
// deliberate — the guard should fail for next_cursor, isLast, page_info
// or a spelling nobody has thought of yet.
var paginationTokens = []string{
	// "pagination" is listed explicitly: it does NOT contain "page" as a
	// substring check would need ("pagination" has no "page" — the letters
	// run p-a-g-i), so the most likely field name of all slipped straight
	// through the first version of this guard.
	"pagination", "page", "cursor", "islast", "is_last", "total", "offset", "start_at", "startat", "next", "more", "limit",
}

func TestMetaCarriesNoPaginationState(t *testing.T) {
	t.Parallel()
	meta := reflect.TypeFor[envelope.Meta]()
	for i := range meta.NumField() {
		f := meta.Field(i)
		name := strings.ToLower(f.Name + " " + f.Tag.Get("json"))
		for _, token := range paginationTokens {
			if strings.Contains(name, token) {
				t.Errorf("envelope.Meta.%s looks like pagination state (%q); it belongs inside the operation's data, "+
					"because compact prints bare data and --jq filters what the mode prints — in meta it is unreachable to both",
					f.Name, f.Tag.Get("json"))
			}
		}
	}
}

// TestPageStateCarriesTruncation pins the field a silent-undercount bug
// would need. A drain that stops at a cap and says so only in a warning
// leaves a compact or --jq consumer counting a subset while believing it
// counted everything: `--jq '.issues | length'` returns a plausible
// number with nothing in its input contradicting it. Warnings ride
// outside data, so the machine-facing signal has to be here.
func TestPageStateCarriesTruncation(t *testing.T) {
	t.Parallel()
	ps := reflect.TypeFor[envelope.PageState]()
	for _, want := range []string{"truncated", "is_last"} {
		found := false
		for i := range ps.NumField() {
			f := ps.Field(i)
			if tag := f.Tag.Get("json"); strings.Split(tag, ",")[0] == want {
				found = true
				// The published contract says these are JSON booleans. A
				// *bool or a string would still carry the right tag while
				// making a consumer branch on a different shape — absence
				// or a string — so the TYPE is pinned, not just the name.
				if f.Type.Kind() != reflect.Bool {
					t.Errorf("PageState.%s is %s, want a plain bool — the contract publishes a JSON boolean", want, f.Type)
				}
				tag := f.Tag.Get("json")
				// Both must serialize unconditionally: with omitempty a
				// consumer cannot tell "false" from "this build predates
				// the field", which is the ambiguity the flag exists to
				// remove.
				if strings.Contains(tag, "omitempty") {
					t.Errorf("PageState.%s is omitempty; a consumer must be able to branch on false, not on absence", want)
				}
			}
		}
		if !found {
			t.Errorf("envelope.PageState has no %q field — a truncated walk would be indistinguishable from a complete one", want)
		}
	}
}
