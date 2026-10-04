package adf_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

func TestFromMarkdownStatusAfterInlineOwners(t *testing.T) {
	const ready = `{"type":"status","attrs":{"text":"READY","color":"green"}}`
	for _, tc := range []struct{ name, source, preceding string }{
		{name: "link", source: `[open](https://example.test):status[READY]{color=green}`, preceding: `{"type":"text","text":"open","marks":[{"type":"link","attrs":{"href":"https://example.test"}}]}`},
		{name: "strong-link", source: `**[open](https://example.test)**:status[READY]{color=green}`, preceding: `{"type":"text","text":"open","marks":[{"type":"strong"},{"type":"link","attrs":{"href":"https://example.test"}}]}`},
		{name: "link-then-strong", source: `[open](https://example.test)**!**:status[READY]{color=green}`, preceding: `{"type":"text","text":"open","marks":[{"type":"link","attrs":{"href":"https://example.test"}}]},{"type":"text","text":"!","marks":[{"type":"strong"}]}`},
		{name: "code", source: "`https://example.test`:status[READY]{color=green}", preceding: `{"type":"text","text":"https://example.test","marks":[{"type":"code"}]}`},
		{name: "autolink", source: `<https://example.test>:status[READY]{color=green}`, preceding: `{"type":"text","text":"https://example.test","marks":[{"type":"link","attrs":{"href":"https://example.test"}}]}`},
		{name: "status", source: `:status[https://example.test]{color=blue}:status[READY]{color=green}`, preceding: `{"type":"status","attrs":{"text":"https://example.test","color":"blue"}}`},
		{name: "strong", source: `**https://example.test**:status[READY]{color=green}`, preceding: `{"type":"text","text":"https://example.test","marks":[{"type":"strong"}]}`},
		{name: "strikethrough", source: `~~https://example.test~~:status[READY]{color=green}`, preceding: `{"type":"text","text":"https://example.test","marks":[{"type":"strike"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, warnings := mdConvert(t, tc.source)
			mdAssertNoWarnings(t, warnings)
			mdValidate(t, doc)
			raw, err := doc.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			want := []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[` + tc.preceding + `,` + ready + `]}]}`)
			equal, err := adf.StructuralEqual(want, raw)
			if err != nil || !equal {
				t.Fatalf("completed inline node changed following directive: %s, %v", raw, err)
			}
		})
	}
}

func TestFromMarkdownStatusKeepsURLDelimiterLiterals(t *testing.T) {
	for _, source := range []string{
		`https://example.test/*:status[READY]{color=green}`,
		`mailto:person@example.test?subject=*:status[READY]{color=green}`,
		`//example.test/*:status[READY]{color=green}`,
	} {
		t.Run(source, func(t *testing.T) {
			doc, warnings := mdConvert(t, source)
			mdAssertNoWarnings(t, warnings)
			mdValidate(t, doc)
			var literal strings.Builder
			blocks := doc.Content()
			if len(blocks) != 1 || blocks[0].Type() != "paragraph" {
				t.Fatal("literal URL changed its block")
			}
			for _, child := range blocks[0].Content() {
				if child.Type() != "text" || len(child.Marks()) != 0 {
					t.Fatal("literal URL delimiter activated authoring or formatting")
				}
				literal.WriteString(child.Text())
			}
			if literal.String() != source {
				t.Fatalf("literal URL = %q, want %q", literal.String(), source)
			}
		})
	}
}

func TestFromMarkdownStatusKeepsFormattedURLLiterals(t *testing.T) {
	for _, tc := range []struct{ name, source, expected string }{
		{name: "strong-opaque", source: `**mailto:person@example.test?subject=:status[READY]{color=green}**`, expected: `{"type":"text","text":"mailto:person@example.test?subject=:status[READY]{color=green}","marks":[{"type":"strong"}]}`},
		{name: "emphasis-opaque", source: `*mailto:person@example.test?subject=:status[READY]{color=green}*`, expected: `{"type":"text","text":"mailto:person@example.test?subject=:status[READY]{color=green}","marks":[{"type":"em"}]}`},
		{name: "strike-opaque", source: `~~mailto:person@example.test?subject=:status[READY]{color=green}~~`, expected: `{"type":"text","text":"mailto:person@example.test?subject=:status[READY]{color=green}","marks":[{"type":"strike"}]}`},
		{name: "inside-http", source: `https://example.test/*x*:status[READY]{color=green}`, expected: `{"type":"text","text":"https://example.test/"},{"type":"text","text":"x","marks":[{"type":"em"}]},{"type":"text","text":":status[READY]{color=green}"}`},
		{name: "inside-opaque", source: `mailto:person@example.test?subject=*x*:status[READY]{color=green}`, expected: `{"type":"text","text":"mailto:person@example.test?subject="},{"type":"text","text":"x","marks":[{"type":"em"}]},{"type":"text","text":":status[READY]{color=green}"}`},
		{name: "inside-relative", source: `//example.test/*x*:status[READY]{color=green}`, expected: `{"type":"text","text":"//example.test/"},{"type":"text","text":"x","marks":[{"type":"em"}]},{"type":"text","text":":status[READY]{color=green}"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, warnings := mdConvert(t, tc.source)
			mdAssertNoWarnings(t, warnings)
			mdValidate(t, doc)
			// Inline parsing may split adjacent text at link-label punctuation.
			// Compare text and formatting runs without requiring that segmentation.
			blocks := doc.Content()
			if len(blocks) != 1 || blocks[0].Type() != "paragraph" {
				t.Fatal("URL changed its block")
			}
			var runs []adf.Node
			for _, child := range blocks[0].Content() {
				if child.Type() != "text" || len(child.Content()) != 0 {
					t.Fatal("URL activated directive authoring")
				}
				if len(runs) > 0 && reflect.DeepEqual(runs[len(runs)-1].Marks(), child.Marks()) {
					last := &runs[len(runs)-1]
					last.SetText(last.Text() + child.Text())
				} else {
					runs = append(runs, child)
				}
			}
			blocks[0].SetContent(runs)
			doc.SetContent(blocks)
			raw, err := doc.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			want := []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[` + tc.expected + `]}]}`)
			equal, err := adf.StructuralEqual(want, raw)
			if err != nil || !equal {
				t.Fatalf("formatted URL changed: %s, %v", raw, err)
			}
		})
	}
}

func TestFromMarkdownStatusInlineBoundaryBlockOffsets(t *testing.T) {
	const inline = "**https://example.test**:status[READY]{color=green}"
	for _, tc := range []struct{ name, source string }{
		{name: "heading", source: "# " + inline},
		{name: "list", source: "- " + inline},
		{name: "task", source: "- [ ] " + inline},
		{name: "quote", source: "> " + inline},
		{name: "panel", source: "> [!NOTE]\n> " + inline},
		{name: "table", source: "| H |\n| - |\n| " + inline + " |"},
		{name: "second-paragraph", source: "intro\n\n" + inline},
		{name: "second-line", source: "intro\n" + inline},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, warnings := mdConvert(t, tc.source)
			mdAssertNoWarnings(t, warnings)
			mdValidate(t, doc)
			statuses := statusNodes(doc)
			if len(statuses) != 1 {
				t.Fatalf("authored %d status nodes, want one", len(statuses))
			}
			status, err := adf.AsStatus(&statuses[0])
			if err != nil || status.Attrs.Text != "READY" || status.Attrs.Color != "green" {
				t.Fatal("block offsets changed directive", err)
			}
			var found bool
			var walk func([]adf.Node)
			walk = func(nodes []adf.Node) {
				for i, node := range nodes {
					if node.Type() == "status" {
						if i == 0 {
							t.Fatal("preceding formatted URL disappeared")
						}
						before := nodes[i-1]
						if before.Type() != "text" || before.Text() != "https://example.test" || len(before.Marks()) != 1 || before.Marks()[0].Type() != "strong" {
							t.Fatal("preceding URL formatting changed")
						}
						found = true
					}
					walk(node.Content())
				}
			}
			walk(doc.Content())
			if !found {
				t.Fatal("native status not reached")
			}
		})
	}
}

func TestFromMarkdownStatusKeepsURLInUnclosedLinkLabel(t *testing.T) {
	for _, source := range []string{
		`*[https://example.test**:status[READY]{color=green}](https://target.test)*`,
		`*[mailto:person@example.test?subject=**:status[READY]{color=green}](https://target.test)*`,
	} {
		t.Run(source, func(t *testing.T) {
			doc, warnings := mdConvert(t, source)
			mdAssertNoWarnings(t, warnings)
			mdValidate(t, doc)
			if len(statusNodes(doc)) != 0 {
				t.Fatal("link-label URL authored status")
			}
			if !strings.Contains(statusPlainText(doc), ":status[READY]{color=green}") {
				t.Fatal("literal URL text disappeared")
			}
		})
	}
}

func TestFromMarkdownStatusKeepsURIInFailedFutureLink(t *testing.T) {
	for _, prefix := range []string{
		`https://example.test`,
		`mailto:person@example.test?subject=x`,
	} {
		source := "*[" + prefix + "**:status[READY]{color=green}](not a destination)"
		t.Run(source, func(t *testing.T) {
			doc, warnings := mdConvert(t, source)
			mdAssertNoWarnings(t, warnings)
			mdValidate(t, doc)
			// At the directive, the pending link label fences the outside
			// emphasis opener. Its later failure must not activate URI text.
			statusBoundaryAssertParagraph(t, doc, `{"type":"text","text":"[`+prefix+`","marks":[{"type":"em"}]},{"type":"text","text":"*:status[READY]{color=green}](not a destination)"}`)
		})
	}
}

func TestFromMarkdownStatusAfterFormattingInFutureLink(t *testing.T) {
	source := `[**https://example.test**:status[READY]{color=green}](https://target.test)`
	doc, warnings := mdConvert(t, source)
	mdValidate(t, doc)
	if len(warnings) != 1 || !warnings[0].Lossy || warnings[0].NodeType != "status directive" {
		t.Fatal("completed formatting lost inherited-link diagnostic", warnings)
	}
	if len(statusNodes(doc)) != 0 || !strings.Contains(statusPlainText(doc), ":status[READY]{color=green}") {
		t.Fatal("unsupported linked status lost literal text")
	}
}

func TestFromMarkdownStatusPendingReferenceLinkBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, source       string
		statuses, warnings int
	}{
		{name: "valid-uri-label", source: `*[https://example.test**:status[READY]{color=green}][target]*` + "\n\n[target]: https://target.test"},
		{name: "missing-uri-label", source: `*[https://example.test**:status[READY]{color=green}][missing]`},
		{name: "valid-formatted-owner", source: `[**https://example.test**:status[READY]{color=green}][target]` + "\n\n[target]: https://target.test", warnings: 1},
		{name: "missing-formatted-owner", source: `[**https://example.test**:status[READY]{color=green}][missing]`, statuses: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, warnings := mdConvert(t, tc.source)
			mdValidate(t, doc)
			if len(statusNodes(doc)) != tc.statuses || len(warnings) != tc.warnings {
				t.Fatalf("native reference-link boundary produced %d statuses, %d warnings; want %d, %d", len(statusNodes(doc)), len(warnings), tc.statuses, tc.warnings)
			}
			for _, warning := range warnings {
				if !warning.Lossy || warning.NodeType != "status directive" {
					t.Fatal("reference link lost inherited formatting diagnostic")
				}
			}
		})
	}
}

func TestFromMarkdownStatusPendingLinkStopsBeforeFutureFormatting(t *testing.T) {
	source := `[https://example.test?value=:status[READY]{color=green} **after**](https://target.test)`
	doc, warnings := mdConvert(t, source)
	mdAssertNoWarnings(t, warnings)
	mdValidate(t, doc)
	if len(statusNodes(doc)) != 0 {
		t.Fatal("future formatting activated status in preceding URI")
	}
	if statusPlainText(doc) != "https://example.test?value=:status[READY]{color=green} after" {
		t.Fatal("future formatting changed literal URI text")
	}
}

func TestEditStatusBoundaryResolvesExternalReferences(t *testing.T) {
	_, session := prepareEdit(t, editFixture)
	const body = `*[https://example.test**:status[READY]{color=green}][target]*`
	first := strings.Replace(editChunk(t, session, 0), "\nbefore\n", "\n"+body+"\n", 1)
	candidate, err := session.Apply(first + editChunk(t, session, 1) + editChunk(t, session, 2) + "[target]: https://target.test\n")
	if err != nil {
		t.Fatal("external reference lost native pending-link boundary", err)
	}
	if len(statusNodes(candidate)) != 0 {
		t.Fatal("reference outside edited region activated status in URL")
	}
	nodes := candidate.Content()[0].Content()
	var text strings.Builder
	for _, child := range nodes {
		if child.Type() != "text" || len(child.Marks()) != 2 {
			t.Fatal("external reference lost native link/emphasis marks")
		}
		var link, em bool
		for _, mark := range child.Marks() {
			link = link || mark.Type() == "link"
			em = em || mark.Type() == "em"
		}
		if !link || !em {
			t.Fatal("external reference changed mark types")
		}
		text.WriteString(child.Text())
	}
	if text.String() != "https://example.test**:status[READY]{color=green}" {
		t.Fatal("external reference changed literal URI text")
	}
}

func TestFromMarkdownStatusKeepsFutureDelimiterOwnership(t *testing.T) {
	source := `*before **https://example.test**:status[READY]{color=green} after*`
	doc, warnings := mdConvert(t, source)
	mdValidate(t, doc)
	if len(warnings) != 1 || !warnings[0].Lossy || warnings[0].NodeType != "status directive" {
		t.Fatal("future emphasis closer lost inherited-formatting diagnostic", warnings)
	}
	if len(statusNodes(doc)) != 0 || statusPlainText(doc) != "before https://example.test:status[READY]{color=green} after" {
		t.Fatal("future emphasis closer changed literal fallback")
	}
	for _, child := range doc.Content()[0].Content() {
		var em, strong bool
		for _, mark := range child.Marks() {
			em = em || mark.Type() == "em"
			strong = strong || mark.Type() == "strong"
		}
		if !em {
			t.Fatal("live outer delimiter was consumed before its future closer")
		}
		if child.Text() == "https://example.test" && !strong {
			t.Fatal("completed strong span lost its mark")
		}
	}
}

func TestFromMarkdownStatusCompletedDirectiveInPendingLink(t *testing.T) {
	const body = `:status[https://example.test]{color=blue}:status[READY]{color=green}`
	for _, tc := range []struct {
		name, destination  string
		statuses, warnings int
	}{
		{name: "valid-link", destination: "https://target.test", warnings: 2},
		{name: "failed-link", destination: "not a destination", statuses: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, warnings := mdConvert(t, "["+body+"]("+tc.destination+")")
			mdValidate(t, doc)
			statuses := statusNodes(doc)
			if len(statuses) != tc.statuses || len(warnings) != tc.warnings {
				t.Fatalf("completed directive produced %d statuses, %d diagnostics; want %d, %d", len(statuses), len(warnings), tc.statuses, tc.warnings)
			}
			for _, warning := range warnings {
				if !warning.Lossy || warning.NodeType != "status directive" {
					t.Fatal("composed directives lost inherited-link diagnostic")
				}
			}
			if tc.statuses == 2 {
				first, err := adf.AsStatus(&statuses[0])
				if err != nil || first.Attrs.Text != "https://example.test" || first.Attrs.Color != "blue" {
					t.Fatal("first completed status changed", err)
				}
				next, err := adf.AsStatus(&statuses[1])
				if err != nil || next.Attrs.Text != "READY" || next.Attrs.Color != "green" {
					t.Fatal("second status changed", err)
				}
				if statusPlainText(doc) != "[](not a destination)" {
					t.Fatal("failed link changed surrounding literal syntax")
				}
				statusBoundaryAssertParagraph(t, doc, `{"type":"text","text":"["},{"type":"status","attrs":{"text":"https://example.test","color":"blue"}},{"type":"status","attrs":{"text":"READY","color":"green"}},{"type":"text","text":"](not a destination)"}`)
			} else if statusPlainText(doc) != body {
				t.Fatal("linked directives changed literal spelling")
			}
		})
	}
}

func statusBoundaryAssertParagraph(t *testing.T, doc *adf.Document, content string) {
	t.Helper()
	blocks := doc.Content()
	if len(blocks) != 1 || blocks[0].Type() != "paragraph" {
		t.Fatal("inline boundary changed its block")
	}
	// Ordinary inline parsing can split adjacent text at link punctuation.
	// Preserve the complete document while coalescing equal formatting runs.
	var runs []adf.Node
	for _, child := range blocks[0].Content() {
		if child.Type() == "text" && len(runs) > 0 && runs[len(runs)-1].Type() == "text" && reflect.DeepEqual(runs[len(runs)-1].Marks(), child.Marks()) {
			last := &runs[len(runs)-1]
			last.SetText(last.Text() + child.Text())
		} else {
			runs = append(runs, child)
		}
	}
	blocks[0].SetContent(runs)
	doc.SetContent(blocks)
	raw, err := doc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[` + content + `]}]}`)
	equal, err := adf.StructuralEqual(want, raw)
	if err != nil || !equal {
		t.Fatalf("inline boundary changed native document: %s, %v", raw, err)
	}
}

func TestFromMarkdownStatusURIPrefixWithDirectiveLabelReference(t *testing.T) {
	source := `*[https://example.test**:status[READY]{color=green}](https://target.test)*` + "\n\n[READY]: https://nested.test"
	doc, warnings := mdConvert(t, source)
	mdAssertNoWarnings(t, warnings)
	mdValidate(t, doc)
	if len(statusNodes(doc)) != 0 {
		t.Fatal("directive label reference changed URI ownership")
	}
}
