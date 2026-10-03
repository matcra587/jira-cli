package contract

import (
	"encoding/json"
	"testing"
)

// TestStrayPositionalRoutesByValidator pins both halves of one rule: a
// stray positional is a MISCOUNT on a leaf command and an UNKNOWN
// COMMAND on a parent.
//
// Which one the caller gets is decided by comparing the command's Args
// validator against cobra.NoArgs BY FUNCTION POINTER
// (root.isNoArgs). That works today because every command assigns
// cobra.NoArgs directly — and it breaks the moment anything wraps the
// validator, however transparently, because a closure around NoArgs is
// not NoArgs. The grouper then reports a miscount, loses its `field`
// and suggestions, and offers a hint about argument counts for what was
// actually a mistyped verb.
//
// Nothing pinned that. A construction path that builds validators is
// exactly the thing that would wrap them, so this asserts the OUTCOME
// from outside the binary — it survives a reimplementation, where a
// test of the pointer comparison would only survive the current one.
//
// Verified by planting a transparent forwarding wrapper around every
// validator: the parent case flips to arg_count_invalid, this fails.
func TestStrayPositionalRoutesByValidator(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		args      []string
		wantCode  string
		wantField string
	}{
		// A leaf: there is no subcommand it could have meant.
		{name: "leaf", args: []string{"version", "extraarg"}, wantCode: "arg_count_invalid"},
		// A parent, reached the ONLY way this branch can be: after "--".
		// Without the terminator the pre-dispatch command-path check
		// catches the stray token first and never consults a validator,
		// so `jira extraarg` proves nothing about this rule — it was the
		// first thing this test asserted, and a planted wrapper sailed
		// through it.
		//
		// The terminator declares the token a non-command, the check
		// skips it, and cobra's NoArgs is what finally rejects it. The
		// root qualifies because it is runnable AND has subcommands; a
		// plain group is not runnable, so cobra shows help before ever
		// validating arguments.
		{name: "parent after terminator", args: []string{"--", "extraarg"}, wantCode: "command_unknown", wantField: "extraarg"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, exit := runBinary(t, append(tt.args, "--output=json")...)
			if exit != 3 {
				t.Fatalf("exit = %d, want 3 (stderr: %q)", exit, stderr)
			}

			var frame struct {
				Errors []struct {
					Code  string `json:"code"`
					Field string `json:"field"`
					Hint  string `json:"hint"`
				} `json:"errors"`
			}
			if err := json.Unmarshal([]byte(stdout), &frame); err != nil {
				t.Fatalf("decode %q: %v", stdout, err)
			}
			if len(frame.Errors) != 1 {
				t.Fatalf("got %d errors, want exactly 1: %s", len(frame.Errors), stdout)
			}
			got := frame.Errors[0]
			if got.Code != tt.wantCode {
				t.Errorf("code = %q, want %q — the validator comparison stopped distinguishing a parent from a leaf", got.Code, tt.wantCode)
			}
			if got.Field != tt.wantField {
				t.Errorf("field = %q, want %q", got.Field, tt.wantField)
			}
			if got.Hint == "" {
				t.Error("no hint; every code carries one")
			}
		})
	}
}
