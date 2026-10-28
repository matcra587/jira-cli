package tui

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/tui/dashboard"
)

// dashboardOptions projects public settings out of ordinary config.
// The captured file snapshot owns conflicts; a successful save advances it.
func dashboardOptions(ctx context.Context, resolved *config.Resolved) (dashboard.Options, error) {
	file, err := resolved.OpenSettingsFile(ctx)
	if err != nil {
		return dashboard.Options{}, err
	}
	options := dashboard.Options{SettingSources: make(map[string]string)}
	// Binding occurs once before Tea starts any commands. The original snapshot
	// stays fixed while publication and query I/O inherit the screen lifetime.
	options.BindContext = func(session context.Context) { ctx = session }
	c := resolved.Config
	environmentTheme := cli.ThemeEnvironmentName()
	options.SettingValues = make(map[string]string)
	values := c.Values()
	for _, key := range config.WritableSettingKeys() {
		options.SettingValues[key] = values[key]
		options.SettingSources[key] = string(resolved.Source(key))
	}
	options.Theme = c.Theme
	if c.ThemePath != "" && environmentTheme == "" {
		path := themePathForConfig(resolved.Path, c.ThemePath)
		definition, themeErr := config.LoadThemeFile(ctx, path)
		if themeErr == nil {
			options.ThemeLabel = themeDisplayName(path, definition)
		} else {
			if ctx.Err() != nil {
				return dashboard.Options{}, ctx.Err()
			}
			options.ThemeLabel = "Custom theme unavailable"
			options.ThemeNotice = "Custom theme could not be loaded: " + cli.Text(themeErr.Error())
		}
	}
	if environmentTheme != "" {
		options.Theme = environmentTheme
		options.ThemeLabel = environmentTheme
		options.ThemeNotice = ""
	}
	if environmentTheme != "" || resolved.Source("theme_path") == config.LayerEnv {
		options.SettingSources["theme"] = "env"
	}
	options.Settings = map[string]bool{
		"tui_refresh_on_focus": c.TUIRefreshOnFocus, "tui_change_markers": c.TUIChangeMarkers,
		"tui_auto_page": c.TUIAutoPage,
		"tui_compact":   c.TUICompact, "tui_updated": c.TUIUpdated,
		"tui_preview": c.TUIPreview, "tui_status": c.TUIStatus, "tui_assignee": c.TUIAssignee,
		"tui_labels": c.TUILabels, "tui_priority": c.TUIPriority, "tui_issue_type": c.TUIIssueType,
	}
	var snapshotErr error
	options.SaveSettings = func(values map[string]string) error {
		if snapshotErr != nil {
			return snapshotErr
		}
		update, err := file.PrepareSettings(values)
		if err != nil {
			return err
		}
		if _, err := update.Save(ctx); err != nil {
			return err
		}
		// A successful publication stays successful even if the next snapshot
		// cannot be opened. The next save/reload reports that separate failure.
		next, err := config.OpenFile(ctx, resolved.Path)
		if err != nil {
			snapshotErr = err
			return nil
		}
		file, snapshotErr = next.SelectProfile(resolved.Profile)
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

	themeDir := filepath.Join(filepath.Dir(resolved.Path), "themes")
	options.LoadThemes = func(active string) ([]*config.SavedTheme, error) {
		entries, err := config.LoadSavedThemes(ctx, themeDir)
		if err != nil {
			return nil, err
		}
		if active != "" {
			active = themePathForConfig(resolved.Path, active)
			found := false
			for _, entry := range entries {
				if entry.Path == active {
					entry.Active = true
					found = true
					break
				}
			}
			if !found {
				def, err := config.LoadThemeFile(ctx, active)
				name := themeDisplayName(active, def)
				entries = append(entries, &config.SavedTheme{Name: name, Path: active, Definition: def, Err: err, Active: true})
			}
		}
		return entries, nil
	}
	options.ReadTheme = func(path string) (*config.ThemeFile, error) { return config.LoadThemeFile(ctx, path) }
	options.WriteTheme = func(name string, def *config.ThemeFile, previous *config.SavedTheme) (*config.SavedTheme, bool, error) {
		return config.SaveTheme(ctx, themeDir, name, def, previous)
	}
	options.ExportTheme = func(path string, def *config.ThemeFile) (bool, error) { return config.ExportTheme(ctx, path, def) }
	return options, nil
}

func themePathForConfig(configPath, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(filepath.Dir(configPath), path)
}

func themeDisplayName(path string, definition *config.ThemeFile) string {
	if definition != nil && definition.Name != "" {
		return definition.Name
	}
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}
