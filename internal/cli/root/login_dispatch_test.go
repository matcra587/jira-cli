package root

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/1password/onepassword-sdk-go"

	adapteronepassword "github.com/matcra587/jira-cli/internal/adapters/onepassword"
	"github.com/matcra587/jira-cli/internal/cli"
	authcmd "github.com/matcra587/jira-cli/internal/cli/auth"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

type loginDispatchClient struct{ core.JiraClient }

func (loginDispatchClient) CurrentUser(context.Context) (core.UserRead, error) {
	return core.UserRead{User: &jira.UserDetails{AccountID: jira.NullableOf("dispatch-id"), EmailAddress: jira.NullableNull[string]()}}, nil
}

type loginDispatchSDK struct {
	*sdkAuthFixture
	after func() error
}

func (s loginDispatchSDK) PutItem(ctx context.Context, item sdk.Item) (sdk.Item, error) {
	result, err := s.sdkAuthFixture.PutItem(ctx, item)
	if err == nil && s.after != nil {
		err = s.after()
	}
	return result, err
}

func TestLoginSDKDispatchRetainsActualPublicationAcrossModes(t *testing.T) {
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		for _, outcome := range []string{"saved", "provider unknown", "settings conflict", "output failure"} {
			if mode == "human" && outcome == "output failure" {
				continue
			}
			t.Run(mode+"/"+outcome, func(t *testing.T) {
				const token = "synthetic-dispatch-token"
				path := filepath.Join(t.TempDir(), "settings.toml")
				for key, value := range map[string]string{
					"JIRA_BASE_URL": "https://example.invalid", "JIRA_EMAIL": "public@example.invalid", "JIRA_CLOUD_ID": "", "JIRA_API_TOKEN": "", "JIRA_API_TOKEN_ENV": "", "JIRA_SECRET_BACKEND": "1password", "JIRA_ONEPASSWORD_ACCOUNT": "desktop", "JIRA_ONEPASSWORD_VAULT": "vault-id", "JIRA_ONEPASSWORD_ITEM": "item-id", "AGENT": "", "JIRA_NO_UPDATE_CHECK": "1",
				} {
					t.Setenv(key, value)
				}
				config.SetFlagLayer(config.FlagLayer{Path: path})
				t.Cleanup(func() { config.SetFlagLayer(config.FlagLayer{}) })
				fixture := &sdkAuthFixture{item: sdk.Item{ID: "item-id", VaultID: "vault-id", Fields: []sdk.ItemField{{ID: "unmanaged", Value: "retained"}}}}
				provider := loginDispatchSDK{sdkAuthFixture: fixture, after: func() error {
					if outcome == "provider unknown" {
						return errors.New("synthetic-private-provider-payload")
					}
					if outcome == "settings conflict" {
						return os.WriteFile(path, []byte("# independently changed public settings\n"), 0o600)
					}
					return nil
				}}
				store := adapteronepassword.New(adapteronepassword.Config{Account: "desktop", Vault: "vault-id", Item: "item-id", LookupEnv: func(string) (string, bool) { return "synthetic-service-token", true }, ClientFactory: func(context.Context, adapteronepassword.ClientOptions) (adapteronepassword.Client, error) {
					return provider, nil
				}})
				deps := testDeps(t)
				defaultsCalls, clientCalls, providerCalls := 0, 0, 0
				deps.LoginSite = func(ctx context.Context, defaults authcmd.LoginDefaults) (authcmd.SiteInfo, error) {
					defaultsCalls++
					return resolveLoginSite(ctx, config.Options{Path: path, PermitMissingTarget: true}, defaults, func(config.Config) core.CredentialStore {
						t.Fatal("complete public defaults opened old provider")
						return nil
					})
				}
				deps.IdentitySaveClientFor = func(site, email, candidate, cloud string) (core.JiraClient, error) {
					clientCalls++
					if site != "https://example.invalid" || email != "public@example.invalid" || candidate != token || cloud != "" {
						t.Fatal("SDK dispatch changed verified tuple")
					}
					return loginDispatchClient{}, nil
				}
				deps.LoginCredentials = func(ctx context.Context, selected core.CredentialLocation) (core.CredentialStore, error) {
					providerCalls++
					if ctx.Err() != nil || selected.Backend != core.CredentialBackendOnePassword || selected.SDKAccount != "desktop" || selected.Vault != "vault-id" || selected.Item != "item-id" {
						t.Fatal("SDK dispatch changed publication destination")
					}
					return store, nil
				}
				var stdout, stderr bytes.Buffer
				program, sink := newProgram(deps, &stdout, &stderr)
				program.Root.SetIn(strings.NewReader(token + "\n"))
				args := []string{"auth", "login", "--with-token", "--config=" + path, "--no-input"}
				if mode == "agent" {
					t.Setenv("AGENT", "1")
					deps.Detect = func() cli.Detection { return cli.Detection{Agent: true, Format: output.FormatCompact} }
				} else {
					args = append(args, "--output="+mode)
				}
				if outcome == "output failure" {
					args = append(args, "--jq=error(\"fixture output failure\")")
				}
				exit := execute(t.Context(), program, sink, args)
				if (exit == 0) != (outcome == "saved") || defaultsCalls != 1 || clientCalls != 1 || providerCalls != 1 || fixture.puts != 1 || fixture.creates != 0 {
					t.Fatalf("SDK publication skipped or repeated: exit=%d output=%s error=%s", exit, &stdout, &stderr)
				}
				if strings.Contains(stdout.String()+stderr.String(), token) || strings.Contains(stdout.String()+stderr.String(), "synthetic-private-provider-payload") {
					t.Fatal("SDK result exposed private material")
				}
				if mode == "human" {
					if !strings.Contains(stdout.String(), "dispatch-id") || !strings.Contains(stdout.String(), "1password") {
						t.Fatal("human SDK result lost retained public candidate")
					}
				} else {
					data := json.RawMessage(stdout.Bytes())
					if mode == "json" || outcome != "saved" {
						var frame struct {
							Data   json.RawMessage  `json:"data"`
							Errors []envelope.Error `json:"errors"`
						}
						if json.Unmarshal(stdout.Bytes(), &frame) != nil {
							t.Fatal("SDK failure lost envelope")
						}
						data = frame.Data
						if outcome == "output failure" && (len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeJQEvalFailed) {
							t.Fatal("SDK output failure lost classification")
						}
					}
					var out envelope.AuthLoginOutput
					if json.Unmarshal(data, &out) != nil || !out.Verified || out.AccountID != "dispatch-id" || out.Credential.Backend != core.CredentialBackendOnePassword || !out.Credential.Attempted || out.Credential.Saved != (outcome != "provider unknown") || out.Settings.Saved != (outcome == "saved" || outcome == "output failure") {
						t.Fatalf("SDK retained receipt denied actual effects: %s", &stdout)
					}
					if outcome == "provider unknown" && out.Credential.Status != "unknown" {
						t.Fatal("unknown SDK write was denied")
					}
				}
				if outcome == "saved" || outcome == "output failure" {
					fresh, err := config.Resolve(t.Context(), config.Options{Path: path, Environ: func() []string { return nil }}, nil)
					if err != nil || fresh.Config.SecretBackend != core.CredentialBackendOnePassword || fresh.Config.OnePasswordAccount != "desktop" || fresh.Config.OnePasswordVault != "vault-id" || fresh.Config.OnePasswordItem != "item-id" || fresh.Config.CachedAccountID() != "dispatch-id" || fresh.Config.APIToken != "" {
						t.Fatal("fresh SDK process lost public selection or stored a secret", err)
					}
				}
			})
		}
	}
}
