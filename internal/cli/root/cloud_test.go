package root

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/1password/onepassword-sdk-go"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	adapteronepassword "github.com/matcra587/jira-cli/internal/adapters/onepassword"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
)

type cloudRootTransport func(*http.Request) (*http.Response, error)

func (transport cloudRootTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

// This composes the real parser, credential resolution, SDK adapter and Jira
// transport. Only provider and network effects use synthetic fixtures.
func TestScopedLoginRequiresActiveConfiguredBindingAndFreshStatus(t *testing.T) {
	for _, mode := range []string{"human", "json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			for _, name := range []string{"JIRA_API_TOKEN", "JIRA_API_TOKEN_ENV", "JIRA_CLOUD_ID", "JIRA_EMAIL", "JIRA_BASE_URL", "JIRA_SECRET_BACKEND"} {
				t.Setenv(name, "")
			}
			path := filepath.Join(t.TempDir(), "config.toml")
			prepared, err := config.PrepareLegacyMigration(t.Context(), []byte("default_profile='work'\n[[profiles]]\nname='work'\nbase_url='https://fixture.atlassian.net'\nemail='person@example.invalid'\nsecret_backend='keyring'\n"), "", "")
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := prepared.Candidate()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, candidate, 0o600); err != nil {
				t.Fatal(err)
			}
			fixture := &sdkAuthFixture{item: sdk.Item{ID: "item-id", VaultID: "vault-id", Title: "Scoped Fixture", Fields: []sdk.ItemField{{ID: "personal", Value: "retain"}}}}
			store := adapteronepassword.New(adapteronepassword.Config{Vault: "vault-id", Item: "item-id", LookupEnv: func(string) (string, bool) { return "synthetic-service-token", true }, ClientFactory: func(context.Context, adapteronepassword.ClientOptions) (adapteronepassword.Client, error) {
				return fixture, nil
			}})
			requests := []string{}
			transport := cloudRootTransport(func(request *http.Request) (*http.Response, error) {
				endpoint := request.URL.String()
				requests = append(requests, endpoint)
				body, code := "", http.StatusOK
				switch endpoint {
				case "https://fixture.atlassian.net/rest/api/3/myself":
					code = http.StatusUnauthorized
				case "https://fixture.atlassian.net/_edge/tenant_info":
					if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
						t.Fatal("public discovery received credentials")
					}
					body = `{"cloudId":"fixture-cloud"}`
				case "https://api.atlassian.com/ex/jira/fixture-cloud/rest/api/3/myself":
					email, token, ok := request.BasicAuth()
					if !ok || email != "person@example.invalid" || token != "synthetic-scoped-token" {
						t.Fatal("gateway lost candidate identity")
					}
					body = `{"accountId":"fixture-account","displayName":"Scoped Fixture"}`
				default:
					t.Fatalf("unexpected request %s", endpoint)
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})
			deps := testDeps(t)
			deps.Credentials = func() (core.CredentialStore, error) { return store, nil }
			deps.JiraClientFor = func(site, email, token, cloudID string) (core.JiraClient, error) {
				return adapterjira.New(adapterjira.Config{BaseURL: site, CloudID: cloudID, Email: email, APIToken: token, HTTPClient: &http.Client{Transport: transport}})
			}
			invoke := func(stdin string, args ...string) (int, string) {
				t.Helper()
				args = append(args, "--config", path, "--output="+mode, "--no-input")
				applyGlobalFlags(args)
				defer applyGlobalFlags(nil)
				var stdout, stderr bytes.Buffer
				program, sink := newProgram(deps, &stdout, &stderr)
				program.Root.SetIn(strings.NewReader(stdin))
				exit := execute(t.Context(), program, sink, args)
				if strings.Contains(stdout.String()+stderr.String(), "synthetic-scoped-token") {
					t.Fatal("ordinary output exposed token")
				}
				return exit, stdout.String() + stderr.String()
			}
			exit, output := invoke("synthetic-scoped-token\n", "auth", "login", "--with-token")
			if exit != 3 || fixture.puts != 0 || (mode != "human" && (!strings.Contains(output, "cloud_id") || !strings.Contains(output, "fixture-cloud"))) {
				t.Fatalf("inactive login published: exit=%d writes=%d output=%s", exit, fixture.puts, output)
			}
			exit, _ = invoke("", "auth", "status")
			if exit != 1 || fixture.puts != 0 {
				t.Fatal("refused login became authenticated")
			}
			exit, _ = invoke("synthetic-scoped-token\n", "auth", "login", "--with-token", "--dry-run")
			if exit != 0 || fixture.puts != 0 {
				t.Fatal("scope preview could not verify independently")
			}
			exit, _ = invoke("", "config", "set", "cloud_id", "fixture-cloud")
			if exit != 0 {
				t.Fatal("scope repair failed")
			}
			for _, spelling := range [][]string{{"--cloud-id=fixture-cloud"}, {"--cloud-id", "fixture-cloud"}} {
				requests = nil
				args := append([]string{"auth", "login", "--with-token"}, spelling...)
				exit, output = invoke("synthetic-scoped-token\n", args...)
				if exit != 0 || len(requests) != 1 || !strings.Contains(output, "fixture-cloud") {
					t.Fatalf("explicit scoped parser failed: exit=%d requests=%v output=%s", exit, requests, output)
				}
				requests = nil
				exit, output = invoke("", "auth", "status")
				if exit != 0 || len(requests) != 1 || !strings.Contains(output, "fixture-cloud") {
					t.Fatalf("fresh status lost saved scope: exit=%d requests=%v output=%s", exit, requests, output)
				}
				if mode == "json" {
					var frame struct {
						OK   bool `json:"ok"`
						Data struct {
							CloudID string `json:"cloud_id"`
							BaseURL string `json:"base_url"`
						} `json:"data"`
					}
					if json.Unmarshal([]byte(output), &frame) != nil || !frame.OK || frame.Data.CloudID != "fixture-cloud" || frame.Data.BaseURL != "https://fixture.atlassian.net" {
						t.Fatal("typed frame lost site/scope")
					}
				}
			}
			requests = nil
			writes := fixture.puts
			exit, _ = invoke("synthetic-scoped-token\n", "auth", "login", "--with-token", "--cloud-id=")
			if exit != 1 || len(requests) != 1 || requests[0] != "https://fixture.atlassian.net/rest/api/3/myself" || fixture.puts != writes {
				t.Fatal("explicit empty scope triggered discovery or publication")
			}
		})
	}
}
