package contract

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/adapters"
	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
)

// zshCompletionReplies exercises ZLE, compinit and _arguments. Calling the
// generated function outside a completion widget misses its scope transitions.
func zshCompletionReplies(t *testing.T, script string, env []string, line string) []string {
	t.Helper()
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("native Zsh completion requires zsh")
	}
	dir := t.TempDir()
	write := func(name, contents string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if err := os.Symlink(jiraBinary(t), filepath.Join(dir, "jira")); err != nil {
		t.Fatal(err)
	}
	scriptPath := write("completion.zsh", script)
	// The child has its own home and rc directory; it never reads user rc files.
	write(".zshenv", "unsetopt RCS\n")
	candidates := filepath.Join(dir, "candidates")
	callerState := filepath.Join(dir, "caller-state")
	init := write("init.zsh", `
autoload -Uz compinit
compinit -D || exit 20
source "$JIRA_TEST_COMPLETION_SCRIPT" || exit 21
functions[_jira_test_original]=$functions[_jira]
function _jira() {
    local i=91 completion_result
    _jira_test_original "$@"
    completion_result=$?
    if (( i != 91 )); then
        print -r -- 'completion changed caller variable i' > "$JIRA_TEST_COMPLETION_STATE"
    fi
    return $completion_result
}
function compadd() {
    local -a observed
    builtin compadd -O observed "$@"
    if (( ${#observed} )); then
        printf '%s\0' "${observed[@]}" >> "$JIRA_TEST_COMPLETION_CANDIDATES"
    fi
    builtin compadd "$@"
}
function _jira_test_capture() {
    _main_complete
    print -r -- '\nJIRA_COMPLETION_DONE'
}
zle -C jira-test-complete complete-word _jira_test_capture
bindkey -e
bindkey '^I' jira-test-complete
PS1='fixture> '
print -r -- JIRA_COMPLETION_READY
`)
	driver := `
zmodload zsh/zpty || exit 22
zpty child "$JIRA_TEST_ZSH" -d || exit 23
trap 'zpty -d child 2>/dev/null' EXIT
zpty -w child 'source "$JIRA_TEST_COMPLETION_INIT"'
zpty -r child observed '*JIRA_COMPLETION_READY*' || exit 24
print -r -- "$observed"
zpty -w -n child "$JIRA_TEST_COMPLETION_LINE"$'\t'
zpty -r child observed '*JIRA_COMPLETION_DONE*' || exit 25
print -r -- "$observed"
`
	extra := append(slices.Clone(env),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"ZDOTDIR="+dir, "TERM=xterm", "JIRA_TEST_ZSH="+zsh,
		"JIRA_TEST_COMPLETION_INIT="+init, "JIRA_TEST_COMPLETION_SCRIPT="+scriptPath,
		"JIRA_TEST_COMPLETION_CANDIDATES="+candidates, "JIRA_TEST_COMPLETION_LINE="+line,
		"JIRA_TEST_COMPLETION_STATE="+callerState,
	)
	stdout, stderr, exit := runExecutableIOInDirStarted(t, zsh, dir, extra, nil, nil, nil, "-f", "-c", driver)
	if exit != 0 || stderr != "" || !strings.Contains(stdout, "JIRA_COMPLETION_DONE") {
		t.Fatalf("native Zsh completion exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	for _, failure := range []string{"command not found", "bad substitution", "parse error", "can only be called from completion function"} {
		if strings.Contains(stdout, failure) {
			t.Fatalf("native Zsh runtime failure: %q", stdout)
		}
	}
	if _, err := os.Stat(callerState); !os.IsNotExist(err) {
		t.Fatalf("native Zsh completion changed caller state: %v", err)
	}
	data, err := os.ReadFile(candidates)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var values []string
	for value := range bytes.SplitSeq(bytes.TrimSuffix(data, []byte{0}), []byte{0}) {
		if !slices.Contains(values, string(value)) {
			values = append(values, string(value))
		}
	}
	slices.Sort(values)
	return values
}

func TestZshNativeCompletionPreservesParentRolesAndSelectedConfig(t *testing.T) {
	env, selected := prepareBinaryMetadataCompletion(t)
	disk, err := adapters.NewDiskCache()
	if err != nil {
		t.Fatal(err)
	}
	store, err := adapters.NewScopedCache(disk, "https://example.com", "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := cachecmd.RecordIssueKeys(store, []string{"DEMO-1"}); err != nil {
		t.Fatal(err)
	}
	script, stderr, exit := runBinaryEnv(t, env, "completion", "zsh")
	if exit != 0 || stderr != "" {
		t.Fatalf("generate Zsh exit=%d stderr=%q", exit, stderr)
	}
	prefix := "jira --config " + shell.Quote(selected) + " "
	for _, scenario := range []struct {
		name, line string
		want       []string
	}{
		{name: "runnable parent issue slot", line: prefix + "issue transition DEM", want: []string{"DEMO-1"}},
		{name: "runnable parent child", line: prefix + "issue transition li", want: []string{"list"}},
		{name: "child issue slot", line: prefix + "issue transition list DEM", want: []string{"DEMO-1"}},
		{name: "status slot is not an issue key", line: prefix + "issue transition DEMO-1 DEM"},
		{name: "default project", line: prefix + "config set default_project DEM", want: []string{"DEMO"}},
		{name: "default issue type", line: prefix + "config set default_issue_type Ta", want: []string{"Task"}},
		{name: "default board", line: prefix + "config set default_board Exam", want: []string{"Example board"}},
		{name: "automatic theme setting", line: prefix + "config set theme au", want: []string{"auto"}},
		{name: "client refresh key", line: prefix + "config set refresh_i", want: []string{"refresh_interval"}},
		{name: "team metadata key", line: prefix + "config set team_ac", want: []string{"team_account_ids"}},
		{name: "automatic theme flag", line: prefix + "config theme --name au", want: []string{"auto"}},
		{name: "described flag identity", line: prefix + "epic list --project DEM", want: []string{"DEMO"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			values := zshCompletionReplies(t, script, env, scenario.line)
			if !slices.Equal(values, scenario.want) {
				t.Fatalf("native Zsh candidates=%q want=%q", values, scenario.want)
			}
		})
	}
}
