//go:build outputaudit

package audit

func init() {
	const config = "base_url='https://example.com'\nemail='person@example.com'\nkeyring=true\n"
	files := map[string]string{"settings.toml": config}
	scenarios = append(scenarios,
		Scenario{ID: "cache-clear-success", Op: "cache.clear", Category: CategorySuccess, Args: []string{"cache", "clear", "--force", "--config=settings.toml", "--no-input"}, Files: files},
		Scenario{ID: "cache-clear-preview", Op: "cache.clear", Category: CategoryDryRun, Args: []string{"cache", "clear", "--dry-run", "--config=settings.toml", "--no-input"}, Files: files},
		Scenario{ID: "cache-clear-refusal", Op: "cache.clear", Category: CategoryFailure, Args: []string{"cache", "clear", "--config=settings.toml", "--no-input"}, Files: files, ExpectExit: 3, ExpectCode: "flag_required"},
		Scenario{ID: "cache-clear-help", Op: "cache.clear", Category: CategoryHelp, Args: []string{"cache", "clear", "--help"}},
	)
	stylingExpected["cache.clear"] = false
	successDataShape["cache.clear"] = struct{ required, optional []string }{required: []string{"dry_run", "resource", "would_remove", "removed", "unclassified", "entries"}}
	dryRunTense["cache.clear"] = struct {
		wantProspective string
		forbidCompleted []string
	}{wantProspective: "would remove", forbidCompleted: []string{"removed  "}}
	dryRunPreview["cache.clear"] = func(data map[string]any) (bool, string) {
		entries, ok := data["entries"].([]any)
		return ok && len(entries) == 0 && data["dry_run"] == true && data["resource"] == "all" && data["would_remove"] == float64(0) && data["removed"] == float64(0), "empty selected namespace preview fabricated a deletion or omitted the plan"
	}
}
