package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDeliveredVersionThreeFixtureReopensWithoutDefaultReinterpretation(t *testing.T) {
	t.Parallel()
	path, recovery := loadDeliveredMigrationFixture(t, 3)
	transaction, err := LoadLegacyTransaction(t.Context(), path)
	if err != nil || transaction.configuration.record.Version != 3 || bytes.Contains(recovery.Candidate, []byte("default_project")) {
		t.Fatal("delivered policy3 was reinterpreted", err)
	}
	if _, err := transaction.Rollback(t.Context(), refuseEnvironmentProvider(t)); err != nil {
		t.Fatal("policy3 exact rollback failed", err)
	}
	if _, err := os.Stat(recovery.Target.Path); !os.IsNotExist(err) {
		t.Fatal("policy3 target absence lost", err)
	}
	recovery.Version = 4
	if err := recovery.validate(t.Context()); err == nil {
		t.Fatal("policy4 accepted old candidate bytes")
	}
}

func TestLegacyDefaultMappingsRetainEarlierDeclarationPolicies(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		"[profiles]\nname='work'\ndefault_project='DEFAULT'\ndefault_issue_type='Task'\ndefault_board='42'\n",
		"[[profiles]]\nNAME='work'\nDEFAULT_PROJECT='DEFAULT'\nDEFAULT_ISSUE_TYPE='Task'\nDEFAULT_BOARD='42'\n",
	} {
		for _, version := range []int{1, 2, 3, 4} {
			prepared, err := prepareLegacyMigration(t.Context(), []byte("default_profile='work'\n"+source), "", "work", version)
			if err != nil {
				t.Fatal(err)
			}
			if version < 4 {
				if len(prepared.Unmapped()) != 3 {
					t.Fatal("old policy lost unsupported declaration refusal")
				}
				continue
			}
			if len(prepared.Unmapped()) != 0 {
				t.Fatal("supported defaults remain unmapped")
			}
			candidate, err := prepared.Candidate()
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "settings.toml")
			writeTransactionFile(t, target, candidate)
			resolved, err := Load(Options{Path: target, Environ: func() []string { return nil }})
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Config.DefaultProject != "DEFAULT" || resolved.Config.DefaultIssueType != "Task" || resolved.Config.DefaultBoard != "42" {
				t.Fatal("new mapping changed selected defaults or numeric board name")
			}
		}
	}
}
