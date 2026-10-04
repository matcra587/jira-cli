package adf_test

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

func TestPanelProjectionFiveAlertKindsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ kind, marker string }{{kind: "info", marker: "NOTE"}, {kind: "tip", marker: "TIP"}, {kind: "note", marker: "IMPORTANT"}, {kind: "warning", marker: "WARNING"}, {kind: "error", marker: "CAUTION"}} {
		t.Run(tc.kind, func(t *testing.T) {
			raw := `{"type":"doc","version":1,"content":[{"type":"panel","attrs":{"panelType":"` + tc.kind + `"},"content":[{"type":"paragraph","content":[{"type":"text","text":"content"}]}]}]}`
			doc := mustParse(t, raw)
			if err := adf.Validate([]byte(raw)); err != nil {
				t.Fatal("invalid independent panel", err)
			}
			projected := adf.ToMarkdownLossy(doc)
			if projected.Markdown != "> [!"+tc.marker+"]\n>\n> content\n" || len(projected.LossyConstructs) != 0 {
				t.Fatalf("inverse alert missing or unnecessarily lossy: %+v", projected)
			}
			converted, warnings, err := adf.FromMarkdownLossy(projected.Markdown)
			if err != nil || len(warnings) != 0 {
				t.Fatal("projected panel is not authorable", err, warnings)
			}
			equal, err := adf.StructuralEqual([]byte(raw), editBytes(t, converted))
			if err != nil || !equal {
				t.Fatal("panel structure lost in conversion", err)
			}
			if !bytes.Equal(editBytes(t, doc), []byte(raw)) {
				t.Fatal("panel projection changed native bytes")
			}
			session, err := adf.PrepareEdit(doc)
			if err != nil {
				t.Fatal(err)
			}
			if session.Regions()[0].Protected {
				t.Fatal("proven representable panel remained protected")
			}
			unchanged, err := session.Apply(session.Markdown())
			if err != nil || !bytes.Equal(editBytes(t, unchanged), []byte(raw)) {
				t.Fatal("representable no-op changed native bytes", err)
			}
			changed, err := session.Apply(strings.Replace(session.Markdown(), "content", "changed", 1))
			if err != nil {
				t.Fatal("anchored panel edit refused", err)
			}
			expected := strings.Replace(raw, "content\"}", "changed\"}", 1)
			equal, err = adf.StructuralEqual([]byte(expected), editBytes(t, changed))
			if err != nil || !equal {
				t.Fatal("anchored edit changed panel shape", err, string(editBytes(t, changed)))
			}
		})
	}
}

func TestPanelProjectionStructuredContentAndTypeEdits(t *testing.T) {
	t.Parallel()
	raw := `{"type":"doc","version":1,"content":[{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Heading","marks":[{"type":"strong"}]}]},{"type":"paragraph","content":[{"type":"text","text":"site","marks":[{"type":"link","attrs":{"href":"https://example.com/a(b)"}}]}]},{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"item"}]}]}]},{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"one\n\u0060two\u0060"}]},{"type":"rule"}]}]}`
	if err := adf.Validate([]byte(raw)); err != nil {
		t.Fatal("invalid independent structured panel", err)
	}
	doc, session := prepareEdit(t, raw)
	projection := adf.ToMarkdownLossy(doc)
	if len(projection.LossyConstructs) != 0 || session.Regions()[0].Protected {
		t.Fatal("representable structured panel lost", projection)
	}
	for _, want := range []string{"> [!WARNING]", "> ## **Heading**", "> - item", "> ```go", "> ---", "[site](<https://example.com/a(b)>)"} {
		if !strings.Contains(projection.Markdown, want) {
			t.Error("missing structured content", want, projection.Markdown)
		}
	}
	changed, err := session.Apply(strings.Replace(session.Markdown(), "[!WARNING]", "[!TIP]", 1))
	if err != nil {
		t.Fatal(err)
	}
	equal, err := adf.StructuralEqual([]byte(strings.Replace(raw, `"warning"`, `"tip"`, 1)), editBytes(t, changed))
	if err != nil || !equal {
		t.Fatal("panel type edit lost structure", err)
	}
}

func TestPanelProjectionOpaqueStateStaysProtected(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, node string }{
		{name: "native identity", node: `{"type":"panel","attrs":{"panelType":"warning","localId":"keep"},"content":[{"type":"paragraph","content":[{"type":"text","text":"content"}]}]}`},
		{name: "future root", node: `{"type":"panel","attrs":{"panelType":"warning"},"future":{"id":9007199254740993},"content":[{"type":"paragraph","content":[{"type":"text","text":"content"}]}]}`},
		{name: "success type", node: `{"type":"panel","attrs":{"panelType":"success"},"content":[{"type":"paragraph","content":[{"type":"text","text":"content"}]}]}`},
		{name: "custom type", node: `{"type":"panel","attrs":{"panelType":"custom","panelColor":"#123456"},"content":[{"type":"paragraph","content":[{"type":"text","text":"content"}]}]}`},
		{name: "malformed type", node: `{"type":"panel","attrs":{"panelType":5},"content":[{"type":"paragraph","content":[{"type":"text","text":"content"}]}]}`},
		{name: "missing type", node: `{"type":"panel","content":[{"type":"paragraph","content":[{"type":"text","text":"content"}]}]}`},
		{name: "empty child attrs", node: `{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","attrs":{},"content":[{"type":"text","text":"content"}]}]}`},
		{name: "empty child marks", node: `{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":"content","marks":[]}]}]}`},
		{name: "unknown mark", node: `{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":"content","marks":[{"type":"futureMark","attrs":{"id":"keep"}}]}]}]}`},
		{name: "boundary text", node: `{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":" content "}]}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"type":"doc","version":1,"content":[` + tc.node + `]}`
			doc, session := prepareEdit(t, raw)
			projected := adf.ToMarkdownLossy(doc)
			if !slices.Contains(projected.LossyConstructs, "panel") || !session.Regions()[0].Protected {
				t.Fatal("native state protection lost", projected, session.Regions())
			}
			requireEditError(t, session, "", adf.EditProtectedRegion)
			requireEditError(t, session, strings.Replace(session.Markdown(), "content", "changed", 1), adf.EditProtectedRegion)
			noOp, err := session.Apply(session.Markdown())
			if err != nil || !bytes.Equal(editBytes(t, noOp), []byte(raw)) {
				t.Fatal("opaque no-op lost bytes", err)
			}
		})
	}
}

func TestPanelProjectionNestedContextRemainsExplicit(t *testing.T) {
	t.Parallel()
	raw := `{"type":"doc","version":1,"content":[{"type":"blockquote","content":[{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":"content"}]}]}]}]}`
	doc, session := prepareEdit(t, raw)
	projected := adf.ToMarkdownLossy(doc)
	if !slices.Contains(projected.LossyConstructs, "panel") || strings.Contains(projected.Markdown, "[!WARNING]") || !session.Regions()[0].Protected {
		t.Fatal("nested alert incorrectly claimed representable", projected)
	}
}

func TestPanelProjectionMergeRetainsCurrentStateAndConflicts(t *testing.T) {
	t.Parallel()
	base := `{"type":"doc","version":1,"content":[{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":"content"}]}]},{"type":"paragraph","content":[{"type":"text","text":"outside"}]}]}`
	_, session := prepareEdit(t, base)
	local := strings.Replace(session.Markdown(), "content", "changed", 1)
	current := mustParse(t, strings.Replace(strings.Replace(base, `"version":1`, `"version":1,"future":{"id":9007199254740993}`, 1), `"outside"`, `"remote outside"`, 1))
	candidate, err := session.Merge(local, current)
	if err != nil {
		t.Fatal("independent remote edit refused", err)
	}
	expected := strings.Replace(string(editBytes(t, current)), `"content"}`, `"changed"}`, 1)
	equal, err := adf.StructuralEqual([]byte(expected), editBytes(t, candidate))
	if err != nil || !equal {
		t.Fatal("panel merge dropped current native state", err)
	}
	current = mustParse(t, strings.Replace(base, `"content"}`, `"remote content"}`, 1))
	candidate, err = session.Merge(local, current)
	var conflict *adf.EditError
	if candidate != nil || !errors.As(err, &conflict) || conflict.Kind != adf.EditMergeConflict {
		t.Fatal("concurrent panel edit silently merged", err)
	}
}

func TestPanelProjectionEmptyAndStructuralChanges(t *testing.T) {
	t.Parallel()
	empty := `{"type":"panel","attrs":{"panelType":"info"},"content":[{"type":"paragraph"}]}`
	plain := `{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":"content"}]}]}`
	opaque := `{"type":"panel","attrs":{"panelType":"tip","localId":"keep"},"content":[{"type":"paragraph","content":[{"type":"text","text":"opaque"}]}]}`
	doc, session := prepareEdit(t, `{"type":"doc","version":1,"content":[`+empty+`,`+plain+`,`+opaque+`]}`)
	regions := session.Regions()
	if len(regions) != 3 || regions[0].Protected || regions[1].Protected || !regions[2].Protected {
		t.Fatal("empty or opaque panel protection wrong", regions)
	}
	// Removing, moving and inserting whole editable panels uses the same
	// anchor owner while the opaque panel retains its exact original bytes.
	changed, err := session.Apply(editChunk(t, session, 2) + editChunk(t, session, 1) + "> [!TIP]\n>\n> inserted\n\n")
	if err != nil {
		t.Fatal("panel structural edit refused", err)
	}
	inserted := `{"type":"panel","attrs":{"panelType":"tip"},"content":[{"type":"paragraph","content":[{"type":"text","text":"inserted"}]}]}`
	equal, err := adf.StructuralEqual([]byte(`{"type":"doc","version":1,"content":[`+opaque+`,`+plain+`,`+inserted+`]}`), editBytes(t, changed))
	if err != nil || !equal {
		t.Fatal("panel insertion/removal/order lost structure", err)
	}
	opaqueBytes, err := changed.Content()[0].MarshalJSON()
	if err != nil || !bytes.Equal(opaqueBytes, []byte(opaque)) {
		t.Fatal("panel structural edit changed opaque bytes", err)
	}
	if !bytes.Equal(editBytes(t, doc), []byte(`{"type":"doc","version":1,"content":[`+empty+`,`+plain+`,`+opaque+`]}`)) {
		t.Fatal("structural edit changed source")
	}
	// Standalone empty panels have an exact inverse, even though an empty
	// paragraph outside a panel has no Markdown representation.
	emptyDoc := mustParse(t, `{"type":"doc","version":1,"content":[`+empty+`]}`)
	projection := adf.ToMarkdownLossy(emptyDoc)
	if len(projection.LossyConstructs) != 0 {
		t.Fatal("exact empty alert reported child loss", projection)
	}
}

func TestPanelProjectionFailedProofRetainsChildLosses(t *testing.T) {
	t.Parallel()
	// Known statuses now have an inverse. Future state must still make the
	// panel proof fail without erasing the child's status diagnostic.
	raw := `{"type":"doc","version":1,"content":[{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"status","attrs":{"text":"Ready","color":"green","future":true}}]}]}]}`
	doc, session := prepareEdit(t, raw)
	projection := adf.ToMarkdownLossy(doc)
	if !slices.Contains(projection.LossyConstructs, "panel") || !slices.Contains(projection.LossyConstructs, "status") || !session.Regions()[0].Protected {
		t.Fatal("failed panel proof erased child diagnostics", projection)
	}
}
