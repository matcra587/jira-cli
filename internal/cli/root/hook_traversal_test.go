package root

import (
	"io"
	"slices"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/output"
)

func TestConcurrentProgramConstruction(t *testing.T) {
	t.Parallel()
	const constructors = 16
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range constructors {
		workers.Go(func() {
			<-start
			NewProgram(&Deps{}, io.Discard, io.Discard)
		})
	}
	close(start)
	workers.Wait()
}

// Keep execution serial with the other runtime tests: conductor configures
// process-wide logging. Construction itself must remain race-free while an
// already constructed command reads Cobra's traversal policy.
func TestProgramConstructionPreservesPersistentHookOrder(t *testing.T) {
	program, _ := newProgram(testDeps(t), io.Discard, io.Discard)
	var hooks []string
	rootPreRun := program.Root.PersistentPreRunE
	program.Root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if err := rootPreRun(cmd, args); err != nil {
			return err
		}
		hooks = append(hooks, "root pre")
		return nil
	}
	program.Root.PersistentPostRunE = func(*cobra.Command, []string) error {
		hooks = append(hooks, "root post")
		return nil
	}
	parent := &cobra.Command{Use: "hook-parent"}
	parent.PersistentPreRunE = func(*cobra.Command, []string) error {
		hooks = append(hooks, "parent pre")
		return nil
	}
	parent.PersistentPostRunE = func(*cobra.Command, []string) error {
		hooks = append(hooks, "parent post")
		return nil
	}
	leaf := &cobra.Command{Use: "hook-leaf"}
	leaf.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		hooks = append(hooks, "leaf pre")
		format, ok := cli.FormatFromContext(cmd.Context())
		if !ok || format != output.FormatCompact {
			t.Errorf("leaf pre-hook format = %v/%v, want compact/true", format, ok)
		}
		return nil
	}
	leaf.PersistentPostRunE = func(*cobra.Command, []string) error {
		hooks = append(hooks, "leaf post")
		return nil
	}
	leaf.RunE = func(*cobra.Command, []string) error {
		hooks = append(hooks, "run")
		return nil
	}
	parent.AddCommand(leaf)
	program.Root.AddCommand(parent)

	var workers sync.WaitGroup
	workers.Go(func() {
		for range 16 {
			NewProgram(&Deps{}, io.Discard, io.Discard)
		}
	})
	defer workers.Wait()
	program.Root.SetArgs([]string{"hook-parent", "hook-leaf", "--output=compact"})
	if err := program.Root.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext(hook-parent hook-leaf --output=compact) = %v, want nil", err)
	}
	want := []string{"root pre", "parent pre", "leaf pre", "run", "leaf post", "parent post", "root post"}
	if !slices.Equal(hooks, want) {
		t.Errorf("persistent hooks = %q, want %q", hooks, want)
	}
}
