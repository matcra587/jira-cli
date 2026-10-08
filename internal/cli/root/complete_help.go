package root

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
)

// helpPathCandidates uses the same command tree as execution without resolving
// settings or constructing a Jira client. Forwarded flags are not help topics.
func helpPathCandidates(args []string) []string {
	_, positionals, err := completionPositionals(args)
	if err != nil {
		return nil
	}
	program, _ := newProgramWithTheme(defaultDeps(), io.Discard, io.Discard, cli.BootstrapTheme)
	return helpCommandCandidates(program.Root, positionals)
}

func helpCommandCandidates(root *cobra.Command, path []string) []string {
	cmd, remaining, err := root.Find(path)
	if err != nil || len(remaining) != 0 {
		return nil
	}
	var candidates []string
	for _, child := range cmd.Commands() {
		if child.Hidden || child.Deprecated != "" || (!child.IsAvailableCommand() && child.Name() != "help") {
			continue
		}
		for _, name := range append([]string{child.Name()}, child.Aliases...) {
			if cli.Text(name) != name {
				continue
			}
			candidates = append(candidates, name+"\t"+cli.Text(child.Short))
		}
	}
	return candidates
}
