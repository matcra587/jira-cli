package onepassword

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestSDKRecoveryCanceledOperationsInitializeNothing(t *testing.T) {
	client := newRecoveryClient()
	store := New(fixtureConfig(client))
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
				err = store.Backup(ctx, core.NewCredentialBackupID(), nil, nil)
			case "read backup":
				_, _, err = store.ReadBackup(ctx, core.NewCredentialBackupID())
			case "apply":
				attempted, err = store.Apply(ctx, "default", core.NewCredentialBackupID())
			case "restore":
				attempted, err = store.Restore(ctx, "default", core.NewCredentialBackupID())
			}
			if attempted || !errors.Is(err, context.Canceled) || len(client.calls) != 0 || store.client != nil {
				t.Fatal("canceled SDK recovery initialized a client or entered native storage")
			}
			assertBackendFailure(t, err, false)
		})
	}
}

func TestSDKRecoveryStartedWritesRetainAcknowledgmentAndUncertainty(t *testing.T) {
	for _, operation := range []string{"backup", "apply", "restore"} {
		for _, fail := range []bool{false, true} {
			t.Run(operation+"/"+map[bool]string{false: "acknowledged", true: "uncertain"}[fail], func(t *testing.T) {
				client := newRecoveryClient()
				store := New(fixtureConfig(client))
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
				if operation == "restore" {
					if _, err := store.Apply(t.Context(), "default", name); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				cause := errors.New("synthetic-private-material")
				client.persistOnCreateError, client.persistOnPutError = true, true
				client.hook = func(_ context.Context, call string) error {
					if call == "create item" || call == "put item" {
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
				case "restore":
					attempted, err = store.Restore(ctx, "default", name)
				}
				if !attempted || !errors.Is(ctx.Err(), context.Canceled) || (err != nil) != fail {
					t.Fatal("started SDK mutation lost its actual acknowledgment")
				}
				if fail {
					assertBackendFailure(t, err, true)
					if !errors.Is(err, cause) || !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), cause.Error()) {
						t.Fatal("uncertain SDK mutation lost native cause/cancellation or exposed secret details")
					}
				}
				client.hook = nil
				fresh := New(fixtureConfig(client))
				if _, _, err := fresh.ReadBackup(t.Context(), name); err != nil {
					t.Fatal("interrupted operation discarded its provider-owned backup")
				}
				if operation == "apply" {
					if value, err := fresh.Get(t.Context(), "default"); err != nil || value != "synthetic-after" {
						t.Fatal("entered write result did not match the independent provider state")
					}
				}
				if operation == "restore" || operation == "backup" {
					field, err := before.(*fieldSnapshot).state.field()
					if err != nil || !reflect.DeepEqual(client.state["item-id"].Fields[1], field) {
						t.Fatal("actual SDK state lost the original owned-field metadata")
					}
				}
			})
		}
	}
}

func TestSDKRecoveryReadCancellationRetainsCauseAndReturnsNoSnapshot(t *testing.T) {
	client := newRecoveryClient()
	store := New(fixtureConfig(client))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cause := errors.New("synthetic-private-read-error")
	client.hook = func(_ context.Context, call string) error {
		if call == "get item" {
			cancel()
			return cause
		}
		return nil
	}
	state, err := store.Capture(ctx, "default")
	if state != nil || !errors.Is(err, cause) || !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), cause.Error()) {
		t.Fatal("canceled SDK read exposed state or lost its native/cancellation cause")
	}
	assertBackendFailure(t, err, false)
}

func TestSDKRecoveryCorruptAndUnownedBackupsAuthorizeNoMutation(t *testing.T) {
	for _, corruption := range []string{"private JSON", "unowned field", "wrong name", "wrong target ID", "duplicate member"} {
		t.Run(corruption, func(t *testing.T) {
			client := newRecoveryClient()
			store, name, _ := sdkRecoveryBackup(t, client)
			backupID := "created-" + name
			item := client.state[backupID]
			item.Fields = slices.Clone(item.Fields)
			switch corruption {
			case "private JSON":
				item.Fields[0].Value = `{"private-synthetic-material":"corrupt"}`
			case "unowned field":
				item.Fields[0].ID = "unmanaged"
			case "wrong name":
				item.Fields[0].Value = strings.ReplaceAll(item.Fields[0].Value, name, core.NewCredentialBackupID())
			case "wrong target ID":
				item.Fields[0].Value = strings.ReplaceAll(item.Fields[0].Value, "item-id", "different-item")
			case "duplicate member":
				item.Fields[0].Value = `{"version":1,` + item.Fields[0].Value[1:]
			}
			client.state[backupID] = item
			before := client.state["item-id"]
			if attempted, err := store.Apply(t.Context(), "default", name); attempted || err == nil || strings.Contains(err.Error(), "private-synthetic-material") {
				t.Fatal("invalid backup authorized a provider mutation or leaked its input")
			}
			if !reflect.DeepEqual(client.state["item-id"], before) {
				t.Fatal("failed backup validation changed the credential item")
			}
		})
	}
}
