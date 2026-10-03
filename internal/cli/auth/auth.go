package auth

import (
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

// New returns the `jira auth` command group.
//
// Credentials live here, settings live in `jira config`. The split is
// not tidiness: it is what lets the config file hold nothing secret, so
// it can be printed, diffed, and committed to a dotfiles repo without a
// redaction problem — the thing that has kept another CLI's equivalent
// unstable for years.
func New(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate with Jira",
		Long: "Manage the credentials `jira` uses.\n\n" +
			"The API token is stored in your operating system's credential " +
			"store — Keychain on macOS, Credential Manager on Windows, Secret " +
			"Service on Linux — never in the config file. The site and account " +
			"are stored WITH it, as one record: they are useless apart from " +
			"the token and it is useless apart from them, which is what makes " +
			"a first run a single command. `jira config` holds preferences.\n\n" +
			"Tokens are read from standard input, never from a flag: a flag " +
			"value is visible in `ps` and lands in shell history.",
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newLogin(deps), newStatus(deps), newWhoami(deps), newLogout(deps), newToken(deps))
	command.SetMeta(cmd, command.Meta{
		Op:                "auth",
		NoSuccessEnvelope: "a bare group prints help, not an operation result",
	})
	return cmd
}
