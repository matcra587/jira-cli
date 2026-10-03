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
	)
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
