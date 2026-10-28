package tui

import (
	"context"
	"path/filepath"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/tui/dashboard"
)

func scopeOf(client core.JiraClient) core.JiraScope {
	if scoped, ok := client.(core.JiraScopedClient); ok {
		return scoped.JiraScope()
	}
	return core.JiraScope{Site: client.BaseURL()}
}

func runtimeReloader(parent context.Context, deps Deps) func() (*dashboard.Model, error) {
	if deps.Reload == nil {
		return nil
	}
	return func() (*dashboard.Model, error) {
		ctx, cancel := context.WithCancel(parent)
		retained := false
		defer func() {
			if !retained {
				cancel()
			}
		}()
		resolved, client, err := deps.Reload(ctx)
		if err != nil {
			return nil, err
		}
		options, err := dashboardOptions(ctx, resolved)
		if err != nil {
			return nil, err
		}
		palettePath := resolved.Config.ThemePath
		if palettePath != "" && !filepath.IsAbs(palettePath) {
			palettePath = filepath.Join(filepath.Dir(resolved.Path), palettePath)
		}
		// Startup environment themes retain their ownership after library cleanup.
		applyTheme := func() {}
		if cli.ThemeEnvironmentName() == "" {
			applyTheme, err = cli.PrepareRuntimeTheme(ctx, options.Theme, palettePath)
			if err != nil {
				return nil, err
			}
		}
		options.Project = resolved.Config.DefaultProject
		if options.Project != "" {
			options.ProjectQuery, err = command.ComposeJQL(command.JQLInput{Project: []string{options.Project}, Status: []string{"<Done"}, OrderBy: "updated"})
			if err != nil {
				return nil, err
			}
		}
		options.Actions = dashboardActions(ctx, client, resolved.Config.ReadOnly)
		options.RuntimeScope, options.StopRuntime = scopeOf(client), cancel
		options.ReloadSettings = runtimeReloader(parent, deps)
		options.ApplyRuntime = func() {
			applyTheme()
			cli.SetGlyphs(&resolved.Config.Glyphs)
		}
		retained = true
		return dashboard.New(ctx, client, options), nil
	}
}
