package config_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/config"
)

const legacyRecoveryFixture = `# Preserve formatting and the unselected profile.
default_profile='work'
[[profiles]]
name='default'
email='other@example.invalid'
[[profiles]]
name='work'
base_url='https://work.invalid/'
email='work@example.invalid'
timeout=0
`

func openRecoveryConfig(t *testing.T, path string) *config.File {
	t.Helper()
	file, err := config.OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func recoveryPlan(t *testing.T, sourcePath, targetPath string) *config.LegacyRecoveryPlan {
	t.Helper()
	plan, err := config.PrepareLegacyRecovery(t.Context(), openRecoveryConfig(t, sourcePath), openRecoveryConfig(t, targetPath), "")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestLegacyRecoveryPublishesBeforeConfigurationAndRestoresExactPreimage(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"in place", "absent destination", "existing flat destination"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			sourcePath := filepath.Join(dir, "old.toml")
			if err := os.WriteFile(sourcePath, []byte(legacyRecoveryFixture), 0o600); err != nil {
				t.Fatal(err)
			}
			targetPath := sourcePath
			original := []byte(legacyRecoveryFixture)
			if kind != "in place" {
				targetPath = filepath.Join(dir, "new.toml")
				original = nil
			}
			if kind == "existing flat destination" {
				original = []byte("# Keep this byte spelling on rollback.\nread_only = true\n")
				if err := os.WriteFile(targetPath, original, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			plan := recoveryPlan(t, sourcePath, targetPath)
			recoveryPath := filepath.Join(dir, "private", "recovery.json")
			if _, err := os.Stat(filepath.Dir(recoveryPath)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("preparation created a recovery directory")
			}
			recovery, created, err := plan.Create(t.Context(), recoveryPath)
			if err != nil || !created || recovery == nil {
				t.Fatalf("complete recovery was not published: created=%v err=%v", created, err)
			}
			before, readErr := os.ReadFile(targetPath)
			if !bytes.Equal(before, original) || kind == "absent destination" && !errors.Is(readErr, os.ErrNotExist) || kind != "absent destination" && readErr != nil {
				t.Fatal("recovery publication changed the destination")
			}
			recordBytes, err := os.ReadFile(recoveryPath)
			if err != nil {
				t.Fatal(err)
			}
			if xos.IsUnix() {
				info, err := os.Stat(recoveryPath)
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatal("recovery preimages are not protected")
				}
			}
			// Reload both times to prove recovery does not depend on process memory.
			recovery, err = config.LoadLegacyRecovery(t.Context(), recoveryPath)
			if err != nil {
				t.Fatal(err)
			}
			changed, err := recovery.ApplyConfig(t.Context())
			if err != nil || !changed {
				t.Fatalf("prepared migration refused: changed=%v err=%v", changed, err)
			}
			candidate, err := os.ReadFile(targetPath)
			if err != nil || config.ValidateFile(candidate) != nil || bytes.Contains(candidate, []byte("other@example.invalid")) {
				t.Fatal("migration did not publish the selected flat profile")
			}
			recovery, err = config.LoadLegacyRecovery(t.Context(), recoveryPath)
			if err != nil {
				t.Fatal(err)
			}
			changed, err = recovery.RestoreConfig(t.Context())
			if err != nil || !changed {
				t.Fatalf("rollback refused the expected postimage: changed=%v err=%v", changed, err)
			}
			after, readErr := os.ReadFile(targetPath)
			if !bytes.Equal(after, original) || kind == "absent destination" && !errors.Is(readErr, os.ErrNotExist) || kind != "absent destination" && readErr != nil {
				t.Fatal("rollback lost exact destination preimage or prior absence")
			}
			if changed, err := recovery.RestoreConfig(t.Context()); changed || err != nil {
				t.Fatal("already restored configuration was rewritten")
			}
			retained, err := os.ReadFile(recoveryPath)
			if err != nil || !bytes.Equal(retained, recordBytes) {
				t.Fatal("apply or rollback discarded recovery evidence")
			}
		})
	}
}

func TestLegacyRecoveryRefusesChangedPreimagesBeforePublication(t *testing.T) {
	t.Parallel()
	for _, changed := range []string{"source", "target"} {
		t.Run(changed, func(t *testing.T) {
			dir := t.TempDir()
			sourcePath, targetPath := filepath.Join(dir, "old.toml"), filepath.Join(dir, "new.toml")
			if err := os.WriteFile(sourcePath, []byte(legacyRecoveryFixture), 0o600); err != nil {
				t.Fatal(err)
			}
			plan := recoveryPlan(t, sourcePath, targetPath)
			path := sourcePath
			if changed == "target" {
				path = targetPath
			}
			if err := os.WriteFile(path, []byte("read_only=true\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			recoveryPath := filepath.Join(dir, "missing", "record.json")
			if recovery, created, err := plan.Create(t.Context(), recoveryPath); recovery != nil || created || !errors.Is(err, config.ErrFileChanged) {
				t.Fatal("changed preimage produced a recovery record")
			}
			if _, err := os.Stat(filepath.Dir(recoveryPath)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("refused publication created directories")
			}
		})
	}
}

func TestLegacyRecoveryRefusesRecordAndPostimageChanges(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"record before apply", "target before apply", "postimage before rollback", "record before rollback"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			path, recoveryPath := filepath.Join(dir, "old.toml"), filepath.Join(dir, "record.json")
			if err := os.WriteFile(path, []byte(legacyRecoveryFixture), 0o600); err != nil {
				t.Fatal(err)
			}
			recovery, _, err := recoveryPlan(t, path, path).Create(t.Context(), recoveryPath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stage, "rollback") {
				if _, err := recovery.ApplyConfig(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			changedPath := path
			if strings.HasPrefix(stage, "record") {
				changedPath = recoveryPath
			}
			if err := os.WriteFile(changedPath, []byte("# External edit\nread_only=true\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			operation := recovery.ApplyConfig
			if strings.Contains(stage, "rollback") {
				operation = recovery.RestoreConfig
			}
			if changed, err := operation(t.Context()); changed || !errors.Is(err, config.ErrFileChanged) {
				t.Fatal("changed recovery state was overwritten")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(after, before) {
				t.Fatal("refused operation changed the target")
			}
		})
	}
}

func TestLegacyRecoveryRefusesCancellationAndInvalidPlans(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "old.toml")
	if err := os.WriteFile(path, []byte(legacyRecoveryFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := recoveryPlan(t, path, path)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	recoveryPath := filepath.Join(dir, "missing", "record.json")
	if recovery, created, err := plan.Create(ctx, recoveryPath); recovery != nil || created || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled publication created a recovery record")
	}
	if _, err := os.Stat(filepath.Dir(recoveryPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled publication created directories")
	}
	if recovery, created, err := new(config.LegacyRecoveryPlan).Create(t.Context(), recoveryPath); recovery != nil || created || !errors.Is(err, config.ErrLegacyRecovery) {
		t.Fatal("zero plan became publishable")
	}
	if changed, err := new(config.LegacyRecovery).ApplyConfig(t.Context()); changed || !errors.Is(err, config.ErrLegacyRecovery) {
		t.Fatal("unpublished recovery owner changed configuration")
	}
	if _, err := config.PrepareLegacyRecovery(t.Context(), nil, openRecoveryConfig(t, path), ""); !errors.Is(err, config.ErrLegacyRecovery) {
		t.Fatal("missing source became recoverable")
	}
}

func TestLegacyRecoverySupportsMissingParentsUnderSymlinkedAncestors(t *testing.T) {
	t.Parallel()
	if xos.IsWindows() {
		t.Skip("symlink creation requires an enabled Windows privilege")
	}
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "old.toml")
	if err := os.WriteFile(source, []byte(legacyRecoveryFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(link, "missing", "nested", "new.toml")
	plan := recoveryPlan(t, source, target)
	if _, err := os.Stat(filepath.Join(real, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preparation created target directories")
	}
	recovery, _, err := plan.Create(t.Context(), filepath.Join(dir, "record.json"))
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := recovery.ApplyConfig(t.Context()); err != nil || !changed {
		t.Fatalf("canonical absent target refused creation: changed=%v err=%v", changed, err)
	}
	actual := filepath.Join(real, "missing", "nested", "new.toml")
	data, err := os.ReadFile(actual)
	if err != nil || config.ValidateFile(data) != nil {
		t.Fatal("publication lost the resolved symlink ancestor")
	}
	if changed, err := recovery.RestoreConfig(t.Context()); err != nil || !changed {
		t.Fatalf("canonical target refused restoration: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(actual); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("restoration did not retain prior target absence")
	}
}

func TestLegacyRecoveryRefusesAuthoredTokensAndExistingRecoveryNames(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source, target := filepath.Join(dir, "old.toml"), filepath.Join(dir, "new.toml")
	if err := os.WriteFile(source, []byte(legacyRecoveryFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, declaration := range []string{"api_token='synthetic-private-material'", "API_TOKEN='synthetic-private-material'", "api_token=''"} {
		if err := os.WriteFile(target, []byte(declaration+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		plan, err := config.PrepareLegacyRecovery(t.Context(), openRecoveryConfig(t, source), openRecoveryConfig(t, target), "")
		if plan != nil || !errors.Is(err, config.ErrLegacyRecovery) || strings.Contains(err.Error(), "synthetic-private-material") {
			t.Fatal("authored token declaration became recoverable configuration")
		}
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	plan := recoveryPlan(t, source, target)
	if recovery, created, err := plan.Create(t.Context(), target); recovery != nil || created || !errors.Is(err, config.ErrLegacyRecovery) {
		t.Fatal("recovery replaced its absent configuration destination")
	}
	recoveryPath := filepath.Join(dir, "record.json")
	const existing = "existing private record"
	if err := os.WriteFile(recoveryPath, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	if recovery, created, err := plan.Create(t.Context(), recoveryPath); recovery != nil || created || !errors.Is(err, config.ErrFileChanged) {
		t.Fatal("recovery publication replaced an existing record")
	}
	data, err := os.ReadFile(recoveryPath)
	if err != nil || string(data) != existing {
		t.Fatal("existing recovery bytes changed")
	}
}

func TestLegacyRecoveryApplyRefusesChangedSeparateSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source, target := filepath.Join(dir, "old.toml"), filepath.Join(dir, "new.toml")
	if err := os.WriteFile(source, []byte(legacyRecoveryFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	recovery, _, err := recoveryPlan(t, source, target).Create(t.Context(), filepath.Join(dir, "record.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("[[profiles]]\nname='default'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := recovery.ApplyConfig(t.Context()); changed || !errors.Is(err, config.ErrFileChanged) {
		t.Fatal("changed source profile still authorized configuration publication")
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("changed source produced a destination configuration")
	}
}

func TestLegacyRecoveryDoesNotWeakenOrdinaryFlatPublication(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.toml")
	const original = "read_only=true\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	file := openRecoveryConfig(t, path)
	if changed, err := file.Save(t.Context(), []byte(legacyRecoveryFixture)); changed || !errors.Is(err, config.ErrFileInvalid) {
		t.Fatal("ordinary save accepted a legacy recovery preimage")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != original {
		t.Fatal("refused ordinary save changed configuration")
	}
	absent := filepath.Join(dir, "missing", "new.toml")
	file, err = config.OpenNewFile(t.Context(), absent)
	if err != nil {
		t.Fatal(err)
	}
	if created, err := file.Create(t.Context(), []byte(legacyRecoveryFixture)); created || !errors.Is(err, config.ErrFileInvalid) {
		t.Fatal("ordinary creation accepted a legacy recovery preimage")
	}
	if _, err := os.Stat(filepath.Dir(absent)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid ordinary creation created directories")
	}
}
