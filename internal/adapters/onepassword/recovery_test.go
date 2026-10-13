package onepassword

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	sdk "github.com/1password/onepassword-sdk-go"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

type recoveryClient struct {
	*fakeClient
	stateMu              sync.Mutex
	state                map[string]sdk.Item
	persistOnCreateError bool
	persistOnPutError    bool
}

func newRecoveryClient() *recoveryClient {
	fake := fixtureClient()
	code := "synthetic-code"
	details := sdk.NewItemFieldDetailsTypeVariantOTP(&sdk.OTPFieldDetails{Code: &code})
	fake.item.Fields[1].FieldType, fake.item.Fields[1].Details = sdk.ItemFieldTypeTOTP, &details
	return &recoveryClient{fakeClient: fake, state: map[string]sdk.Item{fake.item.ID: fake.item}}
}

func (f *recoveryClient) ListItems(ctx context.Context, vault string) ([]sdk.ItemOverview, error) {
	if err := f.call(ctx, "list items"); err != nil {
		return nil, err
	}
	f.stateMu.Lock()
	defer f.stateMu.Unlock()
	var items []sdk.ItemOverview
	for _, item := range f.state {
		if item.VaultID == vault {
			items = append(items, sdk.ItemOverview{ID: item.ID, VaultID: vault, Title: item.Title})
		}
	}
	return items, nil
}

func (f *recoveryClient) GetItem(ctx context.Context, vault, id string) (sdk.Item, error) {
	if err := f.call(ctx, "get item"); err != nil {
		return sdk.Item{}, err
	}
	f.stateMu.Lock()
	defer f.stateMu.Unlock()
	item, found := f.state[id]
	if !found || item.VaultID != vault {
		return sdk.Item{}, errors.New("synthetic missing SDK item")
	}
	item.Fields = slices.Clone(item.Fields)
	return item, nil
}

func (f *recoveryClient) CreateItem(ctx context.Context, params sdk.ItemCreateParams) (sdk.Item, error) {
	err := f.call(ctx, "create item")
	f.stateMu.Lock()
	defer f.stateMu.Unlock()
	item := sdk.Item{ID: "created-" + params.Title, VaultID: params.VaultID, Title: params.Title, Category: params.Category, Fields: slices.Clone(params.Fields)}
	if err == nil || f.persistOnCreateError {
		f.state[item.ID] = item
	}
	return item, err
}

func (f *recoveryClient) PutItem(ctx context.Context, item sdk.Item) (sdk.Item, error) {
	err := f.call(ctx, "put item")
	f.stateMu.Lock()
	defer f.stateMu.Unlock()
	if err == nil || f.persistOnPutError {
		item.Fields = slices.Clone(item.Fields)
		f.state[item.ID] = item
	}
	return item, err
}

type recordSink struct {
	core.NoCredentialStore
	raw string
}

func (s *recordSink) Set(ctx context.Context, _, secret string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.raw = secret
	return nil
}

func sdkRecoveryBackup(t *testing.T, client *recoveryClient) (*Store, string, sdk.ItemField) {
	t.Helper()
	store := New(fixtureConfig(client))
	before, err := store.Capture(t.Context(), "default")
	if err != nil {
		t.Fatal(err)
	}
	token, present := before.Secret()
	if !present || token != "synthetic-original" {
		t.Fatal("capture did not retain the old raw SDK token")
	}
	sink := new(recordSink)
	if err := credentials.Save(t.Context(), sink, credentials.Record{Site: "https://work.invalid/", Email: "work@example.invalid", Token: token}); err != nil {
		t.Fatal(err)
	}
	expected, err := store.Replacement(before, sink.raw)
	if err != nil {
		t.Fatal(err)
	}
	name := core.NewCredentialBackupID()
	if err := store.Backup(t.Context(), name, before, expected); err != nil {
		t.Fatal(err)
	}
	return store, name, client.state["item-id"].Fields[1]
}

func TestSDKRecoveryConvertsRawTokenAndRestoresCompleteOwnedMetadata(t *testing.T) {
	client := newRecoveryClient()
	store, name, original := sdkRecoveryBackup(t, client)
	beforeItem := client.state["item-id"]
	if attempted, err := store.Apply(t.Context(), "default", name); !attempted || err != nil {
		t.Fatal("provider-backed SDK apply failed")
	}
	record, err := credentials.Load(t.Context(), store)
	if err != nil || record.Site != "https://work.invalid" || record.Email != "work@example.invalid" || record.Token != "synthetic-original" || !record.Complete() {
		t.Fatal("old raw SDK token became an unusable or misbound new record")
	}
	after := client.state["item-id"]
	if after.Fields[1].FieldType != sdk.ItemFieldTypeConcealed || after.Fields[1].Details != nil || after.Fields[1].Title != original.Title || !reflect.DeepEqual(after.Fields[1].SectionID, original.SectionID) {
		t.Fatal("replacement did not follow the shared SDK owned-field contract")
	}
	after.Fields = slices.Clone(after.Fields)
	after.Fields[1] = original
	if !reflect.DeepEqual(after, beforeItem) {
		t.Fatal("apply changed unmanaged SDK item state")
	}
	// New provider instances use actual IDs and retained provider backups. A user
	// edit to unmanaged fields must survive rollback of the owned field.
	current := client.state["item-id"]
	current.Fields = slices.Clone(current.Fields)
	current.Fields[0].Value, current.Notes = "external-unmanaged-edit", "external notes"
	client.state["item-id"] = current
	config := fixtureConfig(client)
	config.Vault, config.Item = "vault-id", "item-id"
	fresh := New(config)
	if attempted, err := fresh.Restore(t.Context(), "default", name); !attempted || err != nil {
		t.Fatalf("fresh SDK store did not restore its provider preimage: attempted=%v error=%v", attempted, err)
	}
	rolledBack := client.state["item-id"]
	if !reflect.DeepEqual(rolledBack.Fields[1], original) || rolledBack.Fields[0].Value != "external-unmanaged-edit" || rolledBack.Notes != "external notes" {
		t.Fatal("rollback lost original field metadata or unmanaged edits")
	}
	if attempted, err := fresh.Restore(t.Context(), "default", name); attempted || err != nil {
		t.Fatal("already restored SDK state was rewritten")
	}
	if _, _, err := fresh.ReadBackup(t.Context(), name); err != nil {
		t.Fatal("rollback discarded its SDK backup")
	}
}

func TestSDKRecoveryRetainsOwnedFieldAbsenceWithoutDeletingItems(t *testing.T) {
	client := newRecoveryClient()
	item := client.state["item-id"]
	item.Fields = slices.Delete(slices.Clone(item.Fields), 1, 2)
	client.state["item-id"] = item
	store := New(fixtureConfig(client))
	before, err := store.Capture(t.Context(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := before.Secret(); present {
		t.Fatal("unmanaged title was mistaken for an owned credential")
	}
	expected, err := store.Replacement(before, "synthetic-new-record")
	if err != nil {
		t.Fatal(err)
	}
	name := core.NewCredentialBackupID()
	if err := store.Backup(t.Context(), name, before, expected); err != nil {
		t.Fatal(err)
	}
	if attempted, err := store.Apply(t.Context(), "default", name); !attempted || err != nil {
		t.Fatalf("owned-field creation failed: attempted=%v error=%v", attempted, err)
	}
	if attempted, err := store.Restore(t.Context(), "default", name); !attempted || err != nil {
		t.Fatal("owned-field absence was not restored")
	}
	if got, found := client.state["item-id"]; !found || !reflect.DeepEqual(got, item) {
		t.Fatal("rollback deleted an item or changed unmanaged fields")
	}
}

func TestSDKRecoveryRefusesChangedOwnedStateAndBackupCollisions(t *testing.T) {
	client := newRecoveryClient()
	store, name, _ := sdkRecoveryBackup(t, client)
	before, expected, err := store.ReadBackup(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Backup(t.Context(), name, before, expected); !errors.Is(err, core.ErrCredentialBackupExists) {
		t.Fatal("existing SDK backup was replaced")
	}
	if _, err := store.Apply(t.Context(), "default", name); err != nil {
		t.Fatal(err)
	}
	item := client.state["item-id"]
	item.Fields = slices.Clone(item.Fields)
	item.Fields[1].Value = "synthetic-external-secret"
	client.state["item-id"] = item
	if attempted, err := store.Restore(t.Context(), "default", name); attempted || !errors.Is(err, core.ErrCredentialChanged) {
		t.Fatal("changed SDK postimage was overwritten")
	}
	if client.state["item-id"].Fields[1].Value != "synthetic-external-secret" {
		t.Fatal("refused rollback changed the owned field")
	}
	config := fixtureConfig(client)
	config.Account = "different account"
	if _, _, err := New(config).ReadBackup(t.Context(), name); !errors.Is(err, core.ErrCredentialRecoveryInvalid) {
		t.Fatal("backup crossed selected SDK account scope")
	}
}

func TestSDKRecoverySnapshotsOwnMetadataAndNeverRenderSecrets(t *testing.T) {
	client := newRecoveryClient()
	store := New(fixtureConfig(client))
	captured, err := store.Capture(t.Context(), "default")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := captured.(*fieldSnapshot)
	original := bytes.Clone(snapshot.state.Field)
	item := client.state["item-id"]
	*item.Fields[1].SectionID = "changed section"
	*item.Fields[1].Details.OTP().Code = "changed code"
	item.Fields[1].Value = "changed value"
	client.state["item-id"] = item
	if !bytes.Equal(snapshot.state.Field, original) {
		t.Fatal("SDK input mutation changed a captured preimage")
	}
	encoded, err := json.Marshal(captured)
	if err != nil || string(encoded) != "{}" {
		t.Fatal("SDK snapshot became a public JSON payload")
	}
	for _, pattern := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(pattern, captured), "synthetic-original") {
			t.Fatal("SDK snapshot exposed private material through formatting")
		}
	}
}

func TestSDKRecoveryMissingItemDoesNotInventARecoverablePostimage(t *testing.T) {
	client := newRecoveryClient()
	delete(client.state, "item-id")
	store := New(fixtureConfig(client))
	before, err := store.Capture(t.Context(), "default")
	if err != nil || before == nil {
		t.Fatal("missing item was not captured as absence")
	}
	if _, present := before.Secret(); present {
		t.Fatal("missing item invented a source token")
	}
	if expected, err := store.Replacement(before, "synthetic-new-record"); expected != nil || !errors.Is(err, core.ErrCredentialRecoveryUnsupported) {
		t.Fatal("an uncreated SDK item acquired guessed postimage identity")
	}
	if len(client.state) != 0 {
		t.Fatal("pure replacement preparation created an SDK resource")
	}
}
