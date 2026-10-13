// The external package breaks the import cycle: this test needs the LIVE
// assembled tree (internal/cli/root), and root imports cli.
package cli_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/gechr/clog"
	"github.com/spf13/pflag"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/root"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/output"
)

// restoreLogger reinstates the default logger that building a program
// clobbers — conductor configures clog's package globals on every
// construction. Harmless while this binary holds one test, and exactly
// the kind of cross-test bleed that is invisible until the second one
// lands.
func restoreLogger(t *testing.T) {
	t.Helper()
	orig := clog.Default()
	t.Cleanup(func() { clog.SetDefault(orig) })
}

// TestForeignFlagTableTargetsExist pins the table to reality: every
// suggestion it offers must name a flag the assembled tree actually
// carries — a row recommending a flag the binary does not have would
// send the recovering agent straight into a second unknown-flag failure.
// When a suggested flag ships (--jq, --limit), its foreign spellings
// join the table; until then the entry must not exist.
func TestForeignFlagTableTargetsExist(t *testing.T) {
	restoreLogger(t)
	deps := &root.Deps{
		JiraClient: func(ctx context.Context) (core.JiraClient, error) {
			return nil, errors.New("suggest test: no client")
		},
		Detect: func() cli.Detection {
			return cli.Detection{Format: output.FormatJSON}
		},
	}
	program := root.NewProgram(deps, io.Discard, io.Discard)

	known := map[string]bool{}
	collect := func(f *pflag.Flag) { known[f.Name] = true }
	program.Root.PersistentFlags().VisitAll(collect)
	program.Root.Flags().VisitAll(collect)

	for foreign, suggestions := range cli.ForeignFlagTableForTest() {
		if len(suggestions) == 0 {
			t.Errorf("foreign flag %q has an empty suggestion list — a row that offers nothing is a stale entry", foreign)
		}
		if known[foreign] {
			t.Errorf("foreign flag %q exists on this CLI — a working flag never reaches the unknown-flag path, so its row is dead", foreign)
		}
		for _, s := range suggestions {
			name := strings.TrimPrefix(s, "--")
			if i := strings.IndexByte(name, '='); i >= 0 {
				name = name[:i]
			}
			if !known[name] {
				t.Errorf("foreign flag %q suggests %q, but flag --%s does not exist on the tree", foreign, s, name)
			}
		}
	}
}
