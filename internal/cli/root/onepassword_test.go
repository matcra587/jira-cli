package root

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	sdk "github.com/1password/onepassword-sdk-go"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	adapteronepassword "github.com/matcra587/jira-cli/internal/adapters/onepassword"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
)

type sdkAuthFixture struct {
	item    sdk.Item
	puts    int
	creates int
}

func (*sdkAuthFixture) ListVaults(context.Context) ([]sdk.VaultOverview, error) {
	return []sdk.VaultOverview{{ID: "vault-id", Title: "Fixture Vault"}}, nil
}

func (s *sdkAuthFixture) ListItems(context.Context, string) ([]sdk.ItemOverview, error) {
	return []sdk.ItemOverview{{ID: s.item.ID, Title: s.item.Title}}, nil
}

func (s *sdkAuthFixture) GetItem(context.Context, string, string) (sdk.Item, error) {
	item := s.item
	item.Fields = slices.Clone(item.Fields)
	return item, nil
}

func (s *sdkAuthFixture) CreateItem(context.Context, sdk.ItemCreateParams) (sdk.Item, error) {
	s.creates++
	return sdk.Item{}, errors.New("existing fixture item must not be created")
}

func (s *sdkAuthFixture) PutItem(_ context.Context, item sdk.Item) (sdk.Item, error) {
	s.puts++
	s.item = item
	return item, nil
}

func TestAuthCommandsUseSDKRecordAndPreserveUnmanagedItem(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("JIRA_API_TOKEN", "")
	t.Setenv("JIRA_EMAIL", "")
	t.Setenv("JIRA_BASE_URL", "")
	const candidate = "synthetic-candidate-token"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		email, token, ok := r.BasicAuth()
		if !ok || email != "public@example.invalid" || token != candidate || r.URL.Path != "/rest/api/3/myself" {
			t.Error("Jira verification received an unexpected identity or endpoint")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accountId":"fixture-account","displayName":"Fixture User","futureField":null}`))
	}))
	defer server.Close()
	fixture := &sdkAuthFixture{item: sdk.Item{ID: "item-id", VaultID: "vault-id", Title: "Fixture Item", Notes: "retain notes", Tags: []string{"unmanaged"}, Version: 7, Fields: []sdk.ItemField{{ID: "personal", Title: adapteronepassword.CredentialFieldID, Value: "retain field", FieldType: sdk.ItemFieldTypeText}}}}
	before := fixture.item
	factories := 0
	store := adapteronepassword.New(adapteronepassword.Config{Vault: "vault-id", Item: "item-id", LookupEnv: func(string) (string, bool) { return "synthetic-service-account-token", true }, ClientFactory: func(context.Context, adapteronepassword.ClientOptions) (adapteronepassword.Client, error) {
		factories++
		return fixture, nil
	}})
	deps := testDeps(t)
	deps.Credentials = func() (core.CredentialStore, error) { return store, nil }
	deps.JiraClientFor = func(site, email, token, cloudID string) (core.JiraClient, error) {
		return adapterjira.New(adapterjira.Config{BaseURL: site, CloudID: cloudID, Email: email, APIToken: token, HTTPClient: server.Client()})
	}
	invoke := func(stdin string, args ...string) map[string]json.RawMessage {
		t.Helper()
		var stdout, stderr bytes.Buffer
		program, sink := newProgram(deps, &stdout, &stderr)
		program.Root.SetIn(strings.NewReader(stdin))
		args = append(args, "--output=json", "--no-input")
		if exit := execute(t.Context(), program, sink, args); exit != 0 {
			t.Fatalf("auth command failed: exit=%d stdout=%s stderr=%s", exit, &stdout, &stderr)
		}
		var frame struct {
			OK   bool                       `json:"ok"`
			Data map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil || !frame.OK {
			t.Fatalf("invalid success frame: %v", err)
		}
		if bytes.Contains(stdout.Bytes(), []byte(candidate)) {
			t.Fatal("ordinary auth frame leaked token")
		}
		return frame.Data
	}
	invoke(candidate+"\n", "auth", "login", "--with-token", "--site", server.URL, "--email", "public@example.invalid")
	status := invoke("", "auth", "status")
	if string(status["token_source"]) != `"1password"` || string(status["account_id"]) != `"fixture-account"` {
		t.Fatal("status lost SDK provenance or verified identity")
	}
	if fixture.puts != 1 || fixture.creates != 0 || factories != 1 || calls != 2 {
		t.Fatalf("unexpected live effects: writes=%d creates=%d factories=%d Jira=%d", fixture.puts, fixture.creates, factories, calls)
	}
	var tokenOut, tokenErr bytes.Buffer
	program, sink := newProgram(deps, &tokenOut, &tokenErr)
	if exit := execute(t.Context(), program, sink, []string{"auth", "token", "--output=json", "--no-input"}); exit != 0 || tokenOut.String() != candidate+"\n" || tokenErr.Len() != 0 {
		t.Fatal("SDK token command did not retain its explicit raw-secret contract")
	}
	if calls != 2 || fixture.puts != 1 {
		t.Fatal("token lookup performed Jira verification or a store mutation")
	}
	logout := invoke("", "auth", "logout", "--yes")
	if _, present := logout["remaining_source"]; present {
		t.Fatal("logout still claimed SDK credential source")
	}
	if fixture.puts != 2 || !reflect.DeepEqual(fixture.item, before) {
		t.Fatal("logout lost unmanaged metadata or failed to remove only owned field")
	}
}

func TestConfiguredSDKStoreSelectionInitializesNothing(t *testing.T) {
	for _, backend := range []core.CredentialBackend{core.CredentialBackendKeyring, core.CredentialBackendEnvironment, core.CredentialBackendOnePassword} {
		t.Run(string(backend), func(t *testing.T) {
			store := credentialStoreFor(config.Config{SecretBackend: backend, Keyring: false, OnePasswordVault: "fixture-vault", OnePasswordItem: "fixture-item"})
			switch backend {
			case core.CredentialBackendOnePassword:
				provider, ok := store.(core.CredentialBackendProvider)
				if !ok || provider.CredentialBackend() != backend {
					t.Fatal("explicit SDK selection became disabled keyring")
				}
			case core.CredentialBackendEnvironment:
				if _, ok := store.(core.EnvironmentCredentialStore); !ok {
					t.Fatal("environment backend did not retain its no-storage policy")
				}
			default:
				if _, ok := store.(core.NoCredentialStore); !ok {
					t.Fatal("disabled/environment selection opened a native provider")
				}
			}
		})
	}
}

func TestLocalCommandsNeverConstructSDKStoreOrClient(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("JIRA_SECRET_BACKEND", "1password")
	storeCalls, clientCalls := 0, 0
	store := adapteronepassword.New(adapteronepassword.Config{
		Vault: "vault-id", Item: "item-id",
		LookupEnv: func(string) (string, bool) { return "synthetic-service-account-token", true },
		ClientFactory: func(context.Context, adapteronepassword.ClientOptions) (adapteronepassword.Client, error) {
			clientCalls++
			return nil, errors.New("local commands must not initialize SDK")
		},
	})
	for _, args := range [][]string{
		{"auth", "login", "--help"},
		{"agent", "schema", "--path=auth status", "--output=json"},
		{"config", "get", "secret_backend", "--output=json"},
		{"config", "set", "secret_backend", "1password", "--dry-run", "--output=json"},
	} {
		deps := testDeps(t)
		deps.Credentials = func() (core.CredentialStore, error) {
			storeCalls++
			return store, nil
		}
		var stdout, stderr bytes.Buffer
		program, sink := newProgram(deps, &stdout, &stderr)
		if exit := execute(t.Context(), program, sink, args); exit != 0 || stdout.Len() == 0 || stderr.Len() != 0 {
			t.Fatalf("local command failed: %v exit=%d stderr=%s", args, exit, &stderr)
		}
	}
	if storeCalls != 0 || clientCalls != 0 {
		t.Fatalf("local commands constructed credentials: stores=%d SDK=%d", storeCalls, clientCalls)
	}
}
