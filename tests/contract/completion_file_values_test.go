package contract

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const fileCompletionDriver = `source "$1" || exit 40
compopt() { :; }
shift
COMP_WORDS=(jira "$@")
COMP_CWORD=$((${#COMP_WORDS[@]} - 1))
_jira jira "${COMP_WORDS[COMP_CWORD]}" "${COMP_WORDS[COMP_CWORD-1]}" || exit 41
printf '%s\0' "${COMPREPLY[@]}"
`

func TestCompletionInputFileValuesPreserveLiteralPaths(t *testing.T) {
	script, stderr, exit := runBinary(t, "completion", "bash")
	if exit != 0 || stderr != "" {
		t.Fatalf("generate Bash exit=%d stderr=%q", exit, stderr)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "completion.bash")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	want := []string{"candidate space.md", "candidate.txt"}
	for _, name := range append(slices.Clone(want), "unrelated.txt") {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"--config", "candidate"},
		{"api", "post", "/rest/api/3/myself", "--input", "candidate"},
		{"adf", "render", "--input", "candidate"},
		{"adf", "edit", "prepare", "--input", "candidate"},
		{"adf", "edit", "apply", "--input", "candidate"},
		{"adf", "edit", "apply", "--markdown-file", "candidate"},
		{"adf", "edit", "merge", "--input", "candidate"},
		{"adf", "edit", "merge", "--markdown-file", "candidate"},
		{"adf", "edit", "merge", "--current", "candidate"},
		{"issue", "edit", "KEY-1", "--markdown-file", "candidate"},
		{"issue", "edit", "KEY-1", "--json-input", "candidate"},
		{"issue", "edit", "--from-file", "candidate"},
		{"issue", "transition", "KEY-1", "--comment-file", "candidate"},
		{"issue", "link", "create", "KEY-1", "--json-input", "candidate"},
		{"issue", "weblink", "add", "KEY-1", "--json-input", "candidate"},
		{"issue", "clone", "KEY-1", "--json-input", "candidate"},
		{"issue", "move", "--json-input", "candidate"},
		{"issue", "move", "fields", "KEY-1", "--json-input", "candidate"},
		{"worklog", "add", "KEY-1", "--json-input", "candidate"},
	} {
		t.Run(strings.Join(args[:len(args)-1], " "), func(t *testing.T) {
			command := append([]string{"--noprofile", "--norc", "-c", fileCompletionDriver, "file-driver", path}, args...)
			stdout, stderr, exit := runExecutableIOInDirStarted(t, bash, dir, nil, nil, nil, nil, command...)
			got := strings.Split(strings.TrimSuffix(stdout, "\x00"), "\x00")
			slices.Sort(got)
			if exit != 0 || stderr != "" || !slices.Equal(got, want) {
				t.Fatalf("file candidates=%q, want %q; exit=%d stderr=%q", got, want, exit, stderr)
			}
		})
	}
}

func TestDraftDirectoryCompletionDelegatesNativeFallbackWithoutArtifactReads(t *testing.T) {
	script, stderr, exit := runBinary(t, "completion", "bash")
	if exit != 0 || stderr != "" {
		t.Fatal("Bash generator did not run", exit, stderr)
	}
	binary := os.Getenv("JIRA_TEST_BASH_BINARY")
	if binary == "" {
		binary = "bash"
	}
	bash, err := exec.LookPath(binary)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "completion.bash")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	want := []string{"candidate space", "candidate-directory"}
	for _, name := range want {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
		// Deliberately invalid content: completion must inspect directory names
		// alone, without trying to validate or reopen a saved draft.
		if err := os.WriteFile(filepath.Join(dir, name, "source.json"), []byte("invalid draft"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "candidate-file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Modern Bash delegates directory candidates to Readline's plusdirs option.
	// Bash 3.2 has no compopt and returns compgen candidates directly. Exercise
	// both contracts without mistaking this driver for a Readline/PTY receipt.
	const directoryDriver = `source "$1" || exit 40
native_dirs=false
compopt() { [[ "$*" == '-o plusdirs' ]] && native_dirs=true; return 0; }
shift
COMP_WORDS=(jira "$@")
COMP_CWORD=$((${#COMP_WORDS[@]} - 1))
_jira jira "${COMP_WORDS[COMP_CWORD]}" "${COMP_WORDS[COMP_CWORD-1]}" || exit 41
if (( BASH_VERSINFO[0] >= 4 )); then
    [[ "$native_dirs" == true && ${#COMPREPLY[@]} == 0 ]] || exit 42
    while IFS= read -r candidate; do printf '%s\0' "$candidate"; done < <(compgen -d -- "${COMP_WORDS[COMP_CWORD]}")
else
    [[ "$native_dirs" == false ]] || exit 43
    printf '%s\0' "${COMPREPLY[@]}"
fi
`
	for _, args := range [][]string{
		{"issue", "edit", "draft", "KEY-1", "--directory", "candidate"},
		{"issue", "edit", "inspect", "--directory", "candidate"},
		{"issue", "edit", "resume", "--directory", "candidate"},
	} {
		t.Run(args[2], func(t *testing.T) {
			command := append([]string{"--noprofile", "--norc", "-c", directoryDriver, "directory-driver", path}, args...)
			stdout, stderr, exit := runExecutableIOInDirStarted(t, bash, dir, []string{"JIRA_BASE_URL=https://unreachable.example.invalid", "JIRA_API_TOKEN=", "JIRA_KEYRING=false"}, nil, nil, nil, command...)
			got := strings.Split(strings.TrimSuffix(stdout, "\x00"), "\x00")
			for i := range got {
				got[i] = strings.TrimSuffix(got[i], "/")
			}
			slices.Sort(got)
			if exit != 0 || stderr != "" || !slices.Equal(got, want) {
				t.Fatal("native directory candidates lost literal names or admitted files", got, exit, stderr)
			}
		})
	}
}
