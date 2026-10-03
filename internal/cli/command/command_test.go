package command

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/envelope"
)

// TestPositionalArgumentsReachTheHandler pins the route an ArgSpec is
// useless without.
//
// A command can declare arity and a completion predictor for a
// positional argument, and cobra will validate one — but until this
// landed, RunE discarded cobra's slice and Invocation carried only the
// version string, so the handler that exists to USE the argument had no
// way to read it. `issue view ISSUE_KEY`, the next command to be
// written on this path, was unbuildable.
func TestPositionalArgumentsReachTheHandler(t *testing.T) {
	var got []string
	cmd, err := New(Spec[envelope.VersionOutput]{
		Op:    "version",
		Use:   "probe",
		Short: "probe",
		Args:  ArgSpec{Min: 1, Max: 2},
		Run: func(_ context.Context, inv Invocation) (envelope.VersionOutput, error) {
			got = inv.Args
			return envelope.VersionOutput{}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := cmd.Args(cmd, []string{"PROJ-1", "PROJ-2"}); err != nil {
		t.Fatalf("arity rejected two arguments: %v", err)
	}
	// The write fails — no output format was seeded, because that is
	// root's job and there is no root here. Irrelevant: the handler runs
	// first, and whether the argument values reached it is the whole
	// question.
	cmd.SetContext(context.Background())
	_ = cmd.RunE(cmd, []string{"PROJ-1", "PROJ-2"})
	if len(got) != 2 || got[0] != "PROJ-1" || got[1] != "PROJ-2" {
		t.Errorf("handler saw %v, want both positional arguments", got)
	}
}

// TestNewRejectsAnUnregisteredOp pins the construction-time check that
// stands in for the compile-time one Go cannot give: registration is a
// runtime map keyed by the op string, so nothing links a handler's
// return type to a registration except this.
func TestNewRejectsAnUnregisteredOp(t *testing.T) {
	t.Parallel()
	_, err := New(Spec[envelope.VersionOutput]{
		Op:    "nothing.registered.this",
		Use:   "probe",
		Short: "probe",
		Run: func(context.Context, Invocation) (envelope.VersionOutput, error) {
			return envelope.VersionOutput{}, nil
		},
	})
	if err == nil {
		t.Fatal("an unregistered operation was accepted")
	}
	if !strings.Contains(err.Error(), "no envelope registration") {
		t.Errorf("error does not name the cause: %v", err)
	}
}

// TestNewRejectsAMismatchedOutputType pins the other half: an op that
// IS registered, by a command returning the wrong shape.
func TestNewRejectsAMismatchedOutputType(t *testing.T) {
	t.Parallel()
	_, err := New(Spec[envelope.ConfigListOutput]{
		Op:    "version", // registered as VersionOutput
		Use:   "probe",
		Short: "probe",
		Run: func(context.Context, Invocation) (envelope.ConfigListOutput, error) {
			return envelope.ConfigListOutput{}, nil
		},
	})
	if err == nil {
		t.Fatal("a handler returning the wrong Output type was accepted")
	}
	if !strings.Contains(err.Error(), "registered") {
		t.Errorf("error does not name the mismatch: %v", err)
	}
}

// TestNewRejectsAVariantThatPromisesAnotherShape pins that variants get
// the same check. They do not select what is emitted, but the inventory
// publishes them as operations this command reports, so one registered
// under a different Output type promises a shape nothing here produces.
func TestNewRejectsAVariantThatPromisesAnotherShape(t *testing.T) {
	t.Parallel()
	_, err := New(Spec[envelope.VersionOutput]{
		Op:       "version",
		Variants: []Meta{{Op: "config.list"}}, // registered as ConfigListOutput
		Use:      "probe",
		Short:    "probe",
		Run: func(context.Context, Invocation) (envelope.VersionOutput, error) {
			return envelope.VersionOutput{}, nil
		},
	})
	if err == nil {
		t.Fatal("a variant registered under another Output type was accepted")
	}
	if !strings.Contains(err.Error(), "variant") {
		t.Errorf("error does not name the variant: %v", err)
	}
}

// TestNewRefusesACommandWithNoShort pins the help-standard gate: an
// empty Short drops the command out of grouped help, which is a silent
// disappearance rather than a visible fault.
func TestNewRefusesACommandWithNoShort(t *testing.T) {
	t.Parallel()
	_, err := New(Spec[envelope.VersionOutput]{
		Op:  "version",
		Use: "probe",
		Run: func(context.Context, Invocation) (envelope.VersionOutput, error) {
			return envelope.VersionOutput{}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "Short") {
		t.Errorf("a command with no Short was accepted, or the error did not say so: %v", err)
	}
}

// TestFlagsCanUseTheRegisterAndExtendHelpers pins that a converted
// command can actually declare a flag.
//
// Spec.Flags first took a *pflag.FlagSet, which no helper accepts —
// every one of them takes the command, because they attach clib
// metadata as well as declaring the flag. That left raw pflag calls,
// which ruleguard rejects, as the only route, so a converted command
// could not have a flag at all. `version` has none, so the field looked
// fine for as long as it was the only conversion.
func TestFlagsCanUseTheRegisterAndExtendHelpers(t *testing.T) {
	t.Parallel()
	var showToken bool
	cmd, err := New(Spec[envelope.VersionOutput]{
		Op:    "version",
		Use:   "probe",
		Short: "probe",
		Flags: func(c *cobra.Command) {
			AddBoolVar(c, &showToken, "show-token", false, "display the token", FlagOpts{})
		},
		Run: func(context.Context, Invocation) (envelope.VersionOutput, error) {
			return envelope.VersionOutput{}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if cmd.Flags().Lookup("show-token") == nil {
		t.Error("the flag the helper registered is not on the command")
	}
}

// TestAnnotationsAreMergedNotReplaced pins that the path adds its clib
// entry rather than replacing the map.
//
// Nothing else writes an annotation today, so assignment was harmless —
// and would have stayed harmless right up until the first thing that
// did, at which point it would drop that entry silently.
func TestAnnotationsAreMergedNotReplaced(t *testing.T) {
	t.Parallel()
	cmd, err := New(Spec[envelope.VersionOutput]{
		Op:    "version",
		Use:   "probe",
		Short: "probe",
		Args:  ArgSpec{Min: 1, Max: 1, Positions: []Arg{{Name: "KEY", Predictor: "issuekey"}}},
		Flags: func(c *cobra.Command) {
			// A command that annotates for its own reasons, before the
			// path adds its predictor entry.
			c.Annotations = map[string]string{"other": "kept"}
		},
		Run: func(context.Context, Invocation) (envelope.VersionOutput, error) {
			return envelope.VersionOutput{}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := cmd.Annotations["other"]; got != "kept" {
		t.Errorf("a pre-existing annotation was dropped: %q", got)
	}
	if got := cmd.Annotations["clib"]; got != "dynamic-args='issuekey'" {
		t.Errorf("clib annotation = %q, want the predictor", got)
	}
}

// TestAMutationMustRegisterADryRunFlag pins that a command declaring it
// mutates cannot be built without a preview.
//
// The rules doc's answer was to fail SAFE at run time — assume dry-run
// is ON when the flag cannot be read. Right instinct, weaker mechanism:
// it protects the run that already shipped the mistake. Here the flag
// and the claim to mutate come from one declaration, so a mutation with
// no preview never reaches a tree at all.
func TestAMutationMustRegisterADryRunFlag(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name              string
		writesJira        bool
		mutatesLocalState bool
		want              bool // want an error
	}{
		{name: "writes jira", writesJira: true, want: true},
		{name: "writes local state", mutatesLocalState: true, want: true},
		// A read has nothing to preview, and demanding a flag for one
		// would be noise rather than safety.
		{name: "read", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(Spec[envelope.VersionOutput]{
				Op:                "version",
				WritesJira:        tt.writesJira,
				MutatesLocalState: tt.mutatesLocalState,
				Use:               "probe",
				Short:             "probe",
				Run: func(context.Context, Invocation) (envelope.VersionOutput, error) {
					return envelope.VersionOutput{}, nil
				},
			})
			if tt.want && err == nil {
				t.Error("a mutation with no --dry-run flag was accepted")
			}
			if !tt.want && err != nil {
				t.Errorf("a read was refused: %v", err)
			}
		})
	}
}

// TestAMutationThatRegistersTheFlagIsAccepted pins the other side, so
// the check above cannot pass by refusing everything.
func TestAMutationThatRegistersTheFlagIsAccepted(t *testing.T) {
	t.Parallel()
	_, err := New(Spec[envelope.VersionOutput]{
		Op:                "version",
		MutatesLocalState: true,
		Use:               "probe",
		Short:             "probe",
		Flags:             func(c *cobra.Command) { AddDryRunFlag(c) },
		Run: func(context.Context, Invocation) (envelope.VersionOutput, error) {
			return envelope.VersionOutput{}, nil
		},
	})
	if err != nil {
		t.Errorf("a mutation declaring --dry-run was refused: %v", err)
	}
}
