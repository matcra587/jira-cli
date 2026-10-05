//go:build outputaudit

package audit

func init() {
	scenarios = append(scenarios,
		Scenario{ID: "config-init-success", Op: "config.init", Category: CategorySuccess, Args: []string{"config", "init", "--base-url=https://example.invalid", "--email=person@example.invalid", "--config=new-settings.toml", "--no-input"}},
		Scenario{ID: "config-init-preview", Op: "config.init", Category: CategoryDryRun, Args: []string{"config", "init", "--base-url=https://example.invalid", "--email=person@example.invalid", "--config=new-settings.toml", "--dry-run"}},
		Scenario{ID: "config-init-incomplete", Op: "config.init", Category: CategoryFailure, Args: []string{"config", "init", "--base-url=https://example.invalid", "--config=new-settings.toml", "--no-input"}, ExpectExit: 3, ExpectCode: "validation_failed"},
		Scenario{ID: "config-init-help", Op: "config.init", Category: CategoryHelp, Args: []string{"config", "init", "--help"}},
	)
	stylingExpected["config.init"] = false
	dryRunTense["config.init"] = struct {
		wantProspective string
		forbidCompleted []string
	}{wantProspective: "would create config", forbidCompleted: []string{"created config"}}
	successDataShape["config.init"] = struct{ required, optional []string }{
		required: []string{"dry_run", "path", "base_url", "email", "would_create", "created"},
	}
	dryRunPreview["config.init"] = func(data map[string]any) (bool, string) {
		return checkConfigInitialization(data, true)
	}
}

func checkConfigInitialization(data map[string]any, preview bool) (bool, string) {
	if data["dry_run"] != preview || data["base_url"] != "https://example.invalid" || data["email"] != "person@example.invalid" || data["would_create"] != true || data["created"] != !preview {
		return false, "configuration initialization lost explicit values or actual publication state"
	}
	if path, ok := data["path"].(string); !ok || path == "" {
		return false, "configuration initialization names no selected settings file"
	}
	return true, ""
}
