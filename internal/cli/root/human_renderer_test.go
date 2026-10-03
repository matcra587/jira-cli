package root

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// TestEveryEnvelopeOpHasAHumanRenderer closes a hole the machine matrix
// cannot see.
//
// The audit corpus captures success only in json and compact, so human
// mode is never exercised there — and an envelope-bearing command with
// no registered renderer fails at the last step with "no human renderer
// registered", after doing its work. It has shipped that way twice
// (config edit, auth login), both times found by a person running the
// binary rather than by a test.
//
// This package is the right home: it imports every command package, so
// every registration init() has run by the time this executes.
func TestEveryEnvelopeOpHasAHumanRenderer(t *testing.T) {
	t.Parallel()
	program := NewProgram(defaultDeps(), nil, nil)

	// Ops whose command declares its own renderer through the
	// construction path. Those never reach the op-keyed registry — the
	// renderer travels with the result — so asking the registry alone
	// would report every converted command as missing one.
	declared := map[string]bool{}
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if command.DeclaresHuman(cmd) {
			if metas, ok := command.MetaFor(cmd); ok {
				for _, m := range metas {
					declared[m.Op] = true
				}
			}
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(program.Root)

	registrations := envelope.Snapshot()
	if len(registrations) == 0 {
		t.Fatal("no operations registered; this test would pass vacuously")
	}
	for _, reg := range registrations {
		if !output.HasHuman(reg.Op) && !declared[reg.Op] {
			t.Errorf("op %q registers an envelope shape but no human renderer: running it with --output=human fails after the work is done", reg.Op)
		}
	}
}
