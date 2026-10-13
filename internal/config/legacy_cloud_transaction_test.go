package config

import (
	"bytes"
	jsonv2 "encoding/json/v2"
	"errors"
	"os"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

func TestDeliveredVersionTwoFixtureReopensAndRollsBackOriginalRoutingPolicy(t *testing.T) {
	t.Parallel()
	path, recovery := loadDeliveredMigrationFixture(t, 2)
	transaction, err := LoadLegacyTransaction(t.Context(), path)
	if err != nil || transaction.configuration.record.Version != 2 || transaction.record.CredentialState != "external" || bytes.Contains(recovery.Candidate, []byte("cloud_id")) {
		t.Fatal("delivered policy-2 receipt was reinterpreted", err)
	}
	if _, err := transaction.Rollback(t.Context(), refuseEnvironmentProvider(t)); err != nil {
		t.Fatal("policy-2 environment receipt could not restore preimage", err)
	}
	if _, err := os.Stat(recovery.Target.Path); !os.IsNotExist(err) {
		t.Fatal("policy-2 target absence lost", err)
	}
	source, err := os.ReadFile(recovery.Source.Path)
	if err != nil || !bytes.Equal(source, recovery.Source.Contents) {
		t.Fatal("policy-2 source changed", err)
	}
	reopened, err := LoadLegacyTransaction(t.Context(), path)
	if err != nil || reopened.configuration.record.Version != 2 || reopened.record.Version != 2 {
		t.Fatal("rollback upgraded policy-2 records", err)
	}
	recovery.Version = 3
	if err := recovery.validate(t.Context()); err == nil {
		t.Fatal("policy-3 accepted old candidate bytes")
	}
}

func TestLegacyScopedTransactionConvertsBoundRoutingAndRestoresExactBytes(t *testing.T) {
	t.Parallel()
	for _, backend := range []core.CredentialBackend{core.CredentialBackendKeyring, core.CredentialBackendOnePassword} {
		t.Run(string(backend), func(t *testing.T) {
			t.Parallel()
			f := newTransactionTest(t, backend, "in place")
			// Reprepare from the same old profile with a declared scope; the first
			// unused recovery fixture remains isolated and has no primary effects.
			original, err := os.ReadFile(f.sourcePath)
			if err != nil {
				t.Fatal(err)
			}
			original = append(original, []byte("cloud_id='fixture-cloud'\n")...)
			writeTransactionFile(t, f.sourcePath, original)
			source, err := OpenFile(t.Context(), f.sourcePath)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := PrepareLegacyTransaction(t.Context(), source, source, "", "old-keyring-service")
			if err != nil {
				t.Fatal(err)
			}
			transaction, created, err := plan.Create(t.Context(), f.transaction.file.path+"-scoped")
			if err != nil || !created {
				t.Fatal(err)
			}
			f.transaction = transaction
			f.original = original
			receipt, err := transaction.Apply(t.Context(), f.provider.store)
			if err != nil || receipt.Status != "applied" {
				t.Fatal("scoped credential conversion failed", err)
			}
			var record credentials.Record
			if err := jsonv2.Unmarshal([]byte(f.provider.values[f.target.location].value), &record); err != nil || record.Version != 2 || record.CloudID != "fixture-cloud" || record.Site != "https://work.invalid" || record.Token != f.source.value {
				t.Fatal("converted token lost selected routing", err)
			}
			if _, err := f.reload(t).Rollback(t.Context(), f.provider.store); err != nil {
				t.Fatal("scoped rollback failed", err)
			}
			f.assertRestored(t)
		})
	}
}

func TestLegacyCloudValidationDoesNotRetroactivelyChangeOldUnselectedProfiles(t *testing.T) {
	t.Parallel()
	source := []byte("default_profile='work'\n[[profiles]]\nname='work'\nbase_url='https://example.invalid'\n[[profiles]]\nname='other'\ncloud_id='..'\n")
	for _, version := range []int{1, 2, 3} {
		prepared, err := prepareLegacyMigration(t.Context(), source, "", "work", version)
		if err != nil || len(prepared.Unmapped()) != 0 || prepared.Selection().CloudID != "" {
			t.Fatal("new validation rejected protected historical profile", err)
		}
		candidate, err := prepared.Candidate()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(candidate, []byte("cloud_id")) != (version == 3) {
			t.Fatal("mapping version changed original declaration policy")
		}
	}
	if _, err := PrepareLegacyMigration(t.Context(), source, "", "other"); err == nil {
		t.Fatal("unsafe selected scope became a gateway route")
	}
}

func TestCredentialBackupCannotSubstituteAnotherCloudWithValidCommitments(t *testing.T) {
	t.Parallel()
	f := newTransactionTest(t, core.CredentialBackendKeyring, "absent")
	if _, err := f.transaction.Apply(t.Context(), f.provider.store); err != nil {
		t.Fatal(err)
	}
	pair := f.provider.backups[f.transaction.record.CredentialBackup]
	var record credentials.Record
	if err := jsonv2.Unmarshal([]byte(pair.expected.value), &record); err != nil {
		t.Fatal(err)
	}
	record.CloudID = "another-cloud"
	data, err := jsonv2.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	pair.expected.value = string(data)
	// Matching commitments prove snapshot integrity, not selected cloud binding.
	f.transaction.record.ExpectedCommitment = pair.expected.Commitment()
	mutations := len(f.provider.mutations)
	if err := f.transaction.validateBackup(t.Context(), &pair.before, &pair.expected); !errors.Is(err, core.ErrCredentialRecoveryInvalid) {
		t.Fatal("valid snapshot commitment substituted a different cloud", err)
	}
	if len(f.provider.mutations) != mutations {
		t.Fatal("backup validation wrote to the provider")
	}
}
