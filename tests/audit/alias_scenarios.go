//go:build outputaudit

package audit

func init() {
	const config = "keyring=false\n[aliases]\nmine='version'\nUserDefinedName='version'\n"
	files := map[string]string{"aliases.toml": config}
	for _, leaf := range []string{"set", "delete", "import", "list"} {
		op := "alias." + leaf
		args := []string{"alias", leaf, "--config=aliases.toml"}
		stdin := ""
		switch leaf {
		case "set":
			args = append(args, "mine", "version")
		case "delete":
			args = append(args, "mine", "--force")
		case "import":
			args = append(args, "-")
			stdin = "newName: version\n"
		}
		scenarios = append(scenarios,
			Scenario{ID: op + "-success", Op: op, Category: CategorySuccess, Args: args, Stdin: stdin, Files: files},
			Scenario{ID: op + "-help", Op: op, Category: CategoryHelp, Args: []string{"alias", leaf, "--help"}},
		)
		failure := []string{"alias", leaf, "--config=aliases.toml"}
		code := "arg_count_invalid"
		switch leaf {
		case "list":
			failure = append(failure, "unexpected")
		case "delete":
			failure = append(failure, "mine")
			code = "flag_required"
		case "import":
			failure = append(failure, "-")
			code = "alias_invalid"
		}
		badInput := ""
		if leaf == "import" {
			badInput = "newName: null\n"
		}
		scenarios = append(scenarios, Scenario{ID: op + "-failure", Op: op, Category: CategoryFailure, Args: failure, Stdin: badInput, Files: files, ExpectExit: 3, ExpectCode: code})
		if leaf != "list" {
			preview := append([]string(nil), args...)
			preview = append(preview, "--dry-run")
			scenarios = append(scenarios, Scenario{ID: op + "-preview", Op: op, Category: CategoryDryRun, Args: preview, Stdin: stdin, Files: files})
		}
		stylingExpected[op] = false // plain local settings, rendered through shared sinks
	}
	successDataShape["alias.list"] = struct{ required, optional []string }{required: []string{"path", "aliases", "count"}}
	successDataShape["alias.set"] = struct{ required, optional []string }{required: []string{"dry_run", "path", "name", "expansion", "previous", "changed"}}
	successDataShape["alias.delete"] = struct{ required, optional []string }{required: []string{"dry_run", "path", "name", "previous", "deleted", "changed"}}
	successDataShape["alias.import"] = struct{ required, optional []string }{required: []string{"dry_run", "path", "imported", "aliases", "skipped", "changed"}}
	// Literal user-defined map keys are data, including mixed-case names.
	literalMapBoundaries["alias.list"] = []string{"aliases"}
	literalMapBoundaries["alias.import"] = []string{"skipped"}
	dryRunPreview["alias.set"] = func(data map[string]any) (bool, string) {
		return data["name"] == "mine" && data["expansion"] == "version" && data["previous"] == "version" && data["changed"] == false, "alias preview lost authored/current values or claimed a save"
	}
	dryRunPreview["alias.delete"] = func(data map[string]any) (bool, string) {
		return data["name"] == "mine" && data["deleted"] == true && data["previous"] == "version" && data["changed"] == false, "alias delete preview lost existence or claimed a save"
	}
	dryRunPreview["alias.import"] = func(data map[string]any) (bool, string) {
		names, ok := data["aliases"].([]any)
		return ok && len(names) == 1 && names[0] == "newName" && data["imported"] == float64(1) && data["changed"] == false, "alias import preview lost accepted names or claimed a save"
	}
	dryRunTense["alias.set"] = struct {
		wantProspective string
		forbidCompleted []string
	}{"Would set alias mine", []string{"\nSet alias mine"}}
	dryRunTense["alias.delete"] = struct {
		wantProspective string
		forbidCompleted []string
	}{"Would delete alias mine", []string{"\nDeleted alias mine"}}
	dryRunTense["alias.import"] = struct {
		wantProspective string
		forbidCompleted []string
	}{"Would import 1 aliases", []string{"\nImported 1 aliases"}}
}
