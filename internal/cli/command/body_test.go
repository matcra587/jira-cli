package command

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
)

// newBodyCmd registers the body surface on a throwaway command and hands
// back both, so a test drives the REAL registrar rather than a
// hand-built BodyFlags — which would leave the private spellings empty
// and make every refusal name "--".
func newBodyCmd(t *testing.T, opts ...BodyOption) (*cobra.Command, *BodyFlags) {
	t.Helper()
	cmd := &cobra.Command{Use: "add"}
	body := &BodyFlags{}
	AddBodyFlags(cmd, body, opts...)
	return cmd, body
}

// parse runs cobra's own flag parsing, which is what alias folding needs
// to see (Changed is only meaningful after a parse).
func parse(t *testing.T, cmd *cobra.Command, args ...string) {
	t.Helper()
	if err := cmd.Flags().Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
}

// inputError pulls the typed input error out of err, failing the test
// when it is absent — every refusal in this file must be classified, not
// a bare error that would fall through to the untyped-validation
// fallback.
func inputError(t *testing.T, err error) *cli.InputError {
	t.Helper()
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	var e *cli.InputError
	if !errors.As(err, &e) {
		t.Fatalf("error %v (%T) is not a *cli.InputError", err, err)
	}
	return e
}

func TestAddBodyFlagsRegistersCanonicalAndHidesAliases(t *testing.T) {
	cmd, _ := newBodyCmd(t)
	fs := cmd.Flags()

	for _, name := range []string{FlagMarkdown, FlagMarkdownFile, FlagJSONInput} {
		f := fs.Lookup(name)
		if f == nil {
			t.Fatalf("--%s not registered", name)
		}
		if f.Hidden {
			t.Errorf("--%s is hidden; a canonical flag must appear in help and schema", name)
		}
	}

	for _, a := range BodyFlagAliases {
		f := fs.Lookup(a.Flag)
		if f == nil {
			t.Fatalf("alias --%s not registered", a.Flag)
		}
		if !f.Hidden {
			t.Errorf("alias --%s is visible; §2 requires aliases be absent from help, schema and completions", a.Flag)
		}
	}
}

func TestAsCommentRenamesTheMarkdownPairOnly(t *testing.T) {
	cmd, _ := newBodyCmd(t, AsComment())
	fs := cmd.Flags()

	if fs.Lookup("comment") == nil || fs.Lookup("comment-file") == nil {
		t.Fatal("AsComment did not register --comment/--comment-file")
	}
	// --json-input keeps its name: it is named for its purpose already.
	if fs.Lookup(FlagJSONInput) == nil {
		t.Error("--json-input was renamed; only the Markdown pair should move")
	}
	// 0.15's own spelling survives as a hidden alias, which is the whole
	// bargain of the rename — an existing invocation keeps working.
	md := fs.Lookup(FlagMarkdown)
	if md == nil {
		t.Fatal("--markdown should survive as a hidden alias on a renamed bundle")
	}
	if !md.Hidden {
		t.Error("--markdown should be HIDDEN on a renamed bundle, not a second documented spelling")
	}
}

func TestBodyConflictRefusesTwoRoutes(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		names [2]string
	}{
		{
			name:  "adf and inline markdown",
			args:  []string{"--json-input", "b.json", "--markdown", "hi"},
			names: [2]string{FlagJSONInput, FlagMarkdown},
		},
		{
			name:  "adf and markdown file",
			args:  []string{"--json-input", "b.json", "--markdown-file", "b.md"},
			names: [2]string{FlagJSONInput, FlagMarkdownFile},
		},
		{
			name:  "both markdown routes",
			args:  []string{"--markdown", "hi", "--markdown-file", "b.md"},
			names: [2]string{FlagMarkdown, FlagMarkdownFile},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, body := newBodyCmd(t)
			parse(t, cmd, tt.args...)

			e := inputError(t, body.Conflict())
			if e.Code != cli.CodeFlagConflict {
				t.Errorf("code = %q, want %q", e.Code, cli.CodeFlagConflict)
			}
			for _, want := range tt.names {
				if !strings.Contains(e.Message, "--"+want) {
					t.Errorf("message %q does not name --%s", e.Message, want)
				}
			}
			if e.Field != tt.names[0] {
				t.Errorf("Field = %q, want %q", e.Field, tt.names[0])
			}
		})
	}
}

func TestBodyConflictAllowsOneRoute(t *testing.T) {
	for _, args := range [][]string{
		{"--markdown", "hi"},
		{"--markdown-file", "b.md"},
		{"--json-input", "b.json"},
		{},
	} {
		cmd, body := newBodyCmd(t)
		parse(t, cmd, args...)
		if err := body.Conflict(); err != nil {
			t.Errorf("Conflict(%v) = %v, want nil", args, err)
		}
	}
}

// TestBodyAliasesFoldIntoCanonical pins the half that makes the aliases
// worth having: the value has to land in the canonical flag's own bound
// variable, or the command reads an empty body and the alias silently
// does nothing.
func TestBodyAliasesFoldIntoCanonical(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		field func(*BodyFlags) string
		want  string
	}{
		{"body to markdown", []string{"--body", "hello"}, func(b *BodyFlags) string { return b.Markdown }, "hello"},
		{"description to markdown", []string{"--description", "hello"}, func(b *BodyFlags) string { return b.Markdown }, "hello"},
		{"body-file to markdown-file", []string{"--body-file", "n.md"}, func(b *BodyFlags) string { return b.MarkdownFile }, "n.md"},
		{"template to markdown-file", []string{"--template", "n.md"}, func(b *BodyFlags) string { return b.MarkdownFile }, "n.md"},
		{"from-json to json-input", []string{"--from-json", "n.json"}, func(b *BodyFlags) string { return b.JSONInput }, "n.json"},
		{"body-adf to json-input", []string{"--body-adf", "n.json"}, func(b *BodyFlags) string { return b.JSONInput }, "n.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, body := newBodyCmd(t)
			parse(t, cmd, tt.args...)
			if err := body.ResolveAliases(cmd.Flags()); err != nil {
				t.Fatalf("ResolveAliases: %v", err)
			}
			if got := tt.field(body); got != tt.want {
				t.Errorf("folded value = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBodyAliasConflictIsSeenByTheConflictRule is the ordering guard.
// ResolveAliases must run BEFORE Conflict, or an alias-spelled route
// slips past a check that only inspects the canonical fields — and the
// caller gets two bodies merged instead of a refusal.
func TestBodyAliasConflictIsSeenByTheConflictRule(t *testing.T) {
	cmd, body := newBodyCmd(t)
	parse(t, cmd, "--body", "hello", "--json-input", "b.json")

	if err := body.ResolveAliases(cmd.Flags()); err != nil {
		t.Fatalf("ResolveAliases: %v", err)
	}
	e := inputError(t, body.Conflict())
	if e.Code != cli.CodeFlagConflict {
		t.Errorf("code = %q, want %q", e.Code, cli.CodeFlagConflict)
	}
}

// TestBodyRefusalNeverNamesAnAlias is §2's rule, and it is the one a
// later edit is most likely to break by reaching for the flag the caller
// typed. An alias is absent from help, schema and completions, so naming
// one sends an introspecting agent hunting a flag it was never shown.
func TestBodyRefusalNeverNamesAnAlias(t *testing.T) {
	cmd, body := newBodyCmd(t)
	parse(t, cmd, "--body", "hello", "--from-json", "b.json")

	if err := body.ResolveAliases(cmd.Flags()); err != nil {
		t.Fatalf("ResolveAliases: %v", err)
	}
	e := inputError(t, body.Conflict())
	for _, alias := range []string{"body", "from-json"} {
		if strings.Contains(e.Message, "--"+alias) {
			t.Errorf("message names the hidden alias --%s: %q", alias, e.Message)
		}
	}
	if !strings.Contains(e.Message, "--"+FlagJSONInput) || !strings.Contains(e.Message, "--"+FlagMarkdown) {
		t.Errorf("message should name both canonical flags: %q", e.Message)
	}
}

// TestBodyAliasDisagreementRefused pins the other half of the alias
// rule: agreeing signals are fine, disagreeing ones are refused rather
// than resolved by precedence.
func TestBodyAliasDisagreementRefused(t *testing.T) {
	t.Run("disagreeing", func(t *testing.T) {
		cmd, body := newBodyCmd(t)
		parse(t, cmd, "--markdown", "one", "--body", "two")

		e := inputError(t, body.ResolveAliases(cmd.Flags()))
		if e.Code != cli.CodeFlagConflict {
			t.Errorf("code = %q, want %q", e.Code, cli.CodeFlagConflict)
		}
		if strings.Contains(e.Message, "--body") {
			t.Errorf("message names the hidden alias: %q", e.Message)
		}
	})

	t.Run("agreeing", func(t *testing.T) {
		cmd, body := newBodyCmd(t)
		parse(t, cmd, "--markdown", "same", "--body", "same")

		if err := body.ResolveAliases(cmd.Flags()); err != nil {
			t.Errorf("agreeing signals should be accepted, got %v", err)
		}
		if body.Markdown != "same" {
			t.Errorf("Markdown = %q, want %q", body.Markdown, "same")
		}
	})
}

func TestRequireBodyNamesEveryRoute(t *testing.T) {
	cmd, body := newBodyCmd(t)
	parse(t, cmd)

	e := inputError(t, body.RequireBody("a comment"))
	if e.Code != cli.CodeFlagRequired {
		t.Errorf("code = %q, want %q", e.Code, cli.CodeFlagRequired)
	}
	for _, name := range []string{FlagMarkdown, FlagMarkdownFile, FlagJSONInput} {
		if !strings.Contains(e.Message, "--"+name) {
			t.Errorf("message %q does not offer --%s", e.Message, name)
		}
	}
}

// TestResolveReachesTheEncoderFromStdin pins the stdin plumbing end to
// end: text piped in arrives in the converted document, which is the
// assertion the pre-converter version of this test stood in for.
func TestResolveReachesTheEncoderFromStdin(t *testing.T) {
	cmd, body := newBodyCmd(t)
	parse(t, cmd, "--markdown-file", StdinPath)

	got := mustResolve(t, body, strings.NewReader("piped body"), false)
	if !strings.Contains(string(got.ADF), "piped body") {
		t.Errorf("ADF = %s, want the piped text carried into the document", got.ADF)
	}
	if got.Route != RouteMarkdown {
		t.Errorf("Route = %q, want %q", got.Route, RouteMarkdown)
	}
}

// TestMarkdownRoutesConvert replaces the interim refusal test the
// pre-converter tree carried: both Markdown routes now reach the
// converter and come back with a document, on the Markdown route.
func TestMarkdownRoutesConvert(t *testing.T) {
	file := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(file, []byte("from a file"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		args []string
		want string
	}{
		{args: []string{"--markdown", "inline text"}, want: "inline text"},
		{args: []string{"--markdown-file", file}, want: "from a file"},
	} {
		t.Run(tt.args[0], func(t *testing.T) {
			cmd, body := newBodyCmd(t)
			parse(t, cmd, tt.args...)

			got := mustResolve(t, body, strings.NewReader(""), false)
			if !strings.Contains(string(got.ADF), tt.want) {
				t.Errorf("ADF = %s, want it to carry %q", got.ADF, tt.want)
			}
			if got.Route != RouteMarkdown {
				t.Errorf("Route = %q, want %q", got.Route, RouteMarkdown)
			}
		})
	}
}

// TestResolveRefusesStdinFromATerminal: draining a terminal would sit
// with no prompt, which reads as a hang. The same refusal auth login's
// token read makes.
func TestResolveRefusesStdinFromATerminal(t *testing.T) {
	cmd, body := newBodyCmd(t)
	parse(t, cmd, "--markdown-file", StdinPath)

	e := inputError(t, mustFail(t, body, strings.NewReader(""), true))
	if e.Field != FlagMarkdownFile {
		t.Errorf("Field = %q, want %q", e.Field, FlagMarkdownFile)
	}
}

// mustFail runs Resolve expecting a failure, so the terminal test above
// reads as one assertion rather than three lines of plumbing.
// mustResolve is mustFail's success half: Resolve is expected to work,
// and a failure here fails the test rather than being returned.
func mustResolve(t *testing.T, body *BodyFlags, stdin *strings.Reader, isTerminal bool) Body {
	t.Helper()
	got, err := body.Resolve(t.Context(), stdin, isTerminal)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return got
}

func mustFail(t *testing.T, body *BodyFlags, stdin *strings.Reader, isTerminal bool) error {
	t.Helper()
	got, err := body.Resolve(t.Context(), stdin, isTerminal)
	if err == nil {
		t.Fatalf("Resolve succeeded with %q, want a refusal", got.ADF)
	}
	return err
}

func TestResolveRefusesAnOversizedBody(t *testing.T) {
	cmd, body := newBodyCmd(t)
	parse(t, cmd, "--markdown-file", StdinPath)

	e := inputError(t, mustFail(t, body, strings.NewReader(strings.Repeat("x", maxBodyBytes+1)), false))
	if e.Code != cli.CodeFlagValueInvalid {
		t.Errorf("code = %q, want %q", e.Code, cli.CodeFlagValueInvalid)
	}
	// Truncating would be the silent alternative, and the caller would
	// discover it by reading what Jira stored.
	if !strings.Contains(e.Message, "larger than") {
		t.Errorf("message should say the body was too large: %q", e.Message)
	}
}

// TestResolveRefusesAMissingFile pins the classification, which is the
// interesting half: a file that is not there means the VALUE is wrong, not
// that a local file operation failed.
//
// It used to be CodeLocalIO, whose static hint talks about free space and
// the permissions on the config directory. Neither has anything to do with
// the commonest mistake on this flag — passing inline JSON where a path
// belongs — and a hint that sends someone to inspect their disk is worse
// than no hint. A genuine I/O failure (a permission denial on a file that
// DOES exist) is still CodeLocalIO, where that hint fits; see openFailure.
func TestResolveRefusesAMissingFile(t *testing.T) {
	cmd, body := newBodyCmd(t)
	parse(t, cmd, "--markdown-file", "/nonexistent/body.md")

	e := inputError(t, mustFail(t, body, strings.NewReader(""), false))
	if e.Code != cli.CodeFlagValueInvalid {
		t.Errorf("code = %q, want %q", e.Code, cli.CodeFlagValueInvalid)
	}
	if e.Field != FlagMarkdownFile {
		t.Errorf("Field = %q, want %q", e.Field, FlagMarkdownFile)
	}
	// The message carries the recovery the static hint cannot, because
	// hints never interpolate: it has to name both things the flag takes.
	if !strings.Contains(e.Message, StdinPath) {
		t.Errorf("message should offer %q for standard input: %q", StdinPath, e.Message)
	}
}

// TestResolveValidatesJSONInputLocally pins that a malformed ADF
// document fails as the CALLER's input, before any client exists —
// rather than traveling to Jira and coming back as a 400 about a
// payload the caller never wrote.
func TestResolveValidatesJSONInputLocally(t *testing.T) {
	path := writeTemp(t, "body.json", `{"type":"doc"}`) // no version key
	cmd, body := newBodyCmd(t)
	parse(t, cmd, "--json-input", path)

	got, err := body.Resolve(t.Context(), strings.NewReader(""), false)
	if err == nil {
		t.Fatalf("Resolve accepted an invalid ADF document: %s", got.ADF)
	}
	e := inputError(t, err)
	if e.Code != cli.CodeFlagValueInvalid {
		t.Errorf("code = %q, want %q", e.Code, cli.CodeFlagValueInvalid)
	}
	if e.Field != FlagJSONInput {
		t.Errorf("Field = %q, want %q", e.Field, FlagJSONInput)
	}
}

func TestResolveAcceptsAValidADFDocument(t *testing.T) {
	const doc = `{"type":"doc","version":1,"content":[]}`
	path := writeTemp(t, "body.json", doc)
	cmd, body := newBodyCmd(t)
	parse(t, cmd, "--json-input", path)

	got, err := body.Resolve(t.Context(), strings.NewReader(""), false)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Route != RouteADF {
		t.Errorf("Route = %q, want %q", got.Route, RouteADF)
	}
	// The ADF route is the lossless one, so the bytes must arrive
	// unaltered — this is the property that makes it worth having.
	if string(got.ADF) != doc {
		t.Errorf("ADF = %s, want it byte-identical to the file: %s", got.ADF, doc)
	}
	// Lossless by definition — the property that makes ADF the canonical
	// route. A converter landing later must not start reporting losses
	// here: this route never converts anything.
	if len(got.Lossy) != 0 {
		t.Errorf("the ADF route reported %d lossy warnings; it is lossless by definition", len(got.Lossy))
	}
}

// TestResolveNoRouteIsNotAnError: Resolve permits an absent body and
// leaves the requirement to the command, because a comment needs one and
// an edit may not.
func TestResolveNoRouteIsNotAnError(t *testing.T) {
	cmd, body := newBodyCmd(t)
	parse(t, cmd)

	got, err := body.Resolve(t.Context(), strings.NewReader(""), false)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Route != RouteNone || got.ADF != nil {
		t.Errorf("got %+v, want an empty body on the none route", got)
	}
}

// writeTemp writes content to a file in the test's own temp dir and
// returns its path.
func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}
