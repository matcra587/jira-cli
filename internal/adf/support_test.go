package adf

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const supportAuthoringCorpus = "# Heading\n\nPlain **bold** *italic* ~~struck~~ `code` [link](https://example.com).\n\n" +
	"Line one  \nline two.\n\n- bullet one\n- bullet two\n\n4. ordered one\n5. ordered two\n\n" +
	"- [ ] open task\n- [x] done task\n\n> quoted\n\n```go\ncode block\n```\n\n" +
	"| H1 | H2 |\n| --- | --- |\n| a | b |\n\n---\n\n> [!NOTE]\n> **panel**\n\n:status[READY]{color=green}\n"

func TestSupportMatrixCoversCatalogAndPreservesContexts(t *testing.T) {
	t.Parallel()
	rows := SupportMatrix()
	if len(rows) != len(NodeSpecs)+len(MarkSpecs) {
		t.Fatal("support matrix omitted pinned definitions")
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row.Definition] {
			t.Fatal("matrix collapsed or duplicated a context")
		}
		seen[row.Definition] = true
		switch row.Kind {
		case "node":
			if !reflect.DeepEqual(row.NodeBounds, NodeSpecs[row.Definition]) || row.Name != string(row.NodeBounds.Type) || row.Variant != row.NodeBounds.Variant || row.MarkBounds != nil {
				t.Fatalf("node definition lost its actual bounds: %s", row.Definition)
			}
		case "mark":
			if !reflect.DeepEqual(row.MarkBounds, MarkSpecs[row.Definition]) || row.Name != string(row.MarkBounds.Type) || row.NodeBounds != nil {
				t.Fatal("mark definition lost its bounds")
			}
		default:
			t.Fatal("unexpected matrix kind")
		}
		if !row.Capabilities.Preserve || !row.Capabilities.Validate || !row.Capabilities.Submit || row.Capabilities.Edit {
			t.Fatal("matrix fabricated or hid native capabilities")
		}
	}
	for _, name := range []string{"paragraph_with_no_marks_node", "paragraph_with_alignment_node", "codeBlock_root_only_node"} {
		if !seen[name] {
			t.Fatalf("matrix hid context variant %s", name)
		}
	}
}

func TestSupportMatrixDoesNotExposeMutableCatalog(t *testing.T) {
	t.Parallel()
	before, err := json.Marshal(SupportMatrix())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range SupportMatrix() {
		if row.NodeBounds != nil {
			n := row.NodeBounds
			n.Name = "changed"
			for i := range n.Required {
				n.Required[i] = "changed"
			}
			if n.Marks != nil {
				n.Marks.Max = 123
				for i := range n.Marks.Allowed {
					n.Marks.Allowed[i] = "changed"
				}
			}
			if n.Content != nil {
				n.Content.MinItems = 123
				for i := range n.Content.Refs {
					n.Content.Refs[i] = "changed"
				}
			}
			mutateSupportAttrs(n.Attrs)
		}
		if row.MarkBounds != nil {
			row.MarkBounds.Name = "changed"
			mutateSupportAttrs(row.MarkBounds.Attrs)
		}
	}
	after, err := json.Marshal(SupportMatrix())
	if err != nil || string(after) != string(before) {
		t.Fatal("returned support metadata mutated the shared schema catalog")
	}
}

func mutateSupportAttrs(attrs *AttrsSpec) {
	if attrs == nil {
		return
	}
	attrs.Open = !attrs.Open
	for i := range attrs.Required {
		attrs.Required[i] = "changed"
	}
	for i := range attrs.Allowed {
		attrs.Allowed[i] = "changed"
	}
}

func TestSupportMatrixAuthorClaimsMatchExecutedCorpus(t *testing.T) {
	t.Parallel()
	doc, warnings, err := FromMarkdownLossy(supportAuthoringCorpus)
	if err != nil {
		t.Fatal(err)
	}
	for _, warning := range warnings {
		if warning.Lossy {
			t.Fatal("authoring corpus lost content")
		}
	}
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	var emitted []Node
	marks := map[MarkType]bool{}
	var walk func(Node)
	walk = func(node Node) {
		emitted = append(emitted, node)
		for _, mark := range node.Marks() {
			marks[MarkType(mark.Type())] = true
		}
		for _, child := range node.Content() {
			walk(child)
		}
	}
	walk(doc.Node)
	for _, row := range SupportMatrix() {
		produced := marks[MarkType(row.Name)]
		if row.Kind == "node" {
			produced = false
			for i := range emitted {
				if row.NodeBounds.Matches(&emitted[i]) {
					produced = true
					break
				}
			}
		}
		if row.Capabilities.Author != produced {
			t.Errorf("author capability for %s=%t but executed corpus produced=%t", row.Definition, row.Capabilities.Author, produced)
		}
	}
}

// Behavioral corpus above proves emission. These independent source guards
// additionally prevent projection/terminal declarations drifting from dispatch.
func TestSupportMatrixRenderingClaimsMatchDispatch(t *testing.T) {
	t.Parallel()
	nodeCases := supportSwitchCases(t, "projection.go", "node")
	for _, name := range []string{"doc", "tableRow", "tableCell", "tableHeader"} {
		nodeCases[name] = true
	}
	markCases := supportSwitchCases(t, "projection.go", "text")
	terminalMarks := supportSwitchCases(t, "render.go", "inline")
	for _, row := range SupportMatrix() {
		want := nodeCases[row.Name]
		if row.Kind == "mark" {
			want = markCases[row.Name]
			if row.TerminalRender != terminalMarks[row.Name] {
				t.Errorf("terminal mark support drifted: %s", row.Name)
			}
		}
		if row.Capabilities.Render != want {
			t.Errorf("Markdown render claim drifted for %s", row.Definition)
		}
	}
}

func supportSwitchCases(t *testing.T, path, function string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	constants := supportTypeConstants(t)
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	found := false
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Name.Name != function {
			continue
		}
		found = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			clause, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, value := range clause.List {
				identifier, ok := value.(*ast.Ident)
				if ok {
					if name, ok := constants[identifier.Name]; ok {
						out[name] = true
					}
				}
			}
			return true
		})
	}
	if !found {
		t.Fatalf("source guard did not find %s in %s", function, path)
	}
	return out
}

func TestSupportMatrixValidationDefinitionsAreReachable(t *testing.T) {
	t.Parallel()
	var schema map[string]any
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		t.Fatal(err)
	}
	definitions, ok := schema["definitions"].(map[string]any)
	if !ok {
		t.Fatal("pinned schema has no definitions")
	}
	seen := map[string]bool{}
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if ref, ok := node["$ref"].(string); ok && strings.HasPrefix(ref, "#/definitions/") {
				name := strings.TrimPrefix(ref, "#/definitions/")
				if !seen[name] {
					seen[name] = true
					walk(definitions[name])
				}
			}
			for key, value := range node {
				if key != "definitions" {
					walk(value)
				}
			}
		case []any:
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(schema)
	for _, row := range SupportMatrix() {
		if row.Capabilities.Validate && !seen[row.Definition] {
			t.Errorf("matrix claims validation of unreachable definition %s", row.Definition)
		}
	}
}

func supportTypeConstants(t *testing.T) map[string]string {
	t.Helper()
	generated, err := parser.ParseFile(token.NewFileSet(), "nodes_gen.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	constants := map[string]string{}
	ast.Inspect(generated, func(n ast.Node) bool {
		v, ok := n.(*ast.ValueSpec)
		if !ok || len(v.Names) != 1 || len(v.Values) != 1 {
			return true
		}
		literal, ok := v.Values[0].(*ast.BasicLit)
		if ok && literal.Kind == token.STRING {
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			constants[v.Names[0].Name] = value
		}
		return true
	})
	return constants
}

func TestSupportMatrixAuthorEmissionUniverseMatchesSource(t *testing.T) {
	t.Parallel()
	constants := supportTypeConstants(t)
	referenced := map[string]bool{"doc": true} // NewDocument supplies the root.
	for _, path := range []string{"markdown.go", "markdown_alert.go", "markdown_status.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			identifier, ok := n.(*ast.Ident)
			if ok && (strings.HasPrefix(identifier.Name, "NodeType") || strings.HasPrefix(identifier.Name, "MarkType")) {
				if value, ok := constants[identifier.Name]; ok {
					referenced[value] = true
				}
			}
			return true
		})
	}
	claimed := map[string]bool{}
	for _, row := range SupportMatrix() {
		if row.Capabilities.Author {
			claimed[row.Name] = true
		}
	}
	if !reflect.DeepEqual(referenced, claimed) {
		t.Fatal("converter references and authoring declarations diverged; extend the executed authoring corpus for the changed construct")
	}
}
