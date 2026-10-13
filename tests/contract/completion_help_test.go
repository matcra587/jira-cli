package contract

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/gechr/clog"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/root"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/output"
)

func TestCompletionHelpPathsUseRealBinary(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{name: "root", want: []string{"issue", "config", "help", "rn", "up"}},
		{name: "issue", args: []string{"issue"}, want: []string{"attachment", "comment", "list"}},
		{name: "comment", args: []string{"issue", "comment"}, want: []string{"add", "delete", "edit", "list"}},
		{name: "aliases offered", args: []string{"alias"}, want: []string{"list", "ls", "delete", "del", "rm"}},
		{name: "leaf alias", args: []string{"alias", "ls"}},
		{name: "leaf", args: []string{"issue", "list"}},
		{name: "unknown", args: []string{"unavailable-command"}},
		{name: "extra leaf argument", args: []string{"issue", "list", "KEY-1"}},
		{name: "forwarded config", args: []string{"--config", "missing-settings.toml", "issue", "comment"}, want: []string{"add", "delete", "edit", "list"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--@shell=bash", "--@complete=helppath", "--"}, tc.args...)
			stdout, stderr, exit := runBinary(t, args...)
			if exit != 0 || stderr != "" {
				t.Fatalf("help completion %v: exit=%d stderr=%q", tc.args, exit, stderr)
			}
			var names []string
			for _, row := range strings.Split(strings.TrimSpace(stdout), "\n") {
				if row == "" {
					continue
				}
				name, description, described := strings.Cut(row, "\t")
				if !described || description == "" || strings.HasPrefix(name, "-") {
					t.Errorf("help completion %v returned malformed candidate %q", tc.args, row)
				}
				names = append(names, name)
			}
			if len(tc.want) == 0 && len(names) != 0 {
				t.Errorf("help completion %v: names=%q, want none", tc.args, names)
			}
			for _, name := range tc.want {
				if !slices.Contains(names, name) {
					t.Errorf("help completion %v: names=%q, missing %q", tc.args, names, name)
				}
			}
		})
	}
}

// TestCompletionHelpWalkUsesEveryRealCommand exercises every visible command
// path and built-in alias through the hermetic executable, including leaves.
func TestCompletionHelpWalkUsesEveryRealCommand(t *testing.T) {
	originalLogger := clog.Default()
	t.Cleanup(func() { clog.SetDefault(originalLogger) })
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	deps := &root.Deps{
		JiraClient: func(ctx context.Context) (core.JiraClient, error) {
			return nil, errors.New("help inventory must not construct a Jira client")
		},
		Detect: func() cli.Detection { return cli.Detection{Format: output.FormatJSON} },
	}
	program := root.NewProgram(deps, io.Discard, io.Discard)
	visible := func(cmd *cobra.Command) bool {
		return !cmd.Hidden && cmd.Deprecated == "" && (cmd.IsAvailableCommand() || cmd.Name() == "help")
	}
	paths := 0
	var walk func(*cobra.Command, []string)
	walk = func(cmd *cobra.Command, path []string) {
		label := strings.Join(path, "/")
		if label == "" {
			label = "root"
		}
		t.Run(label, func(t *testing.T) {
			paths++
			var want []string
			for _, child := range cmd.Commands() {
				if visible(child) {
					want = append(want, child.Name())
					want = append(want, child.Aliases...)
				}
			}
			args := append([]string{"--@shell=bash", "--@complete=helppath", "--"}, path...)
			stdout, stderr, exit := runBinary(t, args...)
			if exit != 0 || stderr != "" {
				t.Fatalf("help path %q: exit=%d stderr=%q", path, exit, stderr)
			}
			var got []string
			for row := range strings.SplitSeq(strings.TrimSuffix(stdout, "\n"), "\n") {
				if row == "" {
					continue
				}
				value, description, described := strings.Cut(row, "\t")
				if !described || description == "" {
					t.Errorf("malformed help candidate %q", row)
				}
				got = append(got, value)
			}
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("help path %q: got=%q want=%q", path, got, want)
			}
		})
		for _, child := range cmd.Commands() {
			if !visible(child) {
				continue
			}
			names := append([]string{child.Name()}, child.Aliases...)
			for _, name := range names {
				next := append(slices.Clone(path), name)
				walk(child, next)
			}
		}
	}
	walk(program.Root, nil)
	t.Logf("walked %d visible command paths and alias combinations", paths)
}
