package native_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/gechr/clib/complete"

	"github.com/matcra587/jira-cli/internal/cli/completion/native"
)

func testBash(t *testing.T) string {
	t.Helper()
	bashName := os.Getenv("JIRA_TEST_BASH_BINARY")
	explicit := bashName != ""
	if bashName == "" {
		bashName = "bash"
	}
	bash, err := exec.LookPath(bashName)
	if err != nil {
		if explicit {
			t.Fatalf("requested Bash executable is unavailable: %v", err)
		}
		t.Skip("native Bash completion requires bash")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, bash, "--version").CombinedOutput()
	if err != nil || !bytes.HasPrefix(version, []byte("GNU bash, version ")) {
		t.Fatalf("requested executable is not Bash: err=%v output=%q", err, version)
	}
	return bash
}

func bashStaticReplies(t *testing.T, values []string, comma, described bool, prefix string) []string {
	t.Helper()
	bash := testBash(t)
	native.Register()
	spec := complete.Spec{LongFlag: "value", HasArg: true, CommaList: comma, Values: values}
	if described {
		spec.Values = nil
		for _, value := range values {
			spec.ValueDescs = append(spec.ValueDescs, complete.ValueDesc{Value: value, Desc: "Display only"})
		}
	}
	generator := &complete.Generator{AppName: "fixture", Specs: []complete.Spec{spec}}
	var script bytes.Buffer
	if err := generator.Print(&script, "bash"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.bash")
	if err := os.WriteFile(path, script.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "glob-target"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	driver := `source "$1" || exit
if (( BASH_VERSINFO[0] >= 4 )); then compopt() { :; }; fi
val=global-value
sel=global-selection
selected=(global-selected)
found=global-found
COMP_WORDS=(fixture --value "$2")
COMP_CWORD=2
_fixture fixture "$2" --value || exit
if [[ "$val" != global-value || "$sel" != global-selection || "${selected[*]}" != global-selected || "$found" != global-found ]]; then
    printf 'completion changed caller variables\n' >&2
    exit 90
fi
if (( ${#COMPREPLY[@]} )); then printf '%s\0' "${COMPREPLY[@]}"; fi
`
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c", driver, "fixture-driver", path, prefix)
	process.Dir = dir
	process.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir}
	var output, diagnostics bytes.Buffer
	process.Stdout, process.Stderr = &output, &diagnostics
	if err := process.Run(); err != nil || diagnostics.Len() != 0 {
		t.Fatalf("native Bash execution err=%v stderr=%q", err, diagnostics.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "unexpected-command")); !os.IsNotExist(err) {
		t.Fatalf("literal completion value executed a command: %v", err)
	}
	if output.Len() == 0 {
		return nil
	}
	var result []string
	for value := range bytes.SplitSeq(bytes.TrimSuffix(output.Bytes(), []byte{0}), []byte{0}) {
		result = append(result, string(value))
	}
	slices.Sort(result)
	return result
}

func TestBashPositionalCompletionKeepsCallerVariables(t *testing.T) {
	bash := testBash(t)
	native.Register()
	generator := &complete.Generator{AppName: "fixture", DynamicArgs: []string{"argument"}}
	var script bytes.Buffer
	if err := generator.Print(&script, "bash"); err != nil {
		t.Fatal(err)
	}
	driver := `
fixture() { printf 'candidate\tDisplay only\n'; }
j=91
COMP_WORDS=(fixture c)
COMP_CWORD=1
_fixture fixture c fixture || exit
if [[ "$j" != 91 ]]; then
    printf 'positional completion changed caller variable j\n' >&2
    exit 90
fi
printf '%s\n' "${COMPREPLY[@]}"
`
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c", script.String()+driver)
	process.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
	output, err := process.CombinedOutput()
	if err != nil || string(output) != "candidate\n" {
		t.Fatalf("positional completion err=%v output=%q", err, output)
	}
}

func TestBashFileCompletionKeepsCallerStateAndLiteralPaths(t *testing.T) {
	bash := testBash(t)
	native.Register()
	for _, extension := range []bool{false, true} {
		mode := "file hint"
		if extension {
			mode = "extension hint"
		}
		t.Run(mode, func(t *testing.T) {
			spec := complete.Spec{LongFlag: "file", HasArg: true, ValueHint: complete.HintFile}
			if extension {
				spec.ValueHint, spec.Extension = "", "txt"
			}
			var script bytes.Buffer
			if err := (&complete.Generator{AppName: "fixture", Specs: []complete.Spec{spec}}).Print(&script, "bash"); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			for _, name := range []string{"two words.txt", "*.txt", `back\slash.txt`, "plain.txt"} {
				if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for _, callerIFS := range []string{"unset", "custom"} {
				for _, prefix := range []string{"two", "*", `back\`} {
					t.Run(callerIFS+"/"+prefix, func(t *testing.T) {
						driver := `
if (( BASH_VERSINFO[0] >= 4 )); then compopt() { :; }; fi
if [[ "$1" == unset ]]; then unset IFS; else IFS=':|'; fi
COMP_WORDS=(fixture --file "$2")
COMP_CWORD=2
_fixture fixture "$2" --file || exit
if [[ "$1" == unset ]]; then
    [[ -z "${IFS+x}" ]] || { printf 'completion set caller IFS\n' >&2; exit 90; }
else
    [[ "$IFS" == ':|' ]] || { printf 'completion changed caller IFS\n' >&2; exit 90; }
fi
printf '%s\n' "${COMPREPLY[@]}"
`
						ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
						defer cancel()
						process := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c", script.String()+driver, "file-driver", callerIFS, prefix)
						process.Dir = dir
						process.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir}
						output, err := process.CombinedOutput()
						want := map[string]string{"two": "two words.txt\n", "*": "*.txt\n", `back\`: "back\\slash.txt\n"}[prefix]
						if err != nil || string(output) != want {
							t.Fatalf("file completion err=%v output=%q want=%q", err, output, want)
						}
					})
				}
			}
		})
	}
}

func TestBashStaticCommaValuesRemainLiteral(t *testing.T) {
	values := []string{"two words", "*", "bare", "quote'value", "$(touch unexpected-command)", "`touch unexpected-command`", `back\slash`}
	for _, described := range []bool{false, true} {
		mode := "values"
		if described {
			mode = "descriptions"
		}
		t.Run(mode, func(t *testing.T) {
			for _, scenario := range []struct {
				name, prefix string
				want         []string
			}{
				{name: "all choices", want: slices.Sorted(slices.Values(values))},
				{name: "space", prefix: "two", want: []string{"two words"}},
				{name: "literal glob", prefix: "*", want: []string{"*"}},
				{name: "quote", prefix: "quote'", want: []string{"quote'value"}},
				{name: "substitution", prefix: "$(", want: []string{"$(touch unexpected-command)"}},
				{name: "continuation", prefix: "bare,two", want: []string{"bare,two words"}},
				{name: "duplicate", prefix: "bare,bar"},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					got := bashStaticReplies(t, values, true, described, scenario.prefix)
					if !slices.Equal(got, scenario.want) {
						t.Fatalf("literal comma values=%q want=%q", got, scenario.want)
					}
				})
			}
		})
	}
}

func TestBashStaticValuesRemainLiteral(t *testing.T) {
	values := []string{"two words", "*", "bare", "quote'value", "$(touch unexpected-command)", "`touch unexpected-command`", `back\slash`, "one,two"}
	for _, described := range []bool{false, true} {
		mode := "values"
		if described {
			mode = "descriptions"
		}
		t.Run(mode, func(t *testing.T) {
			for _, scenario := range []struct {
				name, prefix string
				want         []string
			}{
				{name: "space", prefix: "two", want: []string{"two words"}},
				{name: "all choices", want: slices.Sorted(slices.Values(values))},
				{name: "literal glob", prefix: "*", want: []string{"*"}},
				{name: "quote", prefix: "quote'", want: []string{"quote'value"}},
				{name: "substitution", prefix: "$(", want: []string{"$(touch unexpected-command)"}},
				{name: "literal comma", prefix: "one,", want: []string{"one,two"}},
				{name: "unmatched", prefix: "missing"},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					got := bashStaticReplies(t, values, false, described, scenario.prefix)
					if !slices.Equal(got, scenario.want) {
						t.Fatalf("literal values=%q want=%q", got, scenario.want)
					}
				})
			}
		})
	}
}
