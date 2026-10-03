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

// All envelope bytes leave the process through output.encodeJSON: it is
// where preservation-grade JSON encoding and the WriteError mapping
// (broken pipes must never be silently lost) are concentrated. A command or
// renderer constructing its own json.NewEncoder gets none of that — the failure mode is
// silent, so the convention is a build check (depguard cannot help:
// encoding/json is stdlib and allowed). The wire tiers are out of
// scope on purpose: internal/adapters and internal/jira encode request
// bodies, not envelopes.

// envelopeLayerDirs are the trees that build and emit envelopes,
// relative to the repo root: the output core, the whole command layer,
// the contract types, and the entry point.
var envelopeLayerDirs = []string{
	"internal/output",
	"internal/cli",
	"internal/envelope",
	"cmd",
}

// sanctionedEncoderFile is the ONE file allowed to construct a JSON
// encoder: the renderer that owns encodeJSON.
const sanctionedEncoderFile = "internal/output/render.go"

// encoderPkg and encoderFunc name the constructor the scan hunts. The
// match is made over the parsed AST rather than the file's text, so it
// binds to the encoding/json IMPORT rather than to the spelling at the
// call site: `import stdjson "encoding/json"` followed by
// stdjson.NewEncoder is caught, and the same token appearing in a
// comment or a string is not. A substring scan got both of those
// backwards.
const (
	encoderPkg            = "encoding/json"
	encoderFunc           = "NewEncoder"
	preservingEncoderPkg  = "github.com/matcra587/jira-cli/internal/jira"
	preservingEncoderFunc = "MarshalNoEscape"
)

// callsRawEncoder reports whether the Go file at path constructs a JSON
// encoder, resolving the local name of the encoding/json import first. A
// dot-import is treated as a match on the bare function name, since it
// makes the package qualifier disappear entirely.
func callsRawEncoder(path string, src []byte) (bool, error) {
	return usesPackageFunction(path, src, encoderPkg, encoderFunc, "")
}

// An empty caller checks references throughout the file, including constructor
// aliases. A named caller checks actual calls inside that function's body.
func usesPackageFunction(path string, src []byte, pkgPath, function, caller string) (bool, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
	if err != nil {
		return false, err
	}
	names := make(map[string]bool)
	dotImported := false
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
			names[filepath.Base(pkgPath)] = true
		case imp.Name.Name == ".":
			dotImported = true
		case imp.Name.Name == "_":
			// Blank import: nothing can be called through it.
		default:
			names[imp.Name.Name] = true
		}
	}
	if len(names) == 0 && !dotImported {
		return false, nil
	}
	var scope ast.Node = file
	if caller != "" {
		scope = nil
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == caller && fn.Body != nil {
				scope = fn.Body
				break
			}
		}
		if scope == nil {
			return false, nil
		}
	}

	found := false
	ast.Inspect(scope, func(n ast.Node) bool {
		if found {
			return false
		}
		if caller != "" {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			n = call.Fun
		}
		switch node := n.(type) {
		case *ast.SelectorExpr:
			if node.Sel.Name != function {
				return true
			}
			if pkg, ok := node.X.(*ast.Ident); ok && names[pkg.Name] {
				found = true
			}
		case *ast.Ident:
			if dotImported && node.Name == function {
				found = true
			}
		}
		return true
	})
	return found, nil
}

func TestEnvelopeEncodingStaysInTheOneWriter(t *testing.T) {
	t.Parallel()
	rootDir := repoRoot(t)

	sanctionedSeen := false
	var offenders []string
	for _, dir := range envelopeLayerDirs {
		err := filepath.WalkDir(filepath.Join(rootDir, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(rootDir, path)
			if err != nil {
				return err
			}
			if filepath.ToSlash(rel) == sanctionedEncoderFile {
				sanctionedSeen, err = usesPackageFunction(path, body, preservingEncoderPkg, preservingEncoderFunc, "encodeJSON")
				return err
			}
			calls, err := callsRawEncoder(path, body)
			if err != nil {
				return err
			}
			if !calls {
				return nil
			}
			offenders = append(offenders, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}

	for _, offender := range offenders {
		t.Errorf("%s constructs a raw json encoder — envelope bytes go through output.Render/encodeJSON, which owns byte fidelity and the write-failure mapping", offender)
	}
	// Keep the sanctioned writer tied to the preservation-grade serializer.
	if !sanctionedSeen {
		t.Errorf("%s no longer contains jira.%s — the sanctioned writer moved; update this guard to follow it", sanctionedEncoderFile, preservingEncoderFunc)
	}
}

// TestCallsRawEncoderBindsToTheImport is the fixture that keeps the scan
// above honest. The guard's real walk finds no unauthorized constructor,
// so a detector that silently stopped matching could accept a bypass.
// These cases pin the property that a
// text scan cannot have: the match follows the encoding/json import
// through whatever local name it is given, and ignores the token
// wherever it is not a call.
func TestCallsRawEncoderBindsToTheImport(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want bool
	}{
		{
			name: "plain import",
			src:  "package p\nimport (\"encoding/json\"\n\"io\")\nfunc f(w io.Writer) { _ = json.NewEncoder(w) }\n",
			want: true,
		},
		{
			name: "aliased import defeats a text scan",
			src:  "package p\nimport (enc \"encoding/json\"\n\"io\")\nfunc f(w io.Writer) { _ = enc.NewEncoder(w) }\n",
			want: true,
		},
		{
			name: "dot import erases the qualifier",
			src:  "package p\nimport (. \"encoding/json\"\n\"io\")\nfunc f(w io.Writer) { _ = NewEncoder(w) }\n",
			want: true,
		},
		{
			name: "same call on a different package is not ours",
			src:  "package p\nimport (\"encoding/xml\"\n\"io\")\nfunc f(w io.Writer) { _ = xml.NewEncoder(w) }\n",
			want: false,
		},
		{
			name: "the token in a comment is prose, not a call",
			src:  "package p\nimport \"encoding/json\"\n// json.NewEncoder is what this file must not build.\nvar _ = json.Marshal\n",
			want: false,
		},
		{
			name: "the token in a string is data, not a call",
			src:  "package p\nimport \"encoding/json\"\nvar msg = \"json.NewEncoder is banned here\"\nvar _ = json.Marshal\n",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := callsRawEncoder(tt.name+".go", []byte(tt.src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got != tt.want {
				t.Errorf("callsRawEncoder = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPreservingEncoderBindsToTheImport(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, src string
		want      bool
	}{
		{"plain", `package p; import "github.com/matcra587/jira-cli/internal/jira"; func encodeJSON() { _, _ = jira.MarshalNoEscape(nil) }`, true},
		{"aliased", `package p; import wire "github.com/matcra587/jira-cli/internal/jira"; func encodeJSON() { _, _ = wire.MarshalNoEscape(nil) }`, true},
		{"dot", `package p; import . "github.com/matcra587/jira-cli/internal/jira"; func encodeJSON() { _, _ = MarshalNoEscape(nil) }`, true},
		{"unused binding", `package p; import "github.com/matcra587/jira-cli/internal/jira"; var _ = jira.MarshalNoEscape; func encodeJSON() {}`, false},
		{"call outside writer", `package p; import "github.com/matcra587/jira-cli/internal/jira"; func encodeBody() { _, _ = jira.MarshalNoEscape(nil) }; func encodeJSON() {}`, false},
		{"binding inside writer", `package p; import "github.com/matcra587/jira-cli/internal/jira"; func encodeJSON() { _ = jira.MarshalNoEscape }`, false},
		{"other package", `package p; import "example.test/jira"; var _ = jira.MarshalNoEscape`, false},
		{"comment", "package p\nimport \"github.com/matcra587/jira-cli/internal/jira\"\n// jira.MarshalNoEscape\nvar _ = jira.NullableOf[string]\n", false},
		{"string", `package p; import "github.com/matcra587/jira-cli/internal/jira"; var _ = "jira.MarshalNoEscape"; var _ = jira.NullableOf[string]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := usesPackageFunction(tc.name+".go", []byte(tc.src), preservingEncoderPkg, preservingEncoderFunc, "encodeJSON")
			if err != nil || got != tc.want {
				t.Fatalf("preserving encoder reference=%t, want %t; error=%v", got, tc.want, err)
			}
		})
	}
}

// repoRoot resolves the module root from the suite's working directory
// and refuses to guess — a wrong root would scan nothing and pass.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("resolve repo root: %s has no go.mod: %v", dir, err)
	}
	return dir
}
