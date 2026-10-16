//go:build linux && nativekeyring

package contract

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

type nativeAuthReadFixture struct {
	Binary, Config, Site, Email, Token string
	Environment                        []string
	Stored                             func(*testing.T) string
	Revoked, Malformed                 *atomic.Bool
}

// These expected values describe the authored fixture, without the production
// auth models, credential decoder, masking helper or human renderer.
func nativeAuthStatusMatches(raw []byte, mode, site, email string, reveal bool) bool {
	const secret = "synthetic-native-fixture-token"
	shown := "****************oken"
	occurrences := 0
	if reveal {
		shown, occurrences = secret, 1
	}
	if bytes.Count(raw, []byte(secret)) != occurrences {
		return false
	}
	if mode == "human" {
		want := strings.TrimPrefix(site, "http://") + " ✓ Logged in to account " + email +
			" (Native fixture) Account ID: native:fixture Token: " + shown
		return strings.Join(strings.Fields(string(raw)), " ") == want
	}
	if !jsontext.Value(raw).IsValid() {
		return false
	}
	data := raw
	if mode == "json" {
		var root map[string]json.RawMessage
		if json.Unmarshal(raw, &root) != nil || len(root) != 5 {
			return false
		}
		for _, key := range []string{"ok", "meta", "data", "errors", "warnings"} {
			if _, present := root[key]; !present {
				return false
			}
		}
		var frame frame
		if json.Unmarshal(raw, &frame) != nil || !frame.OK || frame.Meta.Command != "auth.status" ||
			frame.Meta.ContractVersion != "1" || string(root["errors"]) != "[]" || string(root["warnings"]) != "[]" {
			return false
		}
		data = root["data"]
	} else if mode != "compact" {
		return false
	}
	want := map[string]string{
		"base_url": site, "email": email, "account_id": "native:fixture",
		"display_name": "Native fixture", "token_source": "keyring",
	}
	if reveal {
		want["token"] = secret
	}
	var actual map[string]string
	return json.Unmarshal(data, &actual) == nil && reflect.DeepEqual(actual, want)
}

func testNativeAuthReadModes(t *testing.T, fixture nativeAuthReadFixture) {
	t.Helper()
	stored := fixture.Stored(t)
	public, err := os.ReadFile(fixture.Config)
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Revoked.Store(false)
	defer fixture.Malformed.Store(false)
	for _, mode := range []string{"json", "compact", "human"} {
		for _, step := range []string{"status-masked", "status-reveal", "token", "revoked-reveal", "malformed-reveal"} {
			if !t.Run(mode+"/"+step, func(t *testing.T) {
				fixture.Revoked.Store(step == "revoked-reveal")
				fixture.Malformed.Store(step == "malformed-reveal")
				args := []string{"--config", fixture.Config, "--no-input", "--output=" + mode, "auth", "status"}
				if step == "token" {
					args[len(args)-1] = "token"
				} else if step != "status-masked" {
					args = append(args, "--show-token")
				}
				// Explicit disclosure is confined to this synthetic token and the
				// already validated private bus; no desktop session is inherited.
				stdout, stderr, exit := runExecutableIOInDirStarted(t, fixture.Binary, t.TempDir(), fixture.Environment, nil, nil, nil, args...)
				if stderr != "" && exit == 0 || strings.Contains(stderr, fixture.Token) {
					t.Fatal("native auth emitted unexpected diagnostics or disclosed a token on stderr")
				}
				switch step {
				case "token":
					if exit != 0 || stdout != fixture.Token+"\n" {
						t.Fatal("native token read did not return exactly the authored synthetic credential")
					}
				case "status-masked", "status-reveal":
					if exit != 0 || !nativeAuthStatusMatches([]byte(stdout), mode, fixture.Site, fixture.Email, step == "status-reveal") {
						t.Fatal("native status lost its exact bound identity, selected store or disclosure policy")
					}
				default:
					wantExit, wantCode := 1, "jira_unauthorized"
					if step == "malformed-reveal" {
						wantExit, wantCode = 5, "current_user_response_invalid"
					}
					if exit != wantExit || strings.Contains(stdout, fixture.Token) {
						t.Fatal("failed native verification disclosed a token or changed its exit classification")
					}
					if mode != "human" {
						var frame struct {
							OK     bool
							Data   json.RawMessage
							Errors []struct{ Code string }
						}
						valid := jsontext.Value(stdout).IsValid() && json.Unmarshal([]byte(stdout), &frame) == nil
						if !valid || frame.OK || string(frame.Data) != "null" || len(frame.Errors) != 1 || frame.Errors[0].Code != wantCode {
							t.Fatal("failed native verification lost its original typed refusal")
						}
					} else {
						message, hint := "Fixture credential rejected", "auth login --with-token"
						if step == "malformed-reveal" {
							message, hint = "current user response invalid", "malformed current-account response"
						}
						if stdout != "" || !strings.Contains(stderr, message) || !strings.Contains(stderr, hint) {
							t.Fatal("human native refusal lost its original message, recovery hint or empty result stdout")
						}
					}
				}
				after, err := os.ReadFile(fixture.Config)
				if err != nil || !bytes.Equal(public, after) || fixture.Stored(t) != stored {
					t.Fatal("native authentication read changed public settings or the stored credential")
				}
			}) {
				return
			}
		}
	}
}

func TestNativeAuthStatusOracleRejectsFalseFacts(t *testing.T) {
	valid := `{"base_url":"http://example.invalid","email":"native-fixture@example.invalid","account_id":"native:fixture","display_name":"Native fixture","token_source":"keyring"}`
	if !nativeAuthStatusMatches([]byte(valid), "compact", "http://example.invalid", "native-fixture@example.invalid", false) {
		t.Fatal("independent native status control was refused")
	}
	for name, changed := range map[string]string{
		"wrong-store": strings.ReplaceAll(valid, "keyring", "env"),
		"wrong-user":  strings.ReplaceAll(valid, "native:fixture", "foreign:fixture"),
		"wrong-case":  strings.ReplaceAll(valid, "account_id", "Account_ID"),
		"missing":     strings.ReplaceAll(valid, `,"token_source":"keyring"`, ""),
		"leaked":      strings.ReplaceAll(valid, `"token_source":"keyring"`, `"token_source":"keyring","token":"synthetic-native-fixture-token"`),
		"duplicate":   strings.ReplaceAll(valid, `"account_id":"native:fixture"`, `"account_id":"foreign:fixture","account_id":"native:fixture"`),
		"trailing":    valid + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			if nativeAuthStatusMatches([]byte(changed), "compact", "http://example.invalid", "native-fixture@example.invalid", false) {
				t.Fatal("native status admitted incomplete, foreign or secret-bearing output")
			}
		})
	}
}

func TestNativeAuthStatusOracleRequiresExplicitDisclosure(t *testing.T) {
	const site, email = "http://example.invalid", "native-fixture@example.invalid"
	const secret = "synthetic-native-fixture-token"
	const masked = `{"base_url":"http://example.invalid","email":"native-fixture@example.invalid","account_id":"native:fixture","display_name":"Native fixture","token_source":"keyring"}`
	for _, mode := range []string{"json", "compact", "human"} {
		for _, reveal := range []bool{false, true} {
			name := mode + "/masked"
			data, shown := masked, "****************oken"
			if reveal {
				name = mode + "/reveal"
				data = strings.TrimSuffix(masked, "}") + `,"token":"` + secret + `"}`
				shown = secret
			}
			switch mode {
			case "json":
				data = `{"ok":true,"meta":{"command":"auth.status","contract_version":"1"},"data":` + data + `,"errors":[],"warnings":[]}`
			case "human":
				data = "example.invalid\n  ✓ Logged in to account " + email + " (Native fixture)\nAccount ID: native:fixture\nToken: " + shown + "\n"
			}
			t.Run(name, func(t *testing.T) {
				if !nativeAuthStatusMatches([]byte(data), mode, site, email, reveal) {
					t.Fatal("independent disclosure control was refused")
				}
				if nativeAuthStatusMatches([]byte(data), mode, site, email, !reveal) {
					t.Fatal("disclosure choice did not change the accepted output")
				}
				for _, changed := range []string{strings.ReplaceAll(data, "native:fixture", "foreign:fixture"), strings.ReplaceAll(data, "Native fixture", "Different fixture")} {
					if nativeAuthStatusMatches([]byte(changed), mode, site, email, reveal) {
						t.Fatal("disclosure control admitted a different authenticated identity")
					}
				}
			})
		}
	}
}

// Cached public identity survives logout. It must never authenticate a fresh
// process or let a token command succeed after the native record is removed.
func testNativeAuthMissing(t *testing.T, fixture nativeAuthReadFixture, absent func(*testing.T)) {
	t.Helper()
	public, err := os.ReadFile(fixture.Config)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"json", "compact", "human"} {
		for _, step := range []string{"status", "status-reveal", "whoami", "token"} {
			if !t.Run(mode+"/"+step, func(t *testing.T) {
				operation := step
				if step == "status-reveal" {
					operation = "status"
				}
				args := []string{"--config", fixture.Config, "--no-input", "--output=" + mode, "auth", operation}
				if step == "status-reveal" {
					args = append(args, "--show-token")
				}
				stdout, stderr, exit := runExecutableIOInDirStarted(t, fixture.Binary, t.TempDir(), fixture.Environment, nil, nil, nil, args...)
				if exit != 1 || strings.Contains(stdout+stderr, fixture.Token) {
					t.Fatal("removed native credential still authenticates or appears in output")
				}
				if mode == "human" {
					if stdout != "" || !strings.Contains(stderr, "no API token") || !strings.Contains(stderr, "auth login --with-token") {
						t.Fatal("human missing-credential refusal lost its message or repair hint")
					}
				} else {
					var out struct {
						OK     bool
						Data   json.RawMessage
						Errors []struct{ Code string }
					}
					valid := jsontext.Value(stdout).IsValid() && json.Unmarshal([]byte(stdout), &out) == nil
					if !valid || out.OK || string(out.Data) != "null" || len(out.Errors) != 1 || out.Errors[0].Code != "auth_missing" {
						t.Fatal("missing native record was not classified as missing authentication")
					}
				}
				after, err := os.ReadFile(fixture.Config)
				if err != nil || !bytes.Equal(public, after) {
					t.Fatal("failed post-logout read changed public settings")
				}
				absent(t)
			}) {
				return
			}
		}
	}
}
