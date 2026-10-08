package root

import (
	"io"
	"slices"
	"testing"

	"github.com/spf13/cobra"
)

func TestHelpCommandCandidatesRespectPaths(t *testing.T) {
	root := &cobra.Command{Use: "fixture"}
	group := &cobra.Command{Use: "group", Aliases: []string{"g"}, Short: "Group commands"}
	leaf := &cobra.Command{Use: "view", Aliases: []string{"v"}, Short: "Show value", Run: func(*cobra.Command, []string) {}}
	group.AddCommand(leaf)
	group.AddCommand(&cobra.Command{Use: "hidden", Hidden: true, Run: func(*cobra.Command, []string) {}})
	group.AddCommand(&cobra.Command{Use: "old", Deprecated: "use view", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(group)
	root.InitDefaultHelpCmd()
	for _, tc := range []struct {
		name string
		path []string
		want []string
	}{
		{name: "root", want: []string{"group\tGroup commands", "g\tGroup commands", "help\tHelp about any command"}},
		{name: "canonical group", path: []string{"group"}, want: []string{"view\tShow value", "v\tShow value"}},
		{name: "group alias", path: []string{"g"}, want: []string{"view\tShow value", "v\tShow value"}},
		{name: "leaf", path: []string{"group", "view"}},
		{name: "leaf alias", path: []string{"g", "v"}},
		{name: "unknown path", path: []string{"missing"}},
		{name: "extra leaf argument", path: []string{"group", "view", "value"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := helpCommandCandidates(root, tc.path)
			if !slices.Equal(got, tc.want) {
				t.Errorf("helpCommandCandidates(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestHelpCompletionDeclaration(t *testing.T) {
	program, _ := newProgram(defaultDeps(), io.Discard, io.Discard)
	cmd, remaining, err := program.Root.Find([]string{"help"})
	if err != nil || len(remaining) != 0 {
		t.Fatalf("find help: remaining=%v err=%v", remaining, err)
	}
	if got := cmd.Annotations["clib"]; got != "dynamic-args='helppath'" {
		t.Errorf("help completion annotation = %q, want helppath", got)
	}
	if cmd.Use != "help [COMMAND...]" {
		t.Errorf("help usage = %q, want a variadic command path", cmd.Use)
	}
	if !slices.Contains(CompletionKinds(), "helppath") {
		t.Error("help completion predictor has no handler")
	}
}
