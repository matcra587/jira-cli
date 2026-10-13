//go:build outputaudit

package audit

import "slices"

func init() {
	scenarios = append(scenarios,
		Scenario{ID: "fieldtypes-success", Op: "agent.fieldtypes", Category: CategorySuccess, Args: []string{"agent", "fieldtypes"}},
		Scenario{ID: "fieldtypes-failure", Op: "agent.fieldtypes", Category: CategoryFailure, Args: []string{"agent", "fieldtypes", "unexpected"}, ExpectExit: 3, ExpectCode: "arg_count_invalid"},
		Scenario{ID: "fieldtypes-help", Op: "agent.fieldtypes", Category: CategoryHelp, Args: []string{"agent", "fieldtypes", "--help"}},
		Scenario{ID: "adf-matrix-success", Op: "agent.adf-matrix", Category: CategorySuccess, Args: []string{"agent", "adf-matrix"}},
		Scenario{ID: "adf-matrix-failure", Op: "agent.adf-matrix", Category: CategoryFailure, Args: []string{"agent", "adf-matrix", "unexpected"}, ExpectExit: 3, ExpectCode: "arg_count_invalid"},
		Scenario{ID: "adf-matrix-help", Op: "agent.adf-matrix", Category: CategoryHelp, Args: []string{"agent", "adf-matrix", "--help"}},
		Scenario{ID: "agent-export-success", Op: "agent.export", Category: CategorySuccess, Args: []string{"agent", "export", "--format=agent-skill", "--dir=skills"}},
		Scenario{ID: "agent-export-preview", Op: "agent.export", Category: CategoryDryRun, Args: []string{"agent", "export", "--format=agent-skill", "--dir=skills", "--dry-run"}},
		Scenario{ID: "agent-export-failure", Op: "agent.export", Category: CategoryFailure, Args: []string{"agent", "export", "--format=missing", "--dir=skills"}, ExpectExit: 3, ExpectCode: "flag_value_invalid"},
		Scenario{ID: "agent-export-help", Op: "agent.export", Category: CategoryHelp, Args: []string{"agent", "export", "--help"}},
		Scenario{ID: "agent-schema-success", Op: "agent.schema", Category: CategorySuccess, Args: []string{"agent", "schema", "--path=api"}},
		Scenario{ID: "agent-schema-failure", Op: "agent.schema", Category: CategoryFailure, Args: []string{"agent", "schema", "--path=missing"}, ExpectExit: 3, ExpectCode: "flag_value_invalid"},
		Scenario{ID: "agent-schema-help", Op: "agent.schema", Category: CategoryHelp, Args: []string{"agent", "schema", "--help"}},
	)
	stylingExpected["agent.adf-matrix"] = true
	stylingExpected["agent.fieldtypes"] = true
	successDataShape["agent.fieldtypes"] = struct{ required, optional []string }{required: []string{"types", "handling", "metadata", "official_url"}}
	passthroughBoundary["agent.fieldtypes"] = []string{"types.example"}
	passthroughFidelity["agent.fieldtypes"] = struct {
		path  []string
		value string
	}{[]string{"types", "9", "example", "0", "accountId"}, "example-account-id"}
	successDataShape["agent.adf-matrix"] = struct{ required, optional []string }{required: []string{"schema_sha256", "official_url", "entries", "unknown"}}
	stylingExpected["agent.export"] = false
	successDataShape["agent.export"] = struct{ required, optional []string }{
		required: []string{"dry_run", "contract_version", "format", "directory", "artifacts", "written_paths", "attempted"},
		optional: []string{"scope", "harness", "attempted_path"},
	}
	dryRunPreview["agent.export"] = func(data map[string]any) (bool, string) {
		artifacts, ok := data["artifacts"].([]any)
		if !ok || len(artifacts) != 10 || data["attempted"] != false || data["format"] != "agent-skill" {
			return false, "guide preview lost the complete unpublished portable corpus"
		}
		for _, artifact := range artifacts {
			row, ok := artifact.(map[string]any)
			if !ok || row["written"] != false || row["path"] == "" || row["sha256"] == "" || row["bytes"] == float64(0) {
				return false, "guide preview lost a prepared artifact's path, bytes or digest"
			}
		}
		return true, ""
	}
	dryRunTense["agent.export"] = struct {
		wantProspective string
		forbidCompleted []string
	}{"would write jira-discover-cli/SKILL.md", []string{"\nwritten jira-"}}
	// JSON Schema's $defs/$ref and property names, plus Docent's clib metadata,
	// are an external document vocabulary. Only this registered artifact is
	// open; the operation's wrapper retains the ordinary naming contract.
	for _, op := range []string{"root", "agent.schema"} {
		passthroughBoundary[op] = []string{"schema"}
		path := "jira api"
		if op == "root" {
			path = "jira"
		}
		passthroughFidelity[op] = struct {
			path  []string
			value string
		}{[]string{"schema", "path"}, path}
		stylingExpected[op] = op == "root" // native help is themed; schema JSON is plain
		successDataShape[op] = struct{ required, optional []string }{required: []string{"schema"}}
	}
	for _, route := range []struct {
		op   string
		args []string
	}{{"agent.guide", []string{"agent", "guide"}}, {"guide", []string{"guide"}}} {
		scenarios = append(scenarios,
			Scenario{ID: route.op + "-success", Op: route.op, Category: CategorySuccess, Args: slices.Concat(route.args, []string{"raw-api"})},
			Scenario{ID: route.op + "-failure", Op: route.op, Category: CategoryFailure, Args: slices.Concat(route.args, []string{"missing"}), ExpectExit: 3, ExpectCode: "arg_value_invalid"},
			Scenario{ID: route.op + "-help", Op: route.op, Category: CategoryHelp, Args: slices.Concat(route.args, []string{"--help"})},
		)
		stylingExpected[route.op] = false // guides print plain Markdown
		successDataShape[route.op] = struct{ required, optional []string }{
			required: []string{"contract_version", "kind", "guides", "document", "slug"}, optional: []string{"section"},
		}
	}
}
