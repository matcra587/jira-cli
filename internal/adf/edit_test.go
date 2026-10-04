package adf_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

const editFixture = `{"version":1,"type":"doc","future":{"id":9007199254740993},"content":[{"type":"paragraph","content":[{"type":"text","text":"before"}]},{"type":"panel","attrs":{"panelType":"warning","localId":"keep"},"future":{"value":"<>&\u2028","id":9007199254740993},"content":[{"type":"paragraph","content":[{"type":"text","text":"rich content"}]}]},{"type":"paragraph","content":[{"type":"text","text":"after"}]}]}`

func prepareEdit(t *testing.T, source string) (*adf.Document, *adf.EditSession) {
	t.Helper()
	doc, err := adf.ParseDocument([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	session, err := adf.PrepareEdit(doc)
	if err != nil {
		t.Fatal(err)
	}
	return doc, session
}

func editBytes(t *testing.T, doc *adf.Document) []byte {
	t.Helper()
	data, err := doc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func editChunk(t *testing.T, session *adf.EditSession, index int) string {
	t.Helper()
	id := session.Regions()[index].ID
	markdown := session.Markdown()
	start := strings.Index(markdown, "<!-- jira-adf:begin "+id)
	endText := "<!-- jira-adf:end " + id + " -->\n\n"
	end := strings.Index(markdown, endText)
	if start < 0 || end < start {
		t.Fatal("region not present in exported Markdown")
	}
	return markdown[start : end+len(endText)]
}

func requireEditError(t *testing.T, session *adf.EditSession, markdown string, kind adf.EditErrorKind) {
	t.Helper()
	doc, err := session.Apply(markdown)
	if doc != nil {
		t.Fatal("failed edit returned a partial candidate")
	}
	var editErr *adf.EditError
	if !errors.As(err, &editErr) || editErr.Kind != kind {
		t.Fatalf("want %s, got %v", kind, err)
	}
}

func TestEditNoopPreservesNativeBytes(t *testing.T) {
	for _, source := range []string{
		editFixture,
		`{"type":"doc","version":1}`,
		`{"type":"doc","version":1,"content":null}`,
		`{"type":"doc","version":1,"content":[]}`,
		`{"type":"doc","version":1,"content":[{"type":"future","content":[{"type":"text","text":"preserve"}],"attrs":{"large":9007199254740993}}]}`,
	} {
		t.Run(fmt.Sprintf("source-%x", len(source)), func(t *testing.T) {
			_, session := prepareEdit(t, source)
			for _, markdown := range []string{session.Markdown(), strings.ReplaceAll(session.Markdown(), "\n", "\r\n")} {
				candidate, err := session.Apply(markdown)
				if err != nil {
					t.Fatal(err)
				}
				if data := editBytes(t, candidate); !bytes.Equal(data, []byte(source)) {
					t.Fatalf("no-op changed bytes: %s", data)
				}
			}
		})
	}
}

func TestEditProtectsAllNonrepresentableState(t *testing.T) {
	cases := map[string]string{
		"panel":         `{"type":"panel","attrs":{"panelType":"warning","localId":"keep"},"content":[{"type":"paragraph","content":[{"type":"text","text":"content"}]}]}`,
		"alignment":     `{"type":"paragraph","attrs":{"align":"center"},"content":[{"type":"text","text":"content"}]}`,
		"empty attrs":   `{"type":"paragraph","attrs":{},"content":[{"type":"text","text":"content"}]}`,
		"empty marks":   `{"type":"paragraph","content":[{"type":"text","text":"content","marks":[]}]}`,
		"local id":      `{"type":"paragraph","attrs":{"localId":"keep"},"content":[{"type":"text","text":"content"}]}`,
		"future field":  `{"type":"paragraph","future":{"id":9007199254740993},"content":[{"type":"text","text":"content"}]}`,
		"consumer mark": `{"type":"paragraph","content":[{"type":"text","text":"content","marks":[{"type":"dataConsumer","attrs":{"sources":["keep"]}}]}]}`,
		"table widths":  `{"type":"table","attrs":{"layout":"default","width":760},"content":[{"type":"tableRow","content":[{"type":"tableCell","attrs":{"colwidth":[120]},"content":[{"type":"paragraph","content":[{"type":"text","text":"content"}]}]}]}]}`,
		"media":         `{"type":"mediaSingle","content":[{"type":"media","attrs":{"id":"keep","type":"file","collection":"files","width":120,"height":80}}]}`,
		"extension":     `{"type":"extension","attrs":{"extensionType":"future","extensionKey":"keep","parameters":{"id":9007199254740993}}}`,
		"future node":   `{"type":"future","attrs":{"id":"keep"},"content":[{"type":"text","text":"content"}]}`,
	}
	for name, node := range cases {
		t.Run(name, func(t *testing.T) {
			_, session := prepareEdit(t, `{"type":"doc","version":1,"content":[`+node+`]}`)
			if regions := session.Regions(); len(regions) != 1 || !regions[0].Protected {
				t.Fatalf("nonrepresentable state was editable: %+v", regions)
			}
			requireEditError(t, session, "", adf.EditProtectedRegion)
			chunk := editChunk(t, session, 0)
			openEnd := strings.Index(chunk, "-->\n") + len("-->\n")
			changed := chunk[:openEnd] + "\nreplacement\n\n" + chunk[strings.Index(chunk, "<!-- jira-adf:end"):]
			requireEditError(t, session, changed, adf.EditProtectedRegion)
			candidate, err := session.Apply("new paragraph\n\n" + chunk)
			if err != nil {
				t.Fatal(err)
			}
			data, err := candidate.Content()[1].MarshalJSON()
			if err != nil || !bytes.Equal(data, []byte(node)) {
				t.Fatalf("opaque bytes changed: %s, %v", data, err)
			}
		})
	}
}

func TestEditStructuralChangesRetainOpaqueBytes(t *testing.T) {
	source, session := prepareEdit(t, editFixture)
	regions := session.Regions()
	if regions[0].Protected || !regions[1].Protected || regions[2].Protected {
		t.Fatalf("incorrect region policies: %+v", regions)
	}
	// Insert, reorder, delete an editable block, and replace another with two
	// structurally different blocks. The panel and root metadata must survive.
	markdown := "intro\n\n" + editChunk(t, session, 1) + strings.Replace(editChunk(t, session, 2), "\nafter\n", "\n# heading\n\nnew paragraph\n", 1)
	candidate, err := session.Apply(markdown)
	if err != nil {
		t.Fatal(err)
	}
	content := candidate.Content()
	if len(content) != 4 || content[0].Content()[0].Text() != "intro" || content[1].Type() != "panel" || content[2].Type() != "heading" || content[3].Content()[0].Text() != "new paragraph" {
		t.Fatalf("wrong structural result: %s", editBytes(t, candidate))
	}
	originalPanel, err := source.Content()[1].MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	actualPanel, err := content[1].MarshalJSON()
	if err != nil || !bytes.Equal(actualPanel, originalPanel) {
		t.Fatalf("panel bytes changed: %s, %v", actualPanel, err)
	}
	future, ok := candidate.Field("future")
	if !ok || string(future) != `{"id":9007199254740993}` {
		t.Fatalf("root metadata lost: %s", future)
	}
	if string(editBytes(t, source)) != editFixture {
		t.Fatal("applying an edit mutated source document")
	}
}

func TestEditRejectsInvalidAnchors(t *testing.T) {
	_, session := prepareEdit(t, editFixture)
	first := editChunk(t, session, 0)
	panel := editChunk(t, session, 1)
	id := session.Regions()[1].ID
	for name, markdown := range map[string]string{
		"duplicate editable": first + first + panel,
		"duplicate opaque":   panel + panel,
		"foreign id":         strings.ReplaceAll(panel, id, "foreign"),
		"missing close":      panel[:strings.Index(panel, "<!-- jira-adf:end")],
		"unexpected close":   panel[strings.Index(panel, "<!-- jira-adf:end"):],
		"changed protection": strings.Replace(panel, " protected -->", " editable -->", 1),
		"malformed":          "<!-- jira-adf:banana -->\n\n" + panel,
		"multiline":          strings.Replace(panel, "<!-- jira-adf:begin ", "<!-- jira-adf:\nbegin ", 1),
		"nested open":        first[:strings.Index(first, "<!-- jira-adf:end")] + panel,
		"mismatched close":   strings.Replace(panel, "jira-adf:end "+id, "jira-adf:end foreign", 1),
	} {
		t.Run(name, func(t *testing.T) { requireEditError(t, session, markdown, adf.EditInvalidAnchor) })
	}
}

func TestEditAnchorsInsideCodeAreLiteral(t *testing.T) {
	_, session := prepareEdit(t, editFixture)
	panel := editChunk(t, session, 1)
	for name, code := range map[string]string{
		"fenced":   "```text\n<!-- jira-adf:begin fake protected -->\n<!-- jira-adf:end fake -->\n```\n\n",
		"indented": "    <!-- jira-adf:begin fake protected -->\n    <!-- jira-adf:end fake -->\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			candidate, err := session.Apply(code + panel)
			if err != nil {
				t.Fatal(err)
			}
			if content := candidate.Content(); len(content) != 2 || content[0].Type() != "codeBlock" || !strings.Contains(content[0].Content()[0].Text(), "jira-adf:begin fake") || content[1].Type() != "panel" {
				t.Fatalf("code was consumed as anchors: %s", editBytes(t, candidate))
			}
		})
	}
}

func TestEditRefusesLossyAuthoredContent(t *testing.T) {
	_, session := prepareEdit(t, editFixture)
	for name, markdown := range map[string]string{
		"html":        "<div>important</div>",
		"inline html": "before <span>important</span> after",
		"wiki":        "h1. wiki heading",
	} {
		t.Run(name, func(t *testing.T) {
			requireEditError(t, session, markdown+"\n\n"+editChunk(t, session, 1), adf.EditLossyContent)
		})
	}
}

func TestEditSessionOwnsSourceAndResults(t *testing.T) {
	source, session := prepareEdit(t, editFixture)
	expected := session.Markdown()
	source.Content()[0].Content()[0].SetText("mutated source")
	metadata := session.Regions()
	metadata[1].Protected = false
	metadata[1].ID = "mutated metadata"
	first, err := session.Apply(expected)
	if err != nil {
		t.Fatal(err)
	}
	first.Content()[0].Content()[0].SetText("mutated result")
	second, err := session.Apply(expected)
	if err != nil {
		t.Fatal(err)
	}
	if string(editBytes(t, second)) != editFixture || session.Markdown() != expected || !session.Regions()[1].Protected {
		t.Fatal("session snapshot or metadata was aliased")
	}
}

func TestEditSourceCRLFTextSurvivesOtherChanges(t *testing.T) {
	source := `{"type":"doc","version":1,"content":[{"type":"codeBlock","attrs":{"language":"text"},"content":[{"type":"text","text":"first\r\nsecond"}]}]}`
	_, session := prepareEdit(t, source)
	if strings.Contains(session.Markdown(), "\r") {
		t.Fatal("exported Markdown does not normalize editor line endings")
	}
	candidate, err := session.Apply("new paragraph\n\n" + session.Markdown())
	if err != nil {
		t.Fatal(err)
	}
	if text := candidate.Content()[1].Content()[0].Text(); text != "first\r\nsecond" {
		t.Fatalf("native text changed through editor normalization: %q", text)
	}
}

func TestEditResolvesReferencesAcrossRegions(t *testing.T) {
	_, session := prepareEdit(t, editFixture)
	first := strings.Replace(editChunk(t, session, 0), "\nbefore\n", "\n[documentation][docs]\n", 1)
	markdown := first + editChunk(t, session, 1) + "[docs]: https://example.test/guide\n"
	candidate, err := session.Apply(markdown)
	if err != nil {
		t.Fatal(err)
	}
	text := candidate.Content()[0].Content()[0]
	if text.Text() != "documentation" || len(text.Marks()) != 1 || text.Marks()[0].Type() != "link" || !bytes.Contains(text.Marks()[0].Attrs(), []byte("https://example.test/guide")) {
		t.Fatalf("cross-region reference lost: %s", editBytes(t, candidate))
	}
}

func TestEditPreservesAuthoredLinkTitles(t *testing.T) {
	_, session := prepareEdit(t, editFixture)
	for name, body := range map[string]string{
		"inline":    `[documentation](https://example.test/guide "important title")`,
		"reference": "[documentation][docs]\n\n[docs]: https://example.test/guide \"important title\"",
	} {
		t.Run(name, func(t *testing.T) {
			markdown := strings.Replace(editChunk(t, session, 0), "\nbefore\n", "\n"+body+"\n", 1) + editChunk(t, session, 1)
			candidate, err := session.Apply(markdown)
			if err != nil {
				t.Fatal(err)
			}
			marks := candidate.Content()[0].Content()[0].Marks()
			if len(marks) != 1 || !bytes.Contains(marks[0].Attrs(), []byte(`"title":"important title"`)) {
				t.Fatalf("authored link title lost: %s", editBytes(t, candidate))
			}
		})
	}
}

func TestEditRejectsAmbiguousSource(t *testing.T) {
	for name, source := range map[string]string{
		"future version":  `{"type":"doc","version":2,"content":[]}`,
		"duplicate root":  `{"type":"doc","version":1,"version":1,"content":[]}`,
		"duplicate attrs": `{"type":"doc","version":1,"content":[{"type":"future","attrs":{"id":1,"id":2}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			doc, err := adf.ParseDocument([]byte(source))
			if err != nil {
				t.Fatal(err)
			}
			session, err := adf.PrepareEdit(doc)
			var editErr *adf.EditError
			if session != nil || !errors.As(err, &editErr) || editErr.Kind != adf.EditInvalidSource {
				t.Fatalf("ambiguous source accepted: %v", err)
			}
		})
	}
	if session, err := adf.PrepareEdit(nil); session != nil || err == nil {
		t.Fatal("nil source accepted")
	}
}

func TestEditDeletesEditableContent(t *testing.T) {
	_, session := prepareEdit(t, `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"delete me"}]}]}`)
	candidate, err := session.Apply("")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidate.Content()) != 0 || candidate.Validate() != nil {
		t.Fatalf("deleting an editable region did not produce an empty document: %s", editBytes(t, candidate))
	}
}

func TestEditReordersIdenticalRegionsWithoutConflatingIdentity(t *testing.T) {
	_, session := prepareEdit(t, `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"same"}]},{"type":"paragraph","content":[{"type":"text","text":"same"}]}]}`)
	regions := session.Regions()
	if regions[0].ID == regions[1].ID {
		t.Fatal("identical subtrees have conflated identities")
	}
	markdown := strings.Replace(editChunk(t, session, 1), "\nsame\n", "\nchanged\n", 1) + editChunk(t, session, 0)
	candidate, err := session.Apply(markdown)
	if err != nil {
		t.Fatal(err)
	}
	if content := candidate.Content(); len(content) != 2 || content[0].Content()[0].Text() != "changed" || content[1].Content()[0].Text() != "same" {
		t.Fatalf("reordered edit targeted wrong subtree: %s", editBytes(t, candidate))
	}
}

func FuzzEditProtectsOpaque(f *testing.F) {
	doc, err := adf.ParseDocument([]byte(editFixture))
	if err != nil {
		f.Fatal(err)
	}
	session, err := adf.PrepareEdit(doc)
	if err != nil {
		f.Fatal(err)
	}
	panel, err := doc.Content()[1].MarshalJSON()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(session.Markdown())
	f.Add("")
	f.Add("<!-- jira-adf:begin fake protected -->\n")
	f.Add("```\n" + session.Markdown() + "```\n")
	f.Fuzz(func(t *testing.T, markdown string) {
		candidate, err := session.Apply(markdown)
		if err != nil {
			if candidate != nil {
				t.Fatal("failed edit exposed a candidate")
			}
			return
		}
		count := 0
		for _, node := range candidate.Content() {
			if node.Type() == "panel" {
				data, err := node.MarshalJSON()
				if err != nil || !bytes.Equal(data, panel) {
					t.Fatalf("protected state changed: %s, %v", data, err)
				}
				count++
			}
		}
		if count != 1 {
			t.Fatalf("successful edit has %d original panels", count)
		}
	})
}
