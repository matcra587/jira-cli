package guardrails

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --web is registered ONCE, as a PERSISTENT flag on the `issue` group
// (internal/cli/issue/issue.go), so every subcommand under it accepts
// --web whether or not anyone wired it. Resolving the URL is already
// structural — openIssueBrowseTarget (issue/web.go) is the only way to
// get one — but HONORING it at render time is three hand-written
// branches, one per command:
//
//	if out.WebURL != "" { return renderWebURL(v, out.WebURL) }
//
// Omit that branch and the failure is silent and wrong rather than
// loud: --web skips the fetch, so the payload is at its zero value, and
// the ordinary renderer draws over it. `issue comment list KEY --web`
// would print "No comments." for an issue with fifty, and `issue view
// KEY --web` a glance line with a blank summary and status.
//
// The two mechanisms that would otherwise catch a new command cannot
// catch this one. H1 (human_view_primitives.go) proves a renderer
// writes through the View, not that it checked a field first. The
// output audit compares each operation against a stored baseline, and a
// new command's FIRST capture becomes its own baseline — so an omission
// arrives green.
//
// Written now rather than when it breaks, for the reason
// pagination_placement_test.go states about its own invariant: a
// convention nothing enforces is the defect this codebase reproduces
// most reliably. `issue worklog list` and `issue attachment list` both
// shipped before 0.16, are both issue-scoped reads under this group,
// and both resolve to the same issue page — they are the fourth and
// fifth callers, and the resolver is forced on them while the guard is
// not.

// webRenderScanDirs is the command tree this walks, and
// webEnvelopeDir is where the Output structs that embed WebLink live.
var (
	webRenderScanDirs = []string{"internal/cli"}
	webEnvelopeDir    = "internal/envelope"
)

// webLinkEmbed is the embedded field name that makes an Output struct
// --web-bearing. It is a real embed rather than a copied field
// precisely so this scan can find every one of them by name.
const webLinkEmbed = "WebLink"

// viewWriteMethods are the View calls that DRAW. The guard must appear
// before any of them: reaching one means the ordinary payload is
// already being rendered over a zero value, which is the bug.
//
// output.Table is matched separately by function name, since it is a
// package-level function rather than a method.
var viewWriteMethods = map[string]bool{
	"Fields": true, "Line": true, "Block": true, "Section": true, "Hint": true,
}

// TestWebBearingRenderersGuardWebURLFirst pins that every human
// renderer for an operation whose Output embeds envelope.WebLink
// branches on WebURL before it draws anything.
func TestWebBearingRenderersGuardWebURLFirst(t *testing.T) {
	root := repoRoot(t)

	bearing, err := webLinkOutputs(filepath.Join(root, webEnvelopeDir))
	if err != nil {
		t.Fatalf("scanning %s: %v", webEnvelopeDir, err)
	}
	if len(bearing) == 0 {
		t.Fatal("no envelope Output type embeds WebLink — either the embed was renamed " +
			"(update webLinkEmbed) or --web lost its envelope shape; this guard is blind either way")
	}

	renderers, err := webRenderersFor(root, bearing)
	if err != nil {
		t.Fatalf("scanning renderers: %v", err)
	}
	// One renderer per bearing output is the shape today. Fewer means a
	// Human renderer was not found where one is expected, which would
	// silently narrow this guard to nothing.
	if len(renderers) == 0 {
		t.Fatalf("found %d WebLink-bearing outputs but no human renderer bound to any of them — "+
			"the Spec-to-renderer match broke, and this guard is checking nothing", len(bearing))
	}

	for _, r := range renderers {
		if !r.guardsWebURL {
			t.Errorf("%s: human renderer %q renders %s (which embeds %s) but draws before checking WebURL — "+
				"--web skips the fetch, so this draws the ordinary payload over a zero value. "+
				"Open with: if out.WebURL != \"\" { return renderWebURL(v, out.WebURL) }",
				r.pos, r.fn, r.outputType, webLinkEmbed)
		}
	}
}

// webLinkOutputs returns the set of struct type names in dir that embed
// WebLink.
func webLinkOutputs(dir string) (map[string]bool, error) {
	out := map[string]bool{}
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			return nil, err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			for _, f := range st.Fields.List {
				// An embed has no names; its type is the field.
				if len(f.Names) != 0 {
					continue
				}
				if id, ok := f.Type.(*ast.Ident); ok && id.Name == webLinkEmbed {
					out[ts.Name.Name] = true
				}
			}
			return true
		})
	}
	return out, nil
}

// webRenderer is one human renderer bound to a WebLink-bearing output.
type webRenderer struct {
	fn           string
	outputType   string
	pos          string
	guardsWebURL bool
}

// webRenderersFor finds every `Human:` renderer in a command.Spec whose
// type argument is one of bearing, and reports whether each checks
// WebURL before drawing.
func webRenderersFor(root string, bearing map[string]bool) ([]webRenderer, error) {
	var found []webRenderer
	for _, scanDir := range webRenderScanDirs {
		err := filepath.WalkDir(filepath.Join(root, scanDir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			file, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				return parseErr
			}
			// funcs in this file, so a Human: renderer naming a function
			// declared here can be resolved without type information.
			funcs := map[string]*ast.FuncDecl{}
			for _, decl := range file.Decls {
				if fd, ok := decl.(*ast.FuncDecl); ok && fd.Recv == nil {
					funcs[fd.Name.Name] = fd
				}
			}
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				outputType, ok := specTypeArg(lit.Type)
				if !ok || !bearing[outputType] {
					return true
				}
				name, ok := humanFieldFuncName(lit)
				if !ok {
					return true
				}
				fd, ok := funcs[name]
				if !ok {
					// Declared in another file of the same package. Resolving
					// that needs a package-wide pass; report it rather than
					// skip silently, so the guard cannot narrow unnoticed.
					found = append(found, webRenderer{
						fn: name, outputType: outputType,
						pos:          fset.Position(lit.Pos()).String(),
						guardsWebURL: false,
					})
					return true
				}
				found = append(found, webRenderer{
					fn: name, outputType: outputType,
					pos:          fset.Position(fd.Pos()).String(),
					guardsWebURL: guardsWebURLBeforeDrawing(fd),
				})
				return true
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return found, nil
}

// specTypeArg returns the single type argument of a command.Spec[T]
// composite-literal type, and whether the node was one.
func specTypeArg(t ast.Expr) (string, bool) {
	idx, ok := t.(*ast.IndexExpr)
	if !ok {
		return "", false
	}
	sel, ok := idx.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Spec" {
		return "", false
	}
	// envelope.IssueViewOutput — the package qualifier is not checked,
	// because the name alone already had to match a bearing type.
	if arg, ok := idx.Index.(*ast.SelectorExpr); ok {
		return arg.Sel.Name, true
	}
	if arg, ok := idx.Index.(*ast.Ident); ok {
		return arg.Name, true
	}
	return "", false
}

// humanFieldFuncName returns the identifier a Spec's Human field is set
// to. A function literal returns false: this guard reads a named
// declaration, and an inline closure is a shape no --web command uses.
func humanFieldFuncName(lit *ast.CompositeLit) (string, bool) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "Human" {
			continue
		}
		if id, ok := kv.Value.(*ast.Ident); ok {
			return id.Name, true
		}
	}
	return "", false
}

// guardsWebURLBeforeDrawing reports whether fd reads a WebURL field
// before it calls any View draw method.
//
// Order matters, not mere presence: a WebURL check placed after the
// fields block has already been written is exactly the bug, drawn and
// then apologized for.
func guardsWebURLBeforeDrawing(fd *ast.FuncDecl) bool {
	guarded := false
	drew := false
	ast.Inspect(fd, func(n ast.Node) bool {
		if drew || guarded {
			return false
		}
		switch node := n.(type) {
		case *ast.SelectorExpr:
			if node.Sel.Name == "WebURL" {
				guarded = true
				return false
			}
		case *ast.CallExpr:
			switch fun := node.Fun.(type) {
			case *ast.SelectorExpr:
				if viewWriteMethods[fun.Sel.Name] {
					drew = true
					return false
				}
				// output.Table(v, ...) — package-level, matched by name.
				if fun.Sel.Name == "Table" {
					drew = true
					return false
				}
			case *ast.Ident:
				if fun.Name == "Table" {
					drew = true
					return false
				}
			}
		}
		return true
	})
	return guarded
}
