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
)

func TestContextualPathRolesUseGeneratedNativeShells(t *testing.T) {
	for _, name := range []string{"bash", "fish", "zsh"} {
		t.Run(name, func(t *testing.T) {
			executable, err := exec.LookPath(name)
			if err != nil {
				t.Skip("native shell unavailable: " + name)
			}
			directory := t.TempDir()
			prefix := filepath.Join(directory, "candidate")
			want := []string{prefix + " space.txt", prefix + ",comma.txt", prefix + ".txt"}
			for _, path := range want {
				if err := os.WriteFile(path, []byte("unread content"), 0o000); err != nil {
					t.Fatal(err)
				}
			}
			env := []string{"JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_API_TOKEN=", "JIRA_EMAIL=", "JIRA_BASE_URL=https://unreachable.example.invalid", "XDG_CONFIG_HOME=" + t.TempDir(), "XDG_CACHE_HOME=" + t.TempDir()}
			script, stderr, exit := runBinaryEnv(t, env, "completion", name)
			if exit != 0 || stderr != "" {
				t.Fatal("native generator did not run", name, exit, stderr)
			}
			for _, args := range [][]string{
				{"issue", "attachment", "add", "KEY-1", prefix},
				{"issue", "attachment", "add", "KEY-1", "--file", prefix},
				{"config", "set", "theme_path", prefix},
			} {
				t.Run(strings.Join(args[:len(args)-1], " "), func(t *testing.T) {
					var got []string
					switch name {
					case "bash":
						got = completionArgumentReplies(t, script, env, append([]string{"jira"}, args...))
					case "zsh":
						words := append([]string{"jira"}, args...)
						for i := range words {
							words[i] = shell.Quote(words[i])
						}
						got = zshCompletionReplies(t, script, env, strings.Join(words, " "))
					default:
						work := t.TempDir()
						path := filepath.Join(work, "completion.fish")
						if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
							t.Fatal(err)
						}
						words := append([]string{"jira"}, args...)
						for i := range words {
							words[i] = shell.Quote(words[i])
						}
						line := strings.Join(words, " ")
						extra := append(slices.Clone(env), "PATH="+filepath.Dir(jiraBinary(t))+string(os.PathListSeparator)+os.Getenv("PATH"))
						stdout, stderr, exit := runExecutableIOInDirStarted(t, executable, work, extra, nil, nil, nil, "--no-config", "-c", `source "$argv[1]"; or exit
complete -C "$argv[2]"`, "--", path, line)
						if exit != 0 || stderr != "" {
							t.Fatal("Fish completion failed", exit, stdout, stderr)
						}
						for _, raw := range bytes.Split(bytes.TrimSpace([]byte(stdout)), []byte{'\n'}) {
							value, _, _ := strings.Cut(string(raw), "\t")
							// complete -C exposes logical candidate values, including
							// spaces and CSV quotes, before editor insertion.
							got = append(got, value)
						}
					}
					wantValues := slices.Clone(want)
					if slices.Contains(args, "--file") {
						wantValues[0] = want[0]
						wantValues[1] = `"` + want[1] + `"`
						if name == "fish" {
							for i, value := range want {
								wantValues[i] = `"` + value + `"`
							}
						}
					}
					slices.Sort(got)
					slices.Sort(wantValues)
					if !slices.Equal(got, wantValues) {
						t.Fatal("contextual native path role changed file identity", got, wantValues)
					}
				})
			}
		})
	}
}
