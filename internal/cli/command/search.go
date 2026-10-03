package command

import (
	"github.com/gechr/x/set"

	"github.com/matcra587/jira-cli/internal/envelope"
)

// MaxSearchEmptyPages bounds consecutive empty, advancing search pages.
// A sparse Jira page is not exhaustion, but unlimited empty pages would leave
// a bounded issue count making unlimited requests. --all --unbounded opts out.
const MaxSearchEmptyPages = 50

// SearchProgress tracks cursor progress for issue reads and target resolution.
// Construct it with NewSearchProgress; its zero value is not initialized.
// Call Advance whenever Jira has not reported its last page, including when a
// bounded list has enough issues but still publishes a resume cursor.
// A stop reason is an incomplete walk, never success at exhausting the search.
// Selectors must refuse it before performing writes.
type SearchProgress struct {
	seen      set.Set[string]
	empty     int
	unbounded bool
}

// NewSearchProgress includes the initial request's cursor in cycle detection.
func NewSearchProgress(start string, unbounded bool) *SearchProgress {
	return &SearchProgress{seen: set.New(start), unbounded: unbounded}
}

// Advance records a nonfinal page and returns why the walk cannot continue.
// Cursor identity, not issue count, proves progress through sparse pages.
func (p *SearchProgress) Advance(next string, returned int) string {
	if next == "" {
		return envelope.TruncatedReasonMissingCursor
	}
	if p.seen.Contains(next) {
		return envelope.TruncatedReasonRepeatedCursor
	}
	p.seen.Add(next)
	if returned > 0 {
		p.empty = 0
		return ""
	}
	p.empty++
	if !p.unbounded && p.empty >= MaxSearchEmptyPages {
		return envelope.TruncatedReasonMaxEmptyPages
	}
	return ""
}
