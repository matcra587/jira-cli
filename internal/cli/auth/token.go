package auth

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
)

// newToken returns `jira auth token`.
func newToken(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Print the stored API token",
		Long: "Print the API token on standard output, so it can be piped into " +
			"another tool.\n\n" +
			"This is the one command that deliberately emits a secret, so it " +
			"emits nothing else: no envelope, no formatting, just the token " +
			"and a newline. `--output` does not change it. Wrapping a secret " +
			"in a JSON frame would carry it into logs, `--jq` output, and " +
			"anything that records machine output.\n\n" +
			"Whatever consumes this inherits responsibility for it.",
		Example: "# capture it in a shell variable\n" +
			"$ token=$(jira auth token)\n\n" +
			"# call the REST API directly with it\n" +
			"$ curl -u \"you@example.com:$(jira auth token)\" \\\n" +
			"    https://your-site.atlassian.net/rest/api/3/myself",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runToken(cmd, deps)
		},
	}
	command.SetMeta(cmd, command.Meta{
		Op:                "auth.token",
		NoSuccessEnvelope: "the output IS the secret; a frame would carry it into logs and filters",
	})
	return cmd
}

func runToken(cmd *cobra.Command, deps Deps) error {
	token, err := tokenValue(deps)
	if err != nil {
		return err
	}
	// Straight to the command's writer, unframed. The trailing newline
	// is what makes `$(jira auth token)` behave: command substitution
	// strips it, and a file redirect gets a well-formed line.
	_, err = fmt.Fprintln(cmd.OutOrStdout(), token)
	return err
}

// tokenValue returns the resolved token, or an auth failure when there
// is none.
func tokenValue(deps Deps) (string, error) {
	site, err := deps.Site()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(site.Token) == "" {
		return "", cli.NewCodedError(cli.CodeAuthMissing,
			"no API token stored — pipe one in with `jira auth login --with-token`")
	}
	return site.Token, nil
}
