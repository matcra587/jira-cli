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
	Reload        func(context.Context) (*config.Resolved, core.JiraClient, error)
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
			"Transition, assign, comment, summary and label actions review explicit targets before submission; read-only mode disables them. " +
			"Use e to edit a summary or labels and l for the literal label list. Replacement reviews default to Cancel and use immutable issue IDs. " +
			"Choose Description in the e menu to edit native anchored Markdown while preserving protected regions and unknown ADF. Ctrl+S prepares review; F4 reloads after confirmation. Description replacement is unconditional and concurrent edits can still race the final read. " +
			"Use n to create an issue with project/type choices, summary and Markdown description; F2 edits labels, F3 assignee and F4 context. Ctrl+S reviews before a single submission. " +
			"Forward list navigation loads the next page near the result boundary; disable Load more as you scroll in Settings for manual-only loading with m. Failed page reads wait for an explicit retry. " +
			"Use z to zoom the list and restore its preview. Appearance settings choose Automatic, Left, Right or Bottom docking and a 25–75 percent preview size. Click view tabs and detail section headings to navigate. Small terminals keep full details on Enter. " +
			"The active view refreshes automatically and on terminal focus; R pauses automatic reads and r refreshes explicitly. Settings configures the cadence, focus trigger and changed-row markers. " +
			"Settings → Workspace manages named views, narrowing lenses, tab order and startup choices. Alt+V chooses a visible tab; Alt+L chooses a lens. Startup choices apply on the next launch; saved collections reload immediately. " +
			"Use H for hierarchy roots, Right for native children, Left for their parent level, Backspace for prior lists, and Ctrl+G for hierarchy around the selected issue. Hierarchy settings choose roots by JQL or a visible Jira issue type with F2. " +
			"Tab and Shift+Tab switch views without starting an editor. Empty Search waits for / or Enter to write JQL; Esc exits editing. Use / to edit JQL, Enter to focus a preview, Ctrl+P for commands, comma for settings, and ? for help. " +
			"Settings edits public config values with validation; Ctrl+S saves and hot-reloads the selected profile, and Ctrl+R reloads without saving. Display preferences preview immediately. A failed reload retains the old connection and blocks Jira actions until retry succeeds. Tokens and aliases retain their dedicated auth and alias workflows. " +
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
			parent := ctx
			ctx, cancel := context.WithCancel(parent)
			options.Actions = dashboardActions(ctx, client, options.Actions.ReadOnly)
			options.Project, options.ProjectQuery, options.Query = project, projectQuery, strings.TrimSpace(query)
			options.StopRuntime = cancel
			options.RuntimeScope = scopeOf(client)
			options.ReloadSettings = runtimeReloader(parent, deps)
			return dashboard.WithRuntime(dashboard.New(ctx, client, options))
		},
	})
}
