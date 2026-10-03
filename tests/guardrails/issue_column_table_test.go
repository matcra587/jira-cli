package guardrails

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Which sink renders which issue field is settled ONCE, in
// internal/cli/issue/columns.go, and every issue-scoped table is built
// from that one table.
//
// The pairing used to live inside the function that built `issue list`'s
// columns: KEY through cli.IssueKey, STATUS through cli.StatusPadded,
// TYPE through cli.Type, ASSIGNEE through cli.Entity, UPDATED through
// cli.Time. That is fine while there is one table. The second table is
// where it goes wrong, and quietly: a new command writes its own
// columns, reaches for cli.Text on a status because that is the sink it
// already knows, and ships a table whose STATUS column is plain while
// the neighboring command's is a colored pill. Nothing fails. The
// bytes are valid, the tests are green, and the inconsistency is
// discovered by a person noticing two commands disagree.
//
// So the rule is placement, and placement is what this checks: a
// table.Column literal in the issue package outside the shared file is a
// renderer building its own column, whatever sink it happened to pick.
// Selecting fields is the supported way to differ — `issue link list`
// draws five of the seven and adds one column of its own, all declared
// in columns.go — and that costs a reviewer one file to read rather
// than one per command.
//
// Written with the second table rather than after the third, for the
// reason pagination_placement_test.go states about its own invariant: a
// convention nothing enforces is the defect this codebase reproduces
// most reliably. Four more keyed reads are queued behind link list.

// issueColumnScanDirs covers the domain that owns issue fields. Other domains,
// such as account discovery, have different identities and column contracts;
// their tables cannot be projected through issueRow without inventing fields.
// Shared output sinks and human-output guards still cover every CLI package.
var issueColumnScanDirs = []string{"internal/cli/issue"}

// sharedColumnFile is the ONE file allowed to build columns, and
// primerTablePkg is the import path a `table.Column` must resolve to
// before it counts as one — an unrelated local type named Column in some
// other package is not this rule's business.
const (
	sharedColumnFile = "internal/cli/issue/columns.go"
	primerTablePkg   = "github.com/gechr/primer/table"
	columnTypeName   = "Column"
)

// TestIssueTableColumnsComeFromTheSharedTable fails any table.Column
// literal built outside the shared table's file, naming the offender.
func TestIssueTableColumnsComeFromTheSharedTable(t *testing.T) {
	root := repoRoot(t)

	var offenders, inShared []string
	for _, dir := range issueColumnScanDirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
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
			alias, imported := tableImportName(file)
			if !imported {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok || !isTableColumnType(lit.Type, alias) {
					return true
				}
				where := rel + ":" + strconv.Itoa(fset.Position(lit.Pos()).Line)
				if rel == sharedColumnFile {
					inShared = append(inShared, where)
					return true
				}
				offenders = append(offenders, where)
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("scanning %s: %v", dir, err)
		}
	}

	// Non-vacuity. A rename of the shared file, a switch to another table
	// library, or a refactor that stops using composite literals would
	// all leave the walk above finding nothing and reporting success —
	// the failure mode a placement guard is most prone to.
	if len(inShared) == 0 {
		t.Fatalf("no table.Column literal found in %s — either the shared column table moved "+
			"(update sharedColumnFile) or it no longer builds columns; this guard is blind either way", sharedColumnFile)
	}

	for _, offender := range offenders {
		t.Errorf("%s: builds a table.Column outside %s — which sink renders which issue field is settled once, there. "+
			"Select the fields this table needs (issueColumnsFor), or add the column to the shared table if it is genuinely new",
			offender, sharedColumnFile)
	}
}

// tableImportName returns the identifier primer/table is bound to in
// file, and whether it is imported at all. Resolved per file rather than
// assumed to be "table", so an aliased import is still caught and an
// unrelated package named table is not.
func tableImportName(file *ast.File) (string, bool) {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != primerTablePkg {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name, true
		}
		return filepath.Base(path), true
	}
	return "", false
}

// isTableColumnType reports whether a composite literal's type is
// table.Column[T] or []table.Column[T].
//
// Both forms are matched because both declare columns: a bare
// `table.Column[T]{...}` and the `[]table.Column[T]{ {...}, {...} }`
// slice whose elements carry no type of their own. Matching only the
// first would let the second — the shape a hand-written column set
// actually takes — through untouched.
func isTableColumnType(expr ast.Expr, alias string) bool {
	if arr, ok := expr.(*ast.ArrayType); ok {
		return isTableColumnType(arr.Elt, alias)
	}
	// A generic instantiation is an IndexExpr (one type argument);
	// IndexListExpr would be several, which table.Column does not take.
	idx, ok := expr.(*ast.IndexExpr)
	if !ok {
		return false
	}
	sel, ok := idx.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != columnTypeName {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == alias
}

// The placement scan must recognize real literals, including aliases and the
// implicit elements of a slice. These invalid placements keep the scan honest.
func TestIssueColumnGuardRecognizesForbiddenLiterals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, source string
		want         bool
	}{
		{name: "direct", source: `package issue; import "github.com/gechr/primer/table"; var bad = table.Column[int]{}`, want: true},
		{name: "aliased slice", source: `package issue; import cols "github.com/gechr/primer/table"; var bad = []cols.Column[int]{{}}`, want: true},
		{name: "unrelated column", source: `package issue; import "example.com/table"; var allowed = table.Column[int]{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "forbidden.go", tc.source, 0)
			if err != nil {
				t.Fatal(err)
			}
			alias, imported := tableImportName(file)
			found := false
			ast.Inspect(file, func(n ast.Node) bool {
				if lit, ok := n.(*ast.CompositeLit); ok && imported && isTableColumnType(lit.Type, alias) {
					found = true
				}
				return true
			})
			if found != tc.want {
				t.Fatalf("placement guard missed forbidden literal: found=%t", found)
			}
		})
	}
}
