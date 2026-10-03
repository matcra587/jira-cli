package command

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
)

// TestAddMutationFlagsRegistersOnlyDryRunByDefault pins the registrar's
// central choice: the footprints of the write flags differ sharply
// across 0.15's mutations, so the bare call must register the ONE flag
// every mutation carries and nothing else. A registrar that applied them
// uniformly would invent surface no tool has.
func TestAddMutationFlagsRegistersOnlyDryRunByDefault(t *testing.T) {
	cmd := &cobra.Command{Use: "add"}
	AddMutationFlags(cmd, &MutationFlags{})
	fs := cmd.Flags()

	if fs.Lookup(cli.FlagDryRun) == nil {
		t.Error("--dry-run is not registered; it is unconditional for a mutation")
	}
	for _, name := range []string{FlagForce, FlagValidateRemote, FlagVerify, FlagNoReadback} {
		if fs.Lookup(name) != nil {
			t.Errorf("--%s registered without being asked for", name)
		}
	}
}

func TestMutationOptionsRegisterAndBind(t *testing.T) {
	tests := []struct {
		name string
		opt  MutationOption
		flag string
		get  func(*MutationFlags) bool
	}{
		{"force", WithForce(), FlagForce, func(f *MutationFlags) bool { return f.Force }},
		{"validate-remote", WithValidateRemote(), FlagValidateRemote, func(f *MutationFlags) bool { return f.ValidateRemote }},
		{"verify", WithVerify(), FlagVerify, func(f *MutationFlags) bool { return f.Verify }},
		{"no-readback", WithoutReadback(), FlagNoReadback, func(f *MutationFlags) bool { return f.NoReadback }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "add"}
			flags := &MutationFlags{}
			AddMutationFlags(cmd, flags, tt.opt)

			if cmd.Flags().Lookup(tt.flag) == nil {
				t.Fatalf("--%s not registered", tt.flag)
			}
			// Registration alone is not enough: the flag has to reach the
			// struct field, or the command reads a default forever.
			if err := cmd.Flags().Parse([]string{"--" + tt.flag}); err != nil {
				t.Fatalf("parse --%s: %v", tt.flag, err)
			}
			if !tt.get(flags) {
				t.Errorf("--%s parsed but did not bind into MutationFlags", tt.flag)
			}
		})
	}
}

// TestMutationFlagsCarriesNoDryRunField is the anti-regression guard for
// the bug Invocation.DryRun's doc comment records: `config edit` bound
// its own dryRun local, nothing populated it, and the preview edited the
// file. One carrier for the flag, and it is the path's.
func TestMutationFlagsCarriesNoDryRunField(t *testing.T) {
	cmd := &cobra.Command{Use: "add"}
	flags := &MutationFlags{}
	AddMutationFlags(cmd, flags)

	if err := cmd.Flags().Parse([]string{"--" + cli.FlagDryRun}); err != nil {
		t.Fatalf("parse --dry-run: %v", err)
	}
	// The registrar must not have bound --dry-run into any field of its
	// own; the value is read off the command's flag set by the
	// construction path.
	if flags.Force || flags.ValidateRemote || flags.Verify || flags.NoReadback {
		t.Errorf("--dry-run bound into a MutationFlags field: %+v", flags)
	}
	on, err := cmd.Flags().GetBool(cli.FlagDryRun)
	if err != nil || !on {
		t.Errorf("--dry-run did not land on the command's own flag set (on=%v, err=%v)", on, err)
	}
}
