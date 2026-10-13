//go:build linux

package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gechr/x/shell"
	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"github.com/matcra587/jira-cli/internal/envelope"
)

// TestIssueDraftEditorHelperProcess deliberately leaves both terminal modes
// changed. It saves authored text even on failure or interrupted shutdown.
func TestIssueDraftEditorHelperProcess(t *testing.T) {
	if os.Getenv("JIRA_TEST_DRAFT_EDITOR_PROCESS") != "1" {
		return
	}
	index := slices.Index(os.Args, "--")
	if index < 0 || len(os.Args[index+1:]) != 4 {
		os.Exit(20)
	}
	args := os.Args[index+1:]
	mode, marker, replacement, markdown := args[0], args[1], args[2], args[3]
	interrupt := make(chan os.Signal, 1)
	if mode == "ignore" {
		signal.Ignore(os.Interrupt)
	} else {
		signal.Notify(interrupt, os.Interrupt)
	}
	if _, err := term.MakeRaw(int(os.Stdin.Fd())); err != nil {
		os.Exit(21)
	}
	if _, err := term.MakeRaw(int(os.Stderr.Fd())); err != nil {
		os.Exit(22)
	}
	save := func() {
		data, err := os.ReadFile(replacement)
		if err != nil || os.WriteFile(markdown, data, 0o600) != nil {
			os.Exit(23)
		}
	}
	if mode != "graceful" {
		save()
	}
	if mode == "fifo" {
		if err := os.Remove(markdown); err != nil {
			os.Exit(24)
		}
		if err := unix.Mkfifo(markdown, 0o600); err != nil {
			os.Exit(25)
		}
	}
	if mode == "source_changed" {
		source := filepath.Join(filepath.Dir(markdown), "source.json")
		if err := os.Chmod(source, 0o600); err != nil {
			os.Exit(26)
		}
		if err := os.WriteFile(source, []byte("changed backup"), 0o600); err != nil {
			os.Exit(27)
		}
	}
	data, err := json.Marshal(readyEditor{PID: os.Getpid(), Scratch: markdown})
	if err != nil || os.WriteFile(marker, data, 0o600) != nil {
		os.Exit(28)
	}
	switch mode {
	case "failure":
		os.Exit(29)
	case "graceful":
		<-interrupt
		save()
	case "ignore":
		time.Sleep(time.Hour)
	}
	os.Exit(0)
}

func TestIssueDraftActualEditorRetainsWorkAndRestoresTerminals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode string
		code string
		exit int
	}{
		{mode: "success"},
		{mode: "repair"},
		{mode: "failure", code: "editor_failed", exit: 3},
		{mode: "graceful", code: "canceled", exit: 6},
		{mode: "ignore", code: "canceled", exit: 6},
		{mode: "invalid", code: "issue_edit_draft_invalid", exit: 3},
		{mode: "fifo", code: "issue_edit_draft_invalid", exit: 3},
		{mode: "source_changed", code: "issue_edit_draft_changed", exit: 3},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Parallel()
			server, reads, writes, _ := binaryDraftServer(t)
			env := binaryDraftEnv(t, server.URL)
			dir := t.TempDir()
			directory := filepath.Join(dir, "draft")
			stdout, stderr, exit := runBinaryEnv(t, env, "issue", "edit", "draft", "KEY-1", "--directory", directory, "--output=json")
			if exit != 0 || reads.Load() != 1 || writes.Load() != 0 {
				t.Fatal("draft preparation did not run", exit, stdout, stderr)
			}
			sourcePath, markdownPath := filepath.Join(directory, "source.json"), filepath.Join(directory, "description.md")
			source, err := os.ReadFile(sourcePath)
			if err != nil {
				t.Fatal(err)
			}
			markdown, err := os.ReadFile(markdownPath)
			if err != nil {
				t.Fatal(err)
			}
			authored := bytes.Replace(markdown, []byte("alpha"), []byte("authored"), 1)
			if bytes.Equal(authored, markdown) {
				t.Fatal("authored fixture did not change the editable text")
			}
			if tc.mode == "invalid" {
				authored = []byte{0xff}
			}
			if tc.mode == "repair" {
				if err := os.WriteFile(markdownPath, []byte{0xff}, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			replacement, marker := filepath.Join(dir, "replacement.md"), filepath.Join(dir, "ready.json")
			if err := os.WriteFile(replacement, authored, 0o600); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			editor := strings.Join([]string{shell.Quote(executable), "-test.run=^TestIssueDraftEditorHelperProcess$", "--", tc.mode, shell.Quote(marker), shell.Quote(replacement)}, " ")
			_, input := editorPTY(t)
			_, diagnostics := editorPTY(t)
			beforeInput, beforeDiagnostics := editorTermios(t, input), editorTermios(t, diagnostics)
			var emergency *time.Timer
			var editorPID int
			hook := func(process *os.Process) {
				ready := waitEditorReady(t, marker)
				editorPID = ready.PID
				if ready.Scratch != markdownPath {
					t.Fatal("shared editor received the wrong saved file", ready.Scratch)
				}
				emergency = time.AfterFunc(5*time.Second, func() { _ = process.Kill() })
				if tc.mode == "graceful" || tc.mode == "ignore" {
					if err := process.Signal(os.Interrupt); err != nil {
						t.Fatal(err)
					}
				}
			}
			defer func() {
				if emergency != nil {
					emergency.Stop()
				}
			}()
			// Resume is deliberately disconnected from Jira credentials and routing.
			local := append(append([]string{}, env...), "JIRA_BASE_URL=https://unreachable.example.invalid", "JIRA_EMAIL=", "JIRA_API_TOKEN=", "JIRA_EDITOR="+editor, "JIRA_TEST_DRAFT_EDITOR_PROCESS=1", "GORACE=atexit_sleep_ms=0")
			stdout, stderr, exit = runBinaryIOStarted(t, local, input, diagnostics, hook, "issue", "edit", "resume", "--directory", directory, "--output=json")
			var frame struct {
				OK     bool                          `json:"ok"`
				Data   envelope.IssueEditDraftOutput `json:"data"`
				Errors []struct{ Code string }       `json:"errors"`
			}
			if exit != tc.exit || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK != (tc.code == "") {
				t.Fatal("actual editor invocation failed", exit, stdout, stderr)
			}
			if tc.code != "" && (len(frame.Errors) != 1 || frame.Errors[0].Code != tc.code) {
				t.Fatal("wrong editor failure classification", stdout)
			}
			if frame.Data.Source == nil || string(frame.Data.Source.DescriptionBytes) != binaryDraftNative || !frame.Data.Draft.Ready || !frame.Data.Draft.EditorAttempted || frame.Data.Draft.Directory != directory || reads.Load() != 1 || writes.Load() != 0 {
				t.Fatal("editor lost recovery receipt or opened Jira", stdout)
			}
			if tc.code != "" && (len(frame.Data.Description) != 0 || frame.Data.Changed != nil) {
				t.Fatal("failed editor fabricated a candidate", stdout)
			}
			if tc.code == "" && (!frame.Data.Draft.EditorSucceeded || frame.Data.Changed == nil || !*frame.Data.Changed || !bytes.Contains(frame.Data.Description, []byte("authored")) || !bytes.Contains(frame.Data.Description, []byte("9007199254740993"))) {
				t.Fatal("successful editor lost native candidate", stdout)
			}
			if editorTermios(t, input) != beforeInput || editorTermios(t, diagnostics) != beforeDiagnostics {
				t.Fatal("editor left borrowed terminals changed")
			}
			if err := unix.Kill(editorPID, 0); !errors.Is(err, unix.ESRCH) {
				t.Fatal("editor was not reaped", err)
			}
			if tc.mode != "source_changed" {
				after, err := os.ReadFile(sourcePath)
				if err != nil || !bytes.Equal(after, source) {
					t.Fatal("editor overwrote the native backup", err)
				}
			}
			if tc.mode == "fifo" {
				info, err := os.Lstat(markdownPath)
				if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
					t.Fatal("refusal removed the user's changed member", err)
				}
			} else {
				saved, err := os.ReadFile(markdownPath)
				if err != nil || !bytes.Equal(saved, authored) {
					t.Fatal("editor lost saved work", err)
				}
			}
		})
	}
}
