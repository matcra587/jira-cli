package keyring_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	native "github.com/zalando/go-keyring"

	"github.com/matcra587/jira-cli/internal/adapters/keyring"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

func TestKeyringMigrationTransactionPreservesHistoricalServiceAndDestinationAbsence(t *testing.T) {
	for _, cloudID := range []string{"", "fixture-cloud"} {
		t.Run(map[bool]string{true: "scoped", false: "classic"}[cloudID != ""], func(t *testing.T) {
			native.MockInit()
			t.Cleanup(native.MockInit)
			path := filepath.Join(t.TempDir(), "config.toml")
			legacy := []byte(`default_profile='work-staging'
[[profiles]]
name='work-staging'
base_url='https://WORK.invalid:8443/'
email='work@example.invalid'
`)
			legacy = append(legacy, []byte("cloud_id=\""+cloudID+"\"\n")...)
			if err := os.WriteFile(path, legacy, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := native.Set("legacy-service", "WORK.invalid:8443/work-staging", "synthetic-source-token"); err != nil {
				t.Fatal(err)
			}
			file, err := config.OpenFile(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := config.PrepareLegacyTransaction(t.Context(), file, file, "", "legacy-service")
			if err != nil {
				t.Fatal(err)
			}
			transaction, published, err := plan.Create(t.Context(), filepath.Join(filepath.Dir(path), "recovery"))
			if err != nil || !published {
				t.Fatal(err)
			}
			factory := func(_ context.Context, location core.CredentialLocation) (core.CredentialRecoveryStore, error) {
				return keyring.NewForService(location.Service), nil
			}
			receipt, err := transaction.Apply(t.Context(), factory)
			if err != nil {
				t.Fatal(err)
			}
			record, err := credentials.Load(t.Context(), keyring.New())
			if err != nil || record.Version != 2 || record.CloudID != cloudID || record.Token != "synthetic-source-token" || record.Site != "https://work.invalid:8443" || record.Email != "work@example.invalid" {
				t.Fatal("native keyring migration lost selected source or site binding")
			}
			fresh, err := config.LoadLegacyTransaction(t.Context(), receipt.Journal)
			if err != nil {
				t.Fatal(err)
			}
			backup, err := native.Get("jira-cli", receipt.CredentialBackup)
			if err != nil {
				t.Fatal(err)
			}
			var altered map[string]any
			if err := json.Unmarshal([]byte(backup), &altered); err != nil {
				t.Fatal(err)
			}
			altered["before"].(map[string]any)["value"] = "altered private preimage"
			altered["before"].(map[string]any)["present"] = true
			data, err := json.Marshal(altered)
			if err != nil {
				t.Fatal(err)
			}
			if err := native.Set("jira-cli", receipt.CredentialBackup, string(data)); err != nil {
				t.Fatal(err)
			}
			if _, err := fresh.Rollback(t.Context(), factory); !errors.Is(err, core.ErrCredentialRecoveryInvalid) {
				t.Fatal("before-only native backup modification passed journal commitment")
			}
			if current, err := credentials.Load(t.Context(), keyring.New()); err != nil || current != record {
				t.Fatal("backup integrity refusal changed primary credential")
			}
			if err := native.Set("jira-cli", receipt.CredentialBackup, backup); err != nil {
				t.Fatal(err)
			}
			if _, err := fresh.Rollback(t.Context(), factory); err != nil {
				t.Fatal(err)
			}
			if _, err := native.Get("jira-cli", "default"); !errors.Is(err, native.ErrNotFound) {
				t.Fatal("rollback did not remove originally absent destination slot")
			}
			if value, err := native.Get("legacy-service", "WORK.invalid:8443/work-staging"); err != nil || value != "synthetic-source-token" {
				t.Fatal("rollback changed historical source slot")
			}
			if _, err := native.Get("jira-cli", receipt.CredentialBackup); err != nil {
				t.Fatal("rollback discarded provider backup")
			}
			data, err = os.ReadFile(path)
			if err != nil || !bytes.Equal(data, legacy) {
				t.Fatal("native keyring rollback lost exact config bytes")
			}
		})
	}
}
