package adf_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFromMarkdownAlertsAuthorNativePanels(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind string
	}{
		{name: "NOTE", kind: "info"},
		{name: "TIP", kind: "tip"},
		{name: "IMPORTANT", kind: "note"},
		{name: "WARNING", kind: "warning"},
		{name: "CAUTION", kind: "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, warnings := mdConvert(t, "> [!"+tc.name+"]\n> **Keep** [docs][source].\n>\n> Second paragraph.\n\n[source]: https://example.com \"Reference title\"\n")
			mdAssertNoWarnings(t, warnings)
			panel := mdBlock(t, doc, 0)
			var attrs struct {
				Kind string `json:"panelType"`
			}
			if panel.Type() != "panel" || json.Unmarshal(panel.Attrs(), &attrs) != nil || attrs.Kind != tc.kind {
				t.Fatalf("alert did not become the expected native panel: %s %s", panel.Type(), panel.Attrs())
			}
			if len(panel.Content()) != 2 || mdChild(t, panel, 0, 0).Text() != "Keep" || !mdMarkTypes(mdChild(t, panel, 0, 0))["strong"] {
				t.Fatal("alert marker was retained or body formatting changed")
			}
			link := mdChild(t, panel, 0, 2)
			if !mdMarkTypes(link)["link"] || !strings.Contains(string(link.Marks()[0].Attrs()), "Reference title") {
				t.Fatal("document-scoped reference or title was lost")
			}
			mdValidate(t, doc)
		})
	}
}

func TestFromMarkdownAlertPreservesAllowedBlockShapes(t *testing.T) {
	doc, warnings := mdConvert(t, "> [!WARNING]\n>\n> # Heading\n>\n> - **item**\n>\n> ```go\n> fmt.Println(\"[!NOTE]\")\n> ```\n>\n> ---\n>\n> - [x] Checked\n")
	mdAssertNoWarnings(t, warnings)
	panel := mdBlock(t, doc, 0)
	if panel.Type() != "panel" {
		t.Fatal("rich alert remained a plain quote")
	}
	want := []string{"heading", "bulletList", "codeBlock", "rule", "taskList"}
	if len(panel.Content()) != len(want) {
		t.Fatalf("alert has %d blocks, want %d", len(panel.Content()), len(want))
	}
	for i, kind := range want {
		if panel.Content()[i].Type() != kind {
			t.Fatalf("alert block %d = %s, want %s", i, panel.Content()[i].Type(), kind)
		}
	}
	mdValidate(t, doc)
}

func TestFromMarkdownAlertMarkersRequireSourceContext(t *testing.T) {
	for name, markdown := range map[string]string{
		"unknown":          "> [!OTHER]\n> body",
		"lowercase":        "> [!warning]\n> body",
		"same line":        "> [!WARNING] body",
		"escaped":          "> \\[!WARNING]\n> body",
		"code span":        "> `[!WARNING]`\n> body",
		"strong":           "> **[!WARNING]**\n> body",
		"link":             "> [!WARNING](https://example.com)\n> body",
		"shortcut link":    "> [!WARNING]\n> body\n\n[!WARNING]: https://example.com",
		"second line":      "> introduction\n> [!WARNING]\n> body",
		"nested quote":     "> outer\n>\n> > [!WARNING]\n> > body",
		"nested list":      "- item\n\n  > [!WARNING]\n  > body",
		"nested task item": "- [x] item\n\n  > [!WARNING]\n  > body",
		"fenced code":      "```markdown\n> [!WARNING]\n> body\n```",
		"indented code":    "    > [!WARNING]\n    > body",
	} {
		t.Run(name, func(t *testing.T) {
			doc, warnings := mdConvert(t, markdown)
			for _, warning := range warnings {
				if warning.Lossy {
					t.Fatal("literal alert spelling caused content loss")
				}
			}
			data, err := doc.Marshal()
			if err != nil || strings.Contains(string(data), `"type":"panel"`) || !strings.Contains(string(data), "WARNING") && name != "unknown" && name != "lowercase" {
				t.Fatalf("literal marker was consumed or became a panel: %s, %v", data, err)
			}
			mdValidate(t, doc)
		})
	}
}

func TestFromMarkdownEmptyAlertsRemainSchemaValid(t *testing.T) {
	for _, markdown := range []string{"> [!NOTE]", "> [!NOTE]\n>\n", "> [!NOTE]\r\n>\r\n"} {
		t.Run(markdown, func(t *testing.T) {
			doc, warnings := mdConvert(t, markdown)
			mdAssertNoWarnings(t, warnings)
			panel := mdBlock(t, doc, 0)
			if panel.Type() != "panel" || len(panel.Content()) != 1 || panel.Content()[0].Type() != "paragraph" || len(panel.Content()[0].Content()) != 0 {
				t.Fatal("empty alert did not keep its native panel with an empty paragraph")
			}
			mdValidate(t, doc)
		})
	}
}

func TestFromMarkdownAlertReportsForbiddenNestingAndDrops(t *testing.T) {
	for _, tc := range []struct {
		name     string
		markdown string
		lossy    bool
	}{
		{name: "table", markdown: "> [!WARNING]\n> body\n>\n> | A | B |\n> | - | - |\n> | x | y |\n"},
		{name: "quote", markdown: "> [!WARNING]\n> body\n>\n> > quoted\n"},
		{name: "HTML", markdown: "> [!WARNING]\n> body\n>\n> <script>dropped</script>\n", lossy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, warnings := mdConvert(t, tc.markdown)
			if len(warnings) != 1 || warnings[0].Lossy != tc.lossy || warnings[0].Path == "" {
				t.Fatalf("unsupported alert body lacked its source-mapped diagnostic: %+v", warnings)
			}
			if mdBlock(t, doc, 0).Type() != "panel" {
				t.Fatal("alert body lost its panel")
			}
			if tc.name == "table" && (len(doc.Content()) != 2 || doc.Content()[1].Type() != "table") {
				t.Fatal("table content did not survive outside the panel")
			}
			if tc.name == "table" && (mdChild(t, mdBlock(t, doc, 1), 1, 0, 0, 0).Text() != "x" || mdChild(t, mdBlock(t, doc, 1), 1, 1, 0, 0).Text() != "y") {
				t.Fatal("hoisted table changed its cell text")
			}
			if tc.name == "quote" && (len(doc.Content()[0].Content()) != 2 || mdChild(t, mdBlock(t, doc, 0), 1, 0).Text() != "quoted") {
				t.Fatal("flattened quote lost its text")
			}
			mdValidate(t, doc)
		})
	}
}

func TestFromMarkdownAlertPreservesFirstBodyAutolink(t *testing.T) {
	doc, warnings := mdConvert(t, "> [!NOTE]\n> <https://example.com>\n")
	mdAssertNoWarnings(t, warnings)
	panel := mdBlock(t, doc, 0)
	link := mdChild(t, panel, 0, 0)
	if panel.Type() != "panel" || link.Text() != "https://example.com" || !mdMarkTypes(link)["link"] {
		t.Fatal("alert marker removal swallowed the first body autolink")
	}
	mdValidate(t, doc)
}

func TestFromMarkdownAlertPreservesFirstBodyHTMLDiagnostic(t *testing.T) {
	doc, warnings := mdConvert(t, "> [!NOTE]\n> <span>kept\n")
	if len(warnings) != 1 || !warnings[0].Lossy || !strings.Contains(warnings[0].Path, "line 2") {
		t.Fatalf("alert marker removal swallowed the first body HTML diagnostic: %+v", warnings)
	}
	if mdChild(t, mdBlock(t, doc, 0), 0, 0).Text() != "kept" {
		t.Fatal("alert diagnostic dropped supported body text")
	}
	mdValidate(t, doc)
}
