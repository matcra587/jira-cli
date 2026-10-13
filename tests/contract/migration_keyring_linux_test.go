//go:build linux && nativekeyring

package contract

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func testNativeKeyringMigrationCommands(t *testing.T) {
	t.Helper()
	const token = "synthetic-native-migration-token"
	const previous = "synthetic-prior-flat-record"
	const service = "native-migration-historical"
	const slot = "example.invalid/work"
	for _, cloudID := range []string{"", "fixture-cloud"} {
		for _, mode := range []string{"json", "compact", "human"} {
			t.Run(mode+"/"+map[bool]string{true: "scoped", false: "classic"}[cloudID != ""], func(t *testing.T) {
				dir := t.TempDir()
				source := filepath.Join(dir, "old.toml")
				original := []byte("# Preserve the whole file.\ndefault_profile='work'\n[[profiles]]\nname='work'\nbase_url='https://example.invalid/'\nemail='migration@example.invalid'\n")
				original = append(original, []byte("cloud_id=\""+cloudID+"\"\n")...)
				if err := os.WriteFile(source, original, 0o600); err != nil {
					t.Fatal(err)
				}
				nativeKeyringTool(t, token, 0, "store", "--label=Native migration fixture", "service", service, "username", slot)
				defer nativeKeyringTool(t, "", 0, "clear", "service", service, "username", slot)
				nativeKeyringTool(t, previous, 0, "store", "--label=Native destination fixture", "service", "jira-cli", "username", "default")
				defer nativeKeyringTool(t, "", 0, "clear", "service", "jira-cli", "username", "default")
				env := []string{"JIRA_KEYRING=false", "JIRA_CLOUD_ID=", "JIRA_API_TOKEN=", "JIRA_EMAIL=", "JIRA_BASE_URL=", "JIRA_NO_UPDATE_CHECK=1"}
				for _, name := range []string{"DBUS_SESSION_BUS_ADDRESS", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
					env = append(env, name+"="+os.Getenv(name))
				}
				invoke := func(args ...string) string {
					t.Helper()
					argv := append([]string{"--no-input", "--output=" + mode}, args...)
					stdout, stderr, exit := runExecutableIOInDirStarted(t, os.Getenv("JIRA_TEST_KEYRING_BINARY"), dir, env, nil, nil, nil, argv...)
					if exit != 0 || strings.Contains(stdout+stderr, token) || strings.Contains(stdout+stderr, previous) {
						t.Fatal("native migration failed or exposed credentials", exit, stderr)
					}
					return stdout
				}
				payload := func(body string) []byte {
					t.Helper()
					if mode == "compact" {
						return []byte(body)
					}
					var frame struct {
						OK   bool            `json:"ok"`
						Data json.RawMessage `json:"data"`
					}
					if err := json.Unmarshal([]byte(body), &frame); err != nil || !frame.OK {
						t.Fatal("native migration did not return a successful envelope")
					}
					return frame.Data
				}
				recovery := filepath.Join(dir, "recovery")
				invoke("config", "migrate", "--source", source, "--config", source, "--recovery-dir", recovery, "--legacy-keyring-service", service, "--dry-run")
				if _, err := os.Stat(recovery); !os.IsNotExist(err) {
					t.Fatal("native preview published recovery")
				}
				body := invoke("config", "migrate", "--source", source, "--config", source, "--recovery-dir", recovery, "--legacy-keyring-service", service, "--yes")
				if mode != "human" {
					var data envelope.ConfigMigrateOutput
					if json.Unmarshal(payload(body), &data) != nil || !data.RecoveryPublished || data.Receipt == nil || data.Receipt.Status != "applied" || data.Receipt.CredentialState != "present" {
						t.Fatal("native conversion lost actual receipt")
					}
				}
				journal := filepath.Join(recovery, "migration.json")
				var journalMetadata struct {
					Resources []struct {
						Profile string                  `json:"profile"`
						Target  core.CredentialLocation `json:"target"`
						Backup  string                  `json:"backup"`
					} `json:"resources"`
				}
				journalBytes, err := os.ReadFile(journal)
				if err != nil || json.Unmarshal(journalBytes, &journalMetadata) != nil || len(journalMetadata.Resources) != 1 {
					t.Fatal("native recovery lost profile resource")
				}
				resource := journalMetadata.Resources[0]
				if resource.Profile != "work" || resource.Target.Service != "jira-cli" || uuid.Validate(resource.Target.Account) != nil || !core.ValidCredentialBackupID(resource.Backup) {
					t.Fatal("native recovery lost profile location or provider backup")
				}
				var selected struct {
					OK   bool                     `json:"ok"`
					Data envelope.ConfigGetOutput `json:"data"`
				}
				selectedBody := invoke("config", "get", "credential_id", "--config", source, "--profile", "work", "--output=json")
				if json.Unmarshal([]byte(selectedBody), &selected) != nil || !selected.OK || !selected.Data.Present || selected.Data.Profile != "work" || selected.Data.Value != resource.Target.Account {
					t.Fatal("migrated profile does not select the recorded credential location")
				}
				stored := strings.TrimSuffix(nativeKeyringTool(t, "", 0, "lookup", "service", "jira-cli", "username", resource.Target.Account), "\n")
				var record credentials.Record
				if json.Unmarshal([]byte(stored), &record) != nil || record.Version != 2 || record.CloudID != cloudID || record.Token != token || record.Site != "https://example.invalid" || record.Email != "migration@example.invalid" {
					t.Fatal("native conversion lost bound credential tuple")
				}
				if got := strings.TrimSuffix(nativeKeyringTool(t, "", 0, "lookup", "service", "jira-cli", "username", "default"), "\n"); got != previous {
					t.Fatal("native migration changed the unrelated flat credential")
				}
				preview := invoke("config", "rollback", "--journal", journal, "--dry-run")
				if mode != "human" {
					var data envelope.ConfigRollbackOutput
					if json.Unmarshal(payload(preview), &data) != nil || data.Restored || !data.CredentialInspectionRequired {
						t.Fatal("rollback preview claimed credential verification")
					}
				}
				if got := strings.TrimSuffix(nativeKeyringTool(t, "", 0, "lookup", "service", "jira-cli", "username", resource.Target.Account), "\n"); got != stored {
					t.Fatal("rollback preview changed native credential")
				}
				unrelated := filepath.Join(dir, "unrelated.toml")
				if err := os.WriteFile(unrelated, []byte("secret_backend='env'\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				invoke("config", "rollback", "--journal", journal, "--config", unrelated, "--yes")
				data, err := os.ReadFile(source)
				if err != nil || !bytes.Equal(data, original) {
					t.Fatal("native rollback lost exact legacy bytes")
				}
				if got := nativeKeyringTool(t, "", 1, "lookup", "service", "jira-cli", "username", resource.Target.Account); got != "" {
					t.Fatal("native rollback did not restore profile credential absence")
				}
				if got := strings.TrimSuffix(nativeKeyringTool(t, "", 0, "lookup", "service", "jira-cli", "username", "default"), "\n"); got != previous {
					t.Fatal("native rollback did not restore exact destination")
				}
				if got := strings.TrimSuffix(nativeKeyringTool(t, "", 0, "lookup", "service", service, "username", slot), "\n"); got != token {
					t.Fatal("native migration changed historical source")
				}
				var backup struct {
					Before, Expected struct {
						Account string `json:"account"`
						Present bool   `json:"present"`
						Value   string `json:"value"`
					}
				}
				retained := nativeKeyringTool(t, "", 0, "lookup", "service", "jira-cli", "username", resource.Backup)
				if json.Unmarshal([]byte(retained), &backup) != nil || backup.Before.Account != resource.Target.Account || backup.Before.Present || backup.Before.Value != "" || backup.Expected.Account != resource.Target.Account || !backup.Expected.Present || backup.Expected.Value != stored {
					t.Fatal("native rollback lost the retained absence and expected credential backup")
				}
			})
		}
	}
}
