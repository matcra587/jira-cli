//go:build outputaudit

package audit

func init() {
	const original = "theme='monokai'\n"
	scenarios = append(scenarios,
		Scenario{ID: "config-theme-preset-success", Op: "config.theme", Category: CategorySuccess, Args: []string{"config", "theme", "--name=dracula", "--config=settings.toml", "--no-input"}, Files: map[string]string{"settings.toml": original}},
		Scenario{ID: "config-theme-preset-preview", Op: "config.theme", Category: CategoryDryRun, Args: []string{"config", "theme", "--name=dracula", "--config=settings.toml", "--dry-run", "--no-input"}, Files: map[string]string{"settings.toml": original}},
		Scenario{ID: "config-theme-invalid-name", Op: "config.theme", Category: CategoryFailure, Args: []string{"config", "theme", "--name=unknown-preset"}, ExpectExit: 3, ExpectCode: "flag_value_invalid"},
		Scenario{ID: "config-theme-help", Op: "config.theme", Category: CategoryHelp, Args: []string{"config", "theme", "--help"}},
	)
	stylingExpected["config.theme"] = false
	dryRunTense["config.theme"] = struct {
		wantProspective string
		forbidCompleted []string
	}{wantProspective: "would select theme", forbidCompleted: []string{"stored theme"}}
	successDataShape["config.theme"] = struct{ required, optional []string }{
		required: []string{"dry_run", "path", "file_exists", "name", "name_present", "theme_path", "path_present", "would_change", "changed", "created"},
	}
	dryRunPreview["config.theme"] = func(data map[string]any) (bool, string) {
		return checkThemeSelection(data, true)
	}
}

func checkThemeSelection(data map[string]any, preview bool) (bool, string) {
	if data["dry_run"] != preview || data["name"] != "dracula" || data["name_present"] != true || data["theme_path"] != "" || data["path_present"] != true || data["would_change"] != true || data["changed"] != !preview || data["created"] != false || data["file_exists"] != true {
		return false, "theme selection lost stored name/path declarations or actual publication state"
	}
	if path, ok := data["path"].(string); !ok || path == "" {
		return false, "theme selection names no selected settings file"
	}
	return true, ""
}
