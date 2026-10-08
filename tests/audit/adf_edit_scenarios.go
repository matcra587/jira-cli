//go:build outputaudit

package audit

func init() {
	const native = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"review content"}]}]}`
	// The anchor belongs to this exact native fixture, independently pinned
	// rather than obtained from the command whose output the audit checks.
	const edited = "<!-- jira-adf:begin 71169a0fdce57cce845bdcbb65734d7b2e899c822eb75813b78d0edb49f22243-0 editable -->\n\n**review content**\n\n<!-- jira-adf:end 71169a0fdce57cce845bdcbb65734d7b2e899c822eb75813b78d0edb49f22243-0 -->\n\n"
	scenarios = append(scenarios,
		Scenario{ID: "adf-edit-prepare-success", Op: "adf.edit.prepare", Category: CategorySuccess, Args: []string{"adf", "edit", "prepare"}, Stdin: native},
		Scenario{ID: "adf-edit-prepare-failure", Op: "adf.edit.prepare", Category: CategoryFailure, Args: []string{"adf", "edit", "prepare"}, Stdin: `null`, ExpectExit: 3, ExpectCode: "flag_value_invalid"},
		Scenario{ID: "adf-edit-prepare-help", Op: "adf.edit.prepare", Category: CategoryHelp, Args: []string{"adf", "edit", "prepare", "--help"}},
		Scenario{ID: "adf-edit-apply-success", Op: "adf.edit.apply", Category: CategorySuccess, Args: []string{"adf", "edit", "apply", "--input", "original.json", "--markdown-file", "edited.md"}, Files: map[string]string{"original.json": native, "edited.md": edited}},
		Scenario{ID: "adf-edit-apply-failure", Op: "adf.edit.apply", Category: CategoryFailure, Args: []string{"adf", "edit", "apply", "--input", "original.json", "--markdown-file", "edited.md"}, Files: map[string]string{"original.json": native, "edited.md": "<!-- jira-adf:begin unknown editable -->\n\nchanged\n\n<!-- jira-adf:end unknown -->\n"}, ExpectExit: 3, ExpectCode: "flag_value_invalid"},
		Scenario{ID: "adf-edit-apply-help", Op: "adf.edit.apply", Category: CategoryHelp, Args: []string{"adf", "edit", "apply", "--help"}},
		Scenario{ID: "adf-edit-merge-success", Op: "adf.edit.merge", Category: CategorySuccess, Args: []string{"adf", "edit", "merge", "--input", "original.json", "--markdown-file", "edited.md", "--current", "current.json"}, Files: map[string]string{"original.json": native, "edited.md": edited, "current.json": native}},
		Scenario{ID: "adf-edit-merge-failure", Op: "adf.edit.merge", Category: CategoryFailure, Args: []string{"adf", "edit", "merge", "--input", "original.json", "--markdown-file", "edited.md", "--current", "current.json"}, Files: map[string]string{"original.json": native, "edited.md": edited, "current.json": `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"conflicting content"}]}]}`}, ExpectExit: 3, ExpectCode: "flag_value_invalid"},
		Scenario{ID: "adf-edit-merge-help", Op: "adf.edit.merge", Category: CategoryHelp, Args: []string{"adf", "edit", "merge", "--help"}},
	)
	successDataShape["adf.edit.prepare"] = struct{ required, optional []string }{required: []string{"markdown", "regions"}}
	for _, op := range []string{"adf.edit.prepare", "adf.edit.apply", "adf.edit.merge"} {
		stylingExpected[op] = true
	}
	for _, op := range []string{"adf.edit.apply", "adf.edit.merge"} {
		successDataShape[op] = struct{ required, optional []string }{required: []string{"document", "diagnostics"}}
		passthroughBoundary[op] = []string{"document"}
		passthroughFidelity[op] = struct {
			path  []string
			value string
		}{path: []string{"document", "content", "0", "content", "0", "text"}, value: "review content"}
	}
}
