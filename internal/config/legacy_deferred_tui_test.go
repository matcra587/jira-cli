package config

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const deferredTUIFixture = `# Exact global TUI preferences remain recoverable.
[[profiles]]
name='default'
secret_backend='env'
[tui]
refresh_interval=60
default_tab='issues'
tabs=['issues','epics','search','activity']
preview='private-preview-marker'
preview_size=40
default_lens='private-lens-marker'
icons='unicode'
[[tui.sections]]
title='private-section-marker'
jql='project = DEMO'
[[tui.lenses]]
title='private-lens-marker'
jql='assignee = currentUser()'
[tui.keys]
'Action.MixedCase'=['ctrl+x']
`

func TestLegacyDeferredTUIRetainsAllDeclaredNamesWithoutRuntimeMapping(t *testing.T) {
	t.Parallel()
	original := []byte(deferredTUIFixture)
	migration, err := PrepareLegacyMigrationWithOptions(t.Context(), original, "", "", LegacyMigrationOptions{DeferTUI: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"tui.default_lens", "tui.default_tab", "tui.icons", "tui.keys", "tui.lenses", "tui.preview", "tui.preview_size", "tui.refresh_interval", "tui.sections", "tui.tabs"}
	if !slices.Equal(migration.Deferred(), want) || len(migration.Unmapped()) != 0 || migration.version != 9 {
		t.Fatal("deferral lost recognized declaration names or policy")
	}
	candidate, err := migration.Candidate()
	if err != nil || bytes.Contains(candidate, []byte("tui")) || bytes.Contains(candidate, []byte("private-")) || !bytes.Equal(migration.Original(), original) {
		t.Fatal("deferred preferences became runtime values or original changed", err)
	}
	names := migration.Deferred()
	names[0] = "changed"
	original[0] = 'x'
	if !slices.Equal(migration.Deferred(), want) || !bytes.Equal(migration.Original(), []byte(deferredTUIFixture)) {
		t.Fatal("deferral retained mutable caller state")
	}
	for version := 1; version <= 8; version++ {
		old, err := prepareLegacyMigration(t.Context(), []byte(deferredTUIFixture), "", "", version)
		if err != nil || !slices.Equal(old.Unmapped(), want) || len(old.Deferred()) != 0 {
			t.Fatal("historical policy silently deferred TUI", version, err)
		}
		if candidate, err := old.Candidate(); candidate != nil || !errors.Is(err, ErrLegacyPreferencesUnmapped) {
			t.Fatal("historical TUI refusal lost", version, err)
		}
	}
	ordinary, err := PrepareLegacyMigration(t.Context(), []byte(deferredTUIFixture), "", "")
	if err != nil || ordinary.version != 8 || len(ordinary.Deferred()) != 0 {
		t.Fatal("default migration opted into deferral", err)
	}
}

func TestLegacyDeferredTUIStillRefusesUnknownAndMalformedShapes(t *testing.T) {
	t.Parallel()
	for _, extra := range []string{"\n[tui]\nunknown='private-marker'\n", "\n[tui]\nsections=[{title='ok', unknown='private-marker'}]\n", "\n[tui]\nkeys=['wrong-shape']\n", "\n[tui]\nrefresh_interval='not-a-number'\n"} {
		migration, err := PrepareLegacyMigrationWithOptions(t.Context(), []byte("[[profiles]]\nname='default'\n"+extra), "", "", LegacyMigrationOptions{DeferTUI: true})
		if migration != nil || !errors.Is(err, ErrLegacyConfig) {
			t.Fatal("deferral admitted unknown or malformed structure", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	migration, err := PrepareLegacyMigrationWithOptions(ctx, []byte(deferredTUIFixture), "", "", LegacyMigrationOptions{DeferTUI: true})
	if migration != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("deferral ignored cancellation", err)
	}
}

func TestDeliveredPolicyEightFixtureReopensAndRollsBack(t *testing.T) {
	t.Parallel()
	path, recovery := loadDeliveredMigrationFixture(t, 8)
	transaction, err := LoadLegacyTransaction(t.Context(), path)
	if err != nil || transaction.configuration.record.Version != 8 || !bytes.Contains(recovery.Candidate, []byte("literal,account")) {
		t.Fatal("delivered policy8 recovery could not reopen", err)
	}
	if _, err := transaction.Rollback(t.Context(), refuseEnvironmentProvider(t)); err != nil {
		t.Fatal("policy8 rollback failed", err)
	}
	if _, err := os.Stat(recovery.Target.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("policy8 target absence lost", err)
	}
	original, err := os.ReadFile(recovery.Source.Path)
	if err != nil || !bytes.Equal(original, recovery.Source.Contents) {
		t.Fatal("policy8 source bytes changed", err)
	}
}

func TestLegacyDeferredTUIRecoveryRefusesChangedPostimages(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "old.toml")
	targetPath := filepath.Join(dir, "new.toml")
	writeTransactionFile(t, sourcePath, []byte(deferredTUIFixture))
	source, err := ReadFile(t.Context(), sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	target, err := OpenFile(t.Context(), targetPath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PrepareLegacyTransactionWithOptions(t.Context(), source, target, "", "", LegacyMigrationOptions{DeferTUI: true})
	if err != nil {
		t.Fatal(err)
	}
	transaction, _, err := plan.Create(t.Context(), filepath.Join(dir, "recovery"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Apply(t.Context(), refuseEnvironmentProvider(t)); err != nil {
		t.Fatal(err)
	}
	writeTransactionFile(t, targetPath, []byte("# changed externally\n"))
	reopened, err := LoadLegacyTransaction(t.Context(), transaction.Receipt().Journal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Rollback(t.Context(), refuseEnvironmentProvider(t)); !errors.Is(err, ErrFileChanged) {
		t.Fatal("deferred migration restored changed postimage", err)
	}
	got, err := os.ReadFile(targetPath)
	if err != nil || !bytes.Equal(got, []byte("# changed externally\n")) {
		t.Fatal("refused rollback changed target", err)
	}
}
