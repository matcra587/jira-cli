//go:build outputaudit

package audit

import (
	"encoding/json"
	"testing"
)

func TestIssueDetailsNativeBoundariesKeepCLIKeysStrict(t *testing.T) {
	for _, op := range []string{"issue.view", "issue.list", "issue.mine"} {
		details := map[string]any{"renderedFields": map[string]any{"summary": "native"}, "futureCamelMeta": false}
		doc := map[string]any{"issue": map[string]any{"key": "KEY-1", "details": details}}
		if op != "issue.view" {
			doc = map[string]any{"issues": []any{map[string]any{"key": "KEY-1", "details": details}}, "search_pages": []any{map[string]any{"nextPageToken": "next", "futureCamelPage": 0}}}
		}
		if bad := firstNonSnakeKey("", doc, passthroughBoundary[op]...); bad != "" {
			t.Fatalf("native metadata boundary missing for %s: %s", op, bad)
		}
		doc["cliCamelField"] = true
		if bad := firstNonSnakeKey("", doc, passthroughBoundary[op]...); bad != "$.cliCamelField" {
			t.Fatalf("metadata boundary weakened the CLI guard for %s: %s", op, bad)
		}
	}
}

func TestIssueDetailsNativeMetadataThroughAuditCaptures(t *testing.T) {
	captures, err := corpus()
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"issue.view", "issue.list", "issue.mine"} {
		for _, mode := range []Mode{ModeJSON, ModeCompact} {
			t.Run(op+"/"+string(mode), func(t *testing.T) {
				capture, ok := captureFor(captures, op, "", CategorySuccess, mode)
				if !ok || capture.ExitCode != 0 || capture.Stderr != "" {
					t.Fatal("expanded metadata success capture is missing or unsuccessful")
				}
				var data map[string]json.RawMessage
				if err := json.Unmarshal([]byte(capture.Stdout), &data); err != nil {
					t.Fatal(err)
				}
				if mode == ModeJSON {
					if err := json.Unmarshal(data["data"], &data); err != nil {
						t.Fatal(err)
					}
				}
				var issues []map[string]json.RawMessage
				if op == "issue.view" {
					var issue map[string]json.RawMessage
					if err := json.Unmarshal(data["issue"], &issue); err != nil {
						t.Fatal(err)
					}
					issues = append(issues, issue)
				} else {
					if err := json.Unmarshal(data["issues"], &issues); err != nil || len(issues) != 2 {
						t.Fatal("detail search lost the existing two-page issue walk")
					}
					var pages []map[string]json.RawMessage
					if err := json.Unmarshal(data["search_pages"], &pages); err != nil || len(pages) != 2 {
						t.Fatal("detail search lost native page metadata")
					}
					for _, page := range pages {
						_, duplicate := page["issues"]
						if duplicate || string(page["schema"]) != "null" || string(page["futureCamelPage"]) != "false" || len(page["nextPageToken"]) == 0 {
							t.Fatalf("page metadata changed or duplicated issues: %v", page)
						}
					}
				}
				for _, issue := range issues {
					var details, future map[string]json.RawMessage
					if json.Unmarshal(issue["details"], &details) != nil || json.Unmarshal(details["futureCamelMeta"], &future) != nil || string(details["schema"]) != "null" || string(future["largeValue"]) != "9007199254740993" {
						t.Fatal("issue metadata lost native null, unknown member, or integer precision")
					}
				}
			})
		}
	}
}
