//go:build linux && live

package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gechr/x/shell"
	"golang.org/x/sys/unix"
)

// This supplementary lane consumes a digest-pinned artifact already observed by
// the owned live Jira suite. It exercises local public editing, never a Jira write or
// a historical counterpart. Each scenario receives its own disposable copy.
func TestParityDescriptionEditorActualBinary(t *testing.T) {
	binary := os.Getenv("JIRA_PARITY_DESCRIPTION_BINARY")
	if binary == "" {
		t.Skip("requires an admitted binary and independently verified live Jira draft")
	}
	digest := os.Getenv("JIRA_PARITY_DESCRIPTION_SHA256")
	artifact := parityArchiveArtifact{Binary: binary, Digest: digest}
	parityArchiveFingerprint(t, artifact)
	t.Cleanup(func() { parityArchiveFingerprint(t, artifact) })
	seed := os.Getenv("JIRA_PARITY_DESCRIPTION_SOURCE_DIRECTORY")
	source, err := os.ReadFile(filepath.Join(seed, "source.json"))
	if err != nil || len(os.Getenv("JIRA_PARITY_DESCRIPTION_SOURCE_SHA256")) != 64 || fmt.Sprintf("%x", sha256.Sum256(source)) != os.Getenv("JIRA_PARITY_DESCRIPTION_SOURCE_SHA256") {
		t.Fatal("live Jira source artifact is missing or changed")
	}
	var snapshot map[string]json.RawMessage
	var encoded string
	if json.Unmarshal(source, &snapshot) != nil || json.Unmarshal(snapshot["description_bytes"], &encoded) != nil {
		t.Fatal("source artifact lacks exact native bytes")
	}
	native, err := base64.StdEncoding.DecodeString(encoded)
	const authoredNative = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"source alpha"}]},{"type":"panel","attrs":{"panelType":"note","localId":"11111111-1111-4111-8111-111111111111"},"content":[{"type":"paragraph","content":[{"type":"text","text":"protected native panel"}]}]}]}`
	if err != nil || !parityArchiveJSON(native, []byte(authoredNative)) || !parityArchiveJSON(snapshot["description"], native) {
		t.Fatal("live Jira source differs from the independent authored native document")
	}
	anchor := fmt.Sprintf("%x", sha256.Sum256(native))
	markdown := []byte(fmt.Sprintf("<!-- jira-adf:begin %s-0 editable -->\n\nsource alpha\n\n<!-- jira-adf:end %s-0 -->\n\n<!-- jira-adf:begin %s-1 protected -->\n\n> **note**\n>\n> protected native panel\n\n<!-- jira-adf:end %s-1 -->\n\n", anchor, anchor, anchor, anchor))
	manifest, err := os.ReadFile(filepath.Join(seed, "manifest.json"))
	expectedManifest, _ := json.Marshal(map[string]any{"version": 1, "source_sha256": fmt.Sprintf("%x", sha256.Sum256(source)), "source_bytes": len(source), "initial_markdown_sha256": fmt.Sprintf("%x", sha256.Sum256(markdown))})
	if err != nil || !parityArchiveJSON(manifest, expectedManifest) {
		t.Fatal("live Jira readiness manifest does not bind the independent original Markdown")
	}
	evidence := os.Getenv("JIRA_PARITY_DESCRIPTION_EDITOR_EVIDENCE_DIR")
	if err := parityArchiveOpenEvidence(evidence); err != nil {
		t.Fatal(err)
	}
	var receipts []parityArchiveReceipt
	t.Cleanup(func() {
		if err := parityArchiveWriteReceipts(evidence, receipts); err != nil {
			t.Error(err)
		}
	})
	var calls atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "local editing entered HTTP", http.StatusBadGateway)
	}))
	defer trap.Close()
	versionHome := t.TempDir()
	env := []string{"PATH=", "JIRA_KEYRING=false", "JIRA_SECRET_BACKEND=env", "JIRA_API_TOKEN=", "JIRA_NO_UPDATE_CHECK=1", "HTTP_PROXY=" + trap.URL, "HTTPS_PROXY=" + trap.URL, "NO_PROXY=", "XDG_CONFIG_HOME=" + versionHome, "XDG_CACHE_HOME=" + versionHome}
	version, diagnostics, exit := runExecutableIOInDirStarted(t, binary, versionHome, env, nil, nil, nil, "version", "--output=json", "--no-input")
	var identity struct {
		Data struct {
			Commit string `json:"commit"`
			Dirty  *bool  `json:"dirty"`
		} `json:"data"`
	}
	if exit != 0 || diagnostics != "" || json.Unmarshal([]byte(version), &identity) != nil || identity.Data.Commit == "" || identity.Data.Commit != os.Getenv("JIRA_PARITY_DESCRIPTION_REVISION") || identity.Data.Dirty == nil || *identity.Data.Dirty {
		t.Fatal("selected clean CLI version did not execute")
	}
	for _, mode := range []string{"json", "compact"} {
		for _, outcome := range []string{"success", "unchanged", "failure", "invalid", "graceful", "ignore"} {
			t.Run(mode+"/"+outcome, func(t *testing.T) {
				home := filepath.Join(evidence, mode+"-"+outcome)
				directory := filepath.Join(home, "draft spaces 古い '$;&")
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				for name, body := range map[string][]byte{"source.json": source, "description.md": markdown, "manifest.json": manifest} {
					if err := os.WriteFile(filepath.Join(directory, name), body, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				saved := bytes.Replace(markdown, []byte("source alpha"), []byte("authored alpha"), 1)
				switch outcome {
				case "unchanged":
					saved = bytes.Clone(markdown)
				case "invalid":
					saved = []byte{0xff}
				}
				replacement, marker, release := filepath.Join(home, "replacement '$;.md"), filepath.Join(home, "ready.json"), filepath.Join(home, "release")
				if err := os.WriteFile(replacement, saved, 0o600); err != nil {
					t.Fatal(err)
				}
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				helperMode := outcome
				if helperMode == "unchanged" || helperMode == "invalid" {
					helperMode = "success"
				}
				editorArgv := []string{executable, "-test.run=^TestIssueDraftEditorHelperProcess$", "--", helperMode, marker, replacement}
				quoted := make([]string, len(editorArgv))
				for i, arg := range editorArgv {
					quoted[i] = shell.Quote(arg)
				}
				diagnosticMaster, diagnostics := editorPTY(t)
				_, input := editorPTY(t)
				beforeInput, beforeDiagnostics := editorTermios(t, input), editorTermios(t, diagnostics)
				var pid int
				started := func(process *os.Process) {
					completed := false
					defer func() {
						if !completed {
							// A failed readiness assertion must not leave the helper
							// waiting for release while the outer process unwinds.
							if pid > 0 {
								_ = unix.Kill(pid, unix.SIGKILL)
							}
							_ = os.WriteFile(release, []byte("release\n"), 0o600)
						}
					}()
					ready := waitEditorReady(t, marker)
					pid = ready.PID
					t.Cleanup(func() {
						if pid > 0 {
							_ = unix.Kill(pid, unix.SIGKILL)
						}
					})
					body, readErr := os.ReadFile(marker)
					var observation struct {
						Args []string `json:"args"`
					}
					want := []string{helperMode, marker, replacement, filepath.Join(directory, "description.md")}
					if readErr != nil || json.Unmarshal(body, &observation) != nil || !slices.Equal(observation.Args, want) || ready.Scratch != want[3] {
						t.Fatal("editor did not receive exact literal arguments")
					}
					if editorTermios(t, input) == beforeInput || editorTermios(t, diagnostics) == beforeDiagnostics {
						t.Fatal("editor did not change both borrowed terminal modes")
					}
					if err := os.WriteFile(release, []byte("release\n"), 0o600); err != nil {
						t.Fatal(err)
					}
					if outcome == "graceful" || outcome == "ignore" {
						if err := process.Signal(os.Interrupt); err != nil {
							t.Fatal(err)
						}
					}
					completed = true
				}
				local := append(slices.Clone(env), "XDG_CONFIG_HOME="+home, "XDG_CACHE_HOME="+home, "JIRA_BASE_URL=https://unreachable.example.invalid", "JIRA_READ_ONLY=true", "JIRA_EDITOR="+strings.Join(quoted, " "), "JIRA_TEST_DRAFT_EDITOR_PROCESS=1", "JIRA_TEST_DRAFT_EDITOR_RELEASE="+release, "GORACE=atexit_sleep_ms=0")
				argv := []string{"issue", "edit", "resume", "--directory", directory, "--output=" + mode}
				stdout, _, exit := runExecutableIOInDirStarted(t, binary, home, local, input, diagnostics, started, argv...)
				receipts = append(receipts, parityArchiveReceipt{Lane: "after", Scenario: outcome, Output: mode, BinarySHA256: digest, Argv: argv, Exit: exit, Stdout: stdout, Requests: map[string]int{"observed_http": int(calls.Load())}})
				if err := unix.SetNonblock(int(diagnosticMaster.Fd()), true); err != nil {
					t.Fatal(err)
				}
				buffer := make([]byte, 64*1024)
				count, readErr := unix.Read(int(diagnosticMaster.Fd()), buffer)
				if readErr != nil || count <= 0 {
					t.Fatal("editor diagnostic screen was not observed", readErr)
				}
				terminal := string(buffer[:count])
				receipts[len(receipts)-1].Stderr = terminal
				if !strings.Contains(terminal, "owned-editor-stdout") || !strings.Contains(terminal, "owned-editor-stderr") || strings.Contains(stdout, "owned-editor-") {
					t.Fatal("editor screen was missing or polluted command stdout")
				}
				if editorTermios(t, input) != beforeInput || editorTermios(t, diagnostics) != beforeDiagnostics {
					t.Fatal("CLI failed to restore borrowed terminal modes")
				}
				if err := unix.Kill(pid, 0); !errors.Is(err, unix.ESRCH) {
					t.Fatal("CLI returned without joining its editor", err)
				}
				pid = 0
				wantExit, code := 0, ""
				switch outcome {
				case "failure":
					wantExit, code = 3, "editor_failed"
				case "invalid":
					wantExit, code = 3, "issue_edit_draft_invalid"
				case "graceful", "ignore":
					wantExit, code = 6, "canceled"
				}
				if exit != wantExit {
					t.Fatalf("editor outcome exit %d, want %d; retained raw receipt", exit, wantExit)
				}
				raw := []byte(stdout)
				if mode == "json" || exit != 0 {
					data, failures, valid := parityMachineFrame(raw, "after", "issue.edit.resume", exit)
					if !valid || !parityDescriptionEditorErrors(failures, outcome, executable) {
						t.Fatal("editor outcome lacks its complete machine result and exact refusal")
					}
					raw = data
				}
				state := map[string]any{"directory": directory, "source_file": filepath.Join(directory, "source.json"), "markdown_file": filepath.Join(directory, "description.md"), "manifest_file": filepath.Join(directory, "manifest.json"), "directory_created": false, "source_published": false, "markdown_published": false, "ready": true, "durability": "not_checked", "editor_attempted": true, "editor_succeeded": outcome == "success" || outcome == "unchanged" || outcome == "invalid"}
				want := map[string]any{"dry_run": false, "draft": state, "source": json.RawMessage(source), "editor": editorArgv}
				if code == "" {
					candidate := bytes.Replace(native, []byte("source alpha"), []byte("authored alpha"), 1)
					if outcome == "unchanged" {
						candidate = native
					}
					want["description"], want["changed"] = json.RawMessage(candidate), outcome != "unchanged"
				}
				expected, _ := json.Marshal(want)
				if !parityArchiveJSON(raw, expected) {
					t.Fatal("complete editor candidate or recovery receipt differs; retained raw receipt")
				}
				for name, expected := range map[string][]byte{"source.json": source, "description.md": saved, "manifest.json": manifest} {
					actual, err := os.ReadFile(filepath.Join(directory, name))
					if err != nil || !bytes.Equal(actual, expected) {
						t.Fatal("editor lost recoverable work or changed the native preimage: "+name, err)
					}
				}
				entries, err := os.ReadDir(directory)
				if err != nil || len(entries) != 3 || calls.Load() != 0 {
					t.Fatal("local editor changed draft membership or entered HTTP")
				}
			})
		}
	}
	// The seed remains an immutable historical observation, even though its owned
	// Jira parent was already deleted by the independent live Jira cleanup owner.
	after, err := os.ReadFile(filepath.Join(seed, "source.json"))
	if err != nil || !bytes.Equal(after, source) {
		t.Fatal("supplementary editing changed the original live Jira source")
	}
}

func parityDescriptionEditorErrors(raw []byte, outcome, executable string) bool {
	if outcome == "success" || outcome == "unchanged" {
		return parityArchiveJSON(raw, []byte(`[]`))
	}
	code, message, hint := "", "", ""
	switch outcome {
	case "failure":
		code, message, hint = "editor_failed", fmt.Sprintf("editor %q failed: exit status 29", executable), "Inspect any saved edit and check the configured editor before retrying."
	case "invalid":
		code, message, hint = "issue_edit_draft_invalid", "invalid issue editing draft: saved Markdown is not UTF-8", "Keep source.json and description.md. Inspect the readiness manifest and native backup; repair authored Markdown with issue edit resume. No candidate or Jira write was accepted."
	case "graceful", "ignore":
		suffix := "context canceled"
		if outcome == "ignore" {
			suffix = "signal: killed"
		}
		code, message, hint = "canceled", fmt.Sprintf("context canceled\neditor %q failed: %s", executable, suffix), "The command was interrupted. Inspect any attempted mutations before retrying; rerun interrupted reads as needed."
	default:
		return false
	}
	expected, _ := json.Marshal([]any{map[string]any{"code": code, "message": message, "hint": "Inspect retained data and verify mutation outcomes before retrying. " + hint}})
	return parityArchiveJSON(raw, expected)
}

func TestParityDescriptionEditorRefusalRequiresExactReason(t *testing.T) {
	valid := `[{"code":"issue_edit_draft_invalid","message":"invalid issue editing draft: saved Markdown is not UTF-8","hint":"Inspect retained data and verify mutation outcomes before retrying. Keep source.json and description.md. Inspect the readiness manifest and native backup; repair authored Markdown with issue edit resume. No candidate or Jira write was accepted."}]`
	if !parityDescriptionEditorErrors([]byte(valid), "invalid", "unused") {
		t.Fatal("complete invalid-save refusal refused")
	}
	for name, raw := range map[string]string{"missing": "[]", "wrong_code": strings.Replace(valid, "issue_edit_draft_invalid", "editor_failed", 1), "unrelated_reason": strings.Replace(valid, "saved Markdown is not UTF-8", "unknown command", 1), "missing_recovery": strings.Replace(valid, "Inspect retained data and verify mutation outcomes before retrying. ", "", 1), "null": "null"} {
		t.Run(name, func(t *testing.T) {
			if parityDescriptionEditorErrors([]byte(raw), "invalid", "unused") {
				t.Fatal("unrelated or incomplete refusal admitted")
			}
		})
	}
}

func TestParityDescriptionEditorFrameRequiresExactOperation(t *testing.T) {
	valid := `{"ok":true,"meta":{"command":"issue.edit.resume","contract_version":"1","timestamp":"2026-10-16T00:00:00Z","request_id":"0123456789abcdef0123456789abcdef"},"data":{},"errors":[],"warnings":[]}`
	if _, _, ok := parityMachineFrame([]byte(valid), "after", "issue.edit.resume", 0); !ok {
		t.Fatal("complete independently authored frame refused")
	}
	for name, raw := range map[string]string{"wrong_operation": strings.Replace(valid, "issue.edit.resume", "update", 1), "missing_ok": strings.Replace(valid, `"ok":true,`, "", 1), "null_ok": strings.Replace(valid, `"ok":true`, `"ok":null`, 1), "missing_contract": strings.Replace(valid, `"contract_version":"1",`, "", 1), "missing_errors": strings.Replace(valid, `,"errors":[]`, "", 1)} {
		t.Run(name, func(t *testing.T) {
			if _, _, ok := parityMachineFrame([]byte(raw), "after", "issue.edit.resume", 0); ok {
				t.Fatal("incomplete or unrelated operation admitted")
			}
		})
	}
}
