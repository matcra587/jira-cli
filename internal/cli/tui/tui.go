// Package tui wires the human dashboard through the shared command owner.
package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/tui/dashboard"
)

// Deps retains the ordinary lazy profile and Jira construction owners.
type Deps struct {
	JiraClient    func(context.Context) (core.JiraClient, error)
	Preferences   func() (core.IssuePreferences, error)
	ResolveConfig func(context.Context) (*config.Resolved, error)
}

// New builds the explicit dashboard entry point. Bare jira retains discovery.
func New(deps Deps) *cobra.Command {
	var query string
	var client core.JiraClient
	var project, projectQuery string
	var options dashboard.Options
	return command.MustNew(command.Spec[envelope.TUIOutput]{
		WritesJira: true, MutatesLocalState: true,
		Op: "tui", Use: "tui", Short: "Open the interactive issue dashboard",
		Long: "Browse your work, saved views and issue details in a persistent terminal dashboard. " +
			"Use Ctrl+O to switch issues, v for saved views and lenses, Ctrl+W to change focus, and [ or ] for detail sections. " +
			"Transition, assign and comment actions review one explicit issue before submission; read-only mode disables them. " +
			"Use Tab to switch views, / to edit JQL, Enter to focus a preview, Ctrl+P for commands, comma for settings, and ? for help. " +
			"Requires interactive input and output in human mode. For scripts and agents use jira issue list or jira issue view.",
		Example: "  jira tui\n  jira tui --jql='assignee = currentUser() ORDER BY updated DESC'\n  jira --profile=work tui",
		Flags: func(cmd *cobra.Command) {
			command.AddStringVar(cmd, &query, "jql", "", "Start with an explicit JQL query instead of My work", command.FlagOpts{})
		},
		Validate: func() error {
			if query != "" && strings.TrimSpace(query) == "" {
				err := cli.NewInputError(cli.CodeFlagValueInvalid, "JQL must contain a query")
				err.Field = "jql"
				return err
			}
			return nil
		},
		Run: func(ctx context.Context, _ command.Invocation) (envelope.TUIOutput, error) {
			if deps.Preferences != nil {
				prefs, err := deps.Preferences()
				if err != nil {
					return envelope.TUIOutput{}, err
				}
				project = prefs.DefaultProject
			}
			if project != "" {
				var err error
				projectQuery, err = command.ComposeJQL(command.JQLInput{Project: []string{project}, Status: []string{"<Done"}, OrderBy: "updated"})
				if err != nil {
					return envelope.TUIOutput{}, err
				}
			}
			if deps.ResolveConfig != nil {
				resolved, err := deps.ResolveConfig(ctx)
				if err != nil {
					return envelope.TUIOutput{}, err
				}
				options, err = dashboardOptions(ctx, resolved)
				options.Actions.ReadOnly = resolved.Config.ReadOnly
				if err != nil {
					return envelope.TUIOutput{}, err
				}
			}
			var err error
			client, err = deps.JiraClient(ctx)

			return envelope.TUIOutput{View: "issues"}, err
		},
		Screen: func(ctx context.Context, _ envelope.TUIOutput) tea.Model {
			options.Actions = dashboardActions(ctx, client, options.Actions.ReadOnly)
			options.Project, options.ProjectQuery, options.Query = project, projectQuery, strings.TrimSpace(query)
			return dashboard.New(ctx, client, options)
		},
	})
}
