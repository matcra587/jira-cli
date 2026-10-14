//go:build outputaudit

package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gechr/x/shell"
)

func TestAuditEditorTerminalSuccess(t *testing.T) {
	bin, err := jiraBinary()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"config-edit-terminal-success", "issue-resume-success"} {
		t.Run(id, func(t *testing.T) {
			index := slices.IndexFunc(scenarios, func(s Scenario) bool { return s.ID == id })
			if index < 0 {
				t.Fatal("missing terminal scenario")
			}
			scenario := scenarios[index]
			baseURL := ""
			if scenario.Stub != "" {
				server := httptest.NewServer(stubServers[scenario.Stub]())
				defer server.Close()
				baseURL = server.URL
			}
			for _, mode := range modesFor(scenario.Category) {
				t.Run(string(mode), func(t *testing.T) {
					capture, err := runScenario(bin, scenario, mode, baseURL)
					if err != nil {
						t.Fatal(err)
					}
					if capture.ExitCode != 0 || capture.Stderr != "" {
						t.Fatalf("terminal command did not complete silently: exit=%d stderr=%q", capture.ExitCode, capture.Stderr)
					}
					if mode == ModeHuman || mode == ModeHumanColor {
						want := "no changes"
						if scenario.Op == "issue.edit.resume" {
							want = "This candidate has not changed Jira."
						}
						if !strings.Contains(capture.Stdout, want) {
							t.Fatalf("terminal output lost its actual result: %q", capture.Stdout)
						}
						return
					}
					data := []byte(capture.Stdout)
					if mode == ModeJSON {
						var frame struct {
							OK   bool `json:"ok"`
							Meta struct {
								Command string `json:"command"`
							} `json:"meta"`
							Data json.RawMessage `json:"data"`
						}
						if err := json.Unmarshal(data, &frame); err != nil || !frame.OK || frame.Meta.Command != scenario.Op {
							t.Fatalf("terminal output lost its successful frame: %v; %q", err, capture.Stdout)
						}
						data = frame.Data
					}
					var result struct {
						Changed     *bool           `json:"changed"`
						Editor      []string        `json:"editor"`
						Description json.RawMessage `json:"description"`
					}
					if err := json.Unmarshal(data, &result); err != nil || result.Changed == nil || *result.Changed || len(result.Editor) != 3 || result.Editor[1] != "--audit-editor" || result.Editor[2] != "literal spaces 古い & $ ;" {
						t.Fatalf("terminal output lost the no-op editor result: %v; %q", err, data)
					}
					if scenario.Op == "issue.edit.resume" && (!bytes.Contains(result.Description, []byte(`"nativeMember":"native source value"`)) || !bytes.Contains(result.Description, []byte("9007199254740993"))) {
						t.Fatalf("terminal output lost protected native description fields: %s", result.Description)
					}
				})
			}
		})
	}
}

func TestAuditEditorFixture(t *testing.T) {
	t.Parallel()
	workdir := t.TempDir()
	path := filepath.Join(workdir, "editable spaces 古い & $ ;.txt")
	want := []byte("independent editor input\n古い\n")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	editor, receiptPath, err := auditEditor(workdir)
	if err != nil {
		t.Fatal(err)
	}
	args, err := shell.Split(editor)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], append(args[1:], path)...)
	cmd.Dir = workdir
	cmd.Env = append(scenarioEnv(workdir, Scenario{}, "", ModeJSON), auditEditorReceiptEnv+"="+receiptPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("owned editor did not execute: %v; stderr=%q", err, stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("editor fixture polluted output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if err := checkAuditEditorReceipt(receiptPath, true); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	var receipt auditEditorReceipt
	if err := json.Unmarshal(encoded, &receipt); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(receipt.Argv, []string{"literal spaces 古い & $ ;", path}) || receipt.SHA256 != fmt.Sprintf("%x", sha256.Sum256(want)) {
		t.Fatalf("editor did not observe the literal argv and authored file: %+v", receipt)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("no-op editor changed input: %v; got=%q", err, got)
	}
	// A second invocation must refuse its existing receipt instead of earning
	// credit from stale evidence left by the first process.
	cmd = exec.CommandContext(ctx, args[0], append(args[1:], path)...)
	cmd.Dir, cmd.Env = workdir, append(scenarioEnv(workdir, Scenario{}, "", ModeJSON), auditEditorReceiptEnv+"="+receiptPath)
	stdout.Reset()
	stderr.Reset()
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	var exit *exec.ExitError
	if ctx.Err() != nil || !errors.As(err, &exit) || exit.ExitCode() != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "audit editor receipt already exists:") {
		t.Fatalf("second editor did not enter its exclusive-receipt refusal: %v; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	after, err := os.ReadFile(receiptPath)
	if err != nil || !bytes.Equal(after, encoded) {
		t.Fatalf("refused editor changed the original receipt: %v", err)
	}
}

func TestAuditEditorPreviewAndTerminalRefusal(t *testing.T) {
	t.Parallel()
	bin, err := jiraBinary()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"config-edit-dry-run", "config-edit-no-terminal"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			index := slices.IndexFunc(scenarios, func(s Scenario) bool { return s.ID == id })
			if index < 0 {
				t.Fatal("missing scenario")
			}
			scenario := scenarios[index]
			for _, mode := range modesFor(scenario.Category) {
				capture, err := runScenario(bin, scenario, mode, "")
				if err != nil {
					t.Fatal(err)
				}
				if capture.ExitCode != scenario.ExpectExit || capture.WroteFiles {
					t.Fatalf("%s: exit=%d wrote_files=%v stdout=%q stderr=%q", mode, capture.ExitCode, capture.WroteFiles, capture.Stdout, capture.Stderr)
				}
				if scenario.ExpectCode != "" {
					want := scenario.ExpectCode
					if mode == ModeHuman || mode == ModeHumanColor {
						want = "an editor needs a terminal"
					}
					if !strings.Contains(capture.Stdout+capture.Stderr, want) {
						t.Fatalf("%s: missing intended refusal %q: stdout=%q stderr=%q", mode, want, capture.Stdout, capture.Stderr)
					}
				}
			}
		})
	}
}
