package tui

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewCommand reports the deferred dashboard without loading configuration or credentials.
func NewCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "tui",
		Short:   "Report the deferred dashboard runtime",
		Long:    "The persistent dashboard runtime is deferred during the CLI migration. Use the issue and search commands instead; existing TUI settings remain stored but are not executed.",
		Example: "$ jira issue list\n$ jira search jql 'statusCategory != Done'",
		GroupID: "dashboard",
		Args:    cobra.NoArgs,
		RunE:    func(cmd *cobra.Command, _ []string) error { return Run(cmd) },
	}
}

// Run refuses dashboard entry while its replacement runtime is deferred.
func Run(_ *cobra.Command) error {
	return fmt.Errorf("dashboard runtime is deferred; use the issue and search commands")
}
