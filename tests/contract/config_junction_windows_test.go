//go:build windows && nativefilesystem

package contract

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/testfixture"
)

func TestNativePublicWindowsConfigurationJunctions(t *testing.T) {
	for _, mode := range []string{"json", "compact", "human"} {
		for _, selection := range []string{"default", "explicit"} {
			t.Run("init/"+mode+"/"+selection, func(t *testing.T) {
				dir := t.TempDir()
				target, link := filepath.Join(dir, "actual '古い"), filepath.Join(dir, "selected 'é")
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
				before := testfixture.Junction(t, link, target)
				server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					t.Error("configuration initialization contacted Jira")
				}))
				defer server.Close()
				path := filepath.Join(target, "jira", "config.toml")
				args := []string{"config", "init", "--base-url", server.URL, "--email=owned@example.invalid", "--no-input", "--output=" + mode}
				if selection == "explicit" {
					path = filepath.Join(target, "missing", "nested", "settings.toml")
					args = append(args, "--config", filepath.Join(link, "missing", "nested", "settings.toml"))
				}
				env := []string{"XDG_CONFIG_HOME=" + link, "JIRA_KEYRING=false", "JIRA_API_TOKEN=", "OP_SERVICE_ACCOUNT_TOKEN=", "JIRA_NO_UPDATE_CHECK=1"}
				for _, preview := range []bool{true, false} {
					invocation := append([]string(nil), args...)
					if preview {
						invocation = append(invocation, "--dry-run")
					}
					stdout, stderr, exit := runBinaryEnv(t, env, invocation...)
					if exit != 0 || stderr != "" || stdout == "" {
						t.Fatalf("junction initialization failed: exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
					}
					if mode == "human" {
						state := "created config"
						if preview {
							state = "would create config"
						}
						if !strings.Contains(stdout, state) || !strings.Contains(stdout, path) {
							t.Fatalf("human initialization lost canonical path or state: %q", stdout)
						}
					} else {
						got := decodeConfigInit(t, mode, stdout)
						if got.Path != path || got.DryRun != preview || got.Created == preview || !got.WouldCreate || got.BaseURL != server.URL || got.Email != "owned@example.invalid" {
							t.Fatalf("junction initialization lost canonical publication: %+v", got)
						}
					}
					if preview {
						if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
							t.Fatalf("junction preview published directories: %v", err)
						}
					}
				}
				data, err := os.ReadFile(path)
				if err != nil || config.ValidateFile(data) != nil || !bytes.Contains(data, []byte("owned@example.invalid")) {
					t.Fatalf("canonical target did not receive public settings: %v", err)
				}
				testfixture.SameJunction(t, link, target, before)
			})
		}
		for _, existing := range []bool{false, true} {
			name := "absent"
			if existing {
				name = "existing"
			}
			t.Run("migration/"+mode+"/"+name, func(t *testing.T) {
				dir := t.TempDir()
				actual, link := filepath.Join(dir, "actual"), filepath.Join(dir, "selected")
				if err := os.Mkdir(actual, 0o700); err != nil {
					t.Fatal(err)
				}
				before := testfixture.Junction(t, link, actual)
				source, selected := filepath.Join(dir, "old.toml"), filepath.Join(link, "missing", "settings.toml")
				target, recovery := filepath.Join(actual, "missing", "settings.toml"), filepath.Join(dir, "recovery")
				original := []byte("# retain this entire source\ndefault_profile='work'\n[[profiles]]\nname='other'\nemail='other@example.invalid'\n[[profiles]]\nname='work'\nbase_url='https://example.invalid'\nemail='owned@example.invalid'\nsecret_backend='env'\n")
				if err := os.WriteFile(source, original, 0o600); err != nil {
					t.Fatal(err)
				}
				preimage := []byte("# exact public target preimage\nbase_url='https://previous.invalid'\nemail='previous@example.invalid'\ntimeout=17\n")
				if existing {
					if err := os.Mkdir(filepath.Dir(target), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(target, preimage, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				env := []string{"JIRA_API_TOKEN=", "JIRA_KEYRING=false", "JIRA_SECRET_BACKEND=1password", "OP_SERVICE_ACCOUNT_TOKEN=", "JIRA_NO_UPDATE_CHECK=1"}
				invoke := func(args ...string) string {
					t.Helper()
					stdout, stderr, exit := runBinaryEnv(t, env, append(args, "--output="+mode, "--no-input")...)
					if exit != 0 || stderr != "" || stdout == "" {
						t.Fatalf("junction migration failed: exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
					}
					return stdout
				}
				checkMigration := func(stdout string, preview bool) {
					t.Helper()
					if mode == "human" {
						state := "converted profiles other, work into"
						if preview {
							state = "would convert profiles other, work into"
						}
						if !strings.Contains(strings.Join(strings.Fields(stdout), " "), state) || !strings.Contains(stdout, target) {
							t.Fatalf("human migration lost canonical path or state: %q", stdout)
						}
						return
					}
					var got envelope.ConfigMigrateOutput
					decodeWindowsJunctionOutput(t, mode, stdout, "config.migrate", &got)
					if got.Path != target || got.Profile != "work" || !got.Ready || got.DryRun != preview || got.RecoveryPublished == preview {
						t.Fatalf("junction migration lost canonical candidate or state: %+v", got)
					}
					if !preview && (got.Receipt == nil || got.Receipt.CredentialState != "external" || got.Receipt.Status != "applied" || !got.Receipt.JournalDurable) {
						t.Fatalf("junction migration lost acknowledged external credential scope: %+v", got.Receipt)
					}
				}
				checkMigration(invoke("config", "migrate", "--source", source, "--config", selected, "--recovery-dir", recovery, "--dry-run"), true)
				if _, err := os.Stat(recovery); !os.IsNotExist(err) {
					t.Fatalf("migration preview published recovery: %v", err)
				}
				if !existing {
					if _, err := os.Stat(filepath.Dir(target)); !os.IsNotExist(err) {
						t.Fatalf("migration preview published target directories: %v", err)
					}
				}
				checkMigration(invoke("config", "migrate", "--source", source, "--config", selected, "--recovery-dir", recovery, "--yes"), false)
				candidate, err := os.ReadFile(target)
				if err != nil || config.ValidateFile(candidate) != nil || !bytes.Contains(candidate, []byte("JIRA_TOKEN_WORK")) {
					t.Fatalf("junction migration lost complete public candidate: %v", err)
				}
				journal := filepath.Join(recovery, "migration.json")
				checkRollback := func(stdout string, preview bool) {
					t.Helper()
					if mode == "human" {
						state := "restored migration preimages using"
						if preview {
							state = "would restore migration preimages using"
						}
						if !strings.Contains(stdout, state) || !strings.Contains(stdout, journal) {
							t.Fatalf("human rollback lost journal or state: %q", stdout)
						}
						return
					}
					var got envelope.ConfigRollbackOutput
					decodeWindowsJunctionOutput(t, mode, stdout, "config.rollback", &got)
					if got.Journal != journal || got.DryRun != preview || got.Restored == preview || !got.WouldRestoreConfiguration || got.CredentialInspectionRequired || got.Receipt == nil {
						t.Fatalf("junction rollback lost configuration-only state: %+v", got)
					}
					if !preview && (got.Receipt.Status != "rolled_back" || !got.Receipt.JournalDurable) {
						t.Fatalf("junction rollback lost acknowledged completion: %+v", got.Receipt)
					}
				}
				checkRollback(invoke("config", "rollback", "--journal", journal, "--dry-run"), true)
				afterPreview, err := os.ReadFile(target)
				if err != nil || !bytes.Equal(afterPreview, candidate) {
					t.Fatalf("rollback preview changed target: %v", err)
				}
				checkRollback(invoke("config", "rollback", "--journal", journal, "--yes"), false)
				got, err := os.ReadFile(target)
				if existing {
					if err != nil || !bytes.Equal(got, preimage) {
						t.Fatalf("rollback lost exact target preimage: %v", err)
					}
				} else if !os.IsNotExist(err) {
					t.Fatalf("rollback lost target absence: %v", err)
				}
				got, err = os.ReadFile(source)
				if err != nil || !bytes.Equal(got, original) {
					t.Fatalf("rollback changed complete legacy source: %v", err)
				}
				testfixture.SameJunction(t, link, actual, before)
			})
		}
	}
}

func decodeWindowsJunctionOutput(t *testing.T, mode, stdout, op string, target any) {
	t.Helper()
	data := []byte(stdout)
	if mode == "json" {
		var frame struct {
			OK   bool            `json:"ok"`
			Data json.RawMessage `json:"data"`
			Meta struct {
				Command string `json:"command"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(data, &frame); err != nil || !frame.OK || frame.Meta.Command != op {
			t.Fatalf("junction command lost success envelope: %v %q", err, stdout)
		}
		data = frame.Data
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}
