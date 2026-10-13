//go:build windows && nativekeyring

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"

	"github.com/danieljoos/wincred"
	"github.com/google/uuid"

	"github.com/matcra587/jira-cli/internal/adapters/keyring"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

// These opt-in tests use only fresh random services and synthetic records.
// Independent WinCred reads prevent a process-global library mock from earning
// native acceptance. Never enumerate or inspect the user's ordinary credentials.
func TestNativeWindowsCredentialRecoveryPreservesAdmittedValues(t *testing.T) {
	record := func(token string) string {
		raw, err := json.Marshal(credentials.Record{Version: credentials.Version, Site: "https://jira.example.invalid", CloudID: "synthetic-cloud", Email: "fixture@example.invalid", Token: token})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	for _, tc := range []struct {
		name, before, after string
	}{
		{"absent", "", "synthetic current record"},
		{"present", "synthetic previous record", "synthetic current record"},
		{"large records", strings.Repeat("synthetic prior ", 100), strings.Repeat("synthetic current ", 100)},
		{"unicode records", strings.Repeat("古い値<&>\u2028", 100), strings.Repeat("новая値<&>\u2028", 100)},
		{"bound scoped records", record(strings.Repeat("synthetic prior", 110)), record(strings.Repeat("synthetic current", 110))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := "jira-cli-native-acceptance-" + uuid.NewString()
			const account = "default"
			name := core.NewCredentialBackupID()
			slots := []string{account, name, "replacement-probe", "other-profile"}
			for i := range 4 {
				slots = append(slots, fmt.Sprintf("%s.chunk-%03d", name, i))
			}
			for _, slot := range slots {
				if _, err := wincred.GetGenericCredential(service + ":" + slot); !errors.Is(err, syscall.ERROR_NOT_FOUND) {
					t.Fatal("owned test namespace was not initially absent")
				}
			}
			store := keyring.NewForService(service)
			t.Cleanup(func() {
				for _, slot := range slots {
					if err := store.Delete(context.Background(), slot); err != nil {
						t.Error("cleanup of owned native test entry failed")
					}
				}
			})
			if err := store.Set(t.Context(), "other-profile", "synthetic unrelated record"); err != nil {
				t.Fatal("owned unrelated native fixture failed")
			}
			if tc.before != "" {
				if err := store.Set(t.Context(), account, tc.before); err != nil {
					t.Fatal("native provider rejected the original fixture")
				}
			}
			// Prove the replacement is independently admissible before requiring
			// recovery of the old/new pair; backup must not reduce that capability.
			if err := store.Set(t.Context(), "replacement-probe", tc.after); err != nil {
				t.Fatal("native provider rejected the replacement fixture")
			}
			if err := store.Delete(t.Context(), "replacement-probe"); err != nil {
				t.Fatal("owned replacement probe cleanup failed")
			}
			before, err := store.Capture(t.Context(), account)
			if err != nil {
				t.Fatal("native capture failed")
			}
			expected, err := store.Replacement(before, tc.after)
			if err != nil {
				t.Fatal("native replacement preparation failed")
			}
			if err := store.Backup(t.Context(), name, before, expected); err != nil {
				t.Fatal("native backup cannot retain individually admitted records")
			}
			backup, err := wincred.GetGenericCredential(service + ":" + name)
			if err != nil || len(backup.CredentialBlob) == 0 {
				t.Fatal("backup was not published in native Credential Manager")
			}
			preimage := bytes.Clone(backup.CredentialBlob)
			fresh := keyring.NewForService(service)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if attempted, err := fresh.Apply(ctx, account, name); attempted || !errors.Is(err, context.Canceled) {
				t.Fatal("canceled native recovery admitted a mutation")
			}
			if attempted, err := fresh.Apply(t.Context(), account, name); err != nil || !attempted {
				t.Fatal("fresh native provider could not apply the retained backup")
			}
			current, err := wincred.GetGenericCredential(service + ":" + account)
			if err != nil || string(current.CredentialBlob) != tc.after {
				t.Fatal("native publication changed the replacement bytes")
			}
			if tc.name == "bound scoped records" {
				loaded, err := credentials.Load(t.Context(), fresh)
				if err != nil || loaded.Site != "https://jira.example.invalid" || loaded.CloudID != "synthetic-cloud" || loaded.Email != "fixture@example.invalid" || loaded.Token != strings.Repeat("synthetic current", 110) {
					t.Fatal("native recovery changed the credential tuple or cloud binding")
				}
			}
			if attempted, err := fresh.Apply(t.Context(), account, name); err != nil || attempted {
				t.Fatal("native idempotent apply replayed a write")
			}
			if err := store.Set(t.Context(), account, "synthetic external edit"); err != nil {
				t.Fatal("owned external edit fixture failed")
			}
			if attempted, err := fresh.Restore(t.Context(), account, name); attempted || !errors.Is(err, core.ErrCredentialChanged) {
				t.Fatal("native recovery overwrote changed primary state")
			}
			changed, err := wincred.GetGenericCredential(service + ":" + account)
			if err != nil || string(changed.CredentialBlob) != "synthetic external edit" {
				t.Fatal("refused native recovery changed external bytes")
			}
			if err := store.Set(t.Context(), account, tc.after); err != nil {
				t.Fatal("owned expected postimage fixture failed")
			}
			if attempted, err := keyring.NewForService(service).Restore(t.Context(), account, name); err != nil || !attempted {
				t.Fatal("fresh native provider could not restore its original preimage")
			}
			original, err := wincred.GetGenericCredential(service + ":" + account)
			if tc.before == "" {
				if !errors.Is(err, syscall.ERROR_NOT_FOUND) {
					t.Fatal("native restore did not restore absence")
				}
			} else if err != nil || string(original.CredentialBlob) != tc.before {
				t.Fatal("native restore changed prior bytes")
			}
			unrelated, err := wincred.GetGenericCredential(service + ":other-profile")
			if err != nil || string(unrelated.CredentialBlob) != "synthetic unrelated record" {
				t.Fatal("native recovery changed an unrelated provider entry")
			}
			retained, err := wincred.GetGenericCredential(service + ":" + name)
			if err != nil || !bytes.Equal(retained.CredentialBlob, preimage) {
				t.Fatal("native restore modified or discarded its backup")
			}
		})
	}
}
