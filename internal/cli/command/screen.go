package command

import (
	"os"

	"github.com/gechr/x/terminal"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/output"
)

func requireScreen(cmd *cobra.Command) error {
	format, ok := cli.FormatFromContext(cmd.Context())
	destination, file := cmd.OutOrStdout().(*os.File)
	if !ok || format != output.FormatHuman || cli.FilterFromContext(cmd.Context()) != nil || !interactive(cmd) || !file || !terminal.Is(destination) {
		return cli.NewCodedError(cli.CodeDashboardTerminalRequired,
			"the dashboard requires interactive input and output with --output=human; use jira issue list for machine output")
	}
	return nil
}
