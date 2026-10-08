package contract

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

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
	driver := `source "$1" || exit 40
compopt() { :; }
shift
COMP_WORDS=(jira "$@")
COMP_CWORD=$((${#COMP_WORDS[@]} - 1))
_jira jira "${COMP_WORDS[COMP_CWORD]}" "${COMP_WORDS[COMP_CWORD-1]}" || exit 41
printf '%s\0' "${COMPREPLY[@]}"
`
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
			command := append([]string{"--noprofile", "--norc", "-c", driver, "file-driver", path}, args...)
			stdout, stderr, exit := runExecutableIOInDirStarted(t, bash, dir, nil, nil, nil, nil, command...)
			got := strings.Split(strings.TrimSuffix(stdout, "\x00"), "\x00")
			slices.Sort(got)
			if exit != 0 || stderr != "" || !slices.Equal(got, want) {
				t.Fatalf("file candidates=%q, want %q; exit=%d stderr=%q", got, want, exit, stderr)
			}
		})
	}
}
