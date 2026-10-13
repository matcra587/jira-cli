package contract

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestEnvironmentMigrationAndRollbackStayOfflineWithPublicReference(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			source, target, recovery := filepath.Join(dir, "old.toml"), filepath.Join(dir, "new.toml"), filepath.Join(dir, "recovery")
			original := []byte("# whole source retained\ndefault_profile='work-staging'\n[[profiles]]\nname='other'\nemail='other@example.invalid'\n[[profiles]]\nname='work-staging'\nbase_url='https://example.invalid'\nemail='person@example.invalid'\nsecret_backend='env'\n")
			if err := os.WriteFile(source, original, 0o600); err != nil {
				t.Fatal(err)
			}
			const token = "synthetic-environment-migration-token"
			env := []string{"JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_SECRET_BACKEND=1password", "OP_SERVICE_ACCOUNT_TOKEN=", "JIRA_API_TOKEN=", "JIRA_BASE_URL=", "JIRA_EMAIL=", "JIRA_API_TOKEN_ENV=", "JIRA_TOKEN_WORK_STAGING=" + token, "JIRA_NO_UPDATE_CHECK=1"}
			invoke := func(args ...string) string {
				t.Helper()
				stdout, stderr, exit := runBinaryEnv(t, env, append(args, "--output="+mode, "--no-input")...)
				if exit != 0 || stderr != "" || strings.Contains(stdout+stderr, token) {
					t.Fatalf("offline environment flow failed: exit=%d stderr=%q", exit, stderr)
				}
				return stdout
			}
			// This flow deliberately selects only the environment-backed source.
			// The other profile uses keyring and must remain recovery-only.
			invoke("config", "migrate", "--source", source, "--source-profile", "work-staging", "--config", target, "--dry-run")
			if _, err := os.Stat(recovery); !os.IsNotExist(err) {
				t.Fatal("preview published recovery")
			}
			stdout := invoke("config", "migrate", "--source", source, "--source-profile", "work-staging", "--config", target, "--recovery-dir", recovery, "--yes")
			if mode != "human" {
				var out envelope.ConfigMigrateOutput
				data := []byte(stdout)
				if mode == "json" {
					var frame struct {
						Data json.RawMessage `json:"data"`
					}
					if err := json.Unmarshal(data, &frame); err != nil {
						t.Fatal(err)
					}
					data = frame.Data
				}
				if err := json.Unmarshal(data, &out); err != nil || out.Receipt == nil || out.Receipt.CredentialState != "external" || len(out.Receipt.Effects) != 1 || out.Receipt.SourceCredential.EnvironmentKey != "JIRA_TOKEN_WORK_STAGING" || len(out.Profiles) != 1 || out.Profiles[0] != "work-staging" {
					t.Fatal("typed output lost config-only scope", err)
				}
			}
			for _, path := range []string{target, filepath.Join(recovery, "configuration.json"), filepath.Join(recovery, "migration.json")} {
				data, err := os.ReadFile(path)
				if err != nil || bytes.Contains(data, []byte(token)) {
					t.Fatal("environment secret entered persistence", err)
				}
			}
			// Changing runtime backend cannot retarget a config-only recorded rollback.
			invoke("config", "rollback", "--journal", filepath.Join(recovery, "migration.json"), "--dry-run")
			invoke("config", "rollback", "--journal", filepath.Join(recovery, "migration.json"), "--yes")
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatal("rollback did not restore target absence")
			}
			got, err := os.ReadFile(source)
			if err != nil || !bytes.Equal(got, original) {
				t.Fatal("rollback lost complete source", err)
			}
		})
	}
}

func TestNamedTokenDiscoveryAndRawTokenContractUseConfiguredIdentity(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(path, []byte("base_url='https://example.invalid'\nemail='person@example.invalid'\nsecret_backend='1password'\napi_token_env='JIRA_TOKEN_WORK'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const named = " synthetic-named-cli-token "
	env := []string{"JIRA_API_TOKEN=", "JIRA_API_TOKEN_ENV=", "JIRA_BASE_URL=", "JIRA_EMAIL=", "JIRA_SECRET_BACKEND=", "OP_SERVICE_ACCOUNT_TOKEN=", "JIRA_TOKEN_WORK=" + named, "JIRA_NO_UPDATE_CHECK=1"}
	for _, mode := range []string{"json", "compact", "human"} {
		stdout, stderr, exit := runBinaryEnv(t, env, "config", "list", "--config", path, "--output="+mode, "--no-input")
		if exit != 0 || stderr != "" || strings.Contains(stdout+stderr, named) || !strings.Contains(stdout, "JIRA_TOKEN_WORK") || !strings.Contains(stdout, "person@example.invalid") {
			t.Fatalf("named token required SDK or leaked: mode=%s exit=%d stderr=%q", mode, exit, stderr)
		}
		stdout, stderr, exit = runBinaryEnv(t, env, "auth", "token", "--config", path, "--output="+mode, "--no-input")
		if exit != 0 || stderr != "" || stdout != named+"\n" {
			t.Fatalf("intentional raw token contract changed: mode=%s exit=%d stderr=%q", mode, exit, stderr)
		}
	}
	stdout, stderr, exit := runBinaryEnv(t, env, "--@complete=configsetkey", "--")
	if exit != 0 || stderr != "" || !strings.Contains(stdout, "api_token_env\n") {
		t.Fatal("public selector missing from local completion")
	}
}

func TestEnvironmentMigrationPreservesAllProfilesWithoutSourceSelector(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source, target, recovery := filepath.Join(dir, "old.toml"), filepath.Join(dir, "new.toml"), filepath.Join(dir, "recovery")
	original := []byte("default_profile='second'\n[[profiles]]\nname='first'\nbase_url='https://first.invalid'\nemail='first@example.invalid'\nsecret_backend='env'\n[[profiles]]\nname='second'\nbase_url='https://second.invalid'\nemail='second@example.invalid'\nsecret_backend='env'\n")
	if err := os.WriteFile(source, original, 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_SECRET_BACKEND=1password", "OP_SERVICE_ACCOUNT_TOKEN=", "JIRA_API_TOKEN=", "JIRA_BASE_URL=", "JIRA_EMAIL=", "JIRA_API_TOKEN_ENV=", "JIRA_TOKEN_FIRST=synthetic-first-value", "JIRA_TOKEN_SECOND=synthetic-second-value", "JIRA_NO_UPDATE_CHECK=1"}
	stdout, stderr, exit := runBinaryEnv(t, env, "config", "migrate", "--source", source, "--config", target, "--recovery-dir", recovery, "--yes", "--output=json", "--no-input")
	var frame struct {
		OK   bool                         `json:"ok"`
		Data envelope.ConfigMigrateOutput `json:"data"`
	}
	if exit != 0 || stderr != "" || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK {
		t.Fatalf("all-profile environment migration failed: exit=%d stderr=%q", exit, stderr)
	}
	out := frame.Data
	if len(out.Profiles) != 2 || out.Profiles[0] != "first" || out.Profiles[1] != "second" || out.Profile != "second" || out.Receipt == nil || out.Receipt.Status != "applied" || len(out.Receipt.Profiles) != 2 || len(out.Receipt.Effects) != 1 {
		t.Fatal("default migration dropped a profile or its default")
	}
	for _, resource := range out.Receipt.Profiles {
		if resource.CredentialState != "external" || resource.CredentialBackup != "" || resource.SourceCredential.EnvironmentKey != "JIRA_TOKEN_"+strings.ToUpper(resource.Profile) {
			t.Fatal("environment profile acquired a provider effect or lost its reference")
		}
	}
	for _, path := range []string{target, filepath.Join(recovery, "configuration.json"), filepath.Join(recovery, "migration.json")} {
		data, err := os.ReadFile(path)
		if err != nil || bytes.Contains(data, []byte("synthetic-first-value")) || bytes.Contains(data, []byte("synthetic-second-value")) {
			t.Fatal("all-profile migration persisted an environment value", err)
		}
	}
	for _, name := range []string{"first", "second"} {
		stdout, stderr, exit := runBinaryEnv(t, env, "auth", "token", "--config", target, "--profile", name, "--no-input")
		if exit != 0 || stderr != "" || stdout != "synthetic-"+name+"-value\n" {
			t.Fatalf("migrated profile reference did not resolve independently: profile=%s exit=%d stderr=%q", name, exit, stderr)
		}
	}
	_, stderr, exit = runBinaryEnv(t, env, "config", "rollback", "--journal", out.Receipt.Journal, "--yes", "--output=json", "--no-input")
	if exit != 0 || stderr != "" {
		t.Fatalf("all-profile environment rollback failed: exit=%d stderr=%q", exit, stderr)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("all-profile rollback did not restore target absence")
	}
	got, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal("all-profile rollback changed the source", err)
	}
}
