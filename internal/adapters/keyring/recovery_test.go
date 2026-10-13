package keyring_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	native "github.com/zalando/go-keyring"

	"github.com/matcra587/jira-cli/internal/adapters/keyring"
	"github.com/matcra587/jira-cli/internal/core"
)

func TestKeyringRecoveryRetainsLegacyNamespacesAndRestoresFromProvider(t *testing.T) {
	native.MockInit()
	t.Cleanup(native.MockInit)
	const namespace = "synthetic-migration-service"
	store := keyring.NewForService(namespace)
	const source = "WORK.invalid/work-staging"
	const token = "synthetic-legacy-token"
	if err := store.Set(t.Context(), source, token); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(t.Context(), "other.invalid/work-staging", "synthetic-other-token"); err != nil {
		t.Fatal(err)
	}
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			const destination = "default"
			if present {
				if err := store.Set(t.Context(), destination, "synthetic-prior-record"); err != nil {
					t.Fatal(err)
				}
			} else if err := store.Delete(t.Context(), destination); err != nil {
				t.Fatal(err)
			}
			before, err := store.Capture(t.Context(), destination)
			if err != nil {
				t.Fatal(err)
			}
			expected, err := store.Replacement(before, "synthetic-new-record")
			if err != nil {
				t.Fatal(err)
			}
			name := core.NewCredentialBackupID()
			if err := store.Backup(t.Context(), name, before, expected); err != nil {
				t.Fatal(err)
			}
			// Inspect the native mock independently and use a fresh store to recover.
			if raw, err := native.Get(namespace, name); err != nil || !strings.Contains(raw, "synthetic-new-record") {
				t.Fatal("backup was not retained inside the selected provider")
			}
			fresh := keyring.NewForService(namespace)
			attempted, err := fresh.Apply(t.Context(), destination, name)
			if err != nil || !attempted {
				t.Fatal("provider-backed apply did not enter the expected write")
			}
			if got, err := native.Get(namespace, destination); err != nil || got != "synthetic-new-record" {
				t.Fatal("apply published the wrong provider entry")
			}
			if attempted, err := fresh.Apply(t.Context(), destination, name); attempted || err != nil {
				t.Fatal("already applied credential was rewritten")
			}
			fresh = keyring.NewForService(namespace)
			if attempted, err := fresh.Restore(t.Context(), destination, name); !attempted || err != nil {
				t.Fatal("fresh provider instance could not restore its preimage")
			}
			got, readErr := native.Get(namespace, destination)
			if present && (readErr != nil || got != "synthetic-prior-record") || !present && !errors.Is(readErr, native.ErrNotFound) {
				t.Fatal("restore lost the prior value or absence")
			}
			if attempted, err := fresh.Restore(t.Context(), destination, name); attempted || err != nil {
				t.Fatal("already restored preimage was rewritten")
			}
			if _, err := native.Get(namespace, name); err != nil {
				t.Fatal("restore discarded its provider backup")
			}
		})
	}
	for name, want := range map[string]string{source: token, "other.invalid/work-staging": "synthetic-other-token"} {
		if got, err := native.Get(namespace, name); err != nil || got != want {
			t.Fatal("migration touched a historical or unrelated source slot")
		}
	}
	if _, err := native.Get("jira-cli", source); !errors.Is(err, native.ErrNotFound) {
		t.Fatal("explicit namespace leaked into the default service")
	}
}

func TestKeyringRecoveryRefusesChangedStateAndExistingBackup(t *testing.T) {
	native.MockInit()
	t.Cleanup(native.MockInit)
	store := keyring.New()
	if err := store.Set(t.Context(), "default", "synthetic-before"); err != nil {
		t.Fatal(err)
	}
	before, err := store.Capture(t.Context(), "default")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := store.Replacement(before, "synthetic-after")
	if err != nil {
		t.Fatal(err)
	}
	name := core.NewCredentialBackupID()
	if err := store.Backup(t.Context(), name, before, expected); err != nil {
		t.Fatal(err)
	}
	if err := store.Backup(t.Context(), name, before, expected); !errors.Is(err, core.ErrCredentialBackupExists) {
		t.Fatal("existing provider backup was replaced")
	}
	if _, err := store.Apply(t.Context(), "default", name); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(t.Context(), "default", "synthetic-external-edit"); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []func(context.Context, string, string) (bool, error){store.Apply, store.Restore} {
		if attempted, err := operation(t.Context(), "default", name); attempted || !errors.Is(err, core.ErrCredentialChanged) {
			t.Fatal("changed provider state was overwritten")
		}
	}
	if got, err := native.Get("jira-cli", "default"); err != nil || got != "synthetic-external-edit" {
		t.Fatal("refused recovery changed the credential")
	}
	if _, err := keyring.NewForService("different-service").Replacement(before, "synthetic-after"); !errors.Is(err, core.ErrCredentialRecoveryInvalid) {
		t.Fatal("snapshot crossed provider namespace")
	}
	if attempted, err := store.Restore(t.Context(), "other-slot", name); attempted || !errors.Is(err, core.ErrCredentialRecoveryInvalid) {
		t.Fatal("backup restored into an unrelated slot")
	}
}

func TestKeyringRecoverySnapshotsNeverRenderSecrets(t *testing.T) {
	native.MockInit()
	t.Cleanup(native.MockInit)
	store := keyring.New()
	const secret = "synthetic-private-material"
	if err := store.Set(t.Context(), "default", secret); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Capture(t.Context(), "default")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil || string(encoded) != "{}" {
		t.Fatal("provider snapshot became a public JSON payload")
	}
	for _, pattern := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(pattern, snapshot), secret) {
			t.Fatal("provider snapshot exposed a secret through formatting")
		}
	}
	if value, present := snapshot.Secret(); !present || value != secret {
		t.Fatal("private conversion lost the raw token")
	}
}
