package command

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
)

func TestJSONInputValidationPreservesValueAndChangedState(t *testing.T) {
	t.Parallel()
	for _, spelling := range []string{FlagJSONInput, "from-json", "body-adf"} {
		for _, tc := range []struct {
			name  string
			value string
		}{
			{name: "empty", value: ""},
			{name: "stdin", value: StdinPath},
			{name: "file", value: "request.json"},
			{name: "space", value: " "},
			{name: "unicode", value: "文件.json"},
		} {
			t.Run(spelling+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				cmd := &cobra.Command{}
				var input string
				AddStringVar(cmd, &input, FlagJSONInput, "", "Read native input", FlagOpts{Terse: "Native input", Placeholder: "path"})
				AddHiddenAliases(cmd, ADFRouteAliases())
				fs := cmd.Flags()
				if err := fs.Parse([]string{"--" + spelling + "=" + tc.value}); err != nil {
					t.Fatal(err)
				}
				if err := ResolveHiddenAliases(fs, ADFRouteAliases()); err != nil {
					t.Fatal(err)
				}
				err := ValidateJSONInput(fs)
				if tc.value == "" {
					var refusal *cli.InputError
					if !errors.As(err, &refusal) || refusal.Code != cli.CodeFlagValueInvalid || refusal.Field != FlagJSONInput {
						t.Fatalf("empty authored input lacks canonical refusal: %v", err)
					}
				} else if err != nil {
					t.Fatalf("literal input path was changed or refused: %v", err)
				}
				if input != tc.value || fs.Changed(FlagJSONInput) != (spelling == FlagJSONInput) || !fs.Changed(spelling) {
					t.Fatalf("validation changed input or flag provenance: input=%q canonical_changed=%t authored_changed=%t", input, fs.Changed(FlagJSONInput), fs.Changed(spelling))
				}
			})
		}
	}
}

func TestJSONInputValidationAllowsOmittedOrUnavailableRoute(t *testing.T) {
	t.Parallel()
	for _, register := range []bool{false, true} {
		name := "unavailable"
		if register {
			name = "omitted"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cmd := &cobra.Command{}
			var input string
			if register {
				AddStringVar(cmd, &input, FlagJSONInput, "", "Read native input", FlagOpts{Terse: "Native input", Placeholder: "path"})
			}
			if err := ValidateJSONInput(cmd.Flags()); err != nil {
				t.Fatalf("unauthored route was refused: %v", err)
			}
		})
	}
}
