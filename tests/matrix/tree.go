//go:build flagmatrix

package matrix

import (
	"errors"
	"io"
	"sync"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/root"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/output"
)

var (
	liveOnce sync.Once
	liveRoot *cobra.Command
)

// liveTree assembles the production program once per test process and
// returns its root — the same inert-deps walk surface the audit
// inventory uses. The enumeration must be derived from THIS tree, never
// a hand-maintained list: a hand-written list is the thing this package
// exists to make impossible. The deps refuse to construct a client and
// pin detection, so the walk can never touch the network or probe the
// test environment.
func liveTree() *cobra.Command {
	liveOnce.Do(func() {
		deps := &root.Deps{
			JiraClient: func() (core.JiraClient, error) {
				return nil, errors.New("flagmatrix: the enumerator never constructs a jira client")
			},
			Detect: func() cli.Detection {
				return cli.Detection{Format: output.FormatJSON}
			},
		}
		program := root.NewProgram(deps, io.Discard, io.Discard)
		// Materialize cobra's lazily added built-ins so their surfaces
		// are walked, not silently skipped (the completion init is a
		// no-op — clib disables cobra's default command — kept for
		// parity with the audit inventory's walk).
		program.Root.InitDefaultHelpCmd()
		program.Root.InitDefaultCompletionCmd()
		liveRoot = program.Root
	})
	return liveRoot
}
