//go:build outputaudit

package audit

import (
	"slices"
	"strings"
	"testing"

	clibcobra "github.com/gechr/clib/cli/cobra"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// Dimension is one agent-dx scorecard row (design §8.1): the felt
// surface an agent discovers, composes with, and recovers from.
type Dimension string

const (
	// DimShortPresent — discoverability: the command names itself in
	// grouped help (help standard §1.12).
	DimShortPresent Dimension = "short-present"
	// DimHelpRenders — discoverability: the help surface renders, exits
	// 0, and describes the command.
	DimHelpRenders Dimension = "help-renders"
	// DimOutputSelectorDiscoverable — discoverability/composability: the
	// canonical --output selector carries its enum metadata, so help and
	// completion render the full mode set.
	DimOutputSelectorDiscoverable Dimension = "output-selector-discoverable"
	// DimSchemaEntry — discoverability: the op's published schema exists.
	DimSchemaEntry Dimension = "schema-entry"
	// DimErrorFeedback — error quality: a failing invocation tells the
	// caller what happened through the contract surface for its class.
	DimErrorFeedback Dimension = "error-feedback"
)

// dimensionsFor is the applicability map for the agent-dx checklist,
// derived from metadata exactly like the axis map. The composability
// rows (stdin/`-`, --fields projection) join with the metadata
// capability flags the reference command introduces — adding a
// dimension here without its applicability rule is impossible by
// construction, since this function IS the rule.
func dimensionsFor(m command.Meta) []Dimension {
	dims := []Dimension{DimShortPresent, DimHelpRenders, DimOutputSelectorDiscoverable, DimErrorFeedback}
	if m.NoSuccessEnvelope == "" {
		dims = append(dims, DimSchemaEntry)
	}
	return dims
}

// TestAgentDXChecklist runs every applicable dimension for every
// inventory member and requires a verdict for each — an applicable
// dimension with no check is itself a failure, so the checklist cannot
// silently thin as commands land.
func TestAgentDXChecklist(t *testing.T) {
	members, problems := liveMembers()
	if len(problems) > 0 {
		t.Fatalf("inventory not clean; fix the inventory failures first:\n  %s", strings.Join(problems, "\n  "))
	}
	tree := liveTree()
	captures, err := corpus()
	if err != nil {
		t.Fatalf("build corpus: %v", err)
	}

	for _, member := range members {
		for _, dim := range dimensionsFor(member.Meta) {
			t.Run(member.Key()+"/"+string(dim), func(t *testing.T) {
				switch dim {
				case DimShortPresent:
					checkShortPresent(t, tree, member)
				case DimHelpRenders:
					checkHelpRenders(t, tree, captures, member)
				case DimOutputSelectorDiscoverable:
					checkOutputSelector(t, tree)
				case DimSchemaEntry:
					checkSchemaEntry(t, member)
				case DimErrorFeedback:
					checkErrorFeedback(t, captures, member)
				default:
					t.Errorf("dimension %q has no check — an applicable dimension without a verdict is a checklist gap", dim)
				}
			})
		}
	}
}

// collapseWS folds every whitespace run to one space so re-wrapped help
// prose compares against its source text.
func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// commandForMember resolves the member's cobra command by path.
func commandForMember(tree *cobra.Command, member Member) *cobra.Command {
	if tree.CommandPath() == member.Path {
		return tree
	}
	for _, kid := range tree.Commands() {
		if found := commandForMember(kid, member); found != nil {
			return found
		}
	}
	return nil
}

func checkShortPresent(t *testing.T, tree *cobra.Command, member Member) {
	t.Helper()
	cmd := commandForMember(tree, member)
	if cmd == nil {
		t.Fatalf("no command at path %q", member.Path)
	}
	if strings.TrimSpace(cmd.Short) == "" {
		t.Errorf("command %q has an empty Short — invisible in grouped help", member.Path)
	}
}

func checkHelpRenders(t *testing.T, tree *cobra.Command, captures map[string]Capture, member Member) {
	t.Helper()
	capture, ok := helpCapture(captures, member)
	if !ok {
		t.Fatalf("no help capture for member %s — its help recipe is missing", member.Key())
	}
	if capture.ExitCode != 0 {
		t.Fatalf("help exited %d, want 0", capture.ExitCode)
	}
	cmd := commandForMember(tree, member)
	if cmd == nil {
		t.Fatalf("no command at path %q", member.Path)
	}
	// clib renders cmd.Long as the description blurb (Short belongs to
	// the parent's command list), and the renderer re-wraps prose to the
	// terminal width — so the comparison collapses whitespace on both
	// sides before matching.
	desc := cmd.Long
	if desc == "" {
		desc = cmd.Short
	}
	if !strings.Contains(collapseWS(capture.Stdout), collapseWS(desc)) {
		t.Errorf("help output does not carry the command's description %q", desc)
	}
	// clib's house layout splits discovery in two: a command with
	// subcommands renders its command list and suppresses flag sections
	// (they only take effect under a subcommand), while a leaf renders
	// its full flag surface. Each shape is held to ITS discovery bar.
	if cmd.HasAvailableSubCommands() {
		for _, kid := range cmd.Commands() {
			if kid.Hidden || kid.IsAdditionalHelpTopicCommand() {
				continue
			}
			if !strings.Contains(capture.Stdout, kid.Name()) {
				t.Errorf("grouped help does not list subcommand %q", kid.Name())
			}
		}
		return
	}
	if !strings.Contains(capture.Stdout, "--output") {
		t.Error("help output does not advertise the canonical --output selector")
	}
}

func checkOutputSelector(t *testing.T, tree *cobra.Command) {
	t.Helper()
	for _, meta := range clibcobra.FlagMeta(tree) {
		if meta.Name != cli.FlagOutput {
			continue
		}
		if !slices.Equal(meta.Enum, cli.OutputModeValues) {
			t.Errorf("--output enum metadata = %v, want %v — help and completion render from it", meta.Enum, cli.OutputModeValues)
		}
		return
	}
	t.Errorf("flag %q not found on the tree", cli.FlagOutput)
}

func checkSchemaEntry(t *testing.T, member Member) {
	t.Helper()
	if _, ok := envelope.Registered(member.Meta.Op); !ok {
		t.Errorf("op %q has no published schema entry", member.Meta.Op)
	}
}

func checkErrorFeedback(t *testing.T, captures map[string]Capture, member Member) {
	t.Helper()
	capture, ok := failureCapture(captures, member)
	if !ok {
		t.Fatalf("no failure capture for member %s — its failure recipe is missing", member.Key())
	}
	if capture.ExitCode == 0 {
		t.Error("failure scenario exited 0 — no error signal at all")
	}
	// Typed feedback on stdout, including pre-parse failures. No human duplicate.
	// M11 checks full shape; this row checks channel presence.
	if capture.Stderr != "" {
		t.Error("machine failure emitted diagnostics outside its typed frame")
	}
	if capture.Stdout == "" {
		t.Error("failing invocation left stdout empty — the machine feedback channel is the error frame")
		return
	}
	if !strings.Contains(capture.Stdout, `"errors"`) {
		t.Errorf("failing op left no error frame on stdout: %q", capture.Stdout)
	}
}

// helpCapture and failureCapture resolve a member's category captures
// through its recipes.
func helpCapture(captures map[string]Capture, member Member) (Capture, bool) {
	return captureFor(captures, member.Meta.Op, member.Meta.Variant, CategoryHelp, ModeHuman)
}

func failureCapture(captures map[string]Capture, member Member) (Capture, bool) {
	return captureFor(captures, member.Meta.Op, member.Meta.Variant, CategoryFailure, ModeJSON)
}
