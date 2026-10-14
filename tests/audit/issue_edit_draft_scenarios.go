//go:build outputaudit

package audit

func init() {
	env := []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken, "JIRA_READ_ONLY=true"}
	setup := []string{"issue", "edit", "draft", "KEY-1", "--directory=draft"}
	for _, op := range []string{"issue.edit.draft", "issue.edit.inspect", "issue.edit.resume"} {
		leaf := op[len("issue.edit."):]
		failureArgs := []string{"issue", "edit", leaf}
		if leaf == "draft" {
			failureArgs = append(failureArgs, "KEY-1")
		}
		scenarios = append(scenarios, Scenario{ID: op + "-failure", Op: op, Category: CategoryFailure, Args: failureArgs, ExpectExit: 3, ExpectCode: "flag_required"}, Scenario{ID: op + "-help", Op: op, Category: CategoryHelp, Args: []string{"issue", "edit", leaf, "--help"}})
		stylingExpected[op] = true
		passthroughBoundary[op] = []string{"source.description", "description", "current.description"}
		passthroughFidelity[op] = struct {
			path  []string
			value string
		}{path: []string{"source", "description", "futureCamel", "nativeMember"}, value: "native source value"}
	}
	scenarios = append(scenarios,
		Scenario{ID: "issue-draft-success", Op: "issue.edit.draft", Category: CategorySuccess, Args: setup, Stub: "issue-edit-prepare", Env: env},
		Scenario{ID: "issue-draft-preview", Op: "issue.edit.draft", Category: CategoryDryRun, Args: []string{"issue", "edit", "draft", "KEY-1", "--directory=draft", "--dry-run"}},
		Scenario{ID: "issue-inspect-success", Op: "issue.edit.inspect", Category: CategorySuccess, Args: []string{"issue", "edit", "inspect", "--directory=draft"}, SetupArgs: setup, Stub: "issue-edit-prepare", Env: env},
		Scenario{ID: "issue-resume-success", Op: "issue.edit.resume", Category: CategorySuccess, Args: []string{"issue", "edit", "resume", "--directory=draft"}, SetupArgs: setup, Stub: "issue-edit-prepare", Env: env, Terminal: true, Editor: true},
		Scenario{ID: "issue-resume-preview", Op: "issue.edit.resume", Category: CategoryDryRun, Args: []string{"issue", "edit", "resume", "--directory=draft", "--dry-run"}, SetupArgs: setup, Stub: "issue-edit-prepare", Env: env, Editor: true},
		Scenario{ID: "config-edit-terminal-success", Op: "config.edit", Category: CategorySuccess, Args: []string{"config", "edit", "--config=settings.toml"}, Files: map[string]string{"settings.toml": "read_only=true\n"}, Terminal: true, Editor: true},
	)
	for _, op := range []string{"issue.edit.draft", "issue.edit.resume"} {
		successDataShape[op] = struct{ required, optional []string }{required: []string{"dry_run", "draft", "source", "description", "changed"}, optional: []string{"editor", "current", "requested_issue"}}
		dryRunTense[op] = struct {
			wantProspective string
			forbidCompleted []string
		}{"Preview only", []string{"created editing artifacts"}}
		dryRunPreview[op] = func(data map[string]any) (bool, string) {
			draft, ok := data["draft"].(map[string]any)
			return ok && draft["directory_created"] == false && draft["editor_attempted"] == false && data["description"] == nil, "preview must not create a directory, launch an editor or invent a native candidate"
		}
	}
	successDataShape["issue.edit.inspect"] = struct{ required, optional []string }{required: []string{"draft", "source", "description", "changed", "refreshed"}, optional: []string{"current"}}
	successDataShape["config.edit"] = struct{ required, optional []string }{required: []string{"dry_run", "path", "editor", "changed", "created"}}
}
