package guardrails

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/gechr/clog"
	concobra "github.com/gechr/conductor/cli/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/root"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/output"
)

// guardProgram assembles the production program with inert deps, the
// same walk surface the audit inventory uses: metadata guards must judge
// the REAL assembled tree — conductor's flags included — or a defect in
// composition passes a fixture and ships. The deps refuse to construct a
// client and pin detection, so no guard can touch the network or probe
// the test environment.
//
// Conductor reconfigures clog's package globals on construction, so the
// default logger is restored on cleanup and callers must not run in
// parallel with each other.
func guardProgram(t *testing.T) *concobra.Program {
	t.Helper()
	orig := clog.Default()
	t.Cleanup(func() { clog.SetDefault(orig) })
	deps := &root.Deps{
		JiraClient: func(ctx context.Context) (core.JiraClient, error) {
			return nil, errors.New("guardrails: a metadata walk never constructs a jira client")
		},
		Detect: func() cli.Detection {
			return cli.Detection{Format: output.FormatJSON}
		},
	}
	program := root.NewProgram(deps, io.Discard, io.Discard)
	// Materialize cobra's lazily added built-ins so their surfaces are
	// walked, not silently skipped (the completion init is a no-op —
	// clib disables cobra's default command — kept for parity with the
	// audit inventory's walk).
	program.Root.InitDefaultHelpCmd()
	program.Root.InitDefaultCompletionCmd()
	return program
}
