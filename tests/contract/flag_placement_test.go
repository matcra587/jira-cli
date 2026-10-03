package contract

import (
	"encoding/json"
	"testing"
)

// TestOutputSelectorPlacementAgrees pins the other half of the
// flag-shadowing guard (tests/guardrails) by execution: a root
// persistent flag must be honored identically written before or after
// the subcommand. Compact is the probe mode on purpose — the hermetic
// run is non-TTY, so auto already resolves to json and a shadowed
// selector stuck at its default would emit a full json FRAME instead of
// compact's bare data; asserting bare data catches the silent-wrong-value
// mode that mere before/after equality would miss (a shadow makes both
// placements equally wrong). Compact carries no timestamp or request id,
// so the two runs must also match byte for byte.
func TestOutputSelectorPlacementAgrees(t *testing.T) {
	t.Parallel()
	placements := []struct {
		name string
		args []string
	}{
		{name: "flag before subcommand", args: []string{"--output=compact", "version"}},
		{name: "flag after subcommand", args: []string{"version", "--output=compact"}},
	}

	outputs := make(map[string]string, len(placements))
	for _, tc := range placements {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, exit := runBinary(t, tc.args...)
			if exit != 0 {
				t.Fatalf("exit = %d, want 0 (stderr: %q)", exit, stderr)
			}
			var data map[string]any
			if err := json.Unmarshal([]byte(stdout), &data); err != nil {
				t.Fatalf("stdout does not decode: %v\nstdout: %q", err, stdout)
			}
			// Bare data proves --output=compact actually reached
			// resolution from this placement.
			for _, key := range []string{"ok", "meta", "errors", "warnings"} {
				if _, has := data[key]; has {
					t.Errorf("compact output carries frame key %q — the selector did not bind from this placement", key)
				}
			}
			assertVersionData(t, data)
			outputs[tc.name] = stdout
		})
	}

	if outputs[placements[0].name] != outputs[placements[1].name] {
		t.Errorf("flag placement changed the output:\nbefore subcommand: %q\nafter subcommand:  %q",
			outputs[placements[0].name], outputs[placements[1].name])
	}
}
