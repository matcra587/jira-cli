package command

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
)

// The flag-level half of the bulk selectors: the refusals that can be
// decided with no I/O at all. The ordering rule — resolve before
// confirming — is a property of a whole run and is pinned on `issue
// delete` (internal/cli/issue/selector_test.go).

// TestSelectorIsRefusedBesidePositionalKeys is design §3.5 rule 2. Two
// ways of naming the target in one invocation is the ambiguity §7
// rejects `--key` for, with more at stake: there is no merge rule worth
// inventing, because a caller who wrote both has a wrong belief about
// which one runs and acting on either leaves them with it.
func TestSelectorIsRefusedBesidePositionalKeys(t *testing.T) {
	for _, tt := range []struct {
		name string
		sel  SelectorFlags
		want string
	}{
		{name: "jql", sel: SelectorFlags{JQL: "project = KEY"}, want: FlagJQL},
		{name: "filter", sel: SelectorFlags{Filter: "10042"}, want: FlagFilter},
		{name: "from-file", sel: SelectorFlags{FromFile: "keys.txt"}, want: FlagFromFile},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.sel.Validate(2)
			if err == nil {
				t.Fatalf("--%s beside 2 positional keys was accepted", tt.want)
			}
			var input *cli.InputError
			if !errors.As(err, &input) {
				t.Fatalf("refusal is %T, want a typed *cli.InputError", err)
			}
			if input.Code != cli.CodeFlagConflict {
				t.Errorf("code = %q, want %q", input.Code, cli.CodeFlagConflict)
			}
			if input.Field != tt.want {
				t.Errorf("field = %q, want %q", input.Field, tt.want)
			}
		})
	}
}

// TestTwoSelectorsAreRefused: nothing here composes them with AND, so an
// invocation naming two could only mean one of them is silently ignored
// — the same ambiguity as a selector beside keys.
func TestTwoSelectorsAreRefused(t *testing.T) {
	for _, tt := range []struct {
		name string
		sel  SelectorFlags
	}{
		{name: "jql and filter", sel: SelectorFlags{JQL: "project = KEY", Filter: "10042"}},
		{name: "jql and from-file", sel: SelectorFlags{JQL: "project = KEY", FromFile: "keys.txt"}},
		{name: "filter and from-file", sel: SelectorFlags{Filter: "10042", FromFile: "keys.txt"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.sel.Validate(0)
			if err == nil {
				t.Fatal("two selectors in one invocation were accepted")
			}
			if got := cli.MapError(err).Code; got != cli.CodeFlagConflict {
				t.Errorf("code = %q, want %q", got, cli.CodeFlagConflict)
			}
		})
	}
}

// TestNeitherKeysNorSelectorIsRefusedNamingBothRoutes. Registering a
// selector means dropping the ArgSpec minimum to zero, so an empty
// invocation now reaches Validate instead of cobra's arity check. The
// replacement message has to be at least as good as the one it replaced,
// which means naming the route cobra never knew about.
func TestNeitherKeysNorSelectorIsRefusedNamingBothRoutes(t *testing.T) {
	err := SelectorFlags{}.Validate(0)
	if err == nil {
		t.Fatal("an invocation naming nothing at all was accepted")
	}
	if got := cli.MapError(err).Code; got != cli.CodeArgCountInvalid {
		t.Errorf("code = %q, want %q", got, cli.CodeArgCountInvalid)
	}
	for _, want := range []string{"--" + FlagJQL, "--" + FlagFilter, "--" + FlagFromFile} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not offer %s; cobra's own arity error already said 'name a key', and this one has to add what else is possible", err, want)
		}
	}
}

// TestKeysAloneAndSelectorAloneAreBothAccepted keeps the refusals above
// from being a blanket one — the two shapes the command actually
// supports must survive.
func TestKeysAloneAndSelectorAloneAreBothAccepted(t *testing.T) {
	if err := (SelectorFlags{}).Validate(1); err != nil {
		t.Errorf("named keys with no selector = %v, want accepted", err)
	}
	if err := (SelectorFlags{JQL: "project = KEY"}).Validate(0); err != nil {
		t.Errorf("selector with no keys = %v, want accepted", err)
	}
}

// TestAddSelectorFlagsRegistersAllThree. The registrar exists so five
// commands cannot drift; this pins that a command calling it gets the
// whole surface rather than part of it.
func TestAddSelectorFlagsRegistersAllThree(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	var sel SelectorFlags
	AddSelectorFlags(cmd, &sel)

	for _, name := range []string{FlagJQL, FlagFilter, FlagFromFile} {
		f := cmd.Flags().Lookup(name)
		if f == nil {
			t.Fatalf("--%s is not registered", name)
		}
		if f.Hidden {
			t.Errorf("--%s is hidden; these are documented flags, not aliases", name)
		}
	}
}

// TestSelectorUnresolvedWarningOnlyFiresForASiteRead. --from-file
// resolves locally, so a preview enumerates it truthfully and has
// nothing to warn about. A warning there would tell the caller something
// was withheld when nothing was.
func TestSelectorUnresolvedWarningOnlyFiresForASiteRead(t *testing.T) {
	if w := SelectorUnresolvedWarning(SelectorFlags{FromFile: "keys.txt"}); w.Type != "" {
		t.Errorf("--%s raised %q; a local file needs no site, so nothing was withheld", FlagFromFile, w.Type)
	}
	w := SelectorUnresolvedWarning(SelectorFlags{JQL: "project = KEY"})
	if w.Type != "selector_unresolved" {
		t.Fatalf("type = %q, want selector_unresolved", w.Type)
	}
	if !w.Lossy {
		t.Error("warning is not Lossy; an unresolved selector means the results[] a caller asked to see is the part that is absent")
	}
	if !strings.Contains(w.Message, "--"+FlagValidateRemote) {
		t.Errorf("message %q does not name the flag that would resolve it", w.Message)
	}
}

// TestFilterRefusesANameBeforeAnyRequest. Jira lets two filters share a
// name, so a name cannot say which one to act on — and the commands
// taking this flag are the ones where an ambiguous target set is least
// affordable. Refused locally, so it costs no round trip.
func TestFilterRefusesANameBeforeAnyRequest(t *testing.T) {
	for _, id := range []string{"my bugs", "10042x", "", "-1", "10 042"} {
		if isFilterID(id) {
			t.Errorf("isFilterID(%q) = true, want false", id)
		}
	}
	for _, id := range []string{"1", "10042"} {
		if !isFilterID(id) {
			t.Errorf("isFilterID(%q) = false, want true — a real filter id is digits", id)
		}
	}
}
