package tui

import (
	"context"
	"errors"
	"maps"
	"os"
	"slices"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/tui/dashboard"
)

// dashboardOptions projects only public display values out of ordinary config.
// The captured file snapshot owns conflicts; a successful save advances it.
func dashboardOptions(ctx context.Context, resolved *config.Resolved) (dashboard.Options, error) {
	file, err := config.OpenFile(ctx, resolved.Path)
	if err != nil {
		return dashboard.Options{}, err
	}
	options := dashboard.Options{Settings: make(map[string]bool), SettingSources: make(map[string]string)}
	// Binding occurs once before Tea starts any commands. The original snapshot
	// stays fixed while publication and query I/O inherit the screen lifetime.
	options.BindContext = func(session context.Context) { ctx = session }
	c := resolved.Config
	options.Theme = c.Theme
	if name := cli.ThemeEnvironmentName(); name != "" {
		options.Theme = name
		options.SettingSources["theme"] = "env"
	}
	options.SettingSources["theme"] = string(resolved.Source("theme"))
	if cli.ThemeEnvironmentName() != "" || resolved.Source("theme_path") == config.LayerEnv {
		options.SettingSources["theme"] = "env"
	}
	options.Settings = map[string]bool{
		"tui_compact": c.TUICompact, "tui_updated": c.TUIUpdated,
		"tui_preview": c.TUIPreview, "tui_status": c.TUIStatus, "tui_assignee": c.TUIAssignee,
		"tui_labels": c.TUILabels, "tui_priority": c.TUIPriority, "tui_issue_type": c.TUIIssueType,
	}
	for key := range options.Settings {
		options.SettingSources[key] = string(resolved.Source(key))
	}
	options.SaveSettings = func(values map[string]string) error {
		update, err := file.PrepareSettings(values)
		if err != nil {
			return err
		}
		if _, err := update.Save(ctx); err != nil {
			return err
		}
		next, err := config.OpenFile(ctx, resolved.Path)
		if err != nil {
			return err
		}
		file = next
		return nil
	}
	options.LoadViews = func() ([]dashboard.SavedView, error) {
		queries, err := config.LoadQueries(ctx, c.QueriesPath)
		if errors.Is(err, os.ErrNotExist) {
			return []dashboard.SavedView{}, nil
		}
		if err != nil {
			return nil, err
		}
		views := make([]dashboard.SavedView, 0, len(queries))
		for _, name := range slices.Sorted(maps.Keys(queries)) {
			q := queries[name]
			views = append(views, dashboard.SavedView{Name: q.Name, Description: q.Description, Query: q.JQL})
		}
		return views, nil
	}
	options.SaveView = func(name, query string) error {
		_, err := config.SaveQuery(ctx, c.QueriesPath, name, query)
		return err
	}
	return options, nil
}
