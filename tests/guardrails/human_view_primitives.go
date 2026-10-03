package guardrails

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// This file is the H1 axis's own evidence: "human layout goes through
// the View primitives (Fields, Line, Block, Section) rather than a
// renderer writing to its writer directly." Byte-level output cannot
// prove that — aligned-by-Fields and aligned-by-hand produce identical
// bytes — so this is a source-level guard instead, in the same style as
// this package's other two (no_raw_envelope_encoder_test.go,
// no_terminal_probe_test.go): parse with go/parser, resolve calls
// through the file's own import bindings rather than a text scan, and
// fail naming the offender.
//
// It differs from those two in one way: it is NOT a _test.go file. Every
// other guard in this package keeps its detector private inside its own
// _test.go, because nothing outside `go test ./tests/guardrails` needs
// it. This one is exported from a plain .go file instead, because
// tests/audit's H1 matrix cell (human_test.go) needs to attach this
// SAME verdict to the operation it belongs to — the design's own
// resolution to "does H1's verdict belong in the matrix, or does the
// matrix reference it": one guard, one piece of evidence, referenced
// rather than re-implemented.
//
// SCOPING — read this before trusting a clean result on code this file
// has not seen:
//
//   - An entry point is exactly a `Human:` field inside a
//     `command.Spec[T]{...}` composite literal — the same field
//     HumanRenderFunc documents as "renders the success payload for a
//     person" (output/render.go). Nothing outside internal/cli builds a
//     Spec, so nothing outside internal/cli is scanned.
//   - From each entry, the call graph is walked ONE PACKAGE DEEP: a call
//     to another function declared in the SAME DIRECTORY is followed
//     (across files — auth/render.go's writeIdentity is reached this way
//     from auth/login.go, auth/logout.go and auth/status.go, which each
//     declare it in a different file of the one auth package); a call
//     into any OTHER package is not followed. This is deliberate, not an
//     oversight: every value a Human renderer can legally touch — the
//     cli.Text/Status/Priority/Entity/Time sinks, internal/adf's
//     Render — takes no writer and returns a plain string, so nothing
//     reachable through them can perform the write this guard exists to
//     catch. A future helper package that DID accept a writer and got
//     called from a renderer would cross this guard's horizon
//     undetected — see ScanHumanRenderers' own doc comment for the
//     concrete gap this leaves.
//   - Three write shapes are matched, each bound to its import the same
//     way callsRawEncoder/callsTerminalProbe already do (an alias is
//     caught, the token in a comment or a string is not): fmt.Fprint /
//     fmt.Fprintf / fmt.Fprintln, io.WriteString, and any read of
//     os.Stdout or os.Stderr. A FOURTH shape the design doc names —
//     hand-rolled column padding via strings.Repeat(" ", …) — is
//     deliberately NOT matched: config/list.go's own pad() helper does
//     exactly that today, with a doc comment explaining why (no Table
//     primitive exists yet — design doc, "Table (RESOLVED)" — so
//     renderHuman's column math is reviewed, temporary, in-scope debt,
//     not a violation). A regex that cannot tell that case apart from an
//     actual hand-rolled layout is the noisy check the brief says not to
//     ship; this guard says so here instead.
//   - A bare `.Write(...)` method call on an arbitrary receiver is also
//     NOT matched, for the same false-positive reason: without type
//     information this guard cannot tell a raw io.Writer.Write from an
//     unrelated method that happens to be named Write, and guessing
//     wrong in either direction is worse than not checking. The three
//     shapes above cover every way a renderer can reach an OS stream
//     without deliberately constructing one of its own — which nothing
//     in this codebase does today.

// humanRenderScanDirs is the tree H1 walks. Scoped to internal/cli: that
// is the only tree that builds a command.Spec, and both
// internal/output/view.go (the primitive itself) and internal/adf
// (string-returning, no writer) are correctly out of scope by never
// containing an entry point at all.
var humanRenderScanDirs = []string{"internal/cli"}

// commandSpecPkg is the import path a composite literal's type must
// resolve to before its Human field counts as an entry point.
const commandSpecPkg = "github.com/matcra587/jira-cli/internal/cli/command"

// HumanRenderViolation is one place a Human entry point's reachable code
// (see this file's scoping note) writes to a destination directly
// instead of through a View primitive. Op carries the operation whose
// entry point reached it, so a violation attaches to ONE matrix cell
// rather than reading as a whole-package failure.
type HumanRenderViolation struct {
	Op     string
	File   string // repo-relative
	Line   int
	Detail string
}

func (v HumanRenderViolation) String() string {
	return fmt.Sprintf("%s:%d: op %q: %s", v.File, v.Line, v.Op, v.Detail)
}

// parsedFile pairs a parsed AST with the repo-relative path it came
// from, so a violation can name a file without every caller re-deriving
// filepath.Rel.
type parsedFile struct {
	rel  string
	file *ast.File
}

// funcInfo is one package-level function declaration, tracked with the
// file it lives in — needed because that file's OWN import list is what
// resolves fmt/io/os inside its body, and a package spans several files.
type funcInfo struct {
	decl *ast.FuncDecl
	file parsedFile
}

// humanEntry is one `Human:` field found inside a command.Spec literal.
// Exactly one of fn/lit is set: a named function is resolved against the
// declaring package's funcInfo map; an inline closure (issue.view's own
// Human field, wrapping runView's captured preview) is scanned directly
// without a name to add to the call-graph queue.
type humanEntry struct {
	op   string
	name string       // set when Human: names a package-level function
	lit  *ast.FuncLit // set when Human: is an inline closure
	file parsedFile
	pos  token.Pos
}

// ScanHumanRenderers is H1's evidence: every Human entry point under
// rootDir/internal/cli, walked to its own package-local call-graph
// closure (see this file's doc comment for exactly how far that
// closure reaches), and checked for a direct write. A clean tree
// returns (nil, nil).
//
// An entry whose Human field cannot be resolved at all — neither a
// package-level function name nor an inline closure, e.g. a value
// imported from another package — is a coverage gap this guard cannot
// silently accept: it is reported as an error rather than skipped,
// because a Human renderer this scan cannot see is exactly the kind of
// hole the design doc warns an unguarded human surface becomes.
func ScanHumanRenderers(rootDir string) ([]HumanRenderViolation, error) {
	fset := token.NewFileSet()
	byDir := map[string][]parsedFile{}
	for _, root := range humanRenderScanDirs {
		walkRoot := filepath.Join(rootDir, root)
		err := filepath.WalkDir(walkRoot, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return fmt.Errorf("parse %s: %w", path, perr)
			}
			rel, relErr := filepath.Rel(rootDir, path)
			if relErr != nil {
				return relErr
			}
			dir := filepath.Dir(path)
			byDir[dir] = append(byDir[dir], parsedFile{rel: filepath.ToSlash(rel), file: file})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	var violations []HumanRenderViolation
	for _, files := range byDir {
		vs, err := scanPackage(fset, files)
		if err != nil {
			return nil, err
		}
		violations = append(violations, vs...)
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].Op != violations[j].Op {
			return violations[i].Op < violations[j].Op
		}
		if violations[i].File != violations[j].File {
			return violations[i].File < violations[j].File
		}
		return violations[i].Line < violations[j].Line
	})
	return violations, nil
}

// scanPackage runs every Human entry point declared anywhere in files
// (one directory's worth) through its own package-scoped call-graph
// closure.
func scanPackage(fset *token.FileSet, files []parsedFile) ([]HumanRenderViolation, error) {
	funcMap := map[string]funcInfo{}
	for _, pf := range files {
		for _, decl := range pf.file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Body == nil {
				continue
			}
			funcMap[fd.Name.Name] = funcInfo{decl: fd, file: pf}
		}
	}

	var entries []humanEntry
	for _, pf := range files {
		cmdName, cmdDot := importedAs(pf.file, commandSpecPkg)
		if cmdName == "" && !cmdDot {
			continue // this file does not even import command.Spec's package
		}
		found, err := findHumanEntries(pf, cmdName, cmdDot)
		if err != nil {
			return nil, err
		}
		entries = append(entries, found...)
	}

	var violations []HumanRenderViolation
	for _, entry := range entries {
		vs, err := walkEntry(fset, entry, funcMap)
		if err != nil {
			return nil, err
		}
		violations = append(violations, vs...)
	}
	return violations, nil
}

// findHumanEntries collects every `Human:` field from a command.Spec
// composite literal in pf, under the local name (or dot-import) the
// file bound command's package to.
func findHumanEntries(pf parsedFile, cmdName string, cmdDot bool) ([]humanEntry, error) {
	var entries []humanEntry
	var walkErr error
	ast.Inspect(pf.file, func(n ast.Node) bool {
		if walkErr != nil {
			return false
		}
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !isCommandSpecType(lit.Type, cmdName, cmdDot) {
			return true
		}
		var op string
		var humanVal ast.Expr
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			switch key.Name {
			case "Op":
				if bl, ok := kv.Value.(*ast.BasicLit); ok && bl.Kind == token.STRING {
					if s, err := strconv.Unquote(bl.Value); err == nil {
						op = s
					}
				}
			case "Human":
				humanVal = kv.Value
			}
		}
		if humanVal == nil {
			return true // this Spec literal declares no Human renderer at all (an envelope-free path, say)
		}
		if op == "" {
			walkErr = fmt.Errorf("%s: command.Spec literal declares Human but no (or an unresolvable) Op — H1 cannot attribute its verdict", pf.rel)
			return false
		}
		switch v := humanVal.(type) {
		case *ast.Ident:
			entries = append(entries, humanEntry{op: op, name: v.Name, file: pf, pos: v.Pos()})
		case *ast.FuncLit:
			entries = append(entries, humanEntry{op: op, lit: v, file: pf, pos: v.Pos()})
		default:
			walkErr = fmt.Errorf("%s: op %q's Human field is neither a named function nor an inline closure (got %T) — H1 cannot see into it; extend ScanHumanRenderers before this ships", pf.rel, op, humanVal)
			return false
		}
		return true
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return entries, nil
}

// isCommandSpecType reports whether t is `command.Spec[...]` (or, under
// a dot import, bare `Spec[...]`) — the generic instantiation renders as
// an IndexExpr (one type argument, command.Spec[T]'s own shape today) or
// an IndexListExpr (more than one); both are unwrapped to their base
// before the name check, so either shape is recognized.
func isCommandSpecType(t ast.Expr, cmdName string, cmdDot bool) bool {
	switch idx := t.(type) {
	case *ast.IndexExpr:
		t = idx.X
	case *ast.IndexListExpr:
		t = idx.X
	}
	if cmdDot {
		ident, ok := t.(*ast.Ident)
		return ok && ident.Name == "Spec"
	}
	sel, ok := t.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	return ok && pkgIdent.Name == cmdName && sel.Sel.Name == "Spec"
}

// importedAs resolves the local binding a file gives pkgPath: its alias
// or default name, or ("", true) for a dot import, or ("", false) when
// pkgPath is not imported at all. Shared shape with
// no_raw_envelope_encoder_test.go's own import resolution, generalized
// to return the bound name instead of a fixed set — H1 needs the name
// itself (to match a SelectorExpr's package qualifier), where that guard
// only ever needed a yes/no set membership test.
func importedAs(file *ast.File, pkgPath string) (name string, dotImported bool) {
	for _, imp := range file.Imports {
		if imp.Path == nil {
			continue
		}
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != pkgPath {
			continue
		}
		switch {
		case imp.Name == nil:
			return defaultImportName(p), false
		case imp.Name.Name == ".":
			return "", true
		case imp.Name.Name == "_":
			return "", false // blank import: nothing can be called through it
		default:
			return imp.Name.Name, false
		}
	}
	return "", false
}

// defaultImportName returns the identifier an unaliased import binds —
// the path's last segment, skipping a trailing major-version segment
// ("v2", "v3", ...) the way Go's own import resolution does (same
// property no_terminal_probe_test.go's path4Default pins with its own
// lipgloss/v2 fixture; duplicated here, deliberately not shared, because
// that copy lives in a _test.go file and this one must compile under a
// plain `go build`).
func defaultImportName(p string) string {
	segments := strings.Split(p, "/")
	last := len(segments) - 1
	if last > 0 && isImportVersionSegment(segments[last]) {
		last--
	}
	return segments[last]
}

// isImportVersionSegment reports whether a path segment is a module
// major version: "v" followed by digits, and at least one digit.
func isImportVersionSegment(s string) bool {
	rest, ok := strings.CutPrefix(s, "v")
	if !ok || rest == "" {
		return false
	}
	return strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' }) < 0
}

// writerBindings is one file's resolved local names for the three
// packages H1's direct-write shapes are bound to, plus whether any of
// them arrived via a dot import (which erases the qualifier entirely).
type writerBindings struct {
	fmtNames map[string]bool
	fmtDot   bool
	ioNames  map[string]bool
	ioDot    bool
	osNames  map[string]bool
	osDot    bool
}

func bindingsFor(file *ast.File) writerBindings {
	b := writerBindings{fmtNames: map[string]bool{}, ioNames: map[string]bool{}, osNames: map[string]bool{}}
	for _, imp := range file.Imports {
		if imp.Path == nil {
			continue
		}
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		var names *map[string]bool
		var dot *bool
		switch p {
		case "fmt":
			names, dot = &b.fmtNames, &b.fmtDot
		case "io":
			names, dot = &b.ioNames, &b.ioDot
		case "os":
			names, dot = &b.osNames, &b.osDot
		default:
			continue
		}
		switch {
		case imp.Name == nil:
			(*names)[defaultImportName(p)] = true
		case imp.Name.Name == ".":
			*dot = true
		case imp.Name.Name == "_":
			// Blank import: nothing can be called through it.
		default:
			(*names)[imp.Name.Name] = true
		}
	}
	return b
}

var fprintFuncs = map[string]bool{"Fprint": true, "Fprintf": true, "Fprintln": true}

// walkEntry runs one Human entry point's package-local call-graph
// closure and returns every direct-write violation found in it.
func walkEntry(fset *token.FileSet, entry humanEntry, funcMap map[string]funcInfo) ([]HumanRenderViolation, error) {
	type unit struct {
		body ast.Node
		file parsedFile
	}
	var queue []unit
	visited := map[string]bool{}

	if entry.lit != nil {
		queue = append(queue, unit{body: entry.lit.Body, file: entry.file})
	} else {
		fi, ok := funcMap[entry.name]
		if !ok {
			return nil, fmt.Errorf("%s: op %q's Human field names %q, which is not a package-level function in this directory — H1 cannot follow it", entry.file.rel, entry.op, entry.name)
		}
		queue = append(queue, unit{body: fi.decl.Body, file: fi.file})
		visited[entry.name] = true
	}

	var violations []HumanRenderViolation
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		bindings := bindingsFor(u.file.file)

		ast.Inspect(u.body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				switch fn := node.Fun.(type) {
				case *ast.SelectorExpr:
					pkgIdent, ok := fn.X.(*ast.Ident)
					if !ok {
						return true
					}
					switch {
					case bindings.fmtNames[pkgIdent.Name] && fprintFuncs[fn.Sel.Name]:
						violations = append(violations, violationAt(fset, entry.op, u.file, node.Pos(),
							fmt.Sprintf("calls %s.%s directly — human layout goes through a View primitive (Fields/Line/Block/Section), not a hand-written write", pkgIdent.Name, fn.Sel.Name)))
					case bindings.ioNames[pkgIdent.Name] && fn.Sel.Name == "WriteString":
						violations = append(violations, violationAt(fset, entry.op, u.file, node.Pos(),
							fmt.Sprintf("calls %s.WriteString directly — human layout goes through a View primitive, not a hand-written write", pkgIdent.Name)))
					}
				case *ast.Ident:
					switch {
					case bindings.fmtDot && fprintFuncs[fn.Name]:
						violations = append(violations, violationAt(fset, entry.op, u.file, node.Pos(),
							fmt.Sprintf("calls %s directly (dot-imported fmt) — human layout goes through a View primitive, not a hand-written write", fn.Name)))
					case bindings.ioDot && fn.Name == "WriteString":
						violations = append(violations, violationAt(fset, entry.op, u.file, node.Pos(),
							"calls WriteString directly (dot-imported io) — human layout goes through a View primitive, not a hand-written write"))
					case funcMap[fn.Name].decl != nil && !visited[fn.Name]:
						visited[fn.Name] = true
						callee := funcMap[fn.Name]
						queue = append(queue, unit{body: callee.decl.Body, file: callee.file})
					}
				}
			case *ast.SelectorExpr:
				pkgIdent, ok := node.X.(*ast.Ident)
				if ok && bindings.osNames[pkgIdent.Name] && (node.Sel.Name == "Stdout" || node.Sel.Name == "Stderr") {
					violations = append(violations, violationAt(fset, entry.op, u.file, node.Pos(),
						fmt.Sprintf("references %s.%s directly — a human renderer draws through the View its caller built, never a bare stream", pkgIdent.Name, node.Sel.Name)))
				}
			case *ast.Ident:
				if bindings.osDot && (node.Name == "Stdout" || node.Name == "Stderr") {
					violations = append(violations, violationAt(fset, entry.op, u.file, node.Pos(),
						fmt.Sprintf("references %s directly (dot-imported os) — a human renderer draws through the View its caller built, never a bare stream", node.Name)))
				}
			}
			return true
		})
	}
	return violations, nil
}

func violationAt(fset *token.FileSet, op string, file parsedFile, pos token.Pos, detail string) HumanRenderViolation {
	return HumanRenderViolation{
		Op:     op,
		File:   file.rel,
		Line:   fset.Position(pos).Line,
		Detail: detail,
	}
}
