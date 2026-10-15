package issue

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
)

func TestCommentVisibilityRefusesBeforeClient(t *testing.T) {
	for _, verb := range []string{"add", "edit"} {
		for _, route := range []string{"--markdown", "--markdown-file", "--json-input"} {
			t.Run(verb+"/"+route, func(t *testing.T) {
				calls := 0
				deps := Deps{JiraClient: func(context.Context) (core.JiraClient, error) {
					calls++
					return nil, errors.New("client factory must not be entered")
				}}
				var cmd *cobra.Command
				args := []string{"KEY-1"}
				if verb == "add" {
					cmd = newCommentAdd(deps)
				} else {
					cmd = newCommentEdit(deps)
					args = append(args, "10042")
				}
				value := "Authored body"
				if route != "--markdown" {
					value = filepath.Join(t.TempDir(), "missing-input")
				}
				cmd.SetArgs(append(args, route, value, "--visibility-role="))
				cmd.SilenceErrors, cmd.SilenceUsage = true, true
				err := cmd.Execute()
				var input *cli.InputError
				if !errors.As(err, &input) || input.Code != cli.CodeFlagValueInvalid || input.Field != flagVisibilityRole || calls != 0 {
					t.Fatalf("empty visibility entered the client factory or escaped validation: err=%v calls=%d", err, calls)
				}
			})
		}
	}
}
