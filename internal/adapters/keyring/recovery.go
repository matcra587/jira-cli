package keyring

import (
	"bytes"
	"context"
	jsonv2 "encoding/json/v2"
	"errors"

	"github.com/matcra587/jira-cli/internal/core"
)

type recoveryState struct {
	Service string `json:"service"`
	Account string `json:"account"`
	Present bool   `json:"present"`
	Value   string `json:"value"`
}

type snapshot struct {
	state    recoveryState
	proofKey []byte
}

func (s *snapshot) Commitment() string {
	if s == nil {
		return ""
	}
	data, err := jsonv2.Marshal(s.state)
	if err != nil {
		return ""
	}
	return core.CredentialCommitment(s.proofKey, data)
}

func (s *snapshot) Secret() (string, bool) {
	if s == nil {
		return "", false
	}
	return s.state.Value, s.state.Present
}
func (*snapshot) String() string   { return "credential snapshot" }
func (*snapshot) GoString() string { return "credential snapshot" }

func (s *snapshot) Matches(other core.CredentialSnapshot) bool {
	right, ok := other.(*snapshot)
	return s != nil && ok && right != nil && s.state == right.state
}

func (s *snapshot) Location() core.CredentialLocation {
	if s == nil {
		return core.CredentialLocation{}
	}
	return core.CredentialLocation{Backend: core.CredentialBackendKeyring, Service: s.state.Service, Account: s.state.Account}
}

type recoveryBackup struct {
	Version  int           `json:"version"`
	Name     string        `json:"name"`
	Before   recoveryState `json:"before"`
	Expected recoveryState `json:"expected"`
	ProofKey []byte        `json:"proof_key"`
}

var _ core.CredentialRecoveryStore = (*Store)(nil)

func (s *Store) Capture(ctx context.Context, account string) (core.CredentialSnapshot, error) {
	if account == "" || core.ValidCredentialBackupID(account) {
		return nil, recoveryFailure("capture", core.ErrCredentialRecoveryInvalid, false)
	}
	value, err := s.Get(ctx, account)
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, recoveryFailure("capture", errors.Join(err, contextErr), false)
	}
	if err != nil && !errors.Is(err, core.ErrNoCredential) {
		return nil, recoveryFailure("capture", err, false)
	}
	if err := ctx.Err(); err != nil {
		return nil, recoveryFailure("capture", err, false)
	}
	key, keyErr := core.NewCredentialProofKey()
	if keyErr != nil {
		return nil, recoveryFailure("capture", keyErr, false)
	}
	return &snapshot{state: recoveryState{Service: s.serviceName(), Account: account, Present: err == nil, Value: value}, proofKey: key}, nil
}

func (s *Store) Replacement(before core.CredentialSnapshot, secret string) (core.CredentialSnapshot, error) {
	state, ok := before.(*snapshot)
	if !ok || state == nil || !s.validState(state.state) || len(state.proofKey) != 32 || secret == "" {
		return nil, recoveryFailure("prepare replacement", core.ErrCredentialRecoveryInvalid, false)
	}
	next := state.state
	next.Present, next.Value = true, secret
	return &snapshot{state: next, proofKey: bytes.Clone(state.proofKey)}, nil
}

func (s *Store) Backup(ctx context.Context, name string, before, expected core.CredentialSnapshot) error {
	if err := ctx.Err(); err != nil {
		return recoveryFailure("backup", err, false)
	}
	old, next, err := s.snapshotPair(before, expected)
	if err != nil || !core.ValidCredentialBackupID(name) {
		return recoveryFailure("backup", core.ErrCredentialRecoveryInvalid, false)
	}
	current, err := s.Capture(ctx, old.Account)
	if err != nil {
		return err
	}
	if current.(*snapshot).state != old {
		return recoveryFailure("backup", core.ErrCredentialChanged, false)
	}
	_, err = s.Get(ctx, name)
	if contextErr := ctx.Err(); contextErr != nil {
		return recoveryFailure("backup", errors.Join(err, contextErr), false)
	}
	if !errors.Is(err, core.ErrNoCredential) {
		if err == nil {
			err = core.ErrCredentialBackupExists
		}
		return recoveryFailure("backup", err, false)
	}
	raw, err := jsonv2.Marshal(recoveryBackup{Version: 2, Name: name, Before: old, Expected: next, ProofKey: bytes.Clone(before.(*snapshot).proofKey)})
	if err != nil {
		return recoveryFailure("backup", err, false)
	}
	if err := ctx.Err(); err != nil {
		return recoveryFailure("backup", err, false)
	}
	if err, attempted := s.set(ctx, name, string(raw)); err != nil {
		return recoveryFailure("backup", errors.Join(err, ctx.Err()), attempted)
	}
	return nil
}

func (s *Store) ReadBackup(ctx context.Context, name string) (core.CredentialSnapshot, core.CredentialSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, recoveryFailure("read backup", err, false)
	}
	if !core.ValidCredentialBackupID(name) {
		return nil, nil, recoveryFailure("read backup", core.ErrCredentialRecoveryInvalid, false)
	}
	raw, err := s.Get(ctx, name)
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, nil, recoveryFailure("read backup", errors.Join(err, contextErr), false)
	}
	if err != nil {
		return nil, nil, recoveryFailure("read backup", err, false)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, recoveryFailure("read backup", err, false)
	}
	var backup recoveryBackup
	if err := jsonv2.Unmarshal([]byte(raw), &backup, jsonv2.RejectUnknownMembers(true)); err != nil {
		return nil, nil, recoveryFailure("read backup", err, false)
	}
	before, expected := &snapshot{state: backup.Before, proofKey: bytes.Clone(backup.ProofKey)}, &snapshot{state: backup.Expected, proofKey: bytes.Clone(backup.ProofKey)}
	if _, _, err := s.snapshotPair(before, expected); err != nil || backup.Version != 2 || backup.Name != name {
		return nil, nil, recoveryFailure("read backup", core.ErrCredentialRecoveryInvalid, false)
	}
	return before, expected, nil
}

func (s *Store) Apply(ctx context.Context, account, name string) (bool, error) {
	return s.applyBackup(ctx, account, name, false)
}

func (s *Store) Restore(ctx context.Context, account, name string) (bool, error) {
	return s.applyBackup(ctx, account, name, true)
}

func (s *Store) applyBackup(ctx context.Context, account, name string, restore bool) (bool, error) {
	operation := "apply"
	if restore {
		operation = "restore"
	}
	before, expected, err := s.ReadBackup(ctx, name)
	if err != nil {
		return false, err
	}
	old, next, err := s.snapshotPair(before, expected)
	if err != nil || account != old.Account {
		return false, recoveryFailure(operation, core.ErrCredentialRecoveryInvalid, false)
	}
	current, err := s.Capture(ctx, account)
	if err != nil {
		return false, err
	}
	state := current.(*snapshot).state
	want, from := next, old
	if restore {
		want, from = old, next
	}
	if state == want {
		return false, nil
	}
	if state != from {
		return false, recoveryFailure(operation, core.ErrCredentialChanged, false)
	}
	if err := ctx.Err(); err != nil {
		return false, recoveryFailure(operation, err, false)
	}
	var attempted bool
	if want.Present {
		err, attempted = s.set(ctx, account, want.Value)
	} else {
		err, attempted = s.delete(ctx, account)
	}
	if err != nil {
		return attempted, recoveryFailure(operation, errors.Join(err, ctx.Err()), attempted)
	}
	return attempted, nil
}

func (s *Store) snapshotPair(before, expected core.CredentialSnapshot) (recoveryState, recoveryState, error) {
	old, oldOK := before.(*snapshot)
	next, nextOK := expected.(*snapshot)
	if !oldOK || !nextOK || old == nil || next == nil || !s.validState(old.state) || !s.validState(next.state) || old.state.Account != next.state.Account ||
		len(old.proofKey) != 32 || !bytes.Equal(old.proofKey, next.proofKey) {
		return recoveryState{}, recoveryState{}, core.ErrCredentialRecoveryInvalid
	}
	return old.state, next.state, nil
}

func (s *Store) validState(state recoveryState) bool {
	return state.Service == s.serviceName() && state.Account != "" && !core.ValidCredentialBackupID(state.Account) && (state.Present || state.Value == "")
}

func recoveryFailure(operation string, cause error, attempted bool) error {
	return &core.CredentialStoreError{Backend: core.CredentialBackendKeyring, Operation: operation, Cause: cause, Attempted: attempted}
}
