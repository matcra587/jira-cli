//go:build flagmatrix

package matrix

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Corpus cross-reference: which pairs the audit corpus's recipes
// already pass on a real-binary invocation. This is INFORMATIONAL and
// never credits the proof registry, deliberately: a recipe passing
// --limit 2 proves the invocation parses and its envelope holds — it
// does not prove the flag's EFFECT was asserted, which is the matrix's
// bar. The census reports the two sets side by side so phase 2 can be
// split honestly: "exercised, unproved" is most of the worklist.
//
// The extraction is mechanical (go/ast over the committed recipe
// table, args resolved against the live tree the way cobra dispatch
// would), so it cannot drift into a hand-maintained claim; a recipe
// whose Args are built by code rather than string literals would be
// invisible to it, and scenariosArgs fails loudly on that instead of
// under-reporting silently.

// corpusExercised maps owner pair key → the audit scenario IDs whose
// argv passes that flag.
func corpusExercised() (map[string][]string, error) {
	rows, err := scenariosArgs(filepath.Join("..", "audit"))
	if err != nil {
		return nil, err
	}
	exercised := map[string][]string{}
	for _, row := range rows {
		cmd, _, err := liveTree().Find(row.args)
		if err != nil {
			// A recipe that no longer resolves is the audit suite's
			// problem to report; the census just skips it.
			continue
		}
		for _, token := range row.args {
			name, ok := strings.CutPrefix(token, "--")
			if !ok || name == "" {
				continue
			}
			name, _, _ = strings.Cut(name, "=")
			pair, ok := findPair(cmd.CommandPath(), name)
			if !ok {
				// Deliberate unknown-flag failure recipes land here.
				continue
			}
			key := pair.OwnerKey()
			if !contains(exercised[key], row.id) {
				exercised[key] = append(exercised[key], row.id)
			}
		}
	}
	// The harness itself appends --output to every capture and forces
	// --color for the human_color mode, so both root declarations are
	// exercised by construction rather than by any recipe's Args.
	for _, name := range []string{"output", "color"} {
		if pair, ok := findPair(liveTree().CommandPath(), name); ok {
			exercised[pair.OwnerKey()] = append(exercised[pair.OwnerKey()], "(harness-appended per mode)")
		}
	}
	return exercised, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// scenarioArgs is one recipe row as the census sees it.
type scenarioArgs struct {
	id   string
	args []string
}

// scenariosArgs extracts (ID, Args) from the audit recipe table's
// source, resolving package string consts (the recipes name fixture
// IDs like corpusLinkPhrase by const). Anything it cannot resolve is
// an error, not a skip: the census's value is that it cannot silently
// under-count.
func scenariosArgs(dir string) ([]scenarioArgs, error) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var files []*ast.File
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", entry.Name(), err)
		}
		files = append(files, file)
	}
	consts := packageStringConsts(files)
	var table *ast.CompositeLit
	for _, file := range files {
		if lit := scenariosTable(file); lit != nil {
			table = lit
			break
		}
	}
	if table == nil {
		return nil, fmt.Errorf("%s declares no `var scenarios = []Scenario{...}` table", dir)
	}
	var rows []scenarioArgs
	for _, elem := range table.Elts {
		lit, ok := elem.(*ast.CompositeLit)
		if !ok {
			return nil, fmt.Errorf("%s: scenarios element is not a composite literal", dir)
		}
		row := scenarioArgs{}
		for _, field := range lit.Elts {
			kv, ok := field.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			switch key.Name {
			case "ID":
				row.id, err = stringExpr(kv.Value, consts)
			case "Args":
				row.args, err = stringSliceLit(kv.Value, consts)
			}
			if err != nil {
				return nil, fmt.Errorf("%s: scenario %q: %w", dir, row.id, err)
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// packageStringConsts collects every top-level `name = "literal"` const
// in the package, so a recipe arg spelled by const resolves the same
// way the compiler resolves it.
func packageStringConsts(files []*ast.File) map[string]string {
	consts := map[string]string{}
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok || len(value.Names) != len(value.Values) {
					continue
				}
				for i, name := range value.Names {
					if lit, ok := value.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if s, err := strconv.Unquote(lit.Value); err == nil {
							consts[name.Name] = s
						}
					}
				}
			}
		}
	}
	return consts
}

func scenariosTable(file *ast.File) *ast.CompositeLit {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != "scenarios" || len(value.Values) != 1 {
				continue
			}
			if lit, ok := value.Values[0].(*ast.CompositeLit); ok {
				return lit
			}
		}
	}
	return nil
}

// stringExpr resolves a string literal or a named package const.
func stringExpr(expr ast.Expr, consts map[string]string) (string, error) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", fmt.Errorf("expected a string literal, got %s", e.Kind)
		}
		return strconv.Unquote(e.Value)
	case *ast.Ident:
		if s, ok := consts[e.Name]; ok {
			return s, nil
		}
		return "", fmt.Errorf("identifier %s is not a package string const the census can resolve", e.Name)
	default:
		return "", fmt.Errorf("expected a string literal or const, got %T", expr)
	}
}

func stringSliceLit(expr ast.Expr, consts map[string]string) ([]string, error) {
	if ident, ok := expr.(*ast.Ident); ok && ident.Name == "nil" {
		return nil, nil
	}
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, fmt.Errorf("expected a []string literal, got %T — the census cannot see computed args", expr)
	}
	out := make([]string, 0, len(lit.Elts))
	for _, elem := range lit.Elts {
		s, err := stringExpr(elem, consts)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
