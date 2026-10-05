package prompt_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/matcra587/jira-cli/internal/cli/prompt"
)

func TestAccessibleInputDefault(t *testing.T) {
	for _, tc := range []struct {
		name, initial, input, want string
		validationError            bool
	}{
		{name: "Enter retains displayed default", initial: "stored@example.invalid", input: "\n", want: "stored@example.invalid"},
		{name: "whitespace retains default", initial: "stored@example.invalid", input: " \t\n", want: "stored@example.invalid"},
		{name: "replacement is editable", initial: "stored@example.invalid", input: "edited@example.invalid\n", want: "edited@example.invalid"},
		{name: "missing default requires input", input: "\nauthored@example.invalid\n", want: "authored@example.invalid", validationError: true},
		{name: "invalid default requires correction", initial: "invalid", input: "\ncorrected@example.invalid\n", want: "corrected@example.invalid", validationError: true},
		{name: "replacement is validated", initial: "stored@example.invalid", input: "invalid\ncorrected@example.invalid\n", want: "corrected@example.invalid", validationError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := tc.initial
			field := prompt.WithAccessibleDefault(huh.NewInput().Title("Email").Value(&value), func(s string) error {
				if !strings.Contains(s, "@") {
					return errors.New("email is required")
				}
				return nil
			})
			var screen bytes.Buffer
			if err := field.RunAccessible(&screen, strings.NewReader(tc.input)); err != nil {
				t.Fatal(err)
			}
			shown := ansi.Strip(screen.String())
			if value != tc.want || strings.Contains(shown, "email is required") != tc.validationError {
				t.Fatalf("answer or validation changed: value=%q screen=%q", value, shown)
			}
			if tc.initial != "" {
				defaultAt, questionAt := strings.Index(shown, tc.initial), strings.Index(shown, "Email")
				if defaultAt < 0 || defaultAt >= questionAt || strings.Count(shown, tc.initial) != 1 {
					t.Fatalf("default must be displayed once before input: %q", shown)
				}
			} else if strings.Contains(shown, "Default:") {
				t.Fatalf("invented an empty default: %q", shown)
			}
		})
	}
}

func TestAccessibleInputDefaultSanitizesDisplay(t *testing.T) {
	value := "stored\x1b[2J@example.invalid"
	field := prompt.WithAccessibleDefault(huh.NewInput().Title("Email").Value(&value), func(string) error { return nil })
	var screen bytes.Buffer
	if err := field.RunAccessible(&screen, strings.NewReader("\n")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(screen.String(), "\x1b[2J") || !strings.Contains(ansi.Strip(screen.String()), "stored@example.invalid") || value != "stored\x1b[2J@example.invalid" {
		t.Fatalf("default display changed terminal controls or the stored value: %q", screen.String())
	}
}
