package completion

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gechr/clib/complete"
	"github.com/gechr/x/shell"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/output"
)

// shells lists the shells clib ships a generator for, by their x/shell
// names — the same identifiers the generator's registry keys on, so the
// arg that validates here cannot fail to resolve there.
var shells = []string{shell.Bash, shell.Elvish, shell.Fish, shell.Nu, shell.Pwsh, shell.Zsh}

// Shells returns the shell names accepted by the completion command.
func Shells() []string {
	return slices.Clone(shells)
}

// New returns the `jira completion` command. The generator is injected
// as a constructor because the completion spec is derived from the
// fully assembled tree — the root package owns that assembly, and
// building the spec any earlier would bake in a partial command set.
func New(generator func() *complete.Generator) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion <shell>",
		Short: "Print a shell completion script",
		Long: "Print the completion script for a shell to stdout. Source it from " +
			"the shell's config, or write it to the shell's completions " +
			"directory, to get tab completion for commands, flags, and values.\n\n" +
			"Regenerate and load the script after upgrading. PowerShell preserves " +
			"Unicode suggestions, including continued directory paths.\n\n" +
			"The script is the output, so `--output` has no effect here: every " +
			"mode prints the same bytes. Nothing else may reach stdout during a " +
			"completion callback, which is why this command emits no envelope.",
		Example: "# print the fish completion script\n" +
			"$ jira completion fish\n\n" +
			"# install for fish by writing to its completions directory\n" +
			"$ jira completion fish > ~/.config/fish/completions/jira.fish\n\n" +
			"# install for zsh via a directory on $fpath\n" +
			"$ jira completion zsh > ~/.zsh/completions/_jira",
		ValidArgs:   shells,
		Annotations: map[string]string{"clib": "dynamic-args='shell'"},
		// Typed at the source: an arity miss and a wrong shell name are
		// different mistakes with different recoveries, so each carries
		// its own code, the offender rides Field, and near-miss shells
		// ride Suggestions. The message restates the valid set — the
		// error is the help fragment.
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return cli.NewInputError(cli.CodeArgCountInvalid,
					fmt.Sprintf("completion takes exactly one shell argument: %s", strings.Join(shells, ", ")))
			}
			if !slices.Contains(shells, args[0]) {
				e := cli.NewInputError(cli.CodeArgValueInvalid,
					fmt.Sprintf("unknown shell %q: valid shells are %s", args[0], strings.Join(shells, ", ")))
				e.Field = args[0]
				e.Suggestions = cli.SuggestValues(args[0], shells)
				return e
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := generator().Print(cmd.OutOrStdout(), args[0]); err != nil {
				// The script IS this command's output; failing to deliver
				// it is the output_write_failed contract.
				return &output.WriteError{Err: err}
			}
			return nil
		},
	}
	// A script on stdout is this command's whole point — an envelope
	// would corrupt the artifact the shell is about to eval.
	command.SetMeta(cmd, command.Meta{
		Op:                "completion",
		NoSuccessEnvelope: "success emits a shell completion script, not an envelope — the shell evals the bytes",
	})
	return cmd
}
