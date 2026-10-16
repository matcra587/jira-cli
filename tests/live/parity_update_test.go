//go:build live

package live

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func parityUpdateManagerObservation() error {
	raw, err := json.Marshal(os.Args)
	if err != nil {
		return err
	}
	return os.WriteFile(os.Getenv("JIRA_PARITY_UPDATE_MANAGER_MARKER"), raw, 0o600)
}

func parityUpdateData(raw []byte, lane, channel, version string, preview bool) bool {
	if !jsontext.Value(raw).IsValid() {
		return false
	}
	hint := "Install a published release, or rebuild using the original installation method."
	switch channel {
	case "scoop":
		hint = "scoop update jira"
	case "mise":
		hint = "mise up github:matcra587/jira-cli"
	case "unknown":
		if lane != "after" {
			return false
		}
	default:
		return false
	}
	want := map[string]any{"channel": channel, "current": version, "managed": channel != "unknown", "hint": hint, "updated": false, "dry_run": preview}
	if lane == "after" {
		want["attempted"], want["uncertain"] = false, false
	} else if lane != "before" {
		return false
	}
	expected, _ := json.Marshal(want)
	return parityAuthoringJSON(raw, expected)
}

func parityUpdateUnknownError(raw []byte, lane string) bool {
	var frame map[string]json.RawMessage
	if !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &frame) != nil {
		return false
	}
	want := `[{"code":"update_unsupported","message":"the installation method is unknown; no executable was changed","hint":"Inspect retained data and verify mutation outcomes before retrying. Use the original installation method, or install a published release; inspect ` + "`jira update --help`" + `."}]`
	if lane != "after" {
		return false
	}
	return parityAuthoringJSON(frame["errors"], []byte(want))
}

func parityUpdateUnknownDiagnostics(raw []byte) bool {
	return string(raw) == "❌ the installation method is unknown; no executable was changed\n💡 Inspect retained data and verify mutation outcomes before retrying. Use the original installation method, or install a published release; inspect `jira update --help`.\n"
}

func parityUpdateManagerUnentered(marker string) bool {
	_, err := os.Lstat(marker)
	return os.IsNotExist(err)
}

func parityUpdateManagerFixtures(t *testing.T, home string) (string, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	tools := filepath.Join(home, "owned-tools")
	if err := os.Mkdir(tools, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mise", "scoop"} {
		tool := filepath.Join(tools, name)
		if strings.EqualFold(filepath.Ext(executable), ".exe") {
			tool += ".exe"
		}
		if err := os.WriteFile(tool, body, 0o700); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(home, name+"-positive-control.json")
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		cmd := exec.CommandContext(ctx, tool, "--version")
		cmd.Dir = home
		cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "APPDATA=" + home, "LOCALAPPDATA=" + home, "XDG_CONFIG_HOME=" + home, "XDG_CACHE_HOME=" + home, "PATH=" + tools, "JIRA_PARITY_UPDATE_MANAGER_OBSERVER=1", "JIRA_PARITY_UPDATE_MANAGER_MARKER=" + marker}
		for _, variable := range []string{"PATHEXT", "SystemRoot", "WINDIR"} {
			if value, exists := os.LookupEnv(variable); exists {
				cmd.Env = append(cmd.Env, variable+"="+value)
			}
		}
		err := cmd.Run()
		cancel()
		if err != nil {
			t.Fatal("owned installer observer did not execute and join: ", err)
		}
		raw, err := os.ReadFile(marker)
		want, _ := json.Marshal([]string{tool, "--version"})
		if err != nil || !parityAuthoringJSON(raw, want) {
			t.Fatal("owned installer observer lost its literal positive-control argv")
		}
	}
	return tools, filepath.Join(home, "unexpected-installer-entry.json")
}

func TestParityActualBinaryUpdateLocalRouting(t *testing.T) {
	lanes := []parityLane{{Name: "before", Binary: os.Getenv("JIRA_PARITY_BEFORE_BINARY"), Digest: os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")}}
	if lanes[0].Binary == "" || lanes[1].Binary == "" {
		t.Skip("requires both admitted binaries")
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "local update routing entered HTTP", 500)
	}))
	defer server.Close()
	for _, lane := range lanes {
		for _, channel := range []string{"mise", "scoop", "unknown"} {
			// The frozen legacy binary is release-stamped; an unmanaged copy
			// remains an archive installation. The current source build has
			// different provenance, so its refusal is a current-only control.
			if channel == "unknown" && lane.Name == "before" {
				continue
			}
			t.Run(lane.Name+"/"+channel, func(t *testing.T) {
				if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
					t.Fatal(err)
				}
				home := t.TempDir()
				config := parityCachePublicConfig(t, home, lane.Name, server.URL)
				directory := filepath.Join(home, "disposable-installation")
				switch channel {
				case "mise":
					directory = filepath.Join(home, "mise", "installs", "jira", "fixture")
				case "scoop":
					directory = filepath.Join(home, "scoop", "apps", "jira", "fixture")
				}
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				suffix := ""
				if strings.EqualFold(filepath.Ext(lane.Binary), ".exe") {
					suffix = ".exe"
				}
				installed := filepath.Join(directory, "jira-owned-installed"+suffix)
				body, err := os.ReadFile(lane.Binary)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(installed, body, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := parityFingerprint(installed, lane.Digest); err != nil {
					t.Fatal(err)
				}
				tools, marker := parityUpdateManagerFixtures(t, home)
				extra := []string{"PATH=" + tools, "JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_PARITY_UPDATE_MANAGER_OBSERVER=1", "JIRA_PARITY_UPDATE_MANAGER_MARKER=" + marker, "HTTPS_PROXY=" + server.URL, "HTTP_PROXY=" + server.URL, "NO_PROXY="}
				control := parityFixtureProcessAt(t, lane.Name, installed, server.URL, home, parityFixtureInput{Config: config}, extra, "version")
				evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: control.Argv, Exit: control.Exit, OutputBytes: control.Stdout, StderrBytes: control.Stderr})
				parityAuthoringData(t, control.Stdout, lane.Name, "version", 0)
				var version struct{ Version string }
				wantVersion := "v0.15.2"
				if lane.Name == "after" {
					wantVersion = "v0.16.0-dev"
				}
				if control.Exit != 0 || len(control.Stderr) != 0 || json.Unmarshal(parityOriginalFrameData(control.Stdout), &version) != nil || version.Version != wantVersion {
					t.Fatal("disposable selected binary version did not actually execute")
				}
				for _, command := range []string{"update", "up"} {
					for _, mode := range []string{"json", "compact"} {
						for _, step := range []string{"preview", "live", "forced"} {
							t.Run(command+"/"+mode+"/"+step, func(t *testing.T) {
								before := parityLocalTree(t, home)
								args := []string{command}
								switch step {
								case "preview":
									args = append(args, "--dry-run")
								case "forced":
									args = append(args, "--force")
								}
								result := parityFixtureProcessAt(t, lane.Name, installed, server.URL, home, parityFixtureInput{Config: config, Output: mode}, extra, args...)
								evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
								if calls.Load() != 0 || channel != "unknown" && len(result.Stderr) != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
									t.Fatalf("local update routing changed effects: calls%d stderr%q unchanged%t public%s", calls.Load(), result.Stderr, reflect.DeepEqual(before, parityLocalTree(t, home)), result.Stdout)
								}
								if !parityUpdateManagerUnentered(marker) {
									t.Fatal("local update routing entered an installer observer")
								}
								data := result.Stdout
								if channel == "unknown" {
									if result.Exit != 3 || !parityUpdateUnknownError(result.Stdout, lane.Name) || !parityUpdateUnknownDiagnostics(result.Stderr) {
										t.Fatalf("unknown update lost complete refusal: exit%d public%s", result.Exit, result.Stdout)
									}
									parityAuthoringData(t, result.Stdout, lane.Name, "update", 3)
									data = parityOriginalFrameData(result.Stdout)
								} else {
									if result.Exit != 0 {
										t.Fatal("managed update route failed")
									}
									if mode == "json" {
										parityAuthoringData(t, result.Stdout, lane.Name, "update", 0)
										data = parityOriginalFrameData(result.Stdout)
									}
								}
								if !parityUpdateData(data, lane.Name, channel, wantVersion, step == "preview") {
									t.Fatalf("update lost its exact lane-specific local outcome: %s", data)
								}
								check := fmt.Sprintf("C067.update.local.%s.%s.%s.%s", channel, command, mode, step)
								evidence.Checks[check] = append(evidence.Checks[check], lane.Name)
							})
							if t.Failed() {
								return
							}
						}
					}
				}
			})
		}
	}
	saveParityLocal(t, "update-local", evidence)
}

func TestParityUpdateRequiresCompleteLocalFacts(t *testing.T) {
	t.Parallel()
	valid := `{"channel":"mise","current":"v0.16.0-dev","managed":true,"hint":"mise up github:matcra587/jira-cli","updated":false,"dry_run":true,"attempted":false,"uncertain":false}`
	if !parityUpdateData([]byte(valid), "after", "mise", "v0.16.0-dev", true) {
		t.Fatal("complete independently authored update route refused")
	}
	for name, invalid := range map[string]string{"wrong_owner": strings.ReplaceAll(valid, `"channel":"mise"`, `"channel":"scoop"`), "wrong_version": strings.ReplaceAll(valid, "v0.16.0-dev", "v9.9.9"), "wrong_hint": strings.ReplaceAll(valid, "mise up", "mise install"), "invented_write": strings.ReplaceAll(valid, `"updated":false`, `"updated":true`), "invented_attempt": strings.ReplaceAll(valid, `"attempted":false`, `"attempted":true`), "missing_uncertainty": strings.ReplaceAll(valid, `,"uncertain":false`, ""), "invented_release": strings.ReplaceAll(valid, `"channel":"mise"`, `"channel":"mise","latest":"v9.9.9"`), "duplicate": strings.ReplaceAll(valid, `"managed":true`, `"managed":true,"managed":true`)} {
		t.Run(name, func(t *testing.T) {
			if parityUpdateData([]byte(invalid), "after", "mise", "v0.16.0-dev", true) {
				t.Fatal("update admitted foreign or invented local facts")
			}
		})
	}
}

func TestParityUpdateUnknownRequiresRetainedRefusal(t *testing.T) {
	t.Parallel()
	valid := `{"data":{"channel":"unknown","current":"v0.16.0-dev","managed":false,"attempted":false,"updated":false,"uncertain":false,"dry_run":true,"hint":"Install a published release, or rebuild using the original installation method."},"errors":[{"code":"update_unsupported","message":"the installation method is unknown; no executable was changed","hint":"Inspect retained data and verify mutation outcomes before retrying. Use the original installation method, or install a published release; inspect ` + "`jira update --help`" + `."}]}`
	admit := func(raw string) bool {
		return parityUpdateUnknownError([]byte(raw), "after") && parityUpdateData(parityOriginalFrameData([]byte(raw)), "after", "unknown", "v0.16.0-dev", true)
	}
	if !admit(valid) {
		t.Fatal("complete independent retained refusal refused")
	}
	for name, invalid := range map[string]string{"wrong_reason": strings.ReplaceAll(valid, "the installation method is unknown", "authentication failed"), "wrong_hint": strings.ReplaceAll(valid, "Inspect retained data", "Replay this update"), "missing_errors": strings.ReplaceAll(valid, `"errors":`, `"missing_errors":`), "lost_data": strings.ReplaceAll(valid, `"data":{`, `"missing_data":{`), "invented_write": strings.ReplaceAll(valid, `"updated":false`, `"updated":true`), "wrong_channel": strings.ReplaceAll(valid, `"channel":"unknown"`, `"channel":"github-archive"`), "missing_outcome": strings.ReplaceAll(valid, `"attempted":false,`, ""), "duplicate_error": strings.ReplaceAll(valid, `"code":"update_unsupported"`, `"code":"update_unsupported","code":"update_unsupported"`)} {
		t.Run(name, func(t *testing.T) {
			if !json.Valid([]byte(invalid)) {
				t.Fatal("negative update refusal must remain valid JSON")
			}
			if admit(invalid) {
				t.Fatal("update admitted unrelated refusal or lost installation facts")
			}
		})
	}
	diagnostics := []byte("❌ the installation method is unknown; no executable was changed\n💡 Inspect retained data and verify mutation outcomes before retrying. Use the original installation method, or install a published release; inspect `jira update --help`.\n")
	if !parityUpdateUnknownDiagnostics(diagnostics) || parityUpdateUnknownDiagnostics(append(append([]byte{}, diagnostics...), []byte("Executable update observed.\n")...)) {
		t.Fatal("unknown update diagnostics admitted contradictory installation claims")
	}
}

func TestParityUpdateInstallerEntryCannotEarnNoEntryCredit(t *testing.T) {
	t.Parallel()
	marker := filepath.Join(t.TempDir(), "entry.json")
	if !parityUpdateManagerUnentered(marker) {
		t.Fatal("unused installer observer marker was not absent")
	}
	if err := os.WriteFile(marker, []byte(`["owned-tool","upgrade"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if parityUpdateManagerUnentered(marker) {
		t.Fatal("executed installer observer earned no-entry credit")
	}
}
