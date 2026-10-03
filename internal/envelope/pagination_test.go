package envelope_test

import (
	"encoding/json"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

// TestCursorPaginationJSONKeysAreSnakeCase pins CursorPagination's wire
// shape directly, mirroring TestOffsetPaginationJSONKeysAreSnakeCase for
// its sibling — even though nothing calls CursorPagination yet (see its
// doc comment), the shape it will hand `issue list` is worth pinning now
// rather than only when that command lands.
func TestCursorPaginationJSONKeysAreSnakeCase(t *testing.T) {
	p := envelope.CursorPagination{NextPageToken: "abc123", IsLast: false, Returned: 25}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"next_page_token":"abc123","is_last":false,"returned":25}`
	if string(b) != want {
		t.Errorf("marshal = %s, want %s", b, want)
	}
}

// TestCursorPaginationOmitsEmptyNextPageToken proves the last page —
// where Jira sends no token at all — drops next_page_token from the
// wire rather than emitting an empty string a caller might mistake for
// a usable (if odd) token. is_last still serializes unconditionally
// (false must be visible, not just absent), which is exactly the
// asymmetry CursorPagination's own field tags encode.
func TestCursorPaginationOmitsEmptyNextPageToken(t *testing.T) {
	p := envelope.CursorPagination{IsLast: true, Returned: 3}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"is_last":true,"returned":3}`
	if string(b) != want {
		t.Errorf("marshal = %s, want %s", b, want)
	}
}
