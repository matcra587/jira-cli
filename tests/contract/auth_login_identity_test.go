package contract

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestAuthLoginEnvironmentBinaryPublishesPublicMetadataOnly(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		for _, preview := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/live", true: "/preview"}[preview], func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					email, token, ok := r.BasicAuth()
					if !ok || email != "person@example.com" || token != "synthetic-named-login-token" || r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/myself" {
						t.Error("login did not verify the selected environment tuple")
					}
					_, _ = io.WriteString(w, `{"accountId":"native-login-id","emailAddress":null,"displayName":"Native Login"}`)
				}))
				t.Cleanup(server.Close)
				path := filepath.Join(t.TempDir(), "absent", "settings.toml")
				env := append(editReceiptEnv(server.URL), "JIRA_SECRET_BACKEND=env", "JIRA_API_TOKEN=", "JIRA_API_TOKEN_ENV=JIRA_TOKEN_LOGIN", "JIRA_TOKEN_LOGIN=synthetic-named-login-token")
				args := []string{"auth", "login", "--config=" + path, "--no-input"}
				if mode == "agent" {
					env = append(env, "AGENT=1")
				} else {
					args = append(args, "--output="+mode)
				}
				if preview {
					args = append(args, "--dry-run")
				}
				stdout, stderr, exit := runBinaryEnv(t, env, args...)
				if exit != 0 || stderr != "" || calls.Load() != 1 || strings.Contains(stdout+stderr, "synthetic-named-login-token") {
					t.Fatalf("actual env login failed: exit=%d calls=%d stdout=%s stderr=%s", exit, calls.Load(), stdout, stderr)
				}
				if mode == "human" {
					if !strings.Contains(stdout, "native-login-id") || !strings.Contains(stdout, "JIRA_TOKEN_LOGIN") || !strings.Contains(stdout, "external") {
						t.Fatal("human login lost public verification or external provenance")
					}
				} else {
					format := mode
					if mode == "agent" {
						format = "compact"
					}
					var out envelope.AuthLoginOutput
					if json.Unmarshal(adfData(t, stdout, format, "auth.login", true), &out) != nil || !out.Verified || out.DryRun != preview || out.AccountID != "native-login-id" || out.Credential.Backend != core.CredentialBackendEnvironment || out.Credential.Reference != "JIRA_TOKEN_LOGIN" || out.Credential.Status != "external" || out.Credential.Saved || out.Credential.Attempted || out.Settings.Saved == preview || out.Settings.Changed == preview || out.Settings.Created == preview {
						t.Fatalf("false env login publication receipt: %s", stdout)
					}
				}
				if preview {
					if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("login preview created directories")
					}
					return
				}
				fresh, err := config.Resolve(t.Context(), config.Options{Path: path, Environ: func() []string { return nil }}, nil)
				if err != nil || fresh.Config.SecretBackend != core.CredentialBackendEnvironment || fresh.Config.APIToken != "" || fresh.Config.APITokenEnv != "JIRA_TOKEN_LOGIN" || fresh.Config.BaseURL != server.URL || fresh.Config.Email != "person@example.com" || fresh.Config.CachedAccountID() != "native-login-id" {
					t.Fatal("fresh-process metadata is unusable or contains a secret", err)
				}
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				freshEnv := []string{"JIRA_KEYRING=false", "JIRA_SECRET_BACKEND=", "JIRA_BASE_URL=", "JIRA_EMAIL=", "JIRA_CLOUD_ID=", "JIRA_API_TOKEN=", "JIRA_API_TOKEN_ENV=", "JIRA_TOKEN_LOGIN=synthetic-named-login-token", "JIRA_NO_UPDATE_CHECK=1"}
				stdout, stderr, exit = runBinaryEnv(t, freshEnv, "auth", "status", "--config="+path, "--output=json", "--no-input")
				if exit != 0 || stderr != "" || calls.Load() != 2 || !strings.Contains(stdout, `"account_id":"native-login-id"`) || !strings.Contains(stdout, `"token_source":"env"`) {
					t.Fatalf("fresh process did not authenticate from persisted public references: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
				}
				stdout, stderr, exit = runBinaryEnv(t, freshEnv, "auth", "login", "--config="+path, "--dry-run", "--output=human", "--no-input")
				if exit != 0 || stderr != "" || calls.Load() != 3 || !strings.Contains(stdout, "would keep verified login settings unchanged") {
					t.Fatalf("unchanged env preview was not truthful: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
				}
				after, err := os.ReadFile(path)
				if err != nil || string(after) != string(before) {
					t.Fatal("unchanged login preview rewrote authored bytes")
				}
			})
		}
	}
}

func TestAuthLoginEnvironmentBinaryRefusesUnsafeInputsWithoutProvidersOrLookup(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"missing token", "stdin supplied", "invalid reference", "invalid ancestor"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				_, _ = io.WriteString(w, `{"accountId":"native-id"}`)
			}))
			t.Cleanup(server.Close)
			path := filepath.Join(t.TempDir(), "absent", "settings.toml")
			env := append(editReceiptEnv(server.URL), "JIRA_SECRET_BACKEND=env", "JIRA_API_TOKEN=")
			args := []string{"auth", "login", "--config=" + path, "--output=json", "--no-input"}
			wantExit, wantCode := cli.ExitAuth, cli.CodeAuthMissing
			switch kind {
			case "stdin supplied":
				args = append(args, "--with-token")
				wantExit, wantCode = cli.ExitValidation, cli.CodeFlagValueInvalid
			case "invalid reference":
				env = append(env, "JIRA_API_TOKEN_ENV=PRIVATE_SECRET_VARIABLE")
				wantExit, wantCode = cli.ExitValidation, "validation_failed"
			case "invalid ancestor":
				if err := os.MkdirAll(filepath.Dir(filepath.Dir(path)), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Dir(path), []byte("regular ancestor"), 0o600); err != nil {
					t.Fatal(err)
				}
				wantExit, wantCode = cli.ExitValidation, "validation_failed"
			}
			stdout, stderr, exit := runBinaryEnvInputStarted(t, env, "synthetic-supplied-token\n", nil, args...)
			var frame struct {
				OK     bool             `json:"ok"`
				Errors []envelope.Error `json:"errors"`
			}
			if json.Unmarshal([]byte(stdout), &frame) != nil || exit != wantExit || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != wantCode || calls.Load() != 0 || strings.Contains(stdout+stderr, "synthetic-supplied-token") {
				t.Fatalf("unsafe env login entered work or misclassified: exit=%d calls=%d stdout=%s stderr=%s", exit, calls.Load(), stdout, stderr)
			}
		})
	}
}

func TestAuthLoginEnvironmentBinaryRetainsSettingsAfterOutputFailure(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"accountId":"native-id"}`) }))
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "settings.toml")
	stdout, stderr, exit := runBinaryEnv(t, append(editReceiptEnv(server.URL), "JIRA_SECRET_BACKEND=env"), "auth", "login", "--config="+path, "--output=json", "--jq=error(\"synthetic filter failure\")", "--no-input")
	var frame struct {
		OK     bool                     `json:"ok"`
		Data   envelope.AuthLoginOutput `json:"data"`
		Errors []envelope.Error         `json:"errors"`
	}
	if json.Unmarshal([]byte(stdout), &frame) != nil || exit != cli.ExitValidation || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeJQEvalFailed || !frame.Data.Verified || !frame.Data.Settings.Saved || !frame.Data.Settings.Changed || !frame.Data.Settings.Created || frame.Data.Credential.Saved || frame.Data.Credential.Status != "external" {
		t.Fatalf("output failure discarded actual env metadata publication: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
	}
	file, err := config.ReadFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := file.Setting("account_id")
	if err != nil || id.Value != "native-id" {
		t.Fatal("retained publication has no actual stored identity")
	}
}

func TestAuthLoginEnvironmentBinaryScopePreviewAndIndependentStdin(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"blocked preview", "blocked live", "independent stdin preview", "generic token priority"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			wantToken := "synthetic-generic-token"
			if kind == "independent stdin preview" {
				wantToken = "synthetic-independent-token"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, token, ok := r.BasicAuth()
				if !ok || token != wantToken {
					t.Error("login verified an unselected token")
				}
				_, _ = io.WriteString(w, `{"accountId":"actual-id"}`)
			}))
			t.Cleanup(server.Close)
			path := filepath.Join(t.TempDir(), "settings.toml")
			original := "api_token_env='JIRA_TOKEN_RETAINED'\n"
			if strings.HasPrefix(kind, "blocked") {
				original += "base_url='https://other.invalid'\n"
			}
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			env := append(editReceiptEnv(server.URL), "JIRA_SECRET_BACKEND=env", "JIRA_API_TOKEN=synthetic-generic-token", "JIRA_TOKEN_RETAINED=synthetic-named-fallback")
			args := []string{"auth", "login", "--config=" + path, "--output=json", "--no-input"}
			if kind != "blocked live" && kind != "generic token priority" {
				args = append(args, "--dry-run")
			}
			if kind == "independent stdin preview" {
				args = append(args, "--with-token")
			}
			stdout, stderr, exit := runBinaryEnvInputStarted(t, env, "synthetic-independent-token\n", nil, args...)
			var frame struct {
				OK       bool                     `json:"ok"`
				Data     envelope.AuthLoginOutput `json:"data"`
				Warnings []envelope.Warning       `json:"warnings"`
				Errors   []envelope.Error         `json:"errors"`
			}
			if json.Unmarshal([]byte(stdout), &frame) != nil || !frame.Data.Verified || frame.Data.AccountID != "actual-id" || calls.Load() != 1 || strings.Contains(stdout+stderr, wantToken) {
				t.Fatal("login boundary lost verification or leaked selected token")
			}
			switch kind {
			case "blocked preview":
				if exit != 0 || !frame.OK || frame.Data.Settings.Status != "blocked" || len(frame.Warnings) != 1 || frame.Warnings[0].Type != "login_settings_scope_conflict" {
					t.Fatal("blocked preview misrepresented publication", stdout)
				}
			case "blocked live":
				if exit != cli.ExitValidation || frame.OK || frame.Data.Settings.Saved || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeConfigInvalid {
					t.Fatal("live scope conflict was admitted or misclassified", stdout)
				}
			case "independent stdin preview":
				if exit != 0 || !frame.OK || frame.Data.Credential.Status != "not_saved" || frame.Data.Settings.Status != "not_prepared" {
					t.Fatal("independent stdin preview claimed configured-token publication", stdout)
				}
			case "generic token priority":
				if exit != 0 || !frame.OK || frame.Data.Credential.Reference != "JIRA_API_TOKEN" || !frame.Data.Settings.Saved {
					t.Fatal("generic token winner lost actual provenance", stdout)
				}
				file, err := config.ReadFile(t.Context(), path)
				if err != nil {
					t.Fatal(err)
				}
				ref, err := file.Setting("api_token_env")
				if err != nil || ref.Value != "JIRA_TOKEN_RETAINED" {
					t.Fatal("generic token publication replaced authored named fallback", err)
				}
				return
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != original || frame.Data.Settings.Saved || frame.Data.Credential.Saved || frame.Data.Credential.Attempted {
				t.Fatal("refused or preview login changed state")
			}
		})
	}
}
