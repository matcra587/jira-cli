package core

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrCredentialChanged             = errors.New("credential changed before recovery")
	ErrCredentialRecoveryInvalid     = errors.New("invalid credential recovery state")
	ErrCredentialBackupExists        = errors.New("credential recovery backup already exists")
	ErrCredentialRecoveryUnsupported = errors.New("credential recovery cannot prepare this target")
)

// CredentialSnapshot is an opaque provider-owned preimage, including absence.
// Secret returns SECRET material for conversion only; never render or persist it
// outside a credential provider. Implementations keep JSON and fmt output private.
type CredentialSnapshot interface {
	Secret() (value string, present bool)
	Matches(CredentialSnapshot) bool
	Location() CredentialLocation
	// Commitment binds recovery state using a random key retained only inside
	// provider backups. Its public MAC does not permit offline token guessing.
	Commitment() string
}

// NewCredentialProofKey supplies private provider-backup integrity material.
func NewCredentialProofKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// CredentialCommitment authenticates canonical private snapshot bytes. Only the
// public commitment belongs in the journal; the key stays inside the provider.
func CredentialCommitment(key, payload []byte) string {
	if len(key) != 32 {
		return ""
	}
	digest := hmac.New(sha256.New, key)
	_, _ = digest.Write(payload) // hash.Hash.Write never returns an error
	return hex.EncodeToString(digest.Sum(nil))
}

// CredentialLocation is public provider identity, not credential material. SDK
// captures use actual vault/item IDs so later recovery need not trust old titles.
type CredentialLocation struct {
	Backend    CredentialBackend `json:"backend"`
	Service    string            `json:"service,omitempty"`
	Account    string            `json:"account"`
	SDKAccount string            `json:"sdk_account,omitempty"`
	Vault      string            `json:"vault,omitempty"`
	Item       string            `json:"item,omitempty"`
	// EnvironmentKey names external state that migration never reads or changes.
	EnvironmentKey string `json:"environment_key,omitempty"`
}

// CredentialRecoveryStore retains preimages inside the selected secret provider.
// Replacement is pure; Backup must precede a credential write. Restore compares
// the expected postimage and leaves its backup intact. These checks do not provide
// atomic compare-and-swap against other processes or remote SDK writers.
type CredentialRecoveryStore interface {
	Capture(context.Context, string) (CredentialSnapshot, error)
	Replacement(CredentialSnapshot, string) (CredentialSnapshot, error)
	Backup(context.Context, string, CredentialSnapshot, CredentialSnapshot) error
	ReadBackup(context.Context, string) (CredentialSnapshot, CredentialSnapshot, error)
	// Apply/Restore return whether a provider mutation was entered, including
	// failures after entry. A true result alone is not acknowledgment or success.
	Apply(context.Context, string, string) (bool, error)
	Restore(context.Context, string, string) (bool, error)
}

const credentialBackupPrefix = "jira-cli-recovery-"

// NewCredentialBackupID gives provider storage a dedicated, unpredictable name.
func NewCredentialBackupID() string { return credentialBackupPrefix + uuid.NewString() }

// ValidCredentialBackupID refuses ordinary credential slots and arbitrary names.
func ValidCredentialBackupID(name string) bool {
	value, ok := strings.CutPrefix(name, credentialBackupPrefix)
	if !ok {
		return false
	}
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}
