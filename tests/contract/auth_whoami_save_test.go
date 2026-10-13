package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	cfg "github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestAuthWhoamiSaveBinaryPublishesOnlyVerifiedBoundIdentity(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		for _, preview := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/live", true: "/preview"}[preview], func(t *testing.T) {
				t.Parallel()
				const body = `{"accountId":"native-id","displayName":"Native Account","emailAddress":null,"future":{"large":9007199254740993,"null":null}}`
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/myself" {
						t.Errorf("unexpected identity request: %s %s", r.Method, r.URL.Path)
					}
					_, _ = io.WriteString(w, body)
				}))
				t.Cleanup(server.Close)
				path := filepath.Join(t.TempDir(), "absent", "settings.toml")
				args := []string{"auth", "whoami", "--save", "--config=" + path, "--no-input"}
				env := editReceiptEnv(server.URL)
				if mode == "agent" {
					env = append(env, "AGENT=1")
				} else {
					args = append(args, "--output="+mode)
				}
				if preview {
					args = append(args, "--dry-run")
				}
				stdout, stderr, exit := runBinaryEnv(t, env, args...)
				if exit != 0 || stderr != "" || calls.Load() != 1 || strings.Contains(stdout+stderr, "example-fixture-token") {
					t.Fatalf("identity save did not run cleanly: exit=%d calls=%d stdout=%s stderr=%s", exit, calls.Load(), stdout, stderr)
				}
				if mode == "human" {
					for _, want := range []string{"native-id", path} {
						if !strings.Contains(stdout, want) {
							t.Fatalf("human identity publication omits %q: %s", want, stdout)
						}
					}
				} else {
					format := mode
					if mode == "agent" {
						format = "compact"
					}
					data := adfData(t, stdout, format, "auth.whoami.save", true)
					var out envelope.AuthWhoamiSaveOutput
					if err := json.Unmarshal(data, &out); err != nil {
						t.Fatal(err)
					}
					if out.DryRun != preview || out.Scope.BaseURL != server.URL || out.Scope.Email != "person@example.com" || out.Scope.CloudID != "" || !out.Settings.WouldChange || out.Settings.Saved == preview || out.Settings.Changed == preview || out.Settings.Created == preview || !out.User.EmailAddress.Set || !out.User.EmailAddress.Null {
						t.Fatalf("false native identity or publication receipt: %+v", out)
					}
					var raw struct {
						User json.RawMessage `json:"user"`
					}
					if err := json.Unmarshal(data, &raw); err != nil {
						t.Fatal(err)
					}
					decode := func(value []byte) any {
						var result any
						decoder := json.NewDecoder(bytes.NewReader(value))
						decoder.UseNumber()
						if err := decoder.Decode(&result); err != nil {
							t.Fatal(err)
						}
						return result
					}
					if !reflect.DeepEqual(decode(raw.User), decode([]byte(body))) {
						t.Fatal("native unknown/null/large-number identity changed")
					}
				}
				if preview {
					if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("verified preview created directories")
					}
					return
				}
				file, err := cfg.ReadFile(t.Context(), path)
				if err != nil {
					t.Fatal(err)
				}
				for key, want := range map[string]string{"account_id": "native-id", "account_id_site": server.URL, "account_id_email": "person@example.com", "account_id_cloud_id": ""} {
					setting, err := file.Setting(key)
					if err != nil || !setting.Present || setting.Secret || setting.Value != want {
						t.Fatalf("stored binding %s: %+v %v", key, setting, err)
					}
				}
				savedBytes, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				stdout, stderr, exit = runBinaryEnv(t, env, append(append([]string{}, args...), "--dry-run")...)
				if exit != 0 || stderr != "" || calls.Load() != 2 {
					t.Fatalf("unchanged preview did not verify once: exit=%d calls=%d stderr=%s", exit, calls.Load(), stderr)
				}
				if mode == "human" {
					if !strings.Contains(stdout, "would keep verified cached identity unchanged") || strings.Contains(stdout, "would save verified cached identity") {
						t.Fatalf("unchanged human preview claims a write: %s", stdout)
					}
				} else {
					format := mode
					if mode == "agent" {
						format = "compact"
					}
					var kept envelope.AuthWhoamiSaveOutput
					if err := json.Unmarshal(adfData(t, stdout, format, "auth.whoami.save", true), &kept); err != nil {
						t.Fatal(err)
					}
					if !kept.DryRun || kept.Settings.WouldChange || kept.Settings.Saved || kept.Settings.Changed || kept.Settings.Created || kept.Settings.Status != "prepared" {
						t.Fatalf("unchanged preview has false publication receipt: %+v", kept.Settings)
					}
				}
				afterPreview, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(savedBytes, afterPreview) {
					t.Fatal("unchanged preview normalized or replaced stored settings")
				}
				stdout, stderr, exit = runBinaryEnv(t, env, "issue", "watchers", "add", "KEY-1", "--user=@me", "--dry-run", "--config="+path, "--output=json")
				if exit != 0 || stderr != "" || calls.Load() != 2 || !strings.Contains(stdout, `"account_id":"native-id"`) || !strings.Contains(stdout, `"type":"account_id_cached"`) {
					t.Fatalf("saved identity not consumed offline: exit=%d calls=%d stdout=%s stderr=%s", exit, calls.Load(), stdout, stderr)
				}
			})
		}
	}
}

func TestAuthWhoamiSaveBinaryRefusesScopeOverlaysBeforeLookup(t *testing.T) {
	t.Parallel()
	for _, original := range []string{"base_url='https://other.invalid'\n", "email='other@example.invalid'\n", "cloud_id='other-cloud'\n"} {
		t.Run(original, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				_, _ = io.WriteString(w, `{"accountId":"native-id"}`)
			}))
			t.Cleanup(server.Close)
			path := filepath.Join(t.TempDir(), "settings.toml")
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			env := editReceiptEnv(server.URL)
			if strings.HasPrefix(original, "cloud_id=") {
				env = append(env, "JIRA_BASE_URL=https://example.invalid", "JIRA_CLOUD_ID=verified-cloud")
			}
			stdout, stderr, exit := runBinaryEnv(t, env, "auth", "whoami", "--save", "--config="+path, "--output=json")
			var frame struct {
				OK     bool             `json:"ok"`
				Meta   envelope.Meta    `json:"meta"`
				Errors []envelope.Error `json:"errors"`
			}
			if err := json.Unmarshal([]byte(stdout), &frame); err != nil {
				t.Fatalf("scope-refusal invocation: exit=%d stdout=%s stderr=%s: %v", exit, stdout, stderr, err)
			}
			if exit != cli.ExitValidation || frame.OK || frame.Meta.Command != "auth.whoami.save" || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeConfigInvalid || calls.Load() != 0 || strings.Contains(stdout+stderr, "example-fixture-token") {
				t.Fatalf("scope overlay entered lookup or misclassified: exit=%d calls=%d frame=%s stderr=%s", exit, calls.Load(), stdout, stderr)
			}
			contents, err := os.ReadFile(path)
			if err != nil || string(contents) != original {
				t.Fatal("scope overlay rewrote original settings")
			}
		})
	}
}

func TestAuthWhoamiSaveBinaryRefusesChangedSnapshotAndRetainsVerifiedReply(t *testing.T) {
	t.Parallel()
	for _, unchangedCandidate := range []bool{false, true} {
		t.Run(map[bool]string{false: "changed candidate", true: "unchanged candidate"}[unchangedCandidate], func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "settings.toml")
			const intervening = "theme='nord'\n"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if err := os.WriteFile(path, []byte(intervening), 0o600); err != nil {
					t.Error(err)
				}
				_, _ = io.WriteString(w, `{"accountId":"native-id","emailAddress":null}`)
			}))
			t.Cleanup(server.Close)
			original := "theme='dark'\n"
			if unchangedCandidate {
				original += "account_id='native-id'\naccount_id_site='" + server.URL + "'\naccount_id_email='person@example.com'\naccount_id_cloud_id=''\n"
			}
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "auth", "whoami", "--save", "--config="+path, "--output=json")
			var frame struct {
				OK     bool                          `json:"ok"`
				Meta   envelope.Meta                 `json:"meta"`
				Data   envelope.AuthWhoamiSaveOutput `json:"data"`
				Errors []envelope.Error              `json:"errors"`
			}
			if err := json.Unmarshal([]byte(stdout), &frame); err != nil {
				t.Fatal(err)
			}
			if exit != cli.ExitValidation || frame.OK || frame.Meta.Command != "auth.whoami.save" || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeConfigChanged || frame.Data.User.AccountID.Value != "native-id" || frame.Data.Settings.Saved || frame.Data.Settings.Changed || frame.Data.Settings.Status != "not_saved" || strings.Contains(stdout+stderr, "example-fixture-token") {
				t.Fatalf("snapshot conflict lost verified evidence or claimed publication: %s stderr=%s", stdout, stderr)
			}
			contents, err := os.ReadFile(path)
			if err != nil || string(contents) != intervening {
				t.Fatal("snapshot conflict replaced external edit")
			}
		})
	}
}

func TestAuthWhoamiSaveBinaryFilterFailureRetainsActualPublication(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"accountId":"native-id"}`) }))
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "settings.toml")
	stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "auth", "whoami", "--save", "--config="+path, "--output=json", `--jq=error("synthetic filter failure")`)
	var frame struct {
		OK   bool                          `json:"ok"`
		Meta envelope.Meta                 `json:"meta"`
		Data envelope.AuthWhoamiSaveOutput `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &frame); err != nil {
		t.Fatal(err)
	}
	if exit != cli.ExitValidation || frame.OK || frame.Meta.Command != "auth.whoami.save" || !frame.Data.Settings.Saved || !frame.Data.Settings.Changed || !frame.Data.Settings.Created || frame.Data.User.AccountID.Value != "native-id" || strings.Contains(stdout+stderr, "example-fixture-token") {
		t.Fatalf("filter failure lost committed outcome: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
	}
	file, err := cfg.ReadFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	setting, err := file.Setting("account_id")
	if err != nil || !setting.Present || setting.Value != "native-id" {
		t.Fatal("publication receipt not backed by actual file")
	}
}
