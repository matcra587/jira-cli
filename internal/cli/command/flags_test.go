package command_test

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
)

// TestAddOutputFlagsRegistersCanonicalFlag pins the discoverable half of
// the selector: --output is a persistent string flag defaulting to
// "auto", and it is NOT hidden — help, schema, and completion must see it.
func TestAddOutputFlagsRegistersCanonicalFlag(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	command.AddOutputFlags(cmd)

	f := cmd.PersistentFlags().Lookup(cli.FlagOutput)
	if f == nil {
		t.Fatal("--output was not registered")
	}
	if f.Hidden {
		t.Error("--output is hidden; want it discoverable")
	}
	if f.DefValue != "auto" {
		t.Errorf("--output default = %q, want %q", f.DefValue, "auto")
	}
	if f.Value.Type() != "string" {
		t.Errorf("--output type = %q, want string", f.Value.Type())
	}
}

// TestAddOutputFlagsRegistersHiddenAliases pins the sugar half: every
// entry in cli.OutputAliases becomes a hidden bool flag defaulting to
// false, so a reflexive --json from other-CLI muscle memory parses
// without appearing in help or completion.
func TestAddOutputFlagsRegistersHiddenAliases(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	command.AddOutputFlags(cmd)

	for _, alias := range cli.OutputAliases {
		f := cmd.PersistentFlags().Lookup(alias.Flag)
		if f == nil {
			t.Errorf("alias --%s was not registered", alias.Flag)
			continue
		}
		if !f.Hidden {
			t.Errorf("alias --%s is not hidden; want it absent from help/completion", alias.Flag)
		}
		if f.DefValue != "false" {
			t.Errorf("alias --%s default = %q, want %q", alias.Flag, f.DefValue, "false")
		}
		if f.Value.Type() != "bool" {
			t.Errorf("alias --%s type = %q, want bool", alias.Flag, f.Value.Type())
		}
	}
}

// TestAddOutputFlagsAndResolveSelectorAgree closes the loop the doc
// comment on OutputAliases warns about: every flag ResolveSelector reads
// must actually be registered by AddOutputFlags, or a real command would
// panic (or silently no-op) resolving a flag that was never added.
func TestAddOutputFlagsAndResolveSelectorAgree(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	command.AddOutputFlags(cmd)

	if err := cmd.PersistentFlags().Set(cli.FlagAliasJSON, "true"); err != nil {
		t.Fatalf("set --json: %v", err)
	}
	mode, err := cli.ResolveSelector(cmd.PersistentFlags())
	if err != nil {
		t.Fatalf("ResolveSelector: %v", err)
	}
	if mode != cli.OutputJSON {
		t.Errorf("mode = %q, want %q", mode, cli.OutputJSON)
	}
}

// TestAddListFlagsRegistersCanonicalFlags pins the discoverable half of
// the shared list-flags surface: every canonical flag AddListFlags
// documents is registered and NOT hidden.
func TestAddListFlagsRegistersCanonicalFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	var f command.ListFlags
	command.AddListFlags(cmd, &f)

	for _, name := range []string{
		"order-by", "reverse",
		"created-after", "created-before", "updated-after", "updated-before",
		"reporter", "priority", "watching", "count",
	} {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			t.Errorf("--%s was not registered", name)
			continue
		}
		if flag.Hidden {
			t.Errorf("--%s is hidden; want it discoverable", name)
		}
	}
}

// TestAddListFlagsRegistersHiddenAliasesMatchingCanonicalType pins the
// sugar half: every entry in command.ListFlagAliases becomes a hidden
// flag of the SAME pflag type as its canonical flag, so a stray --sort
// value round-trips through ResolveListFlagAliases without a type
// mismatch.
func TestAddListFlagsRegistersHiddenAliasesMatchingCanonicalType(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	var f command.ListFlags
	command.AddListFlags(cmd, &f)

	for _, alias := range command.ListFlagAliases {
		got := cmd.Flags().Lookup(alias.Flag)
		if got == nil {
			t.Errorf("alias --%s was not registered", alias.Flag)
			continue
		}
		if !got.Hidden {
			t.Errorf("alias --%s is not hidden; want it absent from help/completion", alias.Flag)
		}
		canon := cmd.Flags().Lookup(alias.Canonical)
		if canon == nil {
			t.Fatalf("alias --%s names canonical --%s, which was never registered", alias.Flag, alias.Canonical)
		}
		if got.Value.Type() != canon.Value.Type() {
			t.Errorf("alias --%s type = %q, want canonical --%s's own type %q", alias.Flag, got.Value.Type(), alias.Canonical, canon.Value.Type())
		}
	}
}

// TestResolveListFlagAliasesUsesAliasWhenCanonicalNotGiven proves the
// core sugar case: a caller who only ever typed --sort still gets
// --order-by's own field populated.
func TestResolveListFlagAliasesUsesAliasWhenCanonicalNotGiven(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	var f command.ListFlags
	command.AddListFlags(cmd, &f)

	if err := cmd.Flags().Set("sort", "created"); err != nil {
		t.Fatalf("set --sort: %v", err)
	}
	if err := command.ResolveListFlagAliases(cmd.Flags()); err != nil {
		t.Fatalf("ResolveListFlagAliases: %v", err)
	}
	if f.OrderBy != "created" {
		t.Errorf("OrderBy = %q, want %q", f.OrderBy, "created")
	}
}

// TestResolveListFlagAliasesAgreeingSignalsAreFine proves the canonical
// flag and its alias BOTH being given is not an error when they agree —
// cli.ResolveSelector's own rule for --output/--json, generalized here.
func TestResolveListFlagAliasesAgreeingSignalsAreFine(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	var f command.ListFlags
	command.AddListFlags(cmd, &f)

	if err := cmd.Flags().Set("order-by", "created"); err != nil {
		t.Fatalf("set --order-by: %v", err)
	}
	if err := cmd.Flags().Set("sort", "created"); err != nil {
		t.Fatalf("set --sort: %v", err)
	}
	if err := command.ResolveListFlagAliases(cmd.Flags()); err != nil {
		t.Fatalf("ResolveListFlagAliases: %v, want agreeing signals to pass", err)
	}
	if f.OrderBy != "created" {
		t.Errorf("OrderBy = %q, want %q", f.OrderBy, "created")
	}
}

// TestResolveListFlagAliasesDisagreeingSignalsAreRefused proves the
// canonical flag and its alias disagreeing is a classified error naming
// the canonical flag — never a silent pick between them.
func TestResolveListFlagAliasesDisagreeingSignalsAreRefused(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	var f command.ListFlags
	command.AddListFlags(cmd, &f)

	if err := cmd.Flags().Set("order-by", "created"); err != nil {
		t.Fatalf("set --order-by: %v", err)
	}
	if err := cmd.Flags().Set("sort", "updated"); err != nil {
		t.Fatalf("set --sort: %v", err)
	}
	err := command.ResolveListFlagAliases(cmd.Flags())
	if err == nil {
		t.Fatal("ResolveListFlagAliases returned nil error for disagreeing --order-by/--sort values")
	}
	var inputErr *cli.InputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("error = %v (%T), want *cli.InputError", err, err)
	}
	if inputErr.Field != "order-by" {
		t.Errorf("field = %q, want the canonical flag %q", inputErr.Field, "order-by")
	}
}

// TestResolveListFlagAliasesBoolAliasAgreesAndDisagrees is the disagree/
// agree pair for a BOOL-typed canonical (--reverse/--desc), proving the
// same generic loop that handles --order-by's string aliases also
// handles a bool one correctly.
func TestResolveListFlagAliasesBoolAliasAgreesAndDisagrees(t *testing.T) {
	t.Parallel()

	t.Run("alias alone sets the canonical", func(t *testing.T) {
		t.Parallel()
		cmd := &cobra.Command{Use: "test"}
		var f command.ListFlags
		command.AddListFlags(cmd, &f)

		if err := cmd.Flags().Set("desc", "true"); err != nil {
			t.Fatalf("set --desc: %v", err)
		}
		if err := command.ResolveListFlagAliases(cmd.Flags()); err != nil {
			t.Fatalf("ResolveListFlagAliases: %v", err)
		}
		if !f.Reverse {
			t.Error("Reverse = false, want true from --desc")
		}
	})

	t.Run("canonical and alias disagree is refused", func(t *testing.T) {
		t.Parallel()
		cmd := &cobra.Command{Use: "test"}
		var f command.ListFlags
		command.AddListFlags(cmd, &f)

		if err := cmd.Flags().Set("reverse", "true"); err != nil {
			t.Fatalf("set --reverse: %v", err)
		}
		if err := cmd.Flags().Set("desc", "false"); err != nil {
			t.Fatalf("set --desc: %v", err)
		}
		err := command.ResolveListFlagAliases(cmd.Flags())
		if err == nil {
			t.Fatal("ResolveListFlagAliases returned nil error for disagreeing --reverse/--desc values")
		}
		var inputErr *cli.InputError
		if !errors.As(err, &inputErr) {
			t.Fatalf("error = %v (%T), want *cli.InputError", err, err)
		}
		if inputErr.Field != "reverse" {
			t.Errorf("field = %q, want %q", inputErr.Field, "reverse")
		}
	})
}

// TestResolveListFlagAliasesNoSignalIsANoOp proves that with neither the
// canonical flag nor any of its aliases given, resolution leaves the
// struct at its zero value and returns no error.
func TestResolveListFlagAliasesNoSignalIsANoOp(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	var f command.ListFlags
	command.AddListFlags(cmd, &f)

	if err := command.ResolveListFlagAliases(cmd.Flags()); err != nil {
		t.Fatalf("ResolveListFlagAliases: %v, want nil when nothing was given", err)
	}
	if f.OrderBy != "" || f.Reverse || f.Watching {
		t.Errorf("f = %+v, want the zero value untouched", f)
	}
}
