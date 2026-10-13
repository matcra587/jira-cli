package onepassword

import (
	"bytes"
	"context"
	jsonv2 "encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

func TestSDKMigrationTransactionReopensActualIDsAndRestoresOwnedMetadata(t *testing.T) {
	for _, cloudID := range []string{"", "fixture-cloud"} {
		t.Run(map[bool]string{true: "scoped", false: "classic"}[cloudID != ""], func(t *testing.T) {
			client := newRecoveryClient()
			original := client.state["item-id"].Fields[1]
			path := filepath.Join(t.TempDir(), "config.toml")
			legacy := []byte(`default_profile='work'
[[profiles]]
name='work'
base_url='https://work.invalid/'
email='work@example.invalid'
secret_backend='1password'
onepassword_account='synthetic desktop account'
vault='selected vault'
item='selected item'
`)
			legacy = append(legacy, []byte("cloud_id=\""+cloudID+"\"\n")...)
			if err := os.WriteFile(path, legacy, 0o600); err != nil {
				t.Fatal(err)
			}
			file, err := config.OpenFile(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := config.PrepareLegacyTransaction(t.Context(), file, file, "", "")
			if err != nil {
				t.Fatal(err)
			}
			transaction, published, err := plan.Create(t.Context(), filepath.Join(filepath.Dir(path), "recovery"))
			if err != nil || !published {
				t.Fatal(err)
			}
			factory := func(_ context.Context, location core.CredentialLocation) (core.CredentialRecoveryStore, error) {
				settings := fixtureConfig(client)
				settings.Account, settings.Vault, settings.Item = location.SDKAccount, location.Vault, location.Item
				return New(settings), nil
			}
			receipt, err := transaction.Apply(t.Context(), factory)
			if err != nil || receipt.TargetCredential.Vault != "vault-id" || receipt.TargetCredential.Item != "item-id" {
				t.Fatal("migration did not persist native SDK identities")
			}
			store := New(fixtureConfig(client))
			record, err := credentials.Load(t.Context(), store)
			if err != nil || record.Version != 2 || record.CloudID != cloudID || record.Token != original.Value || record.Site != "https://work.invalid" || record.Email != "work@example.invalid" {
				t.Fatal("SDK migration did not establish bound credentials")
			}
			// Titles may change after migration. Concrete recovery IDs still select the
			// intended resources, and unmanaged edits must survive owned-field restore.
			item := client.state["item-id"]
			item.Title, item.Fields[0].Value = "renamed item", "unmanaged external edit"
			client.state["item-id"] = item
			client.vaults[0].Title = "renamed vault"
			fresh, err := config.LoadLegacyTransaction(t.Context(), receipt.Journal)
			if err != nil {
				t.Fatal(err)
			}
			backupID := "created-" + receipt.CredentialBackup
			backupItem := client.state[backupID]
			originalBackup := backupItem.Fields[0].Value
			var backup fieldRecoveryBackup
			if err := jsonv2.Unmarshal([]byte(originalBackup), &backup); err != nil {
				t.Fatal(err)
			}
			owned, err := backup.Before.field()
			if err != nil {
				t.Fatal(err)
			}
			owned.Title = "altered prior metadata"
			backup.Before.Field, err = jsonv2.Marshal(owned)
			if err != nil {
				t.Fatal(err)
			}
			altered, err := jsonv2.Marshal(backup)
			if err != nil {
				t.Fatal(err)
			}
			backupItem.Fields[0].Value = string(altered)
			client.state[backupID] = backupItem
			if _, err := fresh.Rollback(t.Context(), factory); !errors.Is(err, core.ErrCredentialRecoveryInvalid) {
				t.Fatal("before-only SDK metadata alteration passed journal commitment")
			}
			if !reflect.DeepEqual(client.state["item-id"], item) {
				t.Fatal("backup integrity refusal changed primary SDK item")
			}
			backupItem.Fields[0].Value = originalBackup
			client.state[backupID] = backupItem
			if _, err := fresh.Rollback(t.Context(), factory); err != nil {
				t.Fatal(err)
			}
			after := client.state["item-id"]
			if !reflect.DeepEqual(after.Fields[1], original) || after.Fields[0].Value != "unmanaged external edit" || after.Title != "renamed item" {
				t.Fatal("transaction rollback lost owned metadata or current unmanaged content")
			}
			data, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, legacy) || len(client.state) != 2 {
				t.Fatal("rollback lost config bytes or discarded SDK recovery item")
			}
		})
	}
}
