//go:build outputaudit

package audit

func init() {
	const ownedMise = "mise/installs/github-jira/1.0.0/bin/jira"
	scenarios = append(scenarios,
		Scenario{ID: "update-managed-success", Op: "update", Category: CategorySuccess, Executable: ownedMise, Args: []string{"update"}},
		Scenario{ID: "update-managed-preview", Op: "update", Category: CategoryDryRun, Executable: ownedMise, Args: []string{"up", "--dry-run"}},
		Scenario{ID: "update-unknown-failure", Op: "update", Category: CategoryFailure, Args: []string{"update"}, ExpectExit: 3, ExpectCode: "update_unsupported"},
		Scenario{ID: "update-help", Op: "update", Category: CategoryHelp, Args: []string{"update", "--help"}},
	)
	stylingExpected["update"] = false
	dryRunTense["update"] = struct {
		wantProspective string
		forbidCompleted []string
	}{wantProspective: "Preview only;", forbidCompleted: []string{"Executable update observed.", "Update attempted;"}}
	successDataShape["update"] = struct{ required, optional []string }{
		required: []string{"channel", "current", "managed", "attempted", "updated", "uncertain", "dry_run", "hint"},
		optional: []string{"latest", "update_available", "publication", "brew"},
	}
	dryRunPreview["update"] = func(data map[string]any) (bool, string) {
		_, available := data["update_available"]
		return data["channel"] == "mise" && data["managed"] == true && data["attempted"] == false && data["updated"] == false && !available,
			"managed update preview lost the installation owner, fabricated release availability or claimed installation"
	}
}
