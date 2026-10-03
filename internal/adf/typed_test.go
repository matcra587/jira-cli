package adf_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

func parseNode(t *testing.T, data string) *adf.Node {
	t.Helper()
	var n adf.Node
	if err := json.Unmarshal([]byte(data), &n); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return &n
}

// TestCatalogAccountsForEveryDefinition is the completeness half of the
// capability matrix: every schema definition is a node spec, a mark spec,
// or a content group — nothing falls through silently.
func TestCatalogAccountsForEveryDefinition(t *testing.T) {
	accounted := map[string]bool{}
	for name := range adf.NodeSpecs {
		accounted[name] = true
	}
	for name := range adf.MarkSpecs {
		accounted[name] = true
	}
	for name := range adf.Groups {
		accounted[name] = true
	}
	if len(accounted) != len(adf.SchemaDefinitions) {
		t.Errorf("catalog covers %d definitions, schema has %d", len(accounted), len(adf.SchemaDefinitions))
	}
	for _, name := range adf.SchemaDefinitions {
		if !accounted[name] {
			t.Errorf("definition %s is in the schema but not the catalog", name)
		}
	}
}

func TestTypedDispatch(t *testing.T) {
	n := parseNode(t, `{"type":"panel","attrs":{"panelType":"info"},"content":[]}`)
	typed, err := adf.Typed(n)
	if err != nil {
		t.Fatalf("Typed: %v", err)
	}
	panel, ok := typed.(*adf.Panel)
	if !ok {
		t.Fatalf("Typed returned %T, want *adf.Panel", typed)
	}
	if panel.Attrs.PanelType != "info" {
		t.Errorf("PanelType = %q, want info", panel.Attrs.PanelType)
	}
	if panel.Node() != n {
		t.Errorf("view does not wrap the original node")
	}
}

func TestTypedUnknownNode(t *testing.T) {
	_, err := adf.Typed(parseNode(t, `{"type":"futureWidget"}`))
	if !errors.Is(err, adf.ErrUnknownNodeType) {
		t.Errorf("Typed error = %v, want ErrUnknownNodeType", err)
	}
}

func TestMarkTyped(t *testing.T) {
	n := parseNode(t, `{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":"https://example.com"}}]}`)
	marks := n.Marks()
	if len(marks) != 1 {
		t.Fatalf("marks = %d, want 1", len(marks))
	}
	typed, err := adf.MarkTyped(&marks[0])
	if err != nil {
		t.Fatalf("MarkTyped: %v", err)
	}
	link, ok := typed.(*adf.LinkMark)
	if !ok {
		t.Fatalf("MarkTyped returned %T, want *adf.LinkMark", typed)
	}
	if link.Attrs.Href != "https://example.com" {
		t.Errorf("Href = %q", link.Attrs.Href)
	}
}

// TestClassifyDiscriminatesParagraphVariants pins the reason "type" alone
// is not a discriminator: the same runtime type resolves to different
// definitions depending on its marks.
func TestClassifyDiscriminatesParagraphVariants(t *testing.T) {
	aligned := parseNode(t, `{"type":"paragraph","marks":[{"type":"alignment","attrs":{"align":"center"}}],"content":[{"type":"text","text":"x"}]}`)
	matches := adf.ClassifyIn(aligned, "block_content")
	if len(matches) != 1 {
		t.Fatalf("aligned paragraph matched %d specs, want exactly 1", len(matches))
	}
	if matches[0].Variant != "with_alignment" {
		t.Errorf("variant = %q, want with_alignment", matches[0].Variant)
	}

	plain := parseNode(t, `{"type":"paragraph","content":[{"type":"text","text":"x"}]}`)
	plainMatches := adf.ClassifyIn(plain, "block_content")
	if len(plainMatches) < 2 {
		t.Fatalf("plain paragraph matched %d specs, want several (every variant admits it)", len(plainMatches))
	}
	if plainMatches[0].Variant != "with_no_marks" {
		t.Errorf("first match = %q, want with_no_marks (schema anyOf order)", plainMatches[0].Variant)
	}
}

func TestClassifyRejectsInvalidContext(t *testing.T) {
	unknown := parseNode(t, `{"type":"futureWidget"}`)
	if got := adf.ClassifyIn(unknown, "block_content"); len(got) != 0 {
		t.Errorf("unknown node matched %d specs in block_content", len(got))
	}
	// A codeBlock is fine in block content but not inside a panel that
	// already nests it out of reach — use inline context for a clear no.
	code := parseNode(t, `{"type":"codeBlock","content":[]}`)
	if got := adf.ClassifyIn(code, "inline_node"); len(got) != 0 {
		t.Errorf("codeBlock matched %d specs in inline_node", len(got))
	}
}

func TestVariantsListsPrimaryFirst(t *testing.T) {
	variants := adf.Variants(adf.NodeTypeParagraph)
	if len(variants) < 4 {
		t.Fatalf("paragraph variants = %d, want at least 4", len(variants))
	}
	if variants[0].Variant != "" {
		t.Errorf("first variant = %q, want the primary definition", variants[0].Variant)
	}
	if variants[0].Name != "paragraph_node" {
		t.Errorf("primary = %s, want paragraph_node", variants[0].Name)
	}
}
