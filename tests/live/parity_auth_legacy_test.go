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

	koanftoml "github.com/knadh/koanf/parsers/toml"
)

func parityMissingLegacyAuth(raw []byte, member string) bool {
	if parityOriginalFrameData(raw) == nil {
		return false
	}
	var frame map[string]json.RawMessage
	if json.Unmarshal(raw, &frame) != nil || strings.TrimSpace(string(frame["data"])) != "null" || !parityAuthoringJSON(frame["warnings"], []byte(`[]`)) {
		return false
	}
	want, err := json.Marshal([]any{map[string]any{"code": "command_unknown", "message": fmt.Sprintf("unknown command %q for %q", member, "jira auth"), "hint": "Check the command's spelling, or run `--help` on its parent to see the ones it accepts.", "field": member}})
	return err == nil && parityAuthoringJSON(frame["errors"], want)
}

// These are observed missing legacy operations, never equivalent-behavior credit.
// All old operations here are local or preview-only and open no credential store.
func TestParityActualBinaryLegacyAuthBoundaries(t *testing.T) {
	lanes := []parityLane{{Name: "before", Binary: os.Getenv("JIRA_PARITY_BEFORE_BINARY"), Digest: os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")}}
	if lanes[0].Binary == "" || lanes[1].Binary == "" {
		t.Skip("requires both admitted actual binaries")
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string), Differences: make(map[string][]string)}
	t.Cleanup(func() { saveParityLocal(t, "auth-legacy-missing", evidence) })
	for _, mode := range []string{"json", "compact"} {
		for _, scenario := range []string{"refresh", "switch-preview", "switch-apply", "migrate-keyring", "migrate-onepassword"} {
			for _, lane := range lanes {
				t.Run(mode+"/"+scenario+"/"+lane.Name, func(t *testing.T) {
					if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
						t.Fatal(err)
					}
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						calls.Add(1)
						http.Error(w, "local auth entered HTTP", http.StatusBadGateway)
					}))
					defer server.Close()
					home := t.TempDir()
					config := parityCachePublicConfig(t, home, lane.Name, server.URL)
					if lane.Name == "before" {
						public := fmt.Sprintf("default_profile='fixture'\n[[profiles]]\nname='fixture'\nbase_url=%q\nemail='fixture@example.invalid'\nsecret_backend='env'\n[[profiles]]\nname='other'\nbase_url='https://other.invalid'\nemail='other@example.invalid'\nsecret_backend='env'\n", server.URL)
						if err := os.WriteFile(config, []byte(public), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					member, id := "refresh", "C020"
					if strings.HasPrefix(scenario, "switch-") {
						member, id = "switch", "C022"
					} else if strings.HasPrefix(scenario, "migrate-") {
						member, id = "migrate", "C019"
					}
					args := []string{"auth", member}
					want := map[string]any{"profile": "fixture", "auth_type": "token", "refreshed": false, "reason": "selected auth type has no refresh flow"}
					switch member {
					case "switch":
						want = map[string]any{"active": "other", "previous": "fixture", "dry_run": scenario == "switch-preview"}
						if lane.Name == "before" {
							args = append(args, "other")
							if scenario == "switch-preview" {
								args = append(args, "--dry-run")
							}
						}
					case "migrate":
						backend := "keyring"
						if scenario == "migrate-onepassword" {
							backend = "1password"
						}
						want = map[string]any{"target_backend": backend, "dry_run": true, "profiles": []any{map[string]any{"profile": "fixture", "source_backend": "env", "target_backend": backend, "dry_run": true, "migrated": false, "reason": "dry run"}}}
						if lane.Name == "before" {
							args = append(args, "--backend", backend, "--dry-run")
							if backend == "1password" {
								args = append(args, "--onepassword-account=fixture-account", "--vault=Private", "--item=owned-fixture")
							}
						}
					}
					before := parityLocalTree(t, home)
					public, err := os.ReadFile(config)
					if err != nil {
						t.Fatal(err)
					}
					result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE="}, args...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr, PublicConfig: public})
					if calls.Load() != 0 {
						t.Fatal("local legacy auth boundary entered HTTP")
					}
					if lane.Name == "after" {
						if result.Exit != 3 || parityValidateFrame(result.Stdout, lane.Name, "auth", result.Exit) != nil || !parityMissingLegacyAuth(result.Stdout, member) {
							t.Fatal("missing legacy operation lost its exact dispatch refusal")
						}
					} else {
						parityCacheLocalData(t, result, lane.Name, mode, "auth."+member, want)
					}
					after := parityLocalTree(t, home)
					if lane.Name == "before" && scenario == "switch-apply" {
						raw, err := os.ReadFile(config)
						values, parseErr := koanftoml.Parser().Unmarshal(raw)
						if err != nil || parseErr != nil || values["default_profile"] != "other" {
							t.Fatal("legacy switch did not publish its owned default profile")
						}
						rows, ok := values["profiles"].([]any)
						if !ok || len(rows) != 2 {
							t.Fatal("legacy switch changed its profile inventory")
						}
						for index, expected := range []map[string]any{
							{"name": "fixture", "base_url": server.URL, "email": "fixture@example.invalid", "secret_backend": "env"},
							{"name": "other", "base_url": "https://other.invalid", "email": "other@example.invalid", "secret_backend": "env"},
						} {
							row, ok := rows[index].(map[string]any)
							if !ok {
								t.Fatal("legacy switch lost a public profile")
							}
							for key, value := range expected {
								if row[key] != value {
									t.Fatal("legacy switch changed authored profile metadata")
								}
							}
						}
						// Only the explicitly owned public configuration may change.
						name := filepath.Base(config)
						delete(before, name)
						delete(after, name)
					}
					if !reflect.DeepEqual(before, after) {
						t.Fatal("legacy auth changed unrelated local state")
					}
					if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
						t.Fatal(err)
					}
					name := id + ".missing_legacy." + scenario + "." + mode
					evidence.Differences[name] = append(evidence.Differences[name], lane.Name)
				})
			}
		}
	}
}

func TestParityMissingLegacyAuthRequiresExactRefusal(t *testing.T) {
	control := "{\"data\":null,\"errors\":[{\"code\":\"command_unknown\",\"message\":\"unknown command \\\"refresh\\\" for \\\"jira auth\\\"\",\"hint\":\"Check the command's spelling, or run `--help` on its parent to see the ones it accepts.\",\"field\":\"refresh\"}],\"warnings\":[]}"
	if !parityMissingLegacyAuth([]byte(control), "refresh") {
		t.Fatal("independent complete missing-operation refusal rejected")
	}
	for name, changed := range map[string]string{
		"wrong-member":    strings.ReplaceAll(control, "refresh", "switch"),
		"other-error":     strings.Replace(control, "command_unknown", "credential_unavailable", 1),
		"wrong-data-case": strings.Replace(control, `"data":null`, `"Data":null`, 1),
		"false-data":      strings.Replace(control, `"data":null`, `"data":{}`, 1),
		"duplicate":       strings.Replace(control, `"field":"refresh"`, `"field":"other","field":"refresh"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if parityMissingLegacyAuth([]byte(changed), "refresh") {
				t.Fatal("unrelated or incomplete refusal credited as the missing operation")
			}
		})
	}
}
