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

// Asking a terminal what color its background is costs 5108ms against
// one that does not answer.
//
// The query is OSC 11, sent by termenv on the way out of clib's
// theme.Auto and gechr/x's terminal.IsDark, and it waits for a reply
// bounded only by termenv.OSCTimeout — five seconds. A terminal that
// does not implement the reply pays all of it, on EVERY command,
// `version` and `--help` included, because the call happens while the
// command tree is built rather than while anything is drawn.
//
// internal/cli resolves the theme from the environment instead, which
// is why it never returns nil. This guard exists because that fix was
// once applied to the root command alone, and a confirmation dialog in
// internal/cli/auth went on calling theme.Auto() directly — the same
// stall, restored, in the same commit that wrote the rule against it.
// One resolution, no probes, checked rather than remembered.
//
// Not lintable: depguard bans a package, and both packages are needed
// for the theme types the resolution itself returns. What must not
// happen is the CALL.

// probeDirs are the trees this applies to: all production code.
var probeDirs = []string{"internal", "cmd"}

// probes are the entry points that reach the OSC 11 query. Keyed by
// import path, each holding the functions on it that probe — the other
// exports of these packages (theme.Dark, theme.Names, terminal.Is) do
// no I/O and stay allowed.
var probes = map[string][]string{
	"github.com/gechr/clib/theme": {"Auto", "DetectBackground"},
	"github.com/gechr/x/terminal": {"IsDark", "IsLight"},
	// lipgloss carries its OWN implementation of the same query,
	// reached through neither of the above. Nothing calls it today; it
	// is here because internal/cli/auth/form.go already imports
	// lipgloss for styling, which makes it the likeliest place for the
	// next well-meaning probe to appear.
	"charm.land/lipgloss/v2": {"HasDarkBackground", "BackgroundColor"},
}

// callsTerminalProbe reports whether the Go file at path calls one, and
// which. Resolved over the parsed AST against the file's imports, so an
// aliased import is caught and the same name in a comment, a string, or
// a method on an unrelated type is not — the properties a text scan
// cannot have.
func callsTerminalProbe(path string, src []byte) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
	if err != nil {
		return nil, err
	}

	// Local name of each probing import, plus the functions to watch
	// for through it. A dot-import loses the qualifier, so its
	// functions are collected under the empty name and matched bare.
	watched := make(map[string]map[string]bool)
	for _, imp := range file.Imports {
		if imp.Path == nil {
			continue
		}
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		funcs, probing := probes[p]
		if !probing {
			continue
		}
		local := path4Default(p)
		switch {
		case imp.Name == nil:
		case imp.Name.Name == ".":
			local = ""
		case imp.Name.Name == "_":
			// Blank import: nothing can be called through it.
			continue
		default:
			local = imp.Name.Name
		}
		if watched[local] == nil {
			watched[local] = make(map[string]bool)
		}
		for _, fn := range funcs {
			watched[local][fn] = true
		}
	}
	if len(watched) == 0 {
		return nil, nil
	}

	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			pkg, ok := node.X.(*ast.Ident)
			if !ok {
				return true
			}
			if watched[pkg.Name][node.Sel.Name] {
				found = append(found, pkg.Name+"."+node.Sel.Name)
			}
		case *ast.Ident:
			if watched[""][node.Name] {
				found = append(found, node.Name)
			}
		}
		return true
	})
	return found, nil
}

// path4Default returns the name an import path binds to when it is not
// aliased.
//
// Usually the last segment — but NOT for a versioned module path:
// "charm.land/lipgloss/v2" binds to lipgloss, not v2. Taking the last
// segment blindly made this guard silently blind to every lipgloss
// call, which the fixture below caught and the real walk could not
// have. A trailing "vN" is a version, so the name is the segment
// before it.
func path4Default(p string) string {
	segments := strings.Split(p, "/")
	last := len(segments) - 1
	if last > 0 && isVersionSegment(segments[last]) {
		last--
	}
	return segments[last]
}

// isVersionSegment reports whether a path segment is a module major
// version: "v" followed by digits, and at least one digit.
func isVersionSegment(s string) bool {
	rest, ok := strings.CutPrefix(s, "v")
	if !ok || rest == "" {
		return false
	}
	return strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' }) < 0
}

func TestNothingAsksTheTerminalForItsBackground(t *testing.T) {
	t.Parallel()
	rootDir := repoRoot(t)

	var offenders []string
	for _, dir := range probeDirs {
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
			calls, err := callsTerminalProbe(path, body)
			if err != nil {
				return err
			}
			if len(calls) == 0 {
				return nil
			}
			rel, err := filepath.Rel(rootDir, path)
			if err != nil {
				return err
			}
			for _, call := range calls {
				offenders = append(offenders, filepath.ToSlash(rel)+" calls "+call)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}

	for _, offender := range offenders {
		t.Errorf("%s — that probes the terminal over OSC 11 and blocks up to five seconds on one that does not answer; use cli.ResolvedTheme(), which reads the environment instead", offender)
	}
}

// TestCallsTerminalProbeBindsToTheImport keeps the scan above honest.
//
// The real walk expects to find NOTHING, so a detector that quietly
// stopped matching would pass exactly as loudly as a clean tree. These
// cases are the only thing standing between the two.
func TestCallsTerminalProbeBindsToTheImport(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want bool
	}{
		{
			name: "plain call",
			src:  "package p\nimport \"github.com/gechr/clib/theme\"\nfunc f() { _ = theme.Auto() }",
			want: true,
		},
		{
			name: "aliased import",
			src:  "package p\nimport ct \"github.com/gechr/clib/theme\"\nfunc f() { _ = ct.Auto() }",
			want: true,
		},
		{
			name: "the other package",
			src:  "package p\nimport \"github.com/gechr/x/terminal\"\nfunc f() { _, _ = terminal.IsDark() }",
			want: true,
		},
		{
			name: "dot import loses the qualifier",
			src:  "package p\nimport . \"github.com/gechr/clib/theme\"\nfunc f() { _ = Auto() }",
			want: true,
		},
		{
			name: "a non-probing export of the same package",
			src:  "package p\nimport \"github.com/gechr/clib/theme\"\nfunc f() { _ = theme.Dark() }",
			want: false,
		},
		{
			name: "the name on an unrelated package",
			src:  "package p\nimport \"other/theme\"\nfunc f() { _ = theme.Auto() }",
			want: false,
		},
		{
			name: "the name in a comment",
			src:  "package p\nimport \"github.com/gechr/clib/theme\"\n// theme.Auto() is what we do NOT do.\nfunc f() { _ = theme.Names() }",
			want: false,
		},
		{
			name: "the name in a string",
			src:  "package p\nimport \"github.com/gechr/clib/theme\"\nfunc f() { _ = \"theme.Auto()\"; _ = theme.Names() }",
			want: false,
		},
		{
			name: "lipgloss carries its own probe",
			src:  "package p\nimport \"charm.land/lipgloss/v2\"\nfunc f() { _ = lipgloss.HasDarkBackground(nil, nil) }",
			want: true,
		},
		{
			name: "lipgloss styling is untouched",
			src:  "package p\nimport \"charm.land/lipgloss/v2\"\nfunc f() { _ = lipgloss.NewStyle() }",
			want: false,
		},
		{
			name: "blank import cannot call",
			src:  "package p\nimport _ \"github.com/gechr/clib/theme\"\nfunc f() {}",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := callsTerminalProbe("fixture.go", []byte(tt.src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if (len(got) > 0) != tt.want {
				t.Errorf("found %v, want detected=%v", got, tt.want)
			}
		})
	}
}
