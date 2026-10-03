//go:build outputaudit

package audit

import "testing"

func TestBoardsIdentityRejectsNonIdentityRows(t *testing.T) {
	t.Parallel()
	for _, row := range []any{42, "42", nil, map[string]any{}, map[string]any{"id": "42"}, map[string]any{"id": float64(0)}, map[string]any{"id": 1.5}} {
		if ok, _ := checkBoardsIdentity([]any{row}); ok {
			t.Fatalf("invalid board identity accepted: %#v", row)
		}
	}
	if ok, detail := checkBoardsIdentity([]any{map[string]any{"id": float64(42)}}); !ok {
		t.Fatalf("numeric identity refused: %s", detail)
	}
}
