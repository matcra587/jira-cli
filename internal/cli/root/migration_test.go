package root

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	sdk "github.com/1password/onepassword-sdk-go"

	adapteronepassword "github.com/matcra587/jira-cli/internal/adapters/onepassword"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
	"github.com/matcra587/jira-cli/internal/envelope"
)

type migrationSDKFixture struct {
	items    map[string]sdk.Item
	putError error
}

func (*migrationSDKFixture) ListVaults(context.Context) ([]sdk.VaultOverview, error) {
	return []sdk.VaultOverview{{ID: "vault-id", Title: "Fixture Vault"}}, nil
}

func (f *migrationSDKFixture) ListItems(context.Context, string) ([]sdk.ItemOverview, error) {
	var items []sdk.ItemOverview
	for _, item := range f.items {
		items = append(items, sdk.ItemOverview{ID: item.ID, VaultID: item.VaultID, Title: item.Title})
	}
	return items, nil
}

func (f *migrationSDKFixture) GetItem(_ context.Context, vault, id string) (sdk.Item, error) {
	item, ok := f.items[id]
	if !ok || item.VaultID != vault {
		return sdk.Item{}, errors.New("synthetic missing SDK item")
	}
	item.Fields = slices.Clone(item.Fields)
	return item, nil
}

func (f *migrationSDKFixture) CreateItem(_ context.Context, params sdk.ItemCreateParams) (sdk.Item, error) {
	item := sdk.Item{ID: "backup-" + params.Title, VaultID: params.VaultID, Title: params.Title, Category: params.Category, Fields: slices.Clone(params.Fields)}
	f.items[item.ID] = item
	return item, nil
}

func (f *migrationSDKFixture) PutItem(_ context.Context, item sdk.Item) (sdk.Item, error) {
	item.Fields = slices.Clone(item.Fields)
	f.items[item.ID] = item
	return item, f.putError
}

func TestMigrationCommandsReopenRecordedSDKScopeAndRetainUncertainEffects(t *testing.T) {
	for _, mode := range []string{"json", "compact", "human"} {
		for _, failWrite := range []bool{false, true} {
			t.Run(mode+"/"+map[bool]string{false: "acknowledged", true: "uncertain"}[failWrite], func(t *testing.T) {
				t.Setenv("JIRA_SECRET_BACKEND", "env")
				t.Setenv("JIRA_API_TOKEN", "")
				t.Setenv("JIRA_EMAIL", "")
				t.Setenv("JIRA_BASE_URL", "")
				t.Cleanup(func() { config.SetFlagLayer(config.FlagLayer{}) })
				dir := t.TempDir()
				source, target, recovery := filepath.Join(dir, "old.toml"), filepath.Join(dir, "new.toml"), filepath.Join(dir, "recovery")
				original := []byte("default_profile='work'\n[[profiles]]\nname='work'\nbase_url='https://example.invalid'\nemail='person@example.invalid'\nsecret_backend='1password'\nonepassword_account='Fixture Account'\nvault='Fixture Vault'\nitem='Fixture Item'\n")
				if err := os.WriteFile(source, original, 0o600); err != nil {
					t.Fatal(err)
				}
				const token = "synthetic-migration-SDK-token"
				field := sdk.ItemField{ID: adapteronepassword.CredentialFieldID, Title: "Original title", FieldType: sdk.ItemFieldTypeText, Value: token}
				fixture := &migrationSDKFixture{items: map[string]sdk.Item{"item-id": {ID: "item-id", VaultID: "vault-id", Title: "Fixture Item", Notes: "original notes", Fields: []sdk.ItemField{{ID: "personal", Value: "unmanaged"}, field}}}}
				if failWrite {
					fixture.putError = errors.New("synthetic-private-write-error")
				}
				var locations []core.CredentialLocation
				deps := testDeps(t)
				deps.JiraClient = func(context.Context) (core.JiraClient, error) { t.Fatal("migration opened Jira"); return nil, nil }
				deps.Credentials = func() (core.CredentialStore, error) {
					t.Fatal("migration used current credential selection")
					return nil, nil
				}
				deps.MigrationStores = func(_ context.Context, location core.CredentialLocation) (core.CredentialRecoveryStore, error) {
					locations = append(locations, location)
					if location.Backend != core.CredentialBackendOnePassword || location.SDKAccount != "Fixture Account" {
						t.Fatal("migration lost explicit historical SDK scope")
					}
					return adapteronepassword.New(adapteronepassword.Config{Account: location.SDKAccount, Vault: location.Vault, Item: location.Item, LookupEnv: func(string) (string, bool) { return "synthetic-service-account-token", true }, ClientFactory: func(context.Context, adapteronepassword.ClientOptions) (adapteronepassword.Client, error) {
						return fixture, nil
					}}), nil
				}
				invoke := func(wantExit int, args ...string) []byte {
					t.Helper()
					args = append(args, "--output="+mode, "--no-input")
					applyGlobalFlags(args)
					var stdout, stderr bytes.Buffer
					program, sink := newProgram(deps, &stdout, &stderr)
					if exit := execute(t.Context(), program, sink, args); exit != wantExit {
						t.Fatalf("migration exit=%d want=%d stderr=%s", exit, wantExit, &stderr)
					}
					if strings.Contains(stdout.String()+stderr.String(), token) || strings.Contains(stdout.String()+stderr.String(), "synthetic-private-write-error") {
						t.Fatal("migration output exposed provider material")
					}
					if wantExit != 0 && (strings.Contains(stdout.String()+stderr.String(), "would restore migration preimages") || strings.Contains(stdout.String()+stderr.String(), "would convert legacy profile")) {
						t.Fatal("refused preview advertised a prospective success")
					}
					if mode == "human" {
						return nil
					}
					if mode == "compact" && wantExit == 0 {
						return stdout.Bytes()
					}
					var frame struct {
						OK   bool            `json:"ok"`
						Data json.RawMessage `json:"data"`
					}
					if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil || frame.OK != (wantExit == 0) {
						t.Fatal("migration returned an invalid envelope")
					}
					return frame.Data
				}
				invoke(0, "config", "migrate", "--source", source, "--config", target, "--recovery-dir", recovery, "--dry-run")
				if len(locations) != 0 || len(fixture.items) != 1 {
					t.Fatal("SDK preview opened provider or wrote a backup")
				}
				blocked := filepath.Join(dir, "blocked-recovery")
				if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
				invoke(cli.ExitServer, "config", "migrate", "--source", source, "--config", target, "--recovery-dir", blocked, "--dry-run")
				wantExit := 0
				if failWrite {
					wantExit = cli.ExitServer
				}
				body := invoke(wantExit, "config", "migrate", "--source", source, "--config", target, "--recovery-dir", recovery, "--yes")
				if mode != "human" {
					var data envelope.ConfigMigrateOutput
					if json.Unmarshal(body, &data) != nil || !data.RecoveryPublished || data.Receipt == nil {
						t.Fatal("migration result discarded provider receipt")
					}
					if failWrite {
						found := false
						for _, effect := range data.Receipt.Effects {
							if effect.Operation == "credential_apply" && effect.Status == "uncertain" && effect.Attempted != nil && *effect.Attempted {
								found = true
							}
						}
						if !found {
							t.Fatal("uncertain native write fabricated an unattempted result")
						}
					} else if data.Receipt.Status != "applied" || !data.Receipt.JournalDurable {
						t.Fatal("successful conversion lost durable result")
					}
				}
				var record credentials.Record
				if json.Unmarshal([]byte(fixture.items["item-id"].Fields[1].Value), &record) != nil || record.Token != token || record.Site != "https://example.invalid" || record.Email != "person@example.invalid" {
					t.Fatal("SDK command did not convert the historical raw token")
				}
				before := len(locations)
				journal := filepath.Join(recovery, "migration.json")
				invoke(0, "config", "rollback", "--journal", journal, "--dry-run")
				if len(locations) != before {
					t.Fatal("SDK rollback preview opened provider")
				}
				if !failWrite {
					expected, err := os.ReadFile(target)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(target, []byte("email='changed@example.invalid'\n"), 0o600); err != nil {
						t.Fatal(err)
					}
					invoke(cli.ExitValidation, "config", "rollback", "--journal", journal, "--dry-run")
					if len(locations) != before {
						t.Fatal("refused rollback preview opened provider")
					}
					if err := os.WriteFile(target, expected, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				current := fixture.items["item-id"]
				current.Fields = slices.Clone(current.Fields)
				current.Fields[0].Value, current.Notes = "external edit", "external notes"
				fixture.items["item-id"], fixture.putError = current, nil
				invoke(0, "config", "rollback", "--journal", journal, "--config", filepath.Join(dir, "unrelated.toml"), "--yes")
				last := locations[len(locations)-1]
				if last.Vault != "vault-id" || last.Item != "item-id" || !reflect.DeepEqual(fixture.items["item-id"].Fields[1], field) || fixture.items["item-id"].Fields[0].Value != "external edit" || fixture.items["item-id"].Notes != "external notes" {
					t.Fatal("fresh command rollback lost recorded IDs, preimage or unmanaged edits")
				}
				if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) || len(fixture.items) != 2 {
					t.Fatal("rollback lost prior config absence or retained backup")
				}
				if data, err := os.ReadFile(source); err != nil || !bytes.Equal(data, original) {
					t.Fatal("migration changed the historical source")
				}
			})
		}
	}
}

func TestMigrationDiscoveryWithLegacyConfigConstructsNothing(t *testing.T) {
	t.Setenv("JIRA_SECRET_BACKEND", "1password")
	t.Setenv("JIRA_API_TOKEN", "")
	t.Cleanup(func() { config.SetFlagLayer(config.FlagLayer{}) })
	dir := t.TempDir()
	source := filepath.Join(dir, "legacy.toml")
	if err := os.WriteFile(source, []byte("default_profile='work'\n[[profiles]]\nname='work'\nbase_url='https://example.invalid'\nemail='work@example.invalid'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deps := testDeps(t)
	calls := 0
	deps.Credentials = func() (core.CredentialStore, error) { calls++; return nil, errors.New("unexpected credentials") }
	deps.JiraClient = func(context.Context) (core.JiraClient, error) { calls++; return nil, errors.New("unexpected Jira") }
	deps.MigrationStores = func(context.Context, core.CredentialLocation) (core.CredentialRecoveryStore, error) {
		calls++
		return nil, errors.New("unexpected migration provider")
	}
	for _, args := range [][]string{
		{"config", "migrate", "--help"},
		{"config", "rollback", "--help"},
		{"agent", "schema", "--path=config migrate"},
		{"agent", "schema", "--path=config rollback"},
		{"agent", "guide", "migrate-configuration"},
		{"config", "migrate", "--source", source, "--dry-run"},
	} {
		args = append(args, "--config", source, "--output=json", "--no-input")
		applyGlobalFlags(args)
		var stdout, stderr bytes.Buffer
		program, sink := newProgram(deps, &stdout, &stderr)
		if exit := execute(t.Context(), program, sink, args); exit != 0 || stdout.Len() == 0 || stderr.Len() != 0 || calls != 0 {
			t.Fatalf("legacy repair discovery opened a dependency or failed: args=%v exit=%d calls=%d stderr=%s", args, exit, calls, &stderr)
		}
	}
}
