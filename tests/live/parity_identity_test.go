//go:build live

package live

import (
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParityIdentityRootOperation(t *testing.T) {
	for _, lane := range []string{"before", "after"} {
		for _, exit := range []int{0, 5} {
			for _, args := range [][]string{{"me"}, {"me", "--unknown"}} {
				if got := parityExpectedOperation(lane, args, exit); got != "me" {
					t.Errorf("root identity operation: got %q; want me; lane %s; exit %d", got, lane, exit)
				}
			}
		}
	}
	if parityOperation([]string{"me-other"}) != "" {
		t.Fatal("unknown single command borrowed identity operation")
	}
}

const (
	parityIdentityFull    = `{"accountId":"owned-current","accountType":"atlassian","displayName":"Native identity","emailAddress":"native@example.invalid","timeZone":"Europe/London","locale":"en_GB","active":false,"future":{"number":900719925474099312345}}`
	parityIdentityPrivate = `{"accountId":"owned-current","displayName":null,"emailAddress":null,"timeZone":null,"future":{"native":null}}`
)

func parityIdentityProjection(data, native []byte, lane, operation, site, profile string) bool {
	if (operation != "me" && operation != "auth.whoami") || !jsontext.Value(data).IsValid() || !jsontext.Value(native).IsValid() {
		return false
	}
	var source map[string]json.RawMessage
	if json.Unmarshal(native, &source) != nil || source == nil {
		return false
	}
	want := make(map[string]any)
	switch lane {
	case "after":
		var id *string
		if json.Unmarshal(source["accountId"], &id) != nil || id == nil || strings.TrimSpace(*id) == "" {
			return false
		}
		want["base_url"], want["user"] = site, json.RawMessage(native)
	case "before":
		want["profile"] = profile
		for _, field := range []struct{ native, summary string }{{"accountId", "account_id"}, {"displayName", "display_name"}, {"emailAddress", "email_address"}, {"timeZone", "time_zone"}} {
			var value string
			if raw, present := source[field.native]; present && json.Unmarshal(raw, &value) != nil {
				return false
			}
			want[field.summary] = value
		}
		if operation == "auth.whoami" {
			var value string
			if raw, present := source["accountType"]; present && json.Unmarshal(raw, &value) != nil {
				return false
			}
			want["account_type"], want["saved"] = value, false
		}
	default:
		return false
	}
	encoded, err := json.Marshal(want)
	return err == nil && parityAuthoringJSON(data, encoded)
}

func parityIdentityRefusal(stdout []byte, operation string, exit int) bool {
	if !jsontext.Value(stdout).IsValid() || exit != 5 || parityValidateFrame(stdout, "after", operation, exit) != nil {
		return false
	}
	var frame struct {
		Data     json.RawMessage
		Errors   []struct{ Code string }
		Warnings json.RawMessage
	}
	return json.Unmarshal(stdout, &frame) == nil && string(frame.Data) == "null" && len(frame.Errors) == 1 && frame.Errors[0].Code == "current_user_response_invalid" && parityAuthoringJSON(frame.Warnings, []byte(`[]`))
}

// Identity inspection is read-only here. Saving identity, provider selection and
// human presentation require their own acceptance and receive no credit below.
func TestParityActualBinaryIdentityInspection(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			for _, door := range []struct{ id, operation, path string }{{"C024", "auth.whoami", "auth whoami"}, {"C060", "me", "me"}} {
				for _, sample := range []struct {
					name, native string
					invalid      bool
				}{{"native", parityIdentityFull, false}, {"private", parityIdentityPrivate, false}, {"missing-id", `{"displayName":"Unusable identity"}`, true}, {"null-id", `{"accountId":null,"displayName":"Unusable identity"}`, true}} {
					modes := []string{"json", "compact"}
					if sample.invalid {
						modes = []string{"json"}
					}
					for _, mode := range modes {
						t.Run(door.path+"/"+sample.name+"/"+mode, func(t *testing.T) {
							var calls atomic.Int32
							server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								calls.Add(1)
								body, err := io.ReadAll(io.LimitReader(r.Body, 1))
								user, password, ok := r.BasicAuth()
								if err != nil || len(body) != 0 || r.Method != "GET" || r.URL.Path != "/rest/api/3/myself" || r.URL.RawQuery != "" || !ok || user != "fixture@example.invalid" || password != "synthetic-fixture-token" {
									t.Error("identity lookup changed its exact authenticated GET/body")
									http.Error(w, "wrong synthetic request", http.StatusBadRequest)
									return
								}
								w.Header().Set("Content-Type", "application/json")
								_, _ = w.Write([]byte(sample.native))
							}))
							defer server.Close()
							home := t.TempDir()
							config := filepath.Join(home, "public.toml")
							public := "theme='dark'\n"
							if lane.name == "before" {
								public = fmt.Sprintf("default_profile='fixture'\n[[profiles]]\nname='fixture'\nbase_url=%q\nemail='fixture@example.invalid'\nsecret_backend='env'\n", server.URL)
							}
							if err := os.WriteFile(config, []byte(public), 0o600); err != nil {
								t.Fatal(err)
							}
							before := parityLocalTree(t, home)
							if err := parityFingerprint(lane.binary, lane.digest); err != nil {
								t.Fatal(err)
							}
							result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_READ_ONLY=true"}, strings.Fields(door.path)...)
							evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
							if calls.Load() != 1 || !reflect.DeepEqual(parityLocalTree(t, home), before) {
								t.Fatal("identity inspection changed owned files or its native request count")
							}
							if sample.invalid && lane.name == "after" {
								if !parityIdentityRefusal(result.Stdout, door.operation, result.Exit) {
									t.Fatalf("invalid native identity was not refused without a partial account: exit %d; stdout %s", result.Exit, result.Stdout)
								}
							} else {
								if result.Exit != 0 || len(result.Stderr) != 0 {
									t.Fatalf("identity inspection failed: exit %d; stdout %s; stderr %s", result.Exit, result.Stdout, result.Stderr)
								}
								data := result.Stdout
								if mode == "json" {
									var err error
									data, err = json.Marshal(parityAuthoringCleanData(t, data, lane.name, door.operation))
									if err != nil {
										t.Fatal(err)
									}
								}
								if !parityIdentityProjection(data, []byte(sample.native), lane.name, door.operation, server.URL, "fixture") {
									t.Fatalf("identity differs from its independent native account/scope: %s", result.Stdout)
								}
							}
							id := door.id + ".identity." + sample.name + "." + mode
							evidence.Checks[id] = append(evidence.Checks[id], lane.name)
						})
					}
				}
			}
		})
	}
	saveParityLocal(t, "identity-fixture", evidence)
}

func TestLiveParityIdentityInspection(t *testing.T) {
	p := newParityRun(t)
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			for _, door := range []struct{ id, operation, path string }{{"C024", "auth.whoami", "auth whoami"}, {"C060", "me", "me"}} {
				t.Run(door.path, func(t *testing.T) {
					native := rawRead(t, p.owner, "api", "get", "/rest/api/3/myself")
					var identity struct {
						AccountID string `json:"accountId"`
					}
					if json.Unmarshal(native.Response.JSON, &identity) != nil || identity.AccountID == "" || identity.AccountID != p.owner.watcherAccountID {
						t.Fatal("independent native account changed from admitted identity")
					}
					frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, strings.Fields(door.path)...)
					if exit != 0 || !frame.OK {
						t.Fatal("paired live identity inspection failed; inspect retained receipts")
					}
					data, err := json.Marshal(parityAuthoringCleanData(t, stdout, lane.Name, door.operation))
					if err != nil || !parityIdentityProjection(data, native.Response.JSON, lane.Name, door.operation, p.owner.manifest.BaseURL, lane.Profile) {
						t.Fatal("identity command differs from independent native account and public site")
					}
					p.check(t, lane, door.id+".identity.native")
				})
			}
		})
	}
}

func TestParityIdentityProjectionRejectsChangedAccountOrScope(t *testing.T) {
	const old = `{"profile":"fixture","account_id":"owned-current","display_name":"Native identity","email_address":"native@example.invalid","time_zone":"Europe/London","account_type":"atlassian","saved":false}`
	const current = `{"base_url":"https://example.invalid","user":` + parityIdentityFull + `}`
	for _, lane := range []string{"before", "after"} {
		t.Run(lane, func(t *testing.T) {
			valid := old
			if lane == "after" {
				valid = current
			}
			check := func(data string) bool {
				return parityIdentityProjection([]byte(data), []byte(parityIdentityFull), lane, "auth.whoami", "https://example.invalid", "fixture")
			}
			if !check(valid) {
				t.Fatal("independent valid identity projection refused")
			}
			type change struct{ name, original, replacement string }
			cases := []change{{"wrong-account", "owned-current", "other-account"}, {"configured-email", "native@example.invalid", "fixture@example.invalid"}, {"wrong-name", "Native identity", "Configured identity"}}
			if lane == "before" {
				cases = append(cases, change{"wrong-profile", `"profile":"fixture"`, `"profile":"other"`}, change{"false-save", `"saved":false`, `"saved":true`})
			} else {
				cases = append(cases, change{"wrong-site", "https://example.invalid", "https://other.invalid"}, change{"lost-number", "900719925474099312345", "900719925474099312344"}, change{"lost-false", `"active":false`, `"active":null`}, change{"lost-native-extension", `,"future":{"number":900719925474099312345}`, ``})
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					if check(strings.Replace(valid, tc.original, tc.replacement, 1)) {
						t.Fatal("changed identity/account scope earned read credit")
					}
				})
			}
		})
	}
}

func TestParityIdentityRefusalRejectsPartialOrDuplicateOutput(t *testing.T) {
	const valid = `{"ok":false,"meta":{"command":"me","contract_version":"1","timestamp":"2026-01-01T00:00:00Z","request_id":"owned-fixture"},"data":null,"errors":[{"code":"current_user_response_invalid"}],"warnings":[]}`
	if !parityIdentityRefusal([]byte(valid), "me", 5) {
		t.Fatal("independent identity refusal fixture refused")
	}
	for _, tc := range []struct{ name, original, replacement string }{
		{"partial-account", `"data":null`, `"data":{"account_id":"unusable"}`},
		{"wrong-code", `"code":"current_user_response_invalid"`, `"code":"auth_missing"`},
		{"duplicate", `"data":null`, `"data":{},"data":null`},
		{"null-warnings", `"warnings":[]`, `"warnings":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if parityIdentityRefusal([]byte(strings.Replace(valid, tc.original, tc.replacement, 1)), "me", 5) {
				t.Fatal("partial or malformed identity refusal earned read credit")
			}
		})
	}
}
