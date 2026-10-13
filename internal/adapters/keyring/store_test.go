package keyring_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	native "github.com/zalando/go-keyring"

	"github.com/matcra587/jira-cli/internal/adapters/keyring"
	"github.com/matcra587/jira-cli/internal/core"
)

// The library mock replaces its process-wide provider. These tests run serially
// and leave an empty mock installed so no test can reach the real keyring.
func TestStoreReadDistinguishesAbsenceAndBackendFailure(t *testing.T) {
	t.Cleanup(native.MockInit)
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "absent", err: native.ErrNotFound},
		{name: "wrapped absent", err: fmt.Errorf("lookup: %w", native.ErrNotFound)},
		{name: "denied", err: errors.New("fixture backend denied access")},
		{name: "locked", err: errors.New("fixture backend is locked")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			native.MockInitWithError(tc.err)
			secret, err := keyring.New().Get(t.Context(), "default")
			if secret != "" {
				t.Fatal("failed lookup returned a secret")
			}
			if errors.Is(tc.err, native.ErrNotFound) {
				if !errors.Is(err, core.ErrNoCredential) {
					t.Fatalf("absent entry did not retain ordinary absence: %v", err)
				}
			} else if errors.Is(err, core.ErrNoCredential) || !errors.Is(err, tc.err) {
				t.Fatalf("backend failure became absence or lost its cause: %v", err)
			}
		})
	}
}

func TestStoreReadReturnsStoredCredential(t *testing.T) {
	native.MockInit()
	t.Cleanup(native.MockInit)
	store := keyring.New()
	const fixture = "synthetic-keyring-record"
	if err := store.Set(t.Context(), "default", fixture); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(t.Context(), "default")
	if err != nil || got != fixture {
		t.Fatal("successful lookup lost the stored record")
	}
	if err := store.Delete(t.Context(), "default"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(t.Context(), "default"); !errors.Is(err, core.ErrNoCredential) {
		t.Fatalf("deleted record remains readable: %v", err)
	}
}

func canceledStoreContext(t *testing.T, expired bool) context.Context {
	t.Helper()
	if expired {
		ctx, cancel := context.WithDeadline(t.Context(), time.Unix(0, 0))
		t.Cleanup(cancel)
		return ctx
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	t.Cleanup(cancel)
	return ctx
}

func TestStoreCanceledGetSuppressesNativeCall(t *testing.T) {
	t.Cleanup(native.MockInit)
	for _, tc := range []struct {
		name    string
		expired bool
	}{
		{name: "canceled"},
		{name: "deadline", expired: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cause := errors.New("synthetic native lookup reached")
			native.MockInitWithError(cause)
			ctx := canceledStoreContext(t, tc.expired)
			secret, err := keyring.New().Get(ctx, "default")
			if secret != "" || !errors.Is(err, ctx.Err()) || errors.Is(err, cause) {
				t.Fatalf("canceled lookup reached native provider or lost cancellation: has_secret=%t error=%v", secret != "", err)
			}
		})
	}
}

func TestStoreCanceledOperationsRetainNativeCredential(t *testing.T) {
	t.Cleanup(native.MockInit)
	for _, operation := range []string{"get", "set", "delete"} {
		for _, tc := range []struct {
			name    string
			expired bool
		}{
			{name: "canceled"},
			{name: "deadline", expired: true},
		} {
			t.Run(operation+" "+tc.name, func(t *testing.T) {
				native.MockInit()
				const retained = "synthetic-retained-keyring-record"
				if err := native.Set("jira-cli", "default", retained); err != nil {
					t.Fatal(err)
				}
				ctx := canceledStoreContext(t, tc.expired)
				store := keyring.New()
				var err error
				switch operation {
				case "get":
					var secret string
					secret, err = store.Get(ctx, "default")
					if secret != "" {
						t.Error("canceled lookup returned the stored credential")
					}
				case "set":
					err = store.Set(ctx, "default", "synthetic-replacement-keyring-record")
				case "delete":
					err = store.Delete(ctx, "default")
				}
				if !errors.Is(err, ctx.Err()) {
					t.Errorf("unstarted operation did not retain cancellation: %v", err)
				}
				// Read through the native mock independently of the adapter under test.
				observed, readErr := native.Get("jira-cli", "default")
				if readErr != nil || observed != retained {
					t.Fatalf("canceled operation changed the native record: retained=%t error=%v", observed == retained, readErr)
				}
			})
		}
	}
}
