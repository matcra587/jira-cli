package root

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/completion/native"
)

func TestCurrentWordProtocolDecodesObservedBashTokensWithoutExpansion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ raw, want string }{
		{raw: "plain", want: "plain"},
		{raw: `candidate\ space`, want: "candidate space"},
		{raw: `'a,"b,c`, want: `a,"b,c`},
		{raw: `a,\"b,c`, want: `a,"b,c`},
		{raw: `'file$name`, want: "file$name"},
		{raw: `"file$HOME;$(command)`, want: "file$HOME;$(command)"},
		{raw: `'literal\`, want: `literal\`},
		{raw: `''`, want: ""},
		{raw: `$'candidate caf\303\251/'lea`, want: "candidate café/lea"},
		{raw: `$'candidate\'quote\303\251'`, want: "candidate'quoteé"},
		{raw: `$'candidate\u00e9'`, want: "candidateé"},
		{raw: `$'unfinished\303\251`, want: "unfinishedé"},
		{raw: `'$'\''literal'`, want: "$'literal"},
	} {
		word, args, ok := completionWord("bash", []string{native.WordArgument + tc.raw, "--file=one", "--", "KEY-1"})
		if !ok || word != tc.want || !reflect.DeepEqual(args, []string{"--file=one", "--", "KEY-1"}) {
			t.Fatal("current-word transport changed literal quoting or context", tc.raw, word, args, ok)
		}
	}
	for _, raw := range []string{`pending\`, `two words`, `# comment`, `$'bad\377'`, `$'bad\cA'`, `$'bad\E'`} {
		if _, _, ok := completionWord("bash", []string{native.WordArgument + raw}); ok {
			t.Fatal("incomplete escape or multiple shell words were guessed", raw)
		}
	}
	if _, _, ok := completionWord("bash", []string{"missing marker"}); ok {
		t.Fatal("unqualified callback supplied a current word")
	}
	if _, _, ok := completionWord("bash", []string{native.WordArgument + strings.Repeat("x", 64*1024+1)}); ok {
		t.Fatal("unbounded current word was accepted")
	}
}

func TestCurrentCSVFileCandidatesRetainPriorFieldsAndLiteralCommas(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	name := "file,with comma.txt"
	if err := os.WriteFile(filepath.Join(directory, name), []byte("unread"), 0o000); err != nil {
		t.Fatal(err)
	}
	const head = `"already,selected",`
	logical := head + `"` + directory + string(filepath.Separator) + "file,with"
	// PowerShell supplies a logical word. Its later native replacement still
	// needs separate generated-script/TabExpansion2 acceptance.
	args := []string{native.WordArgument + logical, "--", "KEY-1"}
	values := csvFileWordCandidates(t.Context(), "pwsh", args)
	if len(values) != 1 || !strings.HasPrefix(values[0], head) {
		t.Fatal("CSV lookup lost quoted-comma context", values)
	}
	fields, err := csv.NewReader(strings.NewReader(values[0])).Read()
	if err != nil || !reflect.DeepEqual(fields, []string{"already,selected", filepath.Join(directory, name)}) {
		t.Fatal("file completion changed the selected CSV values", fields, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := csvFileWordCandidates(ctx, "pwsh", args); got != nil {
		t.Fatal("canceled file lookup supplied candidates", got)
	}
	if got := csvFileWordCandidates(t.Context(), "pwsh", []string{native.WordArgument + `bare"quote`, "--"}); got != nil {
		t.Fatal("malformed CSV performed a candidate lookup", got)
	}
}

func TestCurrentWordRefusesUnrepresentableBytesAcrossCompletedCSVFields(t *testing.T) {
	t.Parallel()
	for _, shellName := range []string{"bash", "zsh", "fish", "pwsh", "elvish"} {
		for _, word := range []string{"\"earlier\tselected\",candidate", "\"earlier\x1b[31mselected\",candidate", "\"earlier\nselected\",candidate", "bad\xff"} {
			if _, _, ok := completionWord(shellName, []string{native.WordArgument + word}); ok {
				t.Fatal("protocol-unrepresentable completed field was accepted", shellName, word)
			}
		}
	}
}

func TestCurrentWordDecodesCompletedBashContextWithoutExpansion(t *testing.T) {
	t.Parallel()
	word, args, ok := completionWord("bash", []string{native.WordArgument + "candidate", `--config='selected settings.toml'`, `--file='"earlier,selected"'`, "--", `'KEY-1'`})
	want := []string{"--config=selected settings.toml", `--file="earlier,selected"`, "--", "KEY-1"}
	if !ok || word != "candidate" || !reflect.DeepEqual(args, want) {
		t.Fatal("completed Bash context retained shell syntax", args, ok)
	}
	_, args, ok = completionWord("bash", []string{native.WordArgument + "candidate", `--config=$'selected caf\303\251.toml'`, "--", `$'KEY-1'`})
	if !ok || !reflect.DeepEqual(args, []string{"--config=selected café.toml", "--", "KEY-1"}) {
		t.Fatal("completed ANSI-C context changed literal UTF-8 bytes", args, ok)
	}
	if _, _, ok := completionWord("bash", []string{native.WordArgument + "candidate", `$'unfinished`}); ok {
		t.Fatal("unfinished completed ANSI-C token was accepted")
	}
	if _, _, ok := completionWord("bash", []string{native.WordArgument + "candidate", "--config=bad\xff"}); ok {
		t.Fatal("completed invalid UTF-8 was replaced by the selected lexer")
	}
}

func TestCurrentConfigPathRoleDoesNotBroadenOrdinaryValues(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, "palette.toml")
	if err := os.WriteFile(path, []byte("unread palette"), 0o000); err != nil {
		t.Fatal(err)
	}
	args := []string{native.WordArgument + filepath.Join(directory, "palette"), "--", "theme_path"}
	if got := configWordCandidates(t.Context(), "pwsh", args); !reflect.DeepEqual(got, []string{path}) {
		t.Fatal("configuration path role did not select its literal directory", got)
	}
	for _, key := range []string{"editor", "api_token_env", "unknown"} {
		args[2] = key
		if got := configWordCandidates(t.Context(), "pwsh", args); got != nil {
			t.Fatal("ordinary configuration value fell back to filenames", key, got)
		}
	}
	args = []string{native.WordArgument + "dar", "--", "theme"}
	if got := configWordCandidates(t.Context(), "pwsh", args); !reflect.DeepEqual(got, []string{"dark"}) {
		t.Fatal("context-aware value lost its original enum", got)
	}
}

func TestThemePathCompletionPreservesLiteralTraversalAndFinalTransport(t *testing.T) {
	base := t.TempDir()
	cwd := filepath.Join(base, "working")
	other := filepath.Join(base, "other")
	if err := os.MkdirAll(filepath.Join(other, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	if err := os.WriteFile(filepath.Join(other, "palette.toml"), []byte("selected"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(other, "sub"), "link"); err != nil {
		t.Skip("native symlink unavailable", err)
	}
	separator := string(filepath.Separator)
	relative := "link" + separator + ".." + separator
	for _, seed := range []string{relative + "pal", cwd + separator + relative + "pal"} {
		got := configWordCandidates(t.Context(), "pwsh", []string{native.WordArgument + seed, "--", "theme_path"})
		want := cwd + separator + relative + "palette.toml"
		if !reflect.DeepEqual(got, []string{want}) {
			t.Fatal("theme path cleaned literal traversal", got, want)
		}
		contents, err := os.ReadFile(got[0])
		if err != nil || string(contents) != "selected" {
			t.Fatal("theme choice changed file identity", string(contents), err)
		}
	}
	bad := filepath.Join(base, "tab\tworking")
	if err := os.Mkdir(bad, 0o700); err != nil {
		t.Skip("native control filename unavailable", err)
	}
	t.Chdir(bad)
	if err := os.WriteFile("palette.toml", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := configWordCandidates(t.Context(), "pwsh", []string{native.WordArgument + "pal", "--", "theme_path"}); got != nil {
		t.Fatal("absolute reply admitted unrepresentable cwd", got)
	}
}
