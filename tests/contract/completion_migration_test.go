package contract

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gechr/x/shell"
)

func TestLegacyProfileCompletionUsesOnlyExplicitValidatedSource(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "legacy source.toml")
	const data = "default_profile='work'\n[[profiles]]\nname='work'\nbase_url='https://example.invalid'\nemail='work@example.invalid'\n[[profiles]]\nname='other'\nbase_url='https://other.invalid'\nemail='other@example.invalid'\n"
	if err := os.WriteFile(source, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_SECRET_BACKEND=1password", "OP_SERVICE_ACCOUNT_TOKEN=", "JIRA_NO_UPDATE_CHECK=1"}
	for _, args := range [][]string{
		{"config", "migrate", "--help"},
		{"config", "rollback", "--help"},
		{"agent", "schema", "--path=config migrate"},
		{"config", "migrate", "--source", source, "--dry-run"},
	} {
		stdout, stderr, exit := runBinaryEnv(t, env, append(args, "--config", source, "--output=json", "--no-input")...)
		if exit != 0 || stdout == "" || stderr != "" {
			t.Fatalf("legacy-file repair was unreachable: exit=%d stderr=%q", exit, stderr)
		}
	}
	for _, args := range [][]string{{"--source", source}, {"--source=" + source}} {
		stdout, stderr, exit := runBinaryEnv(t, env, append([]string{"--@complete=legacyprofile", "--"}, args...)...)
		if exit != 0 || stderr != "" || stdout != "other\tLegacy profile\nwork\tLegacy profile\n" {
			t.Fatalf("local source callback exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
		}
	}
	for _, args := range [][]string{nil, {"--source", filepath.Join(dir, "missing")}, {"--", "--source", source}} {
		stdout, stderr, exit := runBinaryEnv(t, env, append([]string{"--@complete=legacyprofile", "--"}, args...)...)
		if exit != 0 || stdout != "" || stderr != "" {
			t.Fatal("unselected source completion was not silent")
		}
	}
	for _, bad := range []string{data + "api_token='synthetic-secret'\n", "default_profile='work'\nprofiles='invalid'\n"} {
		if err := os.WriteFile(source, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, exit := runBinaryEnv(t, env, "--@complete=legacyprofile", "--", "--source", source)
		if exit != 0 || stdout != "" || stderr != "" {
			t.Fatal("invalid or secret-bearing source supplied candidates")
		}
	}
}

func TestGeneratedShellsForwardLegacyMigrationSource(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "legacy source.toml")
	if err := os.WriteFile(source, []byte("default_profile='work'\n[[profiles]]\nname='work'\nbase_url='https://example.invalid'\nemail='work@example.invalid'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_SECRET_BACKEND=1password", "OP_SERVICE_ACCOUNT_TOKEN=", "JIRA_NO_UPDATE_CHECK=1"}
	for _, name := range []string{"bash", "fish", "zsh"} {
		t.Run(name, func(t *testing.T) {
			executable, err := exec.LookPath(name)
			if err != nil {
				t.Skip("native shell unavailable: " + name)
			}
			script, stderr, exit := runBinaryEnv(t, env, "completion", name)
			if exit != 0 || stderr != "" {
				t.Fatal("completion generator did not run successfully", exit, stderr)
			}
			line := "jira config migrate --source " + shell.Quote(source) + " --source-profile wo"
			if name == "zsh" {
				if got := zshCompletionReplies(t, script, env, line); !slices.Equal(got, []string{"work"}) {
					t.Fatalf("Zsh profile candidates=%q", got)
				}
				return
			}
			work := t.TempDir()
			path := filepath.Join(work, "completion")
			if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(jiraBinary(t), filepath.Join(work, "jira")); err != nil {
				t.Fatal(err)
			}
			extra := append(slices.Clone(env), "PATH="+work+string(os.PathListSeparator)+os.Getenv("PATH"))
			var args []string
			if name == "bash" {
				driver := `source "$1" || exit
compopt() { :; }
COMP_WORDS=(jira config migrate --source "$2" --source-profile wo)
COMP_CWORD=6
_jira jira wo --source-profile || exit
if (( ${#COMPREPLY[@]} )); then printf '%s\0' "${COMPREPLY[@]}"; fi
`
				args = []string{"--noprofile", "--norc", "-c", driver, "migration-driver", path, source}
			} else {
				args = []string{"--no-config", "-c", `source "$argv[1]"; or exit
complete -C "$argv[2]"`, "--", path, line}
			}
			stdout, stderr, exit := runExecutableIOInDirStarted(t, executable, work, extra, nil, nil, nil, args...)
			got := strings.TrimSuffix(stdout, "\x00")
			if name == "fish" {
				got = strings.Split(strings.TrimSpace(stdout), "\t")[0]
			}
			if exit != 0 || stderr != "" || got != "work" {
				t.Fatalf("generated %s profile=%q exit=%d stderr=%q", name, got, exit, stderr)
			}
		})
	}
}
