package contract

import (
	"bytes"
	jsonv2 "encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/adapters"
	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestNativeShellsCompleteEligiblePositionalTransitions(t *testing.T) {
	env, selected := prepareBinaryMetadataCompletion(t)
	disk, err := adapters.NewDiskCache()
	if err != nil {
		t.Fatal(err)
	}
	store, err := adapters.NewScopedCache(disk, "https://example.com", "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	var list jira.Transitions
	if err := jsonv2.Unmarshal([]byte(transitionListNative), &list); err != nil {
		t.Fatal(err)
	}
	if err := cachecmd.RecordTransitionHints(t.Context(), store, "DEMO-1", &list, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bash", "fish", "zsh"} {
		t.Run(name, func(t *testing.T) {
			executable, err := exec.LookPath(name)
			if err != nil {
				t.Skip("native shell unavailable: " + name)
			}
			script, stderr, exit := runBinaryEnv(t, env, "completion", name)
			if exit != 0 || stderr != "" {
				t.Fatalf("generate %s exit=%d stderr=%q", name, exit, stderr)
			}
			line := "jira --config " + shell.Quote(selected) + " issue transition DEMO-1 3"
			if name == "zsh" {
				if got := zshCompletionReplies(t, script, env, line); !slices.Equal(got, []string{"31"}) {
					t.Fatalf("Zsh eligible identities=%q", got)
				}
				return
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "completion")
			if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
			callbackLog := filepath.Join(dir, "callbacks")
			wrapper := "#!/bin/sh\nprintf '%s\\0' \"$@\" >> \"$JIRA_TEST_CALLBACK_LOG\"\nexec \"$JIRA_TEST_REAL_BINARY\" \"$@\"\n"
			if err := os.WriteFile(filepath.Join(dir, "jira"), []byte(wrapper), 0o700); err != nil {
				t.Fatal(err)
			}
			extra := append(slices.Clone(env), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "JIRA_TEST_CALLBACK_LOG="+callbackLog, "JIRA_TEST_REAL_BINARY="+jiraBinary(t))
			var args []string
			if name == "bash" {
				driver := `source "$1" || exit
compopt() { :; }
COMP_WORDS=(jira --config "$2" issue transition DEMO-1 3)
COMP_CWORD=6
_jira jira 3 DEMO-1 || exit
if (( ${#COMPREPLY[@]} )); then printf '%s\0' "${COMPREPLY[@]}"; fi
`
				args = []string{"--noprofile", "--norc", "-c", driver, "transition-driver", path, selected}
			} else {
				args = []string{"--no-config", "-c", `source "$argv[1]"; or exit
complete -C "$argv[2]"`, "--", path, line}
			}
			stdout, stderr, exit := runExecutableIOInDirStarted(t, executable, dir, extra, nil, nil, nil, args...)
			if exit != 0 || stderr != "" {
				t.Fatalf("native %s exit=%d stdout=%q stderr=%q", name, exit, stdout, stderr)
			}
			if name == "bash" {
				if !bytes.Equal([]byte(stdout), []byte("31\x00")) {
					t.Fatalf("Bash eligible identity=%q", stdout)
				}
			} else if got := strings.Split(strings.TrimSpace(stdout), "\t")[0]; got != "31" {
				trace, err := os.ReadFile(callbackLog)
				t.Fatalf("Fish eligible identity=%q callback argv=%q read err=%v", stdout, trace, err)
			}
		})
	}
}
