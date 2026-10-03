package root

import (
	"context"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestCommandPathAllowsDeclaredParentPositionals(t *testing.T) {
	for _, positional := range []bool{false, true} {
		args := command.ArgSpec{}
		if positional {
			args = command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "VALUE"}}}
		}
		parent := command.MustNew(command.Spec[envelope.VersionOutput]{
			Op: "version", Use: "parent VALUE", Short: "Test runnable parent", Args: args,
			Run: func(context.Context, command.Invocation) (envelope.VersionOutput, error) {
				return envelope.VersionOutput{}, nil
			},
		})
		parent.AddCommand(&cobra.Command{Use: "status", RunE: func(*cobra.Command, []string) error { return nil }})
		root := &cobra.Command{Use: "test"}
		root.AddCommand(parent)
		err := checkCommandPath(root, []string{"parent", "value"})
		if positional && err != nil {
			t.Fatalf("declared positional was mistaken for a child command: %v", err)
		}
		if !positional && (err == nil || cli.MapError(err).Code != cli.CodeCommandUnknown) {
			t.Fatalf("argument-free parent lost unknown-command refusal: %v", err)
		}
		if err := checkCommandPath(root, []string{"parent", "status"}); err != nil {
			t.Fatalf("known child command was refused: %v", err)
		}
	}
}
