package keyring

import (
	"context"
	"errors"
	"strings"
	"testing"

	native "github.com/zalando/go-keyring"

	"github.com/matcra587/jira-cli/internal/core"
)

type nativeRecoveryFixture struct {
	values map[string]string
	calls  []string
	hook   func(string) error
}

func isolatedRecoveryStore() (*Store, *nativeRecoveryFixture) {
	fixture := &nativeRecoveryFixture{values: map[string]string{"default": "synthetic-before"}}
	store := NewForService("synthetic-isolated-service")
	store.native = nativeOperations{
		get: func(service, account string) (string, error) {
			fixture.calls = append(fixture.calls, "get")
			if service != store.serviceName() {
				return "", errors.New("wrong native namespace")
			}
			if fixture.hook != nil {
				if err := fixture.hook("get"); err != nil {
					return "", err
				}
			}
			value, found := fixture.values[account]
			if !found {
				return "", native.ErrNotFound
			}
			return value, nil
		},
		set: func(_, account, secret string) error {
			fixture.calls = append(fixture.calls, "set")
			fixture.values[account] = secret
			if fixture.hook != nil {
				return fixture.hook("set")
			}
			return nil
		},
		delete: func(_, account string) error {
			fixture.calls = append(fixture.calls, "delete")
			delete(fixture.values, account)
			if fixture.hook != nil {
				return fixture.hook("delete")
			}
			return nil
		},
	}
	return store, fixture
}

func keyringFixtureBackup(t *testing.T, store *Store) string {
	t.Helper()
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
	return name
}

func TestKeyringRecoveryCanceledReadsAndMutationsEnterNoNativeCall(t *testing.T) {
	t.Parallel()
	store, fixture := isolatedRecoveryStore()
	before, err := store.Capture(t.Context(), "default")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := store.Replacement(before, "synthetic-after")
	if err != nil {
		t.Fatal(err)
	}
	name := keyringFixtureBackup(t, store)
	fixture.calls = nil
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, operation := range []string{"capture", "backup", "read backup", "apply", "restore"} {
		t.Run(operation, func(t *testing.T) {
			var err error
			attempted := false
			switch operation {
			case "capture":
				_, err = store.Capture(ctx, "default")
			case "backup":
				err = store.Backup(ctx, core.NewCredentialBackupID(), before, expected)
			case "read backup":
				_, _, err = store.ReadBackup(ctx, name)
			case "apply":
				attempted, err = store.Apply(ctx, "default", name)
			case "restore":
				attempted, err = store.Restore(ctx, "default", name)
			}
			var classified *core.CredentialStoreError
			if attempted || !errors.Is(err, context.Canceled) || !errors.As(err, &classified) || classified.Attempted || len(fixture.calls) != 0 {
				t.Fatal("canceled recovery entered native storage or lost cancellation")
			}
		})
	}
}

func TestKeyringRecoveryStartedWritesRetainAcknowledgmentAndUncertainty(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"backup", "apply", "restore", "restore absence"} {
		for _, fail := range []bool{false, true} {
			t.Run(operation+"/"+map[bool]string{false: "acknowledged", true: "uncertain"}[fail], func(t *testing.T) {
				store, fixture := isolatedRecoveryStore()
				if operation == "restore absence" {
					delete(fixture.values, "default")
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
				if operation != "backup" {
					if err := store.Backup(t.Context(), name, before, expected); err != nil {
						t.Fatal(err)
					}
				}
				if strings.HasPrefix(operation, "restore") {
					if _, err := store.Apply(t.Context(), "default", name); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				cause := errors.New("synthetic-private-material")
				fixture.hook = func(call string) error {
					if call == "set" || call == "delete" {
						cancel()
						if fail {
							return cause
						}
					}
					return nil
				}
				attempted := true
				switch operation {
				case "backup":
					err = store.Backup(ctx, name, before, expected)
				case "apply":
					attempted, err = store.Apply(ctx, "default", name)
				default:
					attempted, err = store.Restore(ctx, "default", name)
				}
				if !attempted || !errors.Is(ctx.Err(), context.Canceled) || (err != nil) != fail {
					t.Fatal("started write lost its actual native acknowledgment")
				}
				if fail {
					var classified *core.CredentialStoreError
					if !errors.Is(err, cause) || !errors.Is(err, context.Canceled) || !errors.As(err, &classified) || !classified.Attempted || strings.Contains(err.Error(), "synthetic-private-material") {
						t.Fatal("uncertain native write lost its cause, entry or privacy")
					}
				}
				fixture.hook = nil
				if _, _, err := store.ReadBackup(t.Context(), name); err != nil {
					t.Fatal("started mutation discarded its recoverable provider backup")
				}
				value, present := fixture.values["default"]
				switch operation {
				case "apply":
					if !present || value != "synthetic-after" {
						t.Fatal("native apply entry did not match the independent provider state")
					}
				case "restore":
					if !present || value != "synthetic-before" {
						t.Fatal("native restore did not retain the original provider value")
					}
				case "restore absence":
					if present {
						t.Fatal("native restore did not retain prior provider absence")
					}
				case "backup":
					if !present || value != "synthetic-before" {
						t.Fatal("backup creation changed the destination credential")
					}
				}
			})
		}
	}
}

func TestKeyringRecoveryUnreadableBackupAndProviderErrorsNeverAuthorizeWrites(t *testing.T) {
	t.Parallel()
	store, fixture := isolatedRecoveryStore()
	name := keyringFixtureBackup(t, store)
	retained := fixture.values["default"]
	fixture.values[name] = `{"private-synthetic-material":"corrupt"}`
	if attempted, err := store.Apply(t.Context(), "default", name); attempted || err == nil || strings.Contains(err.Error(), "private-synthetic-material") {
		t.Fatal("corrupt provider backup authorized a write or disclosed its contents")
	}
	if fixture.values["default"] != retained {
		t.Fatal("failed backup validation changed the provider entry")
	}
	cause := errors.New("synthetic-private-provider-error")
	fixture.hook = func(string) error { return cause }
	if snapshot, err := store.Capture(t.Context(), "default"); snapshot != nil || !errors.Is(err, cause) || errors.Is(err, core.ErrNoCredential) || strings.Contains(err.Error(), cause.Error()) {
		t.Fatal("provider read failure became absence or leaked private cause")
	}
}

func TestKeyringRecoveryReadCancellationRetainsNativeCauseAndNoSnapshot(t *testing.T) {
	t.Parallel()
	store, fixture := isolatedRecoveryStore()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cause := errors.New("synthetic-private-read-error")
	fixture.hook = func(string) error {
		cancel()
		return cause
	}
	state, err := store.Capture(ctx, "default")
	if state != nil || !errors.Is(err, cause) || !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), cause.Error()) {
		t.Fatal("canceled native read exposed state or lost its causes")
	}
}
