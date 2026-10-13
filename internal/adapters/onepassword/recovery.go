package onepassword

import (
	"bytes"
	"context"
	jsonv2 "encoding/json/v2"
	"slices"

	sdk "github.com/1password/onepassword-sdk-go"

	"github.com/matcra587/jira-cli/internal/core"
)

type fieldRecoveryState struct {
	Account       string `json:"account"`
	VaultID       string `json:"vault_id"`
	VaultSelector string `json:"vault_selector"`
	ItemID        string `json:"item_id"`
	ItemSelector  string `json:"item_selector"`
	ItemExists    bool   `json:"item_exists"`
	Field         []byte `json:"field"`
}

type fieldSnapshot struct {
	state    fieldRecoveryState
	proofKey []byte
}

func (s *fieldSnapshot) Commitment() string {
	if s == nil {
		return ""
	}
	data, err := jsonv2.Marshal(s.state)
	if err != nil {
		return ""
	}
	return core.CredentialCommitment(s.proofKey, data)
}

func (s *fieldSnapshot) Secret() (string, bool) {
	if s == nil || len(s.state.Field) == 0 {
		return "", false
	}
	field, err := s.state.field()
	if err != nil {
		return "", false
	}
	return field.Value, true
}
func (*fieldSnapshot) String() string   { return "credential snapshot" }
func (*fieldSnapshot) GoString() string { return "credential snapshot" }

func (s *fieldSnapshot) Matches(other core.CredentialSnapshot) bool {
	right, ok := other.(*fieldSnapshot)
	return s != nil && ok && right != nil && s.state.equal(right.state)
}

func (s *fieldSnapshot) Location() core.CredentialLocation {
	if s == nil {
		return core.CredentialLocation{}
	}
	item := s.state.ItemID
	if item == "" {
		item = s.state.ItemSelector
	}
	return core.CredentialLocation{
		Backend: core.CredentialBackendOnePassword, Account: "default",
		SDKAccount: s.state.Account, Vault: s.state.VaultID, Item: item,
	}
}

type fieldRecoveryBackup struct {
	Version  int                `json:"version"`
	Name     string             `json:"name"`
	Before   fieldRecoveryState `json:"before"`
	Expected fieldRecoveryState `json:"expected"`
	ProofKey []byte             `json:"proof_key"`
}

var _ core.CredentialRecoveryStore = (*Store)(nil)

func (s *Store) Capture(ctx context.Context, account string) (core.CredentialSnapshot, error) {
	if err := s.admit(ctx, account, "capture"); err != nil {
		return nil, err
	}
	defer s.gate.Release(1)
	snapshot, _, err := s.captureField(ctx, "capture")
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s *Store) captureField(ctx context.Context, operation string) (*fieldSnapshot, sdk.Item, error) {
	vault, item, found, err := s.selectedItem(ctx, operation)
	if err != nil {
		return nil, sdk.Item{}, err
	}
	state := fieldRecoveryState{
		Account: s.config.Account, VaultID: vault, VaultSelector: s.config.Vault,
		ItemID: item.ID, ItemSelector: s.config.Item, ItemExists: found,
	}
	if found {
		index, err := ownedField(item)
		if err != nil {
			return nil, sdk.Item{}, failure(ctx, operation, err, false)
		}
		if index >= 0 {
			state.Field, err = jsonv2.Marshal(item.Fields[index])
			if err != nil {
				return nil, sdk.Item{}, failure(ctx, operation, err, false)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, sdk.Item{}, failure(ctx, operation, err, false)
	}
	key, err := core.NewCredentialProofKey()
	if err != nil {
		return nil, sdk.Item{}, failure(ctx, operation, err, false)
	}
	return &fieldSnapshot{state: state, proofKey: key}, item, nil
}

func (s *Store) Replacement(before core.CredentialSnapshot, secret string) (core.CredentialSnapshot, error) {
	old, ok := before.(*fieldSnapshot)
	if !ok || old == nil || !s.validRecoveryState(old.state) || len(old.proofKey) != 32 || secret == "" {
		return nil, stateFailure("prepare replacement", core.ErrCredentialRecoveryInvalid)
	}
	// Legacy SDK tokens live in the selected existing item. Creating a different
	// item is a separate admitted write, not a guessed postimage of that item.
	if !old.state.ItemExists {
		return nil, stateFailure("prepare replacement", core.ErrCredentialRecoveryUnsupported)
	}
	field := sdk.ItemField{ID: CredentialFieldID, Title: CredentialFieldID}
	if len(old.state.Field) != 0 {
		var err error
		field, err = old.state.field()
		if err != nil {
			return nil, stateFailure("prepare replacement", err)
		}
	}
	field.Value, field.FieldType, field.Details = secret, sdk.ItemFieldTypeConcealed, nil
	next := old.state
	var err error
	next.Field, err = jsonv2.Marshal(field)
	if err != nil {
		return nil, stateFailure("prepare replacement", err)
	}
	return &fieldSnapshot{state: next, proofKey: bytes.Clone(old.proofKey)}, nil
}

func (s *Store) Backup(ctx context.Context, name string, before, expected core.CredentialSnapshot) error {
	if err := s.admit(ctx, "default", "backup"); err != nil {
		return err
	}
	defer s.gate.Release(1)
	old, next, err := s.fieldPair(before, expected)
	if err != nil || !core.ValidCredentialBackupID(name) || !old.ItemExists {
		return failure(ctx, "backup", core.ErrCredentialRecoveryInvalid, false)
	}
	current, _, err := s.captureField(ctx, "backup")
	if err != nil {
		return err
	}
	if !current.state.equal(old) {
		return failure(ctx, "backup", core.ErrCredentialChanged, false)
	}
	_, _, found, err := s.itemInVault(ctx, "backup", old.VaultID, name)
	if err != nil {
		return err
	}
	if found {
		return failure(ctx, "backup", core.ErrCredentialBackupExists, false)
	}
	raw, err := jsonv2.Marshal(fieldRecoveryBackup{Version: 2, Name: name, Before: old, Expected: next, ProofKey: bytes.Clone(before.(*fieldSnapshot).proofKey)})
	if err != nil {
		return failure(ctx, "backup", err, false)
	}
	field := sdk.ItemField{ID: CredentialFieldID, Title: CredentialFieldID, FieldType: sdk.ItemFieldTypeConcealed, Value: string(raw)}
	if err := ctx.Err(); err != nil {
		return failure(ctx, "backup", err, false)
	}
	item, err := s.client.CreateItem(ctx, sdk.ItemCreateParams{VaultID: old.VaultID, Title: name, Category: sdk.ItemCategorySecureNote, Fields: []sdk.ItemField{field}})
	if err != nil {
		return failure(ctx, "backup", err, true)
	}
	index, fieldErr := ownedField(item)
	if item.ID == "" || item.Title != name || item.VaultID != old.VaultID || fieldErr != nil || index < 0 || item.Fields[index].Value != field.Value || item.Fields[index].FieldType != sdk.ItemFieldTypeConcealed {
		return failure(ctx, "backup", core.ErrCredentialRecoveryInvalid, true)
	}
	return nil
}

func (s *Store) ReadBackup(ctx context.Context, name string) (core.CredentialSnapshot, core.CredentialSnapshot, error) {
	if err := s.admit(ctx, "default", "read backup"); err != nil {
		return nil, nil, err
	}
	defer s.gate.Release(1)
	before, expected, err := s.readFieldBackup(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	return before, expected, nil
}

func (s *Store) readFieldBackup(ctx context.Context, name string) (*fieldSnapshot, *fieldSnapshot, error) {
	if !core.ValidCredentialBackupID(name) {
		return nil, nil, failure(ctx, "read backup", core.ErrCredentialRecoveryInvalid, false)
	}
	vault, err := s.selectedVault(ctx, "read backup")
	if err != nil {
		return nil, nil, err
	}
	_, item, found, err := s.itemInVault(ctx, "read backup", vault, name)
	if err != nil {
		return nil, nil, err
	}
	if !found {
		return nil, nil, failure(ctx, "read backup", core.ErrNoCredential, false)
	}
	index, err := ownedField(item)
	if err != nil || index < 0 || item.Fields[index].FieldType != sdk.ItemFieldTypeConcealed {
		return nil, nil, failure(ctx, "read backup", core.ErrCredentialRecoveryInvalid, false)
	}
	var backup fieldRecoveryBackup
	if err := jsonv2.Unmarshal([]byte(item.Fields[index].Value), &backup, jsonv2.RejectUnknownMembers(true)); err != nil {
		return nil, nil, failure(ctx, "read backup", err, false)
	}
	before, expected := &fieldSnapshot{state: backup.Before, proofKey: bytes.Clone(backup.ProofKey)}, &fieldSnapshot{state: backup.Expected, proofKey: bytes.Clone(backup.ProofKey)}
	old, _, err := s.fieldPair(before, expected)
	if err != nil || backup.Version != 2 || backup.Name != name || old.VaultID != vault || !old.ItemExists {
		return nil, nil, failure(ctx, "read backup", core.ErrCredentialRecoveryInvalid, false)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, failure(ctx, "read backup", err, false)
	}
	return before, expected, nil
}

func (s *Store) Apply(ctx context.Context, account, name string) (bool, error) {
	return s.applyFieldBackup(ctx, account, name, false)
}

func (s *Store) Restore(ctx context.Context, account, name string) (bool, error) {
	return s.applyFieldBackup(ctx, account, name, true)
}

func (s *Store) applyFieldBackup(ctx context.Context, account, name string, restore bool) (bool, error) {
	operation := "apply"
	if restore {
		operation = "restore"
	}
	if err := s.admit(ctx, account, operation); err != nil {
		return false, err
	}
	defer s.gate.Release(1)
	before, expected, err := s.readFieldBackup(ctx, name)
	if err != nil {
		return false, err
	}
	current, item, err := s.captureField(ctx, operation)
	if err != nil {
		return false, err
	}
	want, from := expected.state, before.state
	if restore {
		want, from = before.state, expected.state
	}
	if current.state.equal(want) {
		return false, nil
	}
	if !current.state.equal(from) {
		return false, failure(ctx, operation, core.ErrCredentialChanged, false)
	}
	index, err := ownedField(item)
	if err != nil {
		return false, failure(ctx, operation, err, false)
	}
	item.Fields = slices.Clone(item.Fields)
	if len(want.Field) == 0 {
		item.Fields = slices.Delete(item.Fields, index, index+1)
	} else {
		field, err := want.field()
		if err != nil {
			return false, failure(ctx, operation, err, false)
		}
		if index < 0 {
			item.Fields = append(item.Fields, field)
		} else {
			item.Fields[index] = field
		}
	}
	return s.putWithOutcome(ctx, operation, item)
}

func (s fieldRecoveryState) field() (sdk.ItemField, error) {
	var field sdk.ItemField
	if err := jsonv2.Unmarshal(s.Field, &field, jsonv2.RejectUnknownMembers(true)); err != nil {
		return sdk.ItemField{}, err
	}
	canonical, err := jsonv2.Marshal(field)
	if err != nil || field.ID != CredentialFieldID || !bytes.Equal(canonical, s.Field) {
		return sdk.ItemField{}, core.ErrCredentialRecoveryInvalid
	}
	return field, nil
}

func (s *Store) validRecoveryState(state fieldRecoveryState) bool {
	if state.Account != s.config.Account || state.VaultID == "" || state.ItemSelector == "" ||
		(s.config.Vault != state.VaultSelector && s.config.Vault != state.VaultID) ||
		(s.config.Item != state.ItemSelector && s.config.Item != state.ItemID) {
		return false
	}
	if !state.ItemExists {
		return state.ItemID == "" && len(state.Field) == 0
	}
	if state.ItemID == "" {
		return false
	}
	if len(state.Field) != 0 {
		_, err := state.field()
		return err == nil
	}
	return true
}

func (s *Store) fieldPair(before, expected core.CredentialSnapshot) (fieldRecoveryState, fieldRecoveryState, error) {
	old, oldOK := before.(*fieldSnapshot)
	next, nextOK := expected.(*fieldSnapshot)
	if !oldOK || !nextOK || old == nil || next == nil || !s.validRecoveryState(old.state) || !s.validRecoveryState(next.state) || len(old.proofKey) != 32 || !bytes.Equal(old.proofKey, next.proofKey) {
		return fieldRecoveryState{}, fieldRecoveryState{}, core.ErrCredentialRecoveryInvalid
	}
	left, right := old.state, next.state
	left.Field, right.Field = nil, nil
	if !left.equal(right) {
		return fieldRecoveryState{}, fieldRecoveryState{}, core.ErrCredentialRecoveryInvalid
	}
	return old.state, next.state, nil
}

func (s fieldRecoveryState) equal(other fieldRecoveryState) bool {
	return s.Account == other.Account && s.VaultID == other.VaultID && s.ItemID == other.ItemID &&
		s.ItemExists == other.ItemExists && bytes.Equal(s.Field, other.Field)
}

func stateFailure(operation string, cause error) error {
	return &core.CredentialStoreError{Backend: core.CredentialBackendOnePassword, Operation: operation, Cause: cause}
}
