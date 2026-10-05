package prompt_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli/prompt"
)

// TestAccessibleConfirmation runs the shared form in an isolated process so
// Huh sees the real accessible-mode environment and owns its input normally.
func TestAccessibleConfirmation(t *testing.T) {
	const question = "Remove the stored fixture?"
	const caveat = "The fixture is not revoked."
	if os.Getenv("JIRA_TEST_CONFIRM_CHILD") == "1" {
		answer, err := prompt.Run(t.Context(), prompt.Confirm{
			Question: question, Caveat: os.Getenv("JIRA_TEST_CONFIRM_CAVEAT"),
			Affirmative: "Remove", Negative: "Keep",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprintln(os.Stdout, "answer", answer); err != nil {
			t.Fatal(err)
		}
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	coverageReport := regexp.MustCompile(`^coverage: [0-9]+\.[0-9]+% of statements\n$`)
	for _, tc := range []struct {
		name, input, caveat string
		answer              bool
	}{
		{name: "empty answer refuses", input: "\n", caveat: caveat},
		{name: "negative answer refuses", input: "n\n", caveat: caveat},
		{name: "affirmative answer confirms", input: "y\n", caveat: caveat, answer: true},
		{name: "optional caveat", input: "n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, self, "-test.run=^TestAccessibleConfirmation$")
			cmd.Env = []string{"TERM=dumb", "JIRA_TEST_CONFIRM_CHILD=1", "JIRA_TEST_CONFIRM_CAVEAT=" + tc.caveat}
			cmd.Stdin = strings.NewReader(tc.input)
			cmd.WaitDelay = time.Second
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("accessible form: %v; stderr: %q", err, stderr.String())
			}
			// An instrumented test binary prints its own coverage footer. Only
			// that exact footer may follow the answer and test-runner result.
			want := fmt.Sprintf("answer %t\nPASS\n", tc.answer)
			footer, answered := strings.CutPrefix(stdout.String(), want)
			if !answered || (footer != "" && (testing.CoverMode() == "" || !coverageReport.MatchString(footer))) {
				t.Fatalf("answer or stdout changed: got %q, want %q", stdout.String(), want)
			}
			screen := stderr.String()
			if strings.Count(screen, question) != 1 || !strings.Contains(screen, "[y/N]") {
				t.Fatalf("confirmation lost its identity or default refusal: %q", screen)
			}
			if tc.caveat != "" {
				warning, input := strings.Index(screen, tc.caveat), strings.Index(screen, "[y/N]")
				if warning < 0 || warning >= input || strings.Count(screen, tc.caveat) != 1 {
					t.Fatalf("confirmation must show its caveat once before input: %q", screen)
				}
			} else if strings.Contains(screen, caveat) {
				t.Fatal("confirmation invented an absent caveat")
			}
		})
	}
}
