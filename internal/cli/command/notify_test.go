package command

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
)

// --notify is the first INVERTED hidden alias in this tree: --skip-notify
// means --notify=false, where every alias before it meant exactly its
// canonical flag. That inversion is the whole of what these tests pin.

// notifyCommand builds a command carrying the flag, and parses argv
// through it — the alias only exists on a real flag set, and
// ResolveNotify's whole job is reading Changed() off one.
func notifyCommand(t *testing.T, argv ...string) (*cobra.Command, *MutationFlags) {
	t.Helper()
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	var mut MutationFlags
	AddMutationFlags(cmd, &mut, WithNotify())
	if err := cmd.Flags().Parse(argv); err != nil {
		t.Fatalf("parsing %v: %v", argv, err)
	}
	return cmd, &mut
}

// TestNotifyDefaultsToJirasOwnDefault. The flag exists to DISABLE
// notification; defaulting to anything but true would silently change
// what every existing edit does, which is a behavior change wearing a
// new flag's clothes.
func TestNotifyDefaultsToJirasOwnDefault(t *testing.T) {
	cmd, mut := notifyCommand(t)
	if err := mut.ResolveNotify(cmd.Flags()); err != nil {
		t.Fatalf("ResolveNotify: %v", err)
	}
	if !mut.Notify {
		t.Error("--notify defaults to false; Jira notifies by default, so the CLI must too unless asked otherwise")
	}
}

// TestSkipNotifyMeansNotifyFalse is the inversion itself. Binding the
// alias to the same bool as its canonical flag — which is what every
// other alias here does — would make --skip-notify set Notify TRUE,
// exactly backwards.
func TestSkipNotifyMeansNotifyFalse(t *testing.T) {
	cmd, mut := notifyCommand(t, "--skip-notify")
	if err := mut.ResolveNotify(cmd.Flags()); err != nil {
		t.Fatalf("ResolveNotify: %v", err)
	}
	if mut.Notify {
		t.Error("--skip-notify left Notify true; the alias is the NEGATION of its canonical flag, not a synonym for it")
	}
}

// TestNotifyFalseAndSkipNotifyAgree. The resolver refuses a
// DISAGREEMENT, not a redundancy — these two say the same thing twice,
// which is what a caller who has both spellings in muscle memory will
// type.
func TestNotifyFalseAndSkipNotifyAgree(t *testing.T) {
	cmd, mut := notifyCommand(t, "--notify=false", "--skip-notify")
	if err := mut.ResolveNotify(cmd.Flags()); err != nil {
		t.Fatalf("two flags agreeing that watchers should not be emailed = %v, want accepted", err)
	}
	if mut.Notify {
		t.Error("Notify is true after both spellings said not to notify")
	}
}

// TestNotifyAndSkipNotifyAreRefused is the pair that cannot be honored.
// Last-one-wins is what --force/--yes does, and it is wrong here: those
// two agree, while these ask for opposite outcomes, so guessing sends
// email to every watcher of every issue in a bulk edit.
func TestNotifyAndSkipNotifyAreRefused(t *testing.T) {
	cmd, mut := notifyCommand(t, "--notify", "--skip-notify")
	err := mut.ResolveNotify(cmd.Flags())
	if err == nil {
		t.Fatal("--notify --skip-notify was accepted; they ask for opposite things")
	}
	var input *cli.InputError
	if !errors.As(err, &input) {
		t.Fatalf("refusal is %T, want a typed *cli.InputError so the envelope carries a code", err)
	}
	if input.Code != cli.CodeFlagConflict {
		t.Errorf("code = %q, want %q", input.Code, cli.CodeFlagConflict)
	}
	if input.Field != FlagNotify {
		t.Errorf("field = %q, want %q — the canonical spelling, which is what help documents", input.Field, FlagNotify)
	}
	if !strings.Contains(err.Error(), "--"+FlagSkipNotify) {
		t.Errorf("message %q does not name --%s; the caller has to know which two flags collided", err, FlagSkipNotify)
	}
}

// TestSkipNotifyIsHiddenAndNotifyIsNot. A hidden alias stays out of
// help, the schema and completions, so the documented contract is one
// canonical flag rather than two half-flags a reader has to reconcile.
func TestSkipNotifyIsHiddenAndNotifyIsNot(t *testing.T) {
	cmd, _ := notifyCommand(t)

	canonical := cmd.Flags().Lookup(FlagNotify)
	if canonical == nil {
		t.Fatalf("--%s is not registered", FlagNotify)
	}
	if canonical.Hidden {
		t.Errorf("--%s is hidden; it is the documented spelling", FlagNotify)
	}
	alias := cmd.Flags().Lookup(FlagSkipNotify)
	if alias == nil {
		t.Fatalf("--%s is not registered", FlagSkipNotify)
	}
	if !alias.Hidden {
		t.Errorf("--%s is visible; §5 aliases are hidden so the contract does not widen", FlagSkipNotify)
	}
}

// TestNotifyIsOptIn keeps the registrar honest: a mutation that did not
// ask for --notify must not grow it. Every flag costs help text,
// completion and a schema line an agent reads on every introspection.
func TestNotifyIsOptIn(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	var mut MutationFlags
	AddMutationFlags(cmd, &mut)

	for _, name := range []string{FlagNotify, FlagSkipNotify} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("--%s was registered by a bare AddMutationFlags", name)
		}
	}
}
