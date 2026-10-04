//go:build outputaudit

package audit

func init() {
	const native = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"review content"}]}]}`
	scenarios = append(scenarios,
		Scenario{ID: "adf-convert-success", Op: "adf.convert", Category: CategorySuccess, Args: []string{"adf", "convert"}, Stdin: "**review content**"},
		Scenario{ID: "adf-convert-failure", Op: "adf.convert", Category: CategoryFailure, Args: []string{"adf", "convert"}, Stdin: "<script>lost</script>", ExpectExit: 3, ExpectCode: "adf_content_loss"},
		Scenario{ID: "adf-convert-help", Op: "adf.convert", Category: CategoryHelp, Args: []string{"adf", "convert", "--help"}},
		Scenario{ID: "adf-render-success", Op: "adf.render", Category: CategorySuccess, Args: []string{"adf", "render"}, Stdin: native},
		Scenario{ID: "adf-render-failure", Op: "adf.render", Category: CategoryFailure, Args: []string{"adf", "render"}, Stdin: `{"body":{}}`, ExpectExit: 3, ExpectCode: "flag_value_invalid"},
		Scenario{ID: "adf-render-help", Op: "adf.render", Category: CategoryHelp, Args: []string{"adf", "render", "--help"}},
		Scenario{ID: "adf-validate-success", Op: "adf.validate", Category: CategorySuccess, Args: []string{"adf", "validate"}, Stdin: native},
		Scenario{ID: "adf-validate-failure", Op: "adf.validate", Category: CategoryFailure, Args: []string{"adf", "validate"}, Stdin: `{"type":"doc","version":1}`, ExpectExit: 3, ExpectCode: "adf_invalid"},
		Scenario{ID: "adf-validate-help", Op: "adf.validate", Category: CategoryHelp, Args: []string{"adf", "validate", "--help"}},
		Scenario{ID: "adf-lint-success", Op: "adf.lint", Category: CategorySuccess, Args: []string{"adf", "lint"}, Stdin: native},
		Scenario{ID: "adf-lint-failure", Op: "adf.lint", Category: CategoryFailure, Args: []string{"adf", "lint"}, Stdin: `{"type":"doc","version":1,"content":[{"type":"futureWidget"}]}`, ExpectExit: 3, ExpectCode: "adf_invalid"},
		Scenario{ID: "adf-lint-help", Op: "adf.lint", Category: CategoryHelp, Args: []string{"adf", "lint", "--help"}},
		Scenario{ID: "adf-explain-success", Op: "adf.explain-node", Category: CategorySuccess, Args: []string{"adf", "explain-node", "panel"}},
		Scenario{ID: "adf-explain-failure", Op: "adf.explain-node", Category: CategoryFailure, Args: []string{"adf", "explain-node", "futureWidget"}, ExpectExit: 3, ExpectCode: "adf_type_unknown"},
		Scenario{ID: "adf-explain-help", Op: "adf.explain-node", Category: CategoryHelp, Args: []string{"adf", "explain-node", "--help"}},
	)
	for _, op := range []string{"adf.validate", "adf.lint", "adf.explain-node"} {
		stylingExpected[op] = true
	}
	successDataShape["adf.validate"] = struct{ required, optional []string }{required: []string{"schema_sha256", "valid", "diagnostics"}}
	successDataShape["adf.lint"] = struct{ required, optional []string }{required: []string{"schema_sha256", "valid", "diagnostics", "projection_available", "lossy", "lossy_constructs"}, optional: []string{"projection_diagnostic"}}
	successDataShape["adf.explain-node"] = struct{ required, optional []string }{required: []string{"schema_sha256", "entries"}}
	stylingExpected["adf.convert"] = true
	stylingExpected["adf.render"] = true
	successDataShape["adf.convert"] = struct{ required, optional []string }{required: []string{"document", "lossy", "diagnostics"}}
	successDataShape["adf.render"] = struct{ required, optional []string }{required: []string{"markdown", "lossy", "lossy_constructs"}}
	passthroughBoundary["adf.convert"] = []string{"document"}
	passthroughFidelity["adf.convert"] = struct {
		path  []string
		value string
	}{[]string{"document", "content", "0", "content", "0", "text"}, "review content"}
}
