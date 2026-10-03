package root

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

// TestFlagParseFailuresCarryTheirCode drives REAL pflag parse failures
// through the assembled tree so every branch of newFlagError is covered
// by the thing that actually produces it. Constructing pflag's typed
// errors by hand would pin this file's idea of them rather than the
// parser's: the branches read the flag identity structurally, through
// accessors whose population pflag documents nowhere, so only a genuine
// parse proves Field lands.
//
// Only the unknown-flag branch had coverage before; the other three were
// verified correct by hand and left untested, which is how a taxonomy
// silently drifts.
func TestFlagParseFailuresCarryTheirCode(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantCode  string
		wantField string
	}{
		{
			name:      "unknown long flag",
			args:      []string{"version", "--nope", "--output=json"},
			wantCode:  "flag_unknown",
			wantField: "nope",
		},
		{
			// --output takes a value and is given none: pflag reports
			// ValueRequiredError carrying the resolved *pflag.Flag.
			name:      "value-taking flag at end of argv",
			args:      []string{"version", "--output"},
			wantCode:  "flag_value_missing",
			wantField: "output",
		},
		{
			// --verbose is count-typed, so a non-numeric value fails
			// conversion rather than lookup: InvalidValueError.
			name:      "typed flag given an unconvertible value",
			args:      []string{"version", "--verbose=notanumber", "--output=json"},
			wantCode:  "flag_value_invalid",
			wantField: "verbose",
		},
		{
			// A flag token with no name at all cannot yield a *pflag.Flag,
			// so Field falls back to the offending token, dashes stripped.
			name:      "malformed flag token",
			args:      []string{"version", "--=value", "--output=json"},
			wantCode:  "flag_syntax_invalid",
			wantField: "=value",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exit, frame, raw := runFrame(t, tt.args)
			if exit != cli.ExitValidation {
				t.Fatalf("exit = %d, want %d\nframe: %s", exit, cli.ExitValidation, raw)
			}
			if len(frame.Errors) != 1 {
				t.Fatalf("want exactly one error, got frame %s", raw)
			}
			if got := frame.Errors[0].Code; got != tt.wantCode {
				t.Errorf("code = %q, want %q", got, tt.wantCode)
			}
			if got := frame.Errors[0].Field; got != tt.wantField {
				t.Errorf("field = %q, want %q", got, tt.wantField)
			}
		})
	}
}
