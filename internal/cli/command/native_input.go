package command

import (
	"github.com/spf13/pflag"

	"github.com/matcra587/jira-cli/internal/cli"
)

// ValidateJSONInput refuses an explicitly authored empty native-input path.
// Call after resolving aliases so the canonical value reflects every spelling.
// Omitted input remains available for the command's other input routes.
func ValidateJSONInput(fs *pflag.FlagSet) error {
	flag := fs.Lookup(FlagJSONInput)
	if flag == nil || flag.Value.String() != "" {
		return nil
	}
	given := fs.Changed(FlagJSONInput)
	for _, alias := range ADFRouteAliases() {
		given = given || fs.Changed(alias.Flag)
	}
	if !given {
		return nil
	}
	err := cli.NewInputError(cli.CodeFlagValueInvalid, "--"+FlagJSONInput+": requires a file path or - for standard input")
	err.Field = FlagJSONInput
	return err
}
