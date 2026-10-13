package contract

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestClientPreferenceActualPublicationInspectionAndExplicitClear(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "settings.toml")
			env := []string{"JIRA_KEYRING=false", "JIRA_REFRESH_INTERVAL=60", `JIRA_TEAM_ACCOUNT_IDS=["environment"]`}
			for _, assignment := range []struct{ key, value string }{
				{"refresh_interval", "45"},
				{"team_account_ids", `["a,b"," a ","a,b",""]`},
			} {
				stdout, stderr, exit := runBinaryEnv(t, env, "config", "set", assignment.key, assignment.value, "--config", path, "--no-input", "--output="+mode)
				if exit != 0 || stderr != "" {
					t.Fatalf("client preference publication failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
				}
				stdout, stderr, exit = runBinaryEnv(t, env, "config", "get", assignment.key, "--config", path, "--output=json")
				out := decodeConfigGet(t, "json", stdout)
				if exit != 0 || stderr != "" || !out.Present || out.Value != assignment.value {
					t.Fatal("stored preference was replaced by environment or array IDs changed")
				}
			}
			stdout, stderr, exit := runBinaryEnv(t, env, "config", "set", "team_account_ids", "[]", "--config", path, "--no-input", "--output="+mode)
			if exit != 0 || stderr != "" {
				t.Fatalf("team clear failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			stdout, stderr, exit = runBinaryEnv(t, env, "config", "get", "team_account_ids", "--config", path, "--output=json")
			if out := decodeConfigGet(t, "json", stdout); exit != 0 || stderr != "" || !out.Present || out.Value != "[]" {
				t.Fatal("explicit array clear lost presence")
			}
			contents, err := os.ReadFile(path)
			if err != nil || strings.Contains(string(contents), "environment") || strings.Contains(string(contents), "60") {
				t.Fatal("local metadata publication persisted an environment layer", err)
			}
		})
	}
}

func TestClientPreferenceActualEffectiveViewAndLocalQuery(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(path, []byte("refresh_interval=45\nteam_account_ids=['stored']\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"JIRA_KEYRING=false", "JIRA_REFRESH_INTERVAL=60", `JIRA_TEAM_ACCOUNT_IDS=["foreign-user,1"," other "]`}
	stdout, stderr, exit := runBinaryEnv(t, env, "config", "list", "--config", path, "--output=json")
	if exit != 0 || stderr != "" {
		t.Fatal("effective client metadata view failed", exit, stderr)
	}
	var frame struct {
		Data envelope.ConfigListOutput `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &frame); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, row := range frame.Data.Settings {
		if row.Key == "refresh_interval" || row.Key == "team_account_ids" {
			seen[row.Key] = row.Source == "env" && (row.Key == "refresh_interval" && row.Value == "60" || row.Key == "team_account_ids" && row.Value == `["foreign-user,1"," other "]`)
		}
	}
	if !seen["refresh_interval"] || !seen["team_account_ids"] {
		t.Fatal("effective metadata lost JSON IDs or provenance")
	}
	stdout, stderr, exit = runBinaryEnv(t, env, "issue", "list", "--as-jql", "--jql=status = Open", "--config", path, "--output=human")
	if exit != 0 || stderr != "" || strings.TrimSpace(stdout) != "status = Open" {
		t.Fatalf("metadata altered a local query or started remote work: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	stdout, stderr, exit = runBinaryEnv(t, env, "--@complete=configsetkey", "--")
	if exit != 0 || stderr != "" || !strings.Contains(stdout, "refresh_interval\n") || !strings.Contains(stdout, "team_account_ids\n") {
		t.Fatal("client preferences missing from offline key completion")
	}
}

func TestClientPreferenceActualMigrationAndExactRollback(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			source, target, recovery := filepath.Join(dir, "old.toml"), filepath.Join(dir, "new.toml"), filepath.Join(dir, "recovery")
			const original = "# Entire source retained.\ndefault_profile='work'\n[[profiles]]\nname='work'\nsecret_backend='env'\nrefresh_interval=0\nteam_account_ids=['a,b',' a ','a,b','']\n[[profiles]]\nname='other'\nrefresh_interval=77\nteam_account_ids=['other']\n"
			if err := os.WriteFile(source, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			env := []string{"JIRA_KEYRING=false", "JIRA_REFRESH_INTERVAL=60", `JIRA_TEAM_ACCOUNT_IDS=["environment"]`}
			args := []string{"config", "migrate", "--source", source, "--config", target, "--recovery-dir", recovery, "--no-input", "--output=" + mode}
			stdout, stderr, exit := runBinaryEnv(t, env, append(slices.Clone(args), "--dry-run")...)
			if exit != 0 || stderr != "" {
				t.Fatalf("metadata migration preview failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			if _, err := os.Stat(recovery); !os.IsNotExist(err) {
				t.Fatal("metadata preview published recovery")
			}
			stdout, stderr, exit = runBinaryEnv(t, env, append(args, "--yes")...)
			if exit != 0 || stderr != "" {
				t.Fatalf("metadata migration failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			for _, expected := range []struct{ key, value string }{{"refresh_interval", "30"}, {"team_account_ids", `["a,b"," a ","a,b",""]`}} {
				stdout, stderr, exit = runBinaryEnv(t, env, "config", "get", expected.key, "--config", target, "--output=json")
				if out := decodeConfigGet(t, "json", stdout); exit != 0 || stderr != "" || !out.Present || out.Value != expected.value {
					t.Fatal("selected metadata was changed, lost or copied from environment")
				}
			}
			stdout, stderr, exit = runBinaryEnv(t, env, "config", "rollback", "--journal", filepath.Join(recovery, "migration.json"), "--yes", "--no-input", "--output="+mode)
			if exit != 0 || stderr != "" {
				t.Fatalf("metadata rollback failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			data, err := os.ReadFile(source)
			if err != nil || string(data) != original {
				t.Fatal("metadata rollback changed source profiles", err)
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatal("metadata rollback lost target absence")
			}
		})
	}
}

func TestClientPreferenceGeneratedBashAndFishKeyCompletion(t *testing.T) {
	env := []string{"JIRA_KEYRING=false"}
	for _, name := range []string{"bash", "fish"} {
		t.Run(name, func(t *testing.T) {
			script, stderr, exit := runBinaryEnv(t, env, "completion", name)
			if exit != 0 || stderr != "" {
				t.Fatal("key completion generation failed", exit, stderr)
			}
			for _, tc := range []struct{ prefix, want string }{{"refresh_i", "refresh_interval"}, {"team_ac", "team_account_ids"}} {
				if name == "bash" {
					values := completionArgumentReplies(t, script, env, []string{"jira", "config", "set", tc.prefix})
					if !slices.Equal(values, []string{tc.want}) {
						t.Fatalf("generated Bash metadata keys=%q want=%q", values, tc.want)
					}
					continue
				}
				fish, err := exec.LookPath("fish")
				if err != nil {
					t.Skip("native Fish unavailable")
				}
				dir := t.TempDir()
				path := filepath.Join(dir, "completion.fish")
				if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(jiraBinary(t), filepath.Join(dir, "jira")); err != nil {
					t.Fatal(err)
				}
				line := "jira config set " + shell.Quote(tc.prefix)
				stdout, stderr, exit := runExecutableIOInDirStarted(t, fish, dir, append(slices.Clone(env), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH")), nil, nil, nil, "--no-config", "-c", `source "$argv[1]"; or exit
complete -C "$argv[2]"`, "--", path, line)
				if exit != 0 || stderr != "" || strings.Split(strings.TrimSpace(stdout), "\t")[0] != tc.want {
					t.Fatalf("generated Fish metadata key=%q want=%q exit=%d stderr=%q", stdout, tc.want, exit, stderr)
				}
			}
		})
	}
}
