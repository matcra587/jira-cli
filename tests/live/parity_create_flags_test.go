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
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// Convenience flags are a separate surface from the native JSON create route.
// These independent preview oracles do not invoke the field or ADF encoders.
func parityCreateFlagPreview(raw []byte, lane string, fields map[string]any, markdown bool) bool {
	var want map[string]any
	switch lane {
	case "before":
		preview := map[string]any{"project_key": "KEY", "issue_type": "Task", "summary": "Owned café <&>"}
		for name, value := range fields {
			switch name {
			case "project", "issuetype", "summary":
			case "assignee":
				preview["assignee_account_id"] = "opaque-account"
			case "description":
				preview["description_adf"] = value
			default:
				preview[name] = value
			}
		}
		want = map[string]any{"preview": preview, "dry_run": true, "validated_remotely": false}
	case "after":
		want = map[string]any{"dry_run": true, "attempted": false, "applied": false, "payload": map[string]any{"fields": fields}, "description_route": ""}
		if markdown {
			want["description_route"] = "markdown"
		}
	default:
		return false
	}
	expected, _ := json.Marshal(want)
	return parityAuthoringJSON(raw, expected)
}

func TestParityActualBinaryCreateConveniencePreviews(t *testing.T) {
	lanes := []parityLane{{Name: "before", Binary: os.Getenv("JIRA_PARITY_BEFORE_BINARY"), Digest: os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")}}
	if lanes[0].Binary == "" || lanes[1].Binary == "" {
		t.Skip("requires both selected actual binaries")
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	t.Cleanup(func() { saveParityLocal(t, "create-flags-preview", evidence) })
	for _, lane := range lanes {
		for _, mode := range []string{"json", "compact"} {
			for _, scenario := range []string{"minimal", "scalar-flags", "inline-markdown", "file-markdown", "quoted-csv", "current-fields"} {
				if scenario == "current-fields" && lane.Name == "before" {
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
						http.Error(w, "local preview entered HTTP", http.StatusBadGateway)
					}))
					defer server.Close()
					home := t.TempDir()
					config := parityCachePublicConfig(t, home, lane.Name, server.URL)
					markdownPath := filepath.Join(home, "description spaces 古い '$;&.md")
					if err := os.WriteFile(markdownPath, []byte("**Owned café**"), 0o600); err != nil {
						t.Fatal(err)
					}
					args := []string{"issue", "create", "--project=KEY", "--type=Task", "--summary=Owned café <&>", "--dry-run"}
					fields := map[string]any{"project": map[string]any{"key": "KEY"}, "issuetype": map[string]any{"name": "Task"}, "summary": "Owned café <&>"}
					if scenario != "minimal" {
						args = append(args, "--parent=KEY-1", "--priority=High", "--label=alpha", "--label=beta", "--assignee=opaque-account")
						fields["parent"], fields["priority"] = map[string]any{"key": "KEY-1"}, map[string]any{"name": "High"}
						fields["labels"], fields["assignee"] = []string{"alpha", "beta"}, map[string]any{"accountId": "opaque-account"}
					}
					markdown := scenario == "inline-markdown" || scenario == "file-markdown"
					switch scenario {
					case "inline-markdown":
						args = append(args, "--markdown=**Owned café**")
					case "file-markdown":
						args = append(args, "--markdown-file", markdownPath)
					case "quoted-csv":
						args = slices.DeleteFunc(args, func(arg string) bool { return arg == "--label=alpha" || arg == "--label=beta" })
						args = append(args, `--label="alpha,beta"`, "--label=duplicate", "--label=duplicate")
						fields["labels"] = []string{"alpha,beta", "duplicate", "duplicate"}
					case "current-fields":
						args = append(args, "--reporter=opaque-reporter", "--component=123", "--component=ui", "--fix-version=123", "--affects-version=456", "--due=2026-12-31", "--original-estimate=1h 30m")
						fields["reporter"] = map[string]any{"accountId": "opaque-reporter"}
						fields["components"] = []any{map[string]any{"name": "123"}, map[string]any{"name": "ui"}}
						fields["fixVersions"], fields["versions"] = []any{map[string]any{"name": "123"}}, []any{map[string]any{"name": "456"}}
						fields["duedate"], fields["timetracking"] = "2026-12-31", map[string]any{"originalEstimate": "1h 30m"}
					}
					if markdown {
						fields["description"] = json.RawMessage(parityAuthoringDocument)
					}
					before := parityLocalTree(t, home)
					result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE="}, args...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load() != 0 {
						t.Fatalf("local field preview failed or entered HTTP: exit %d; stdout %s; stderr %s", result.Exit, result.Stdout, result.Stderr)
					}
					raw := result.Stdout
					if mode == "json" {
						parityAuthoringCleanData(t, raw, lane.Name, "issue.create")
						raw = parityOriginalFrameData(raw)
					}
					if !parityCreateFlagPreview(raw, lane.Name, fields, markdown) {
						t.Fatal("create flag preview differs from complete independent native fields and lane receipts")
					}
					if !reflect.DeepEqual(before, parityLocalTree(t, home)) {
						t.Fatal("bare create preview changed its input, public configuration or local storage")
					}
					id := fmt.Sprintf("I011.create.flags.%s.%s", scenario, mode)
					evidence.Checks[id] = append(evidence.Checks[id], lane.Name)
				})
			}
		}
	}
}

func TestParityCreateFlagPreviewRequiresCompleteFields(t *testing.T) {
	fields := map[string]any{"project": map[string]any{"key": "KEY"}, "issuetype": map[string]any{"name": "Task"}, "summary": "Owned café <&>"}
	valid := `{"dry_run":true,"attempted":false,"applied":false,"payload":{"fields":{"project":{"key":"KEY"},"issuetype":{"name":"Task"},"summary":"Owned café <&>"}},"description_route":""}`
	if !parityCreateFlagPreview([]byte(valid), "after", fields, false) {
		t.Fatal("independent complete preview refused")
	}
	for name, raw := range map[string]string{
		"empty":          `{}`,
		"sparse":         `{"dry_run":true}`,
		"attempted":      strings.Replace(valid, `"attempted":false`, `"attempted":true`, 1),
		"applied":        strings.Replace(valid, `"applied":false`, `"applied":true`, 1),
		"wrong_identity": strings.Replace(valid, `"key":"KEY"`, `"id":"KEY"`, 1),
		"wrong_summary":  strings.Replace(valid, "Owned café <&>", "wrong", 1),
		"missing_route":  strings.Replace(valid, `,"description_route":""`, "", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if parityCreateFlagPreview([]byte(raw), "after", fields, false) {
				t.Fatal("missing fields or false mutation effects admitted")
			}
		})
	}
}
