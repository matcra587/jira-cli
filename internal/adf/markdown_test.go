package adf_test

import (
	"encoding/json"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

// Shared helpers for the Markdown→ADF corpus. They exist because the
// substrate reads through getters rather than struct fields: a test that
// spelled out doc.Content()[0].Content()[0].Text() at every assertion
// would bury what it is actually pinning.

// mdConvert converts src and fails the test on a conversion error, which
// is always a defect rather than an expected outcome — unrepresentable
// input is reported through warnings, not errors.
func mdConvert(t *testing.T, src string) (*adf.Document, []adf.Warning) {
	t.Helper()
	doc, warnings, err := adf.FromMarkdownLossy(src)
	if err != nil {
		t.Fatalf("FromMarkdownLossy(%q): %v", src, err)
	}
	return doc, warnings
}

// mdKinds lists the document's top-level node types, the shape most of
// the hoisting tests assert on.
func mdKinds(doc *adf.Document) []string {
	content := doc.Content()
	kinds := make([]string, 0, len(content))
	for i := range content {
		kinds = append(kinds, content[i].Type())
	}
	return kinds
}

// mdChild walks a node's content by index, failing with the path it got
// to rather than panicking on an out-of-range index.
func mdChild(t *testing.T, n *adf.Node, path ...int) *adf.Node {
	t.Helper()
	cur := n
	for step, i := range path {
		content := cur.Content()
		if i >= len(content) {
			t.Fatalf("path %v: step %d wants child %d of a %q node with %d children", path, step, i, cur.Type(), len(content))
		}
		cur = &content[i]
	}
	return cur
}

// mdBlock returns the document's nth top-level block.
func mdBlock(t *testing.T, doc *adf.Document, i int) *adf.Node {
	t.Helper()
	content := doc.Content()
	if i >= len(content) {
		t.Fatalf("document has %d top-level blocks, want at least %d", len(content), i+1)
	}
	return &content[i]
}

// mdContainsType reports whether typ appears anywhere in the subtree —
// for the assertions that care that a node survived, not where it landed.
func mdContainsType(nodes []adf.Node, typ string) bool {
	for i := range nodes {
		if nodes[i].Type() == typ {
			return true
		}
		if mdContainsType(nodes[i].Content(), typ) {
			return true
		}
	}
	return false
}

// mdMarkTypes is the set of mark types on a node, for assertions that
// care about presence rather than order.
func mdMarkTypes(n *adf.Node) map[string]bool {
	marks := n.Marks()
	types := make(map[string]bool, len(marks))
	for i := range marks {
		types[marks[i].Type()] = true
	}
	return types
}

// mdHref decodes a link mark's href. The typed layer generates attr
// structs for nodes only, so mark attrs are decoded here.
func mdHref(t *testing.T, m adf.Mark) string {
	t.Helper()
	var attrs struct {
		Href string `json:"href"`
	}
	raw := m.Attrs()
	if raw == nil {
		t.Fatalf("mark %q carries no attrs", m.Type())
	}
	if err := json.Unmarshal(raw, &attrs); err != nil {
		t.Fatalf("decode %q mark attrs: %v", m.Type(), err)
	}
	return attrs.Href
}

// mdValidate asserts the converted document conforms to the vendored ADF
// schema. Every test that pins a degrade or a hoist runs it: the whole
// reason those reshapings exist is that Jira rejects the natural shape.
func mdValidate(t *testing.T, doc *adf.Document) {
	t.Helper()
	if err := doc.Validate(); err != nil {
		t.Fatalf("converter output failed schema validation: %v", err)
	}
}

// mdAssertNoWarnings fails with the warnings themselves, which is the
// only useful failure message for a "this converts cleanly" test.
func mdAssertNoWarnings(t *testing.T, warnings []adf.Warning) {
	t.Helper()
	if len(warnings) != 0 {
		t.Fatalf("expected a clean conversion, got %d warning(s): %+v", len(warnings), warnings)
	}
}
