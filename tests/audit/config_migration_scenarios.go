//go:build outputaudit

package audit

const migrationFixture = "default_profile='work'\n[[profiles]]\nname='work'\nbase_url='https://example.invalid'\nemail='person@example.invalid'\n"

func init() {
	scenarios = append(scenarios,
		Scenario{ID: "config-migrate-success", Op: "config.migrate", Category: CategorySuccess, Args: []string{"config", "migrate", "--source=old.toml", "--config=new.toml", "--recovery-dir=recovery", "--yes", "--no-input"}, Files: map[string]string{"old.toml": migrationFixture}},
		Scenario{ID: "config-migrate-preview", Op: "config.migrate", Category: CategoryDryRun, Args: []string{"config", "migrate", "--source=old.toml", "--config=new.toml", "--dry-run"}, Files: map[string]string{"old.toml": migrationFixture}},
		Scenario{ID: "config-migrate-refusal", Op: "config.migrate", Category: CategoryFailure, Args: []string{"config", "migrate", "--source=old.toml", "--recovery-dir=recovery", "--no-input"}, ExpectExit: 3, ExpectCode: "flag_required"},
		Scenario{ID: "config-migrate-help", Op: "config.migrate", Category: CategoryHelp, Args: []string{"config", "migrate", "--help"}},
		Scenario{ID: "config-rollback-success", Op: "config.rollback", Category: CategorySuccess, Args: []string{"config", "rollback", "--journal=recovery/migration.json", "--yes", "--no-input"}, Files: map[string]string{"old.toml": migrationFixture}, SetupArgs: []string{"config", "migrate", "--source=old.toml", "--config=new.toml", "--recovery-dir=recovery", "--yes", "--no-input"}},
		Scenario{ID: "config-rollback-preview", Op: "config.rollback", Category: CategoryDryRun, Args: []string{"config", "rollback", "--journal=recovery/migration.json", "--dry-run"}, Files: map[string]string{"old.toml": migrationFixture}, SetupArgs: []string{"config", "migrate", "--source=old.toml", "--config=new.toml", "--recovery-dir=recovery", "--yes", "--no-input"}},
		Scenario{ID: "config-rollback-invalid", Op: "config.rollback", Category: CategoryFailure, Args: []string{"config", "rollback", "--journal=invalid.json", "--yes", "--no-input"}, Files: map[string]string{"invalid.json": "{}"}, ExpectExit: 3, ExpectCode: "config_recovery_invalid"},
		Scenario{ID: "config-rollback-help", Op: "config.rollback", Category: CategoryHelp, Args: []string{"config", "rollback", "--help"}},
	)
	stylingExpected["config.migrate"], stylingExpected["config.rollback"] = false, false
	dryRunTense["config.migrate"] = struct {
		wantProspective string
		forbidCompleted []string
	}{"would convert profiles", []string{"converted profiles"}}
	dryRunTense["config.rollback"] = struct {
		wantProspective string
		forbidCompleted []string
	}{"would restore migration preimages", []string{"restored migration preimages"}}
	successDataShape["config.migrate"] = struct{ required, optional []string }{required: []string{"dry_run", "source_path", "path", "profile", "profiles", "backend", "source_credential", "target_credential", "source_sha256", "mapped", "unmapped", "deferred", "ready", "recovery", "recovery_published", "receipt"}}
	successDataShape["config.rollback"] = struct{ required, optional []string }{required: []string{"dry_run", "journal", "would_restore_configuration", "credential_inspection_required", "restored", "receipt"}}
	dryRunPreview["config.migrate"] = func(data map[string]any) (bool, string) {
		return data["profile"] == "work" && data["ready"] == true && data["recovery_published"] == false && data["receipt"] == nil, "preview must retain selected metadata without publishing or inspecting credentials"
	}
	dryRunPreview["config.rollback"] = func(data map[string]any) (bool, string) {
		return data["restored"] == false && data["receipt"] != nil, "rollback preview must retain recovery metadata without restoration"
	}
}
