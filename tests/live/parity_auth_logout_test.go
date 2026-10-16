//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func parityAuthLogoutData(raw []byte, lane string, preview, present bool) bool {
	if !jsontext.Value(raw).IsValid() {
		return false
	}
	want := map[string]any{"dry_run": preview, "email": "fixture@example.invalid", "env_token_remains": present, "revocation_needed": true}
	if present {
		want["remaining_source"] = "env"
	}
	if lane == "before" {
		want = map[string]any{"profile": "fixture", "backend": "env", "removed": false, "dry_run": preview}
	} else if lane != "after" {
		return false
	}
	expected, _ := json.Marshal(want)
	return parityAuthoringJSON(raw, expected)
}

func parityAuthLogoutConfirmation(raw []byte, lane string) bool {
	var root map[string]json.RawMessage
	if !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &root) != nil || !bytes.Equal(root["data"], []byte("null")) {
		return false
	}
	want := map[string]any{"code": "flag_required", "message": "logout removes a stored credential, so it needs confirmation — pass --yes to confirm without being asked", "hint": "Add the flag the command needs — run it with `--help` to see which.", "field": "yes"}
	if lane == "before" {
		want = map[string]any{"type": "validation", "code": "validation_failed", "message": "auth logout requires --force in headless / agent / --no-input mode", "hint": "Check the values you passed and try again.", "retryable": false}
	} else if lane != "after" {
		return false
	}
	expected, _ := json.Marshal([]any{want})
	return parityAuthoringJSON(root["errors"], expected)
}

func TestParityActualBinaryAuthLogoutEnvironment(t *testing.T) {
	lanes := []parityLane{{Name: "before", Binary: os.Getenv("JIRA_PARITY_BEFORE_BINARY"), Digest: os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")}}
	if lanes[0].Binary == "" || lanes[1].Binary == "" {
		t.Skip("requires both admitted binaries")
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "local logout entered HTTP", 500)
	}))
	defer server.Close()
	for _, mode := range []string{"json", "compact"} {
		for _, step := range []string{"preview", "confirmation", "already_absent"} {
			for _, lane := range lanes {
				t.Run(mode+"/"+step+"/"+lane.Name, func(t *testing.T) {
					if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
						t.Fatal(err)
					}
					home := t.TempDir()
					config := parityCachePublicConfig(t, home, lane.Name, server.URL)
					before := parityLocalTree(t, home)
					args := []string{"auth", "logout"}
					if lane.Name == "before" {
						args = append(args, "fixture")
					}
					var extra []string
					if step == "preview" {
						args = append(args, "--dry-run")
					}
					if step == "already_absent" {
						extra = []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE="}
						if lane.Name == "before" {
							args = append(args, "--force")
						} else {
							args = append(args, "--yes")
						}
					}
					result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, extra, args...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					if calls.Load() != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
						t.Fatal("environment logout changed files or entered HTTP")
					}
					if step == "confirmation" {
						if result.Exit != 3 {
							t.Fatal("logout without consent did not refuse")
						}
						parityAuthoringData(t, result.Stdout, lane.Name, "auth.logout", 3)
						if !parityAuthLogoutConfirmation(result.Stdout, lane.Name) {
							t.Fatal("logout lost its complete independent confirmation reason")
						}
					} else {
						if result.Exit != 0 || len(result.Stderr) != 0 {
							t.Fatalf("environment logout failed: exit%d stdout%s stderr%s", result.Exit, result.Stdout, result.Stderr)
						}
						data := result.Stdout
						if mode == "json" {
							parityAuthoringData(t, result.Stdout, lane.Name, "auth.logout", 0)
							data = parityOriginalFrameData(result.Stdout)
						}
						if !parityAuthLogoutData(data, lane.Name, step == "preview", step != "already_absent") {
							t.Fatalf("logout receipt differs: %s", data)
						}
					}
					evidence.Checks["C018.auth.logout.env."+step+"."+mode] = append(evidence.Checks["C018.auth.logout.env."+step+"."+mode], lane.Name)
				})
			}
		}
	}
	saveParityLocal(t, "auth-logout-local", evidence)
}

func TestParityAuthLogoutRequiresCompleteFacts(t *testing.T) {
	t.Parallel()
	valid := `{"dry_run":true,"email":"fixture@example.invalid","env_token_remains":true,"remaining_source":"env","revocation_needed":true}`
	if !parityAuthLogoutData([]byte(valid), "after", true, true) {
		t.Fatal("complete environment preview refused")
	}
	for name, invalid := range map[string]string{"wrong_account": strings.ReplaceAll(valid, "fixture@", "foreign@"), "missing_remains": strings.ReplaceAll(valid, `"env_token_remains":true,`, ""), "missing_revocation": strings.ReplaceAll(valid, `,"revocation_needed":true`, ""), "wrong_source": strings.ReplaceAll(valid, `"remaining_source":"env"`, `"remaining_source":"keyring"`), "sparse": `{"dry_run":true}`} {
		t.Run(name, func(t *testing.T) {
			if parityAuthLogoutData([]byte(invalid), "after", true, true) {
				t.Fatal("logout admitted incomplete or foreign public outcome")
			}
		})
	}
}

func TestParityAuthLogoutConfirmationRequiresExactReason(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ lane, raw string }{
		{"before", `{"data":null,"errors":[{"type":"validation","code":"validation_failed","message":"auth logout requires --force in headless / agent / --no-input mode","hint":"Check the values you passed and try again.","retryable":false}]}`},
		{"after", `{"data":null,"errors":[{"code":"flag_required","message":"logout removes a stored credential, so it needs confirmation — pass --yes to confirm without being asked","hint":"Add the flag the command needs — run it with ` + "`--help`" + ` to see which.","field":"yes"}]}`},
	} {
		t.Run(tc.lane, func(t *testing.T) {
			if !parityAuthLogoutConfirmation([]byte(tc.raw), tc.lane) {
				t.Fatal("complete independent logout refusal refused")
			}
			for name, invalid := range map[string]string{"wrong_message": strings.ReplaceAll(tc.raw, "logout", "unknown"), "wrong_hint": strings.ReplaceAll(strings.ReplaceAll(tc.raw, "Check the values", "Retry all writes"), "Add the flag", "Retry all writes"), "false_data": strings.ReplaceAll(tc.raw, `"data":null`, `"data":{}`), "missing_field": strings.ReplaceAll(tc.raw, `,"field":"yes"`, ""), "missing_retryable": strings.ReplaceAll(tc.raw, `,"retryable":false`, "")} {
				if invalid == tc.raw {
					continue
				}
				t.Run(name, func(t *testing.T) {
					if !jsontext.Value(invalid).IsValid() {
						t.Fatal("negative refusal must remain complete JSON")
					}
					if parityAuthLogoutConfirmation([]byte(invalid), tc.lane) {
						t.Fatal("logout refused for an unrelated or incomplete reason")
					}
				})
			}
		})
	}
}
