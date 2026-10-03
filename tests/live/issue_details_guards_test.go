//go:build live

package live

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLiveIssueDetailsRejectsFalseAcceptance(t *testing.T) {
	const native = `{"id":"10001","key":"ABC-1","fields":{},"schema":null,"futureCamel":9007199254740993}`
	for _, tc := range []struct {
		name, receipt string
		want          bool
	}{
		{"complete", `{"schema":null,"futureCamel":9007199254740993}`, true},
		{"missing null", `{"futureCamel":9007199254740993}`, false},
		{"lost precision", `{"schema":null,"futureCamel":9007199254740992}`, false},
		{"duplicate identity", `{"key":"ABC-1","schema":null,"futureCamel":9007199254740993}`, false},
		{"missing metadata", `{}`, false},
		{"trailing data", `{"schema":null,"futureCamel":9007199254740993} {}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nativeMetadataMatches(json.RawMessage(tc.receipt), json.RawMessage(native), []string{"id", "key", "fields"}); got != tc.want {
				t.Fatalf("native metadata guard=%t, want %t", got, tc.want)
			}
		})
	}
}

func TestLiveIssueDetailsOperationTokensRemainNarrow(t *testing.T) {
	const native = `{"operations":{"linkGroups":[{"links":[{"href":"https://example.test/action?id=1&atl_token=first&action=watch#native"}]}]},"futureCamel":null}`
	for _, tc := range []struct {
		name, receipt string
		want          bool
	}{
		{"rotated action token", strings.ReplaceAll(native, "atl_token=first", "atl_token=second"), true},
		{"changed action", strings.ReplaceAll(native, "action=watch", "action=delete"), false},
		{"changed identity", strings.ReplaceAll(native, "id=1", "id=2"), false},
		{"changed path", strings.ReplaceAll(native, "/action?", "/other?"), false},
		{"changed host", strings.ReplaceAll(native, "example.test", "other.test"), false},
		{"changed fragment", strings.ReplaceAll(native, "#native", "#other"), false},
		{"missing token", strings.ReplaceAll(native, "&atl_token=first", ""), false},
		{"empty token", strings.ReplaceAll(native, "atl_token=first", "atl_token="), false},
		{"duplicate token", strings.ReplaceAll(native, "atl_token=first", "atl_token=first&atl_token=second"), false},
		{"malformed token escape", strings.ReplaceAll(native, "atl_token=first", "atl_token=%invalid"), false},
		{"missing null", strings.ReplaceAll(native, `,"futureCamel":null`, ""), false},
		{"token outside operations", `{"operations":null,"href":"https://example.test/action?atl_token=second"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nativeMetadataMatches(json.RawMessage(tc.receipt), json.RawMessage(native), nil); got != tc.want {
				t.Fatalf("operation metadata guard=%t, want %t", got, tc.want)
			}
		})
	}
	if nativeMetadataMatches(json.RawMessage(`{"href":"https://example.test/action?atl_token=second"}`), json.RawMessage(`{"href":"https://example.test/action?atl_token=first"}`), nil) {
		t.Fatal("per-request tolerance escaped the native operations boundary")
	}
}
