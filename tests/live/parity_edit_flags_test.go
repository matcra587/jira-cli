//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func parityEditFlagPreview(raw []byte, lane, mode string, payload map[string]any, markdown, notify bool) bool {
	var data map[string]any
	switch lane {
	case "before":
		fields := payload["fields"]
		if mode == "compact" {
			// The pinned legacy compact renderer drops null map members.
			// Current native payloads must retain an explicit unassignment.
			copy := make(map[string]any)
			for name, value := range payload["fields"].(map[string]any) {
				if name != "assignee" || value != nil {
					copy[name] = value
				}
			}
			fields = copy
		}
		data = map[string]any{"issue": map[string]any{"key": "KEY-1"}, "dry_run": true, "validated_remotely": false, "fields": fields}
	case "after":
		route := ""
		if markdown {
			route = "markdown"
		}
		data = map[string]any{"dry_run": true, "notify": notify, "payload": payload, "description_route": route, "results": []any{map[string]any{"issue": map[string]any{"key": "KEY-1"}, "ok": true, "data": map[string]any{}}}}
	default:
		return false
	}
	expected, err := json.Marshal(data)
	return err == nil && parityAuthoringJSON(raw, expected)
}

func TestParityActualBinaryEditConveniencePreviews(t *testing.T) {
	lanes := []parityLane{{Name: "before", Binary: os.Getenv("JIRA_PARITY_BEFORE_BINARY"), Digest: os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")}}
	if lanes[0].Binary == "" || lanes[1].Binary == "" {
		t.Skip("requires both selected actual binaries")
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	t.Cleanup(func() { saveParityLocal(t, "edit-flags-preview", evidence) })
	for _, lane := range lanes {
		for _, mode := range []string{"json", "compact"} {
			for _, scenario := range []string{"summary", "assignee", "unassign", "inline-markdown", "file-markdown", "current-scalars", "current-add", "current-remove", "current-mixed"} {
				if lane.Name == "before" && strings.HasPrefix(scenario, "current-") {
					continue
				}
				t.Run(lane.Name+"/"+mode+"/"+scenario, func(t *testing.T) {
					if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
							t.Error(err)
						}
					})
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						calls.Add(1)
						http.Error(w, "local edit preview entered HTTP", http.StatusBadGateway)
					}))
					defer server.Close()
					home := t.TempDir()
					config := parityCachePublicConfig(t, home, lane.Name, server.URL)
					markdownPath := filepath.Join(home, "edit spaces 古い '$;&.md")
					if err := os.WriteFile(markdownPath, []byte("**Owned café**"), 0o600); err != nil {
						t.Fatal(err)
					}
					args := []string{"issue", "edit", "KEY-1", "--dry-run"}
					fields := map[string]any{}
					payload := map[string]any{"fields": fields}
					markdown, notify := false, true
					switch scenario {
					case "summary":
						args = append(args, "--summary=Owned café <&>")
						fields["summary"] = "Owned café <&>"
					case "assignee":
						args = append(args, "--assignee=opaque-account")
						fields["assignee"] = map[string]any{"accountId": "opaque-account"}
					case "unassign":
						flag := "--unassign"
						if lane.Name == "before" {
							flag = "--assignee=none"
						}
						args = append(args, flag)
						fields["assignee"] = nil
					case "inline-markdown", "file-markdown":
						markdown = true
						fields["description"] = json.RawMessage(parityAuthoringDocument)
						if scenario == "inline-markdown" {
							args = append(args, "--markdown=**Owned café**")
						} else {
							args = append(args, "--markdown-file", markdownPath)
						}
					case "current-scalars":
						notify = false
						args = append(args, "--summary=Owned café <&>", "--type=Task", "--parent=KEY-2", "--priority=High", "--reporter=opaque-reporter", "--due=2026-12-31", "--original-estimate=1h", "--remaining-estimate=30m", "--notify=false")
						fields["summary"], fields["issuetype"] = "Owned café <&>", map[string]any{"name": "Task"}
						fields["parent"], fields["priority"], fields["reporter"] = map[string]any{"key": "KEY-2"}, map[string]any{"name": "High"}, map[string]any{"accountId": "opaque-reporter"}
						fields["duedate"], fields["timetracking"] = "2026-12-31", map[string]any{"originalEstimate": "1h", "remainingEstimate": "30m"}
					case "current-add", "current-remove", "current-mixed":
						prefix, operation := "--", "add"
						if scenario == "current-remove" {
							prefix, operation = "--remove-", "remove"
						}
						args = append(args, prefix+`label="alpha,beta"`, prefix+"label=duplicate", prefix+"label=duplicate", prefix+"component=123", prefix+"fix-version=123", prefix+"affects-version=456")
						delete(payload, "fields")
						update := map[string]any{"labels": []any{map[string]any{operation: "alpha,beta"}, map[string]any{operation: "duplicate"}, map[string]any{operation: "duplicate"}}, "components": []any{map[string]any{operation: map[string]any{"name": "123"}}}, "fixVersions": []any{map[string]any{operation: map[string]any{"name": "123"}}}, "versions": []any{map[string]any{operation: map[string]any{"name": "456"}}}}
						if scenario == "current-mixed" {
							args = append(args, "--remove-label=old", "--remove-component=old", "--remove-fix-version=old", "--remove-affects-version=old")
							for _, field := range []string{"labels", "components", "fixVersions", "versions"} {
								var value any = map[string]any{"name": "old"}
								if field == "labels" {
									value = "old"
								}
								update[field] = append(update[field].([]any), map[string]any{"remove": value})
							}
						}
						payload["update"] = update
					}
					before := parityLocalTree(t, home)
					result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE="}, args...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load() != 0 {
						t.Fatalf("edit preview failed or entered HTTP: exit %d; stdout %s; stderr %s", result.Exit, result.Stdout, result.Stderr)
					}
					raw := result.Stdout
					if mode == "json" {
						parityAuthoringCleanData(t, raw, lane.Name, "issue.edit")
						raw = parityOriginalFrameData(raw)
					}
					if !parityEditFlagPreview(raw, lane.Name, mode, payload, markdown, notify) {
						t.Fatal("edit flag preview differs from complete independent native fields/update and lane receipts")
					}
					if !reflect.DeepEqual(before, parityLocalTree(t, home)) {
						t.Fatal("bare edit preview changed its input, public configuration or local storage")
					}
					id := fmt.Sprintf("I013.edit.flags.%s.%s", scenario, mode)
					evidence.Checks[id] = append(evidence.Checks[id], lane.Name)
				})
			}
		}
	}
}

func TestParityEditFlagPreviewRequiresCompleteFields(t *testing.T) {
	payload := map[string]any{"fields": map[string]any{"summary": "Owned"}}
	valid := `{"dry_run":true,"notify":true,"payload":{"fields":{"summary":"Owned"}},"description_route":"","results":[{"issue":{"key":"KEY-1"},"ok":true,"data":{}}]}`
	if !parityEditFlagPreview([]byte(valid), "after", "json", payload, false, true) {
		t.Fatal("independent complete preview refused")
	}
	for _, field := range []string{"dry_run", "notify", "payload", "description_route", "results"} {
		t.Run(field, func(t *testing.T) {
			var changed map[string]json.RawMessage
			if err := json.Unmarshal([]byte(valid), &changed); err != nil {
				t.Fatal(err)
			}
			delete(changed, field)
			raw, err := json.Marshal(changed)
			if err != nil || parityEditFlagPreview(raw, "after", "json", payload, false, true) {
				t.Fatal("incomplete edit preview accepted")
			}
		})
	}
	for name, changed := range map[string]string{
		"wrong-summary": strings.Replace(valid, `"Owned"`, `"other"`, 1),
		"wrong-key":     strings.Replace(valid, `"KEY-1"`, `"KEY-2"`, 1),
		"false-success": strings.Replace(valid, `"ok":true`, `"ok":false`, 1),
		"false-notify":  strings.Replace(valid, `"notify":true`, `"notify":false`, 1),
		"invented-write": strings.Replace(valid, `"dry_run":true`,
			`"dry_run":true,"applied":true`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if parityEditFlagPreview([]byte(changed), "after", "json", payload, false, true) {
				t.Fatal("changed edit preview facts accepted")
			}
		})
	}
}
