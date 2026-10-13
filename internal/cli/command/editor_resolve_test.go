package command

import (
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/cli"
)

func TestSharedEditorResolutionKeepsStoredChoiceAndLiteralArguments(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	selected := shell.Quote(executable) + ` --wait '$HOME' '$(must-not-run)'`
	for _, source := range []string{"stored", "JIRA_EDITOR", "VISUAL", "EDITOR"} {
		t.Run(source, func(t *testing.T) {
			for _, name := range []string{"JIRA_EDITOR", "VISUAL", "EDITOR"} {
				t.Setenv(name, "")
			}
			configured := ""
			if source == "stored" {
				configured = selected
				t.Setenv("VISUAL", "missing-lower-editor")
				t.Setenv("EDITOR", "missing-last-editor")
			} else {
				t.Setenv(source, selected)
			}
			if source == "JIRA_EDITOR" {
				configured = "missing-stored-editor"
			}
			got, err := ResolveEditor(configured)
			want := []string{executable, "--wait", "$HOME", "$(must-not-run)"}
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("selected editor lost its literal argv or stored precedence: got %q, want %q, error %v", got, want, err)
			}
		})
	}
}

func TestSharedEditorResolutionRefusesInvalidWinnerWithoutFallback(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, winner := range []string{"JIRA_EDITOR", "editor"} {
		for _, invalid := range []string{"definitely-not-an-editor-on-this-machine", "'unterminated"} {
			t.Run(winner+"/"+invalid, func(t *testing.T) {
				t.Setenv("JIRA_EDITOR", "")
				t.Setenv("VISUAL", shell.Quote(executable))
				t.Setenv("EDITOR", shell.Quote(executable))
				configured := invalid
				if winner == "JIRA_EDITOR" {
					t.Setenv("JIRA_EDITOR", invalid)
					configured = shell.Quote(executable)
				}
				_, err := ResolveEditor(configured)
				var input *cli.InputError
				if !errors.As(err, &input) || input.Code != cli.CodeEditorMissing || input.Field != winner {
					t.Fatalf("invalid winning choice used a lower fallback or lost its repair field: %v", err)
				}
			})
		}
	}
}
