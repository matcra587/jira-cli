//go:build outputaudit

package audit

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// The inventory self-test: the inventory's teeth are proven on a fixture
// tree, not trusted. Each planted defect must produce a problem NAMING
// the defective member; a clean fixture must reconcile silently. The
// fixture uses the REAL walk, the REAL command metadata registry
// (pointer-keyed, so fixture commands never collide with production),
// and hand-built schema sources standing in for envelope.Snapshot.

type fixtureCleanOutput struct {
	Name string `json:"name"`
}

type fixtureIdentityOutput struct {
	Issue envelope.IssueRef `json:"issue"`
	Name  string            `json:"name"`
}

type fixtureBareIdentityOutput struct {
	Key string `json:"key"`
}

// fixtureCommand builds one leaf and declares its metadata.
func fixtureCommand(use, short string, metas ...command.Meta) *cobra.Command {
	cmd := &cobra.Command{Use: use, Short: short}
	if len(metas) > 0 {
		command.SetMeta(cmd, metas[0], metas[1:]...)
	}
	return cmd
}

func TestInventorySelfTestCleanFixtureReconciles(t *testing.T) {
	t.Parallel()
	tree := &cobra.Command{Use: "fixture", Short: "fixture root"}
	tree.AddCommand(fixtureCommand("covered", "covered", command.Meta{Op: "fixture.covered"}))

	_, problems := reconcile(sources{
		tree:    tree,
		schemas: map[string]any{"fixture.covered": fixtureCleanOutput{}},
		recipes: []Scenario{
			{ID: "covered-success", Op: "fixture.covered", Category: CategorySuccess, Args: []string{"covered"}},
		},
		framework: map[string]string{},
	})
	if len(problems) > 0 {
		t.Errorf("clean fixture produced problems:\n  %s", strings.Join(problems, "\n  "))
	}
}

func TestInventorySelfTestNamesEveryPlantedDefect(t *testing.T) {
	t.Parallel()
	tree := &cobra.Command{Use: "fixture", Short: "fixture root"}
	tree.AddCommand(
		fixtureCommand("covered", "covered", command.Meta{Op: "fixture.covered"}),
		// A stub registered with no metadata at all — the headline
		// case.
		fixtureCommand("nometa", "no metadata"),
		// Metadata but no schema registration.
		fixtureCommand("noschema", "no schema", command.Meta{Op: "fixture.noschema"}),
		// Declares an identity slot; its Output struct has only a bare
		// string — the Ref marker's negative case.
		fixtureCommand("badidentity", "bad identity", command.Meta{Op: "fixture.badidentity", Identity: true}),
		// The inverse: a ref-typed field the metadata never declares.
		fixtureCommand("sneakyref", "sneaky ref", command.Meta{Op: "fixture.sneakyref"}),
		// Declares envelope-free yet registers a schema.
		fixtureCommand("contradiction", "contradiction", command.Meta{Op: "fixture.contradiction", NoSuccessEnvelope: "claims nothing"}),
		// Empty Short — help standard §1.12.
		fixtureCommand("noshort", "", command.Meta{Op: "fixture.noshort"}),
	)
	// A HIDDEN defective surface: hidden commands are contract surface
	// and must be walked, not skipped.
	ghost := fixtureCommand("ghost", "hidden ghost")
	ghost.Hidden = true
	tree.AddCommand(ghost)
	// A command emitting a default form AND a flag-selected variant from
	// one path: the variant's recipe must pass the selecting token.
	twoform := fixtureCommand("twoform", "two forms",
		command.Meta{Op: "fixture.twoform"},
		command.Meta{Op: "fixture.twoform", Variant: "--count"})
	tree.AddCommand(twoform)
	// The op-borrowing attack: a hidden command claiming an op another
	// command owns would be "covered" by recipes that never execute it.
	borrower := fixtureCommand("borrower", "borrows an op", command.Meta{Op: "fixture.covered"})
	borrower.Hidden = true
	tree.AddCommand(borrower)
	// The mis-aimed recipe attack: args that invoke a different command
	// than the op claims (planted below via the borrowed-args recipe).

	_, problems := reconcile(sources{
		tree: tree,
		schemas: map[string]any{
			"fixture.covered":       fixtureCleanOutput{},
			"fixture.badidentity":   fixtureBareIdentityOutput{},
			"fixture.sneakyref":     fixtureIdentityOutput{},
			"fixture.contradiction": fixtureCleanOutput{},
			"fixture.noshort":       fixtureCleanOutput{},
			"fixture.twoform":       fixtureCleanOutput{},
			// Registered, emitted by nothing: a dead registration.
			"fixture.dead": fixtureCleanOutput{},
		},
		recipes: []Scenario{
			{ID: "covered-success", Op: "fixture.covered", Category: CategorySuccess, Args: []string{"covered"}},
			{ID: "mis-aimed", Op: "fixture.noshort", Category: CategorySuccess, Args: []string{"covered"}},
			{ID: "twoform-default", Op: "fixture.twoform", Category: CategorySuccess, Args: []string{"twoform"}},
			{ID: "twoform-lying-variant", Op: "fixture.twoform", Variant: "--count", Category: CategorySuccess, Args: []string{"twoform"}},
			// A read cannot demand dry_run — the wrong-label class.
			{ID: "covered-mislabeled", Op: "fixture.covered", Category: CategoryDryRun},
			// An orphaned recipe targeting a member that does not exist.
			{ID: "orphan", Op: "fixture.gone", Category: CategorySuccess},
			{ID: "noschema-success", Op: "fixture.noschema", Category: CategorySuccess},
			{ID: "badidentity-success", Op: "fixture.badidentity", Category: CategorySuccess},
			{ID: "sneakyref-success", Op: "fixture.sneakyref", Category: CategorySuccess},
			{ID: "contradiction-success", Op: "fixture.contradiction", Category: CategorySuccess},
			{ID: "noshort-success", Op: "fixture.noshort", Category: CategorySuccess},
		},
		// A framework entry naming a surface that does not exist.
		framework: map[string]string{"fixture vanished": "stale built-in"},
	})

	wants := []struct {
		name string
		part string
	}{
		{name: "missing metadata names the stub", part: `leaf "fixture nometa" has no operation metadata`},
		{name: "hidden surface is walked", part: `leaf "fixture ghost" has no operation metadata`},
		{name: "missing schema names the op", part: `op "fixture.noschema" with no envelope schema registration`},
		{name: "identity without ref slot", part: `op "fixture.badidentity"'s Output struct carries no envelope.Ref field`},
		{name: "ref slot without identity", part: `op "fixture.sneakyref" carries an envelope.Ref field but the metadata does not declare Identity`},
		{name: "envelope-free with schema", part: `no success envelope for op "fixture.contradiction" yet a schema is registered`},
		{name: "empty short", part: `command "fixture noshort" has an empty Short`},
		{name: "dead registration", part: `envelope-registered op "fixture.dead" is emitted by no command`},
		{name: "stale framework entry", part: `framework table names "fixture vanished"`},
		{name: "orphaned recipe", part: `scenario "orphan" targets op "fixture.gone"`},
		{name: "mislabeled recipe", part: `scenario "covered-mislabeled" labels itself "dry_run"`},
		{name: "op borrowing named", part: `already owned by "fixture borrower"`},
		{name: "mis-aimed recipe named", part: `scenario "mis-aimed" claims op "fixture.noshort"`},
		{name: "variant without its token named", part: `scenario "twoform-lying-variant" claims variant "--count"`},
	}
	for _, want := range wants {
		t.Run(want.name, func(t *testing.T) {
			for _, p := range problems {
				if strings.Contains(p, want.part) {
					return
				}
			}
			t.Errorf("no problem contains %q; got:\n  %s", want.part, strings.Join(problems, "\n  "))
		})
	}
}
