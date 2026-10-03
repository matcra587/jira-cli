//go:build outputaudit

package audit

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

func TestNativePreviewTimestampCheckKeepsLayoutGuard(t *testing.T) {
	t.Parallel()
	const stamp = "2026-01-02T12:30:00"
	const payload = `Payload: {"started":"2026-01-02T12:30:00.000+0000"}`
	meta := command.Meta{WritesJira: true, Passthrough: true}
	for _, tc := range []struct {
		name, text string
		category   Category
		meta       command.Meta
		wantRaw    bool
	}{
		{"native preview", payload, CategoryDryRun, meta, false},
		{"surrounding layout", payload + "\nStarted: " + stamp, CategoryDryRun, meta, true},
		{"malformed payload", payload + " garbage", CategoryDryRun, meta, true},
		{"unlabeled JSON", `{"started":"` + stamp + `"}`, CategoryDryRun, meta, true},
		{"read", payload, CategorySuccess, meta, true},
		{"no passthrough", payload, CategoryDryRun, command.Meta{WritesJira: true}, true},
		{"not a Jira write", payload, CategoryDryRun, command.Meta{Passthrough: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := rawISOTimestampRe.MatchString(humanTimestampText(tc.text, tc.category, tc.meta))
			if got != tc.wantRaw {
				t.Fatalf("native preview exception changed timestamp guard: found raw=%t want=%t", got, tc.wantRaw)
			}
		})
	}
}

func TestWorklogNativeBoundariesKeepCLIKeysStrict(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"worklog.add", "worklog.list"} {
		boundary := passthroughBoundary[op]
		var doc any
		if op == "worklog.add" {
			doc = map[string]any{"payload": map[string]any{"timeSpentSeconds": 60}, "results": []any{map[string]any{"data": map[string]any{"worklog": map[string]any{"id": "100", "fields": map[string]any{"timeSpentSeconds": 60, "tenantCamelField": false}}}}}}
		} else {
			doc = map[string]any{"results": []any{map[string]any{"data": map[string]any{"worklogs": []any{map[string]any{"id": "100", "fields": map[string]any{"timeSpentSeconds": 60, "tenantCamelField": false}}}}}}}
		}
		if bad := firstNonSnakeKey("", doc, boundary...); bad != "" {
			t.Fatalf("native boundary missing: %s %s", op, bad)
		}
		doc.(map[string]any)["cliCamelField"] = true
		if bad := firstNonSnakeKey("", doc, boundary...); bad != "$.cliCamelField" {
			t.Fatalf("native boundary weakened CLI naming guard: %s %s", op, bad)
		}
	}
}
