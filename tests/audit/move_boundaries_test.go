//go:build outputaudit

package audit

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

func TestMoveTaskIdentityRequiresObjectWithID(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		data string
		want bool
	}{
		{`{"task":{"id":"100"}}`, true},
		{`{"task":{"id":"opaque task"}}`, true},
		{`{"task":"100"}`, false},
		{`{"task":[{"id":"100"}]}`, false},
		{`{"task":{"key":"KEY-1"}}`, false},
		{`{"task":{"id":100}}`, false},
		{`{"task":{"id":" "}}`, false},
		{`{"task":{"id":null}}`, false},
	} {
		t.Run(tc.data, func(t *testing.T) {
			t.Parallel()
			ok, detail := checkSuccessIdentity(Capture{Mode: ModeCompact, Stdout: tc.data}, command.Meta{Op: "issue.move.status", Identity: true})
			if ok != tc.want {
				t.Fatalf("bulk task identity verdict = %t, want %t: %s", ok, tc.want, detail)
			}
		})
	}
}

func TestMoveNativeBoundariesKeepCLIKeysStrict(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"issue.move", "issue.move.status"} {
		doc := map[string]any{"progress": map[string]any{"taskId": "100", "processedAccessibleIssues": []int{101}, "futureCamelField": false}}
		if op == "issue.move" {
			doc["payload"] = map[string]any{"sendBulkNotification": false, "targetToSourcesMapping": map[string]any{"DEST,10001": map[string]any{"issueIdsOrKeys": []string{"KEY-1"}}}}
			doc["receipt"] = map[string]any{"taskId": "100", "futureCamelField": false}
		}
		if bad := firstNonSnakeKey("", doc, passthroughBoundary[op]...); bad != "" {
			t.Fatalf("move native boundary missing: %s %s", op, bad)
		}
		doc["cliCamelField"] = true
		if bad := firstNonSnakeKey("", doc, passthroughBoundary[op]...); bad != "$.cliCamelField" {
			t.Fatalf("move boundary weakened CLI key guard: %s %s", op, bad)
		}
	}
}
