package command_test

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

func TestMetaClassDerivation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		meta command.Meta
		want command.Class
	}{
		{name: "plain read", meta: command.Meta{Op: "x"}, want: command.ClassRead},
		{name: "jira write", meta: command.Meta{Op: "x", WritesJira: true}, want: command.ClassMutation},
		{name: "local write", meta: command.Meta{Op: "x", MutatesLocalState: true}, want: command.ClassLocalState},
		// The Jira write dominates: dry-run/preview obligations follow
		// the remote mutation even when local state also changes.
		{name: "both writes", meta: command.Meta{Op: "x", WritesJira: true, MutatesLocalState: true}, want: command.ClassMutation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.meta.Class(); got != tt.want {
				t.Errorf("Class() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSetMetaRoundTrip(t *testing.T) {
	// The registry is mutex-guarded (see meta.go), and each case uses a
	// distinct *cobra.Command, so these do not contend.
	cmd := &cobra.Command{Use: "thing"}
	command.SetMeta(cmd,
		command.Meta{Op: "thing.list"},
		command.Meta{Op: "thing.count", Variant: "--count"},
	)

	got, ok := command.MetaFor(cmd)
	if !ok {
		t.Fatal("MetaFor found no metadata after SetMeta")
	}
	if len(got) != 2 || got[0].Op != "thing.list" || got[1].Variant != "--count" {
		t.Errorf("MetaFor = %+v", got)
	}

	// The returned slice is a copy: registry state must be immune to
	// caller mutation.
	got[0].Op = "mutated"
	again, _ := command.MetaFor(cmd)
	if again[0].Op != "thing.list" {
		t.Errorf("mutating MetaFor's result wrote through to the registry: %q", again[0].Op)
	}
}

func TestMetaForUnknownCommand(t *testing.T) {
	// The registry is mutex-guarded (see meta.go), and each case uses a
	// distinct *cobra.Command, so these do not contend.
	if _, ok := command.MetaFor(&cobra.Command{Use: "undeclared"}); ok {
		t.Error("MetaFor invented metadata for an undeclared command")
	}
}

func TestMetaArgumentsOwnStorage(t *testing.T) {
	t.Parallel()
	args := command.ArgSpec{Min: 1, Max: command.AnyArgs, Positions: []command.Arg{{Name: "KEY", Predictor: "issuekey"}}}
	cmd := &cobra.Command{Use: "fixture KEY..."}
	command.SetMeta(cmd, command.Meta{Op: "fixture", Args: &args})
	args.Min = 0
	args.Positions[0].Name = "changed by declaration owner"
	got, _ := command.MetaFor(cmd)
	if got[0].Args.Min != 1 || got[0].Args.Max != command.AnyArgs || got[0].Args.Positions[0].Name != "KEY" || got[0].Args.Positions[0].Predictor != "issuekey" {
		t.Fatal("metadata aliases the caller's argument declaration")
	}
	got[0].Args.Max = 0
	got[0].Args.Positions[0].Predictor = "changed by consumer"
	again, _ := command.MetaFor(cmd)
	if again[0].Args.Max != command.AnyArgs || again[0].Args.Positions[0].Predictor != "issuekey" {
		t.Fatal("metadata consumer changed registered argument facts")
	}
}

func TestSetMetaRejectsMalformedDeclarations(t *testing.T) {
	// The registry is mutex-guarded (see meta.go), and each case uses a
	// distinct *cobra.Command, so these do not contend.
	tests := []struct {
		name     string
		metas    []command.Meta
		wantPart string
	}{
		{
			name:     "empty op",
			metas:    []command.Meta{{}},
			wantPart: "without an operation name",
		},
		{
			name:     "variant as default form",
			metas:    []command.Meta{{Op: "x", Variant: "--count"}},
			wantPart: "default form",
		},
		{
			name:     "second default form",
			metas:    []command.Meta{{Op: "x"}, {Op: "y"}},
			wantPart: "second default form",
		},
		{
			name:     "duplicate variant",
			metas:    []command.Meta{{Op: "x"}, {Op: "y", Variant: "--n"}, {Op: "z", Variant: "--n"}},
			wantPart: "twice",
		},
		{
			name:     "envelope-free with data traits",
			metas:    []command.Meta{{Op: "x", Identity: true, NoSuccessEnvelope: "reason"}},
			wantPart: "envelope-free",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("SetMeta accepted a malformed declaration")
				}
				if msg, ok := r.(string); !ok || !strings.Contains(msg, tt.wantPart) {
					t.Errorf("panic = %v, want mention of %q", r, tt.wantPart)
				}
			}()
			command.SetMeta(&cobra.Command{Use: "bad"}, tt.metas[0], tt.metas[1:]...)
		})
	}
}

func TestSetMetaRejectsDuplicateCommand(t *testing.T) {
	// The registry is mutex-guarded (see meta.go), and each case uses a
	// distinct *cobra.Command, so these do not contend.
	cmd := &cobra.Command{Use: "once"}
	command.SetMeta(cmd, command.Meta{Op: "once"})
	defer func() {
		if recover() == nil {
			t.Fatal("SetMeta accepted a second declaration for the same command")
		}
	}()
	command.SetMeta(cmd, command.Meta{Op: "once.again"})
}
