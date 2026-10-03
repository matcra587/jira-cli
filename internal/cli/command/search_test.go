package command

import (
	"fmt"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestSearchProgressDetectsCursorCycles(t *testing.T) {
	for _, tc := range []struct {
		name   string
		start  string
		tokens []string
	}{
		{name: "same cursor", tokens: []string{"a", "a"}},
		{name: "two cursor cycle", tokens: []string{"a", "b", "a"}},
		{name: "resume cursor cycle", start: "resume", tokens: []string{"a", "resume"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, unbounded := range []bool{false, true} {
				progress := NewSearchProgress(tc.start, unbounded)
				for i, token := range tc.tokens {
					want := ""
					if i == len(tc.tokens)-1 {
						want = envelope.TruncatedReasonRepeatedCursor
					}
					if got := progress.Advance(token, 1); got != want {
						t.Fatalf("unbounded=%t page=%d: reason=%q, want %q", unbounded, i, got, want)
					}
				}
			}
		})
	}
}

func TestSearchProgressBoundsConsecutiveEmptyPages(t *testing.T) {
	progress := NewSearchProgress("", false)
	for i := range MaxSearchEmptyPages {
		want := ""
		if i == MaxSearchEmptyPages-1 {
			want = envelope.TruncatedReasonMaxEmptyPages
		}
		if got := progress.Advance(fmt.Sprintf("empty-%d", i), 0); got != want {
			t.Fatalf("empty page %d: reason=%q, want %q", i+1, got, want)
		}
	}
}

func TestSearchProgressResetsEmptyPageBudget(t *testing.T) {
	progress := NewSearchProgress("", false)
	for i := range 2*MaxSearchEmptyPages - 1 {
		returned := 0
		if i == MaxSearchEmptyPages-1 {
			returned = 1
		}
		if got := progress.Advance(fmt.Sprintf("page-%d", i), returned); got != "" {
			t.Fatalf("sparse but progressing search stopped at page %d: %s", i+1, got)
		}
	}
}

func TestSearchProgressUnboundedStillRequiresCursor(t *testing.T) {
	progress := NewSearchProgress("", true)
	for i := range 2 * MaxSearchEmptyPages {
		if got := progress.Advance(fmt.Sprintf("page-%d", i), 0); got != "" {
			t.Fatalf("unbounded sparse search stopped at page %d: %s", i+1, got)
		}
	}
	if got := progress.Advance("", 0); got != envelope.TruncatedReasonMissingCursor {
		t.Fatalf("missing cursor reason=%q, want %q", got, envelope.TruncatedReasonMissingCursor)
	}
}
