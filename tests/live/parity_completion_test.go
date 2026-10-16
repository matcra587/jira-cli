//go:build live

package live

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParityActualBinaryCompletionInterface(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				http.Error(w, "completion interface entered HTTP", 500)
			}))
			defer server.Close()
			for _, mode := range []string{"json", "compact", "human"} {
				for _, scenario := range []string{"long-help", "short-help", "bare", "unknown-shell", "extra-argument"} {
					if mode == "human" && scenario != "long-help" && scenario != "short-help" {
						continue
					}
					t.Run(mode+"/"+scenario, func(t *testing.T) {
						home := t.TempDir()
						config := parityCachePublicConfig(t, home, lane.name, server.URL)
						before := parityLocalTree(t, home)
						args := []string{"completion"}
						switch scenario {
						case "long-help":
							args = append(args, "--help")
						case "short-help":
							args = append(args, "-h")
						case "unknown-shell":
							args = append(args, "mercury")
						case "extra-argument":
							args = append(args, "fish", "extra")
						}
						if err := parityFingerprint(lane.binary, lane.digest); err != nil {
							t.Fatal(err)
						}
						result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_KEYRING=false", "JIRA_READ_ONLY=true"}, args...)
						evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
						if calls.Load() != 0 || result.Exit == 0 && len(result.Stderr) != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
							t.Fatalf("completion interface entered effects: HTTP %d stderr %s", calls.Load(), result.Stderr)
						}
						if strings.HasSuffix(scenario, "help") || scenario == "bare" && lane.name == "before" {
							children := ""
							if lane.name == "before" {
								children = "bash elvish fish pwsh zsh"
							}
							if result.Exit != 0 || !parityNavigationHelp(result.Stdout, "completion", children) || lane.name == "after" && !strings.Contains(string(result.Stdout), "<shell>") {
								t.Fatalf("completion interface help differs: exit %d stdout %s", result.Exit, result.Stdout)
							}
						} else if !parityCompletionInterfaceRefusal(result.Stdout, result.Stderr, lane.name, scenario, result.Exit) {
							t.Fatalf("completion interface refusal differs: exit %d stdout %s", result.Exit, result.Stdout)
						}
						check := "C041.interface." + scenario + "." + mode
						evidence.Checks[check] = append(evidence.Checks[check], lane.name)
					})
				}
			}
		})
	}
	saveParityLocal(t, "completion-interface", evidence)
}

func parityCompletionInterfaceRefusal(stdout, stderr []byte, lane, scenario string, exit int) bool {
	operation, code, message, field := "completion", "arg_count_invalid", "", ""
	if lane == "before" {
		switch scenario {
		case "unknown-shell":
			operation, code, message = "error", "command_unknown", `unknown command "mercury" for "jira completion"`
		case "extra-argument":
			operation, message = "completion.fish", `unknown command "extra" for "jira completion fish"`
		default:
			return false
		}
	} else {
		message = "completion takes exactly one shell argument: bash, elvish, fish, nu, pwsh, zsh"
		if scenario == "unknown-shell" {
			code, field = "arg_value_invalid", "mercury"
			message = `unknown shell "mercury": valid shells are bash, elvish, fish, nu, pwsh, zsh`
		}
	}
	if !parityLocalRefusal(stdout, lane, operation, exit, code) {
		return false
	}
	hint := "Check how many arguments the command takes; its usage line is in `--help`."
	if scenario == "unknown-shell" {
		hint = "That isn't one of the accepted values — run the command with `--help` to see the choices."
	}
	diagnostic := ""
	if lane == "after" {
		diagnostic = "❌ " + message + "\n💡 " + hint + "\n"
	}
	var frame struct {
		Errors []struct{ Message, Field string }
	}
	return json.Unmarshal(stdout, &frame) == nil && frame.Errors[0].Message == message && frame.Errors[0].Field == field && string(stderr) == diagnostic
}

func TestParityCompletionInterfaceDiagnostics(t *testing.T) {
	for _, tc := range []struct{ lane, scenario, operation, code, message, field, diagnostic string }{
		{"before", "unknown-shell", "error", "command_unknown", `unknown command "mercury" for "jira completion"`, "", ""},
		{"before", "extra-argument", "completion.fish", "arg_count_invalid", `unknown command "extra" for "jira completion fish"`, "", ""},
		{"after", "bare", "completion", "arg_count_invalid", "completion takes exactly one shell argument: bash, elvish, fish, nu, pwsh, zsh", "", "❌ completion takes exactly one shell argument: bash, elvish, fish, nu, pwsh, zsh\n💡 Check how many arguments the command takes; its usage line is in `--help`.\n"},
		{"after", "unknown-shell", "completion", "arg_value_invalid", `unknown shell "mercury": valid shells are bash, elvish, fish, nu, pwsh, zsh`, "mercury", "❌ unknown shell \"mercury\": valid shells are bash, elvish, fish, nu, pwsh, zsh\n💡 That isn't one of the accepted values — run the command with `--help` to see the choices.\n"},
	} {
		t.Run(tc.lane+"/"+tc.scenario, func(t *testing.T) {
			meta := map[string]any{"command": tc.operation, "timestamp": "2026-01-02T12:30:00Z", "request_id": "independent-completion-refusal", "exit_code": 3}
			if tc.lane == "after" {
				meta["contract_version"] = "1"
			}
			frame := map[string]any{"ok": false, "meta": meta, "data": nil, "warnings": []any{}, "errors": []any{map[string]any{"code": tc.code, "message": tc.message, "field": tc.field}}}
			raw, err := json.Marshal(frame)
			if err != nil {
				t.Fatal(err)
			}
			if !parityCompletionInterfaceRefusal(raw, []byte(tc.diagnostic), tc.lane, tc.scenario, 3) {
				t.Fatal("independent completion refusal and diagnostics refused")
			}
			for name, wrong := range map[string]string{"extra": tc.diagnostic + "extra\n", "wrong": "different diagnostic\n"} {
				t.Run(name, func(t *testing.T) {
					if parityCompletionInterfaceRefusal(raw, []byte(wrong), tc.lane, tc.scenario, 3) {
						t.Fatal("changed completion diagnostics admitted")
					}
				})
			}
			if tc.lane == "after" && parityCompletionInterfaceRefusal(raw, nil, tc.lane, tc.scenario, 3) {
				t.Fatal("missing completion diagnostics admitted")
			}
		})
	}
}
