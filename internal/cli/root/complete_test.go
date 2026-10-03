package root

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

// captureStdout runs fn with stdout redirected and returns what it wrote.
// The predictor laws are about what reaches the shell, so the assertion
// has to be on the real descriptor, not a passed-in writer.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- sb.String()
	}()

	fn()
	os.Stdout = orig
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	out := <-done
	if err := r.Close(); err != nil {
		t.Fatalf("close read end: %v", err)
	}
	return out
}

// TestCompletionHandlerPredictorLaws pins the rules a completion callback
// must obey: an unknown kind prints NOTHING (so the shell offers nothing
// rather than falling back to filenames), a known kind prints one
// candidate per line on stdout, and a predictor that finds nothing prints
// nothing rather than erroring.
func TestCompletionHandlerPredictorLaws(t *testing.T) {
	orig := completionKinds
	t.Cleanup(func() { completionKinds = orig })
	completionKinds = map[string]func(args []string) []string{
		"known":   func([]string) []string { return []string{"alpha\tfirst", "beta\tsecond"} },
		"empty":   func([]string) []string { return nil },
		"echoes":  func(args []string) []string { return args },
		"unwired": nil,
	}

	tests := []struct {
		name string
		kind string
		args []string
		want string
	}{
		{name: "unknown kind stays silent", kind: "nope", want: ""},
		{name: "known kind prints candidates", kind: "known", want: "alpha\tfirst\nbeta\tsecond\n"},
		{name: "empty predictor stays silent", kind: "empty", want: ""},
		{name: "args reach the predictor", kind: "echoes", args: []string{"ABC-"}, want: "ABC-\n"},
		// A key registered with no function is a half-wired predictor. It
		// must behave exactly like an unknown kind rather than calling
		// through a nil value.
		{name: "nil predictor stays silent", kind: "unwired", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := captureStdout(t, func() { completionHandler("fish", tt.kind, tt.args) })
			if got != tt.want {
				t.Errorf("stdout = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCompletionHandlerSurvivesHostilePredictors pins the laws under a
// predictor that misbehaves: a candidate carrying a newline would read as
// two completions to the shell, and a panic mid-loop would leave a partial
// list in the user's terminal. Both must degrade to "offers nothing extra"
// rather than corrupt what the shell sees.
func TestCompletionHandlerSurvivesHostilePredictors(t *testing.T) {
	orig := completionKinds
	t.Cleanup(func() { completionKinds = orig })
	completionKinds = map[string]func(args []string) []string{
		"newline": func([]string) []string { return []string{"good", "bad\nsecond-line", "also-good"} },
		"panics":  func([]string) []string { panic("predictor exploded") },
	}

	got := captureStdout(t, func() { completionHandler("fish", "newline", nil) })
	if got != "good\nalso-good\n" {
		t.Errorf("newline candidate not dropped: %q", got)
	}

	got = captureStdout(t, func() { completionHandler("fish", "panics", nil) })
	if got != "" {
		t.Errorf("panicking predictor wrote to stdout: %q", got)
	}
}

// TestCompletionKindsOmitsUnwiredEntries pins the join the predictor
// guardrail depends on. That guard asks which kinds the handler answers
// and trusts the reply; if a key registered with a nil function counted
// as answered, a half-wired predictor would satisfy the guard while TAB
// stayed dead — reintroducing, through the guard itself, the exact defect
// it exists to catch.
func TestCompletionKindsOmitsUnwiredEntries(t *testing.T) {
	orig := completionKinds
	t.Cleanup(func() { completionKinds = orig })
	completionKinds = map[string]func(args []string) []string{
		"wired":   func([]string) []string { return nil },
		"unwired": nil,
	}

	got := CompletionKinds()
	if len(got) != 1 || got[0] != "wired" {
		t.Errorf("CompletionKinds() = %v, want only the wired kind", got)
	}
}

// captureStderr runs fn with the process stderr redirected and returns
// what it wrote. clog holds the real descriptor — conductor wires its
// writer at ApplyFlags time — so a passed-in writer would capture
// nothing.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- sb.String()
	}()

	fn()
	os.Stderr = orig
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	out := <-done
	if err := r.Close(); err != nil {
		t.Fatalf("close read end: %v", err)
	}
	return out
}

// TestCompletionActionFailuresLeaveTyped pins that the pre-parse
// completion path obeys the same contract as everything else. clib
// rejects any flag ITS scanner does not know once an action is
// requested and words it "unknown flag" — true for clib, false for us,
// since --output is a real flag that merely cannot apply to a raw shell
// script. Reported as clib worded it, the CLI told the reader to fix a
// spelling that was already correct, exited 1 (outside the 0-8
// contract), and wrote no envelope at all in a machine mode.
func TestCompletionActionFailuresLeaveTyped(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	args := []string{"--print-completion", "--output=json"}
	os.Args = append([]string{"jira"}, args...)

	var stdout, stderr bytes.Buffer
	program, sink := newProgram(testDeps(t), &stdout, &stderr)
	handled, code := runCompletionAction(t.Context(), program, sink, args)
	if !handled {
		t.Fatal("completion action was not handled")
	}
	if code != cli.ExitValidation {
		t.Errorf("exit = %d, want the validation contract code %d", code, cli.ExitValidation)
	}

	var frame struct {
		OK     bool `json:"ok"`
		Errors []struct {
			Code  string `json:"code"`
			Field string `json:"field"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil {
		t.Fatalf("machine mode wrote no envelope: %v\nstdout: %q", err, stdout.String())
	}
	if frame.OK || len(frame.Errors) != 1 {
		t.Fatalf("want one error in a failure frame, got %s", stdout.String())
	}
	if frame.Errors[0].Code != cli.CodeFlagNotApplicable {
		t.Errorf("code = %q, want %q", frame.Errors[0].Code, cli.CodeFlagNotApplicable)
	}
	if frame.Errors[0].Field != "output" {
		t.Errorf("field = %q, want the offending flag %q", frame.Errors[0].Field, "output")
	}
}
