package keyring

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestKeyringPublicationRetainsExactNativeAdmission(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"before", "entered error", "acknowledged cancellation"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			store, fixture := isolatedRecoveryStore()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cause := errors.New("synthetic-private-provider-payload")
			if stage == "before" {
				cancel()
			}
			fixture.hook = func(string) error {
				cancel()
				if stage == "entered error" {
					return cause
				}
				return nil
			}
			err := store.Set(ctx, "default", "synthetic-new-record")
			var classified *core.CredentialStoreError
			if stage == "acknowledged cancellation" {
				if err != nil || fixture.values["default"] != "synthetic-new-record" || len(fixture.calls) != 1 {
					t.Fatal("late cancellation discarded actual native acknowledgment", err)
				}
				return
			}
			if !errors.As(err, &classified) || classified.Attempted != (stage == "entered error") || strings.Contains(err.Error(), cause.Error()) {
				t.Fatal("native admission facts or private error sanitization lost", err)
			}
			if stage == "before" {
				if !errors.Is(err, context.Canceled) || len(fixture.calls) != 0 || fixture.values["default"] != "synthetic-before" {
					t.Fatal("canceled native write entered storage")
				}
			} else if !errors.Is(err, cause) || len(fixture.calls) != 1 || fixture.values["default"] != "synthetic-new-record" {
				t.Fatal("entered failed write denied actual native effects")
			}
		})
	}
}
