//go:build outputaudit

package audit

import "testing"

func TestUserIdentityGuardRejectsScalarsAndEmptyIDs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		raw  any
		want bool
	}{
		{name: "empty page", raw: []any{}, want: true},
		{name: "account object", raw: []any{map[string]any{"account_id": "fixture-id"}}, want: true},
		{name: "null", raw: nil},
		{name: "bare account", raw: []any{"fixture-id"}},
		{name: "missing id", raw: []any{map[string]any{"key": "KEY-123"}}},
		{name: "empty id", raw: []any{map[string]any{"account_id": ""}}},
		{name: "numeric id", raw: []any{map[string]any{"account_id": 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, msg := checkUserSearchIdentity(tc.raw)
			if ok != tc.want || (!ok && msg == "") {
				t.Fatalf("identity guard accepted invalid data: %v %q", ok, msg)
			}
		})
	}
}
