package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func verifiedIdentityScope() core.AccountIdentityScope {
	return core.AccountIdentityScope{BaseURL: "https://example.invalid", Email: "person@example.invalid"}
}

func TestVerifiedIdentityPublicationBindsScopeAndPreservesUnrelatedDeclarations(t *testing.T) {
	t.Parallel()
	path := aliasFixture(t, "# original\nbase_url='https://EXAMPLE.invalid/'\nemail='PERSON@example.invalid'\ncloud_id=''\ntheme='dark'\n[aliases]\n'Mixed.Name'='version'\n")
	target, err := OpenAccountIdentityTarget(t.Context(), path, verifiedIdentityScope())
	if err != nil {
		t.Fatal(err)
	}
	update, err := target.Prepare("native-id")
	if err != nil || !update.Receipt().WouldChange || update.Receipt().Saved || update.Receipt().Changed {
		t.Fatalf("preparation published or lost candidate: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if receipt, err := update.Save(t.Context()); err != nil || !receipt.Saved || !receipt.Changed || receipt.Created || receipt.Status != "saved" {
		t.Fatalf("verified identity not published: %+v %v", receipt, err)
	}
	loaded, err := Load(Options{Path: path, Environ: func() []string { return nil }})
	if err != nil || loaded.Config.CachedAccountID() != "native-id" || loaded.Config.AccountIDSite != "https://example.invalid" || loaded.Config.AccountIDEmail != "person@example.invalid" || loaded.Config.AccountIDCloudID != "" || loaded.Config.Theme != "dark" || loaded.Config.Aliases["Mixed.Name"] != "version" {
		t.Fatalf("verified binding or unrelated values changed: %+v %v", loaded, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) == string(after) {
		t.Fatal("actual publication absent")
	}
}

func TestVerifiedIdentityTargetRefusesConflictingAuthoredScope(t *testing.T) {
	t.Parallel()
	for _, contents := range []string{"base_url='https://other.invalid'\n", "email='other@example.invalid'\n", "cloud_id='other-cloud'\n", "base_url=''\n", "email=''\n"} {
		t.Run(contents, func(t *testing.T) {
			t.Parallel()
			path := aliasFixture(t, contents)
			if target, err := OpenAccountIdentityTarget(t.Context(), path, verifiedIdentityScope()); target != nil || !errors.Is(err, ErrAccountIdentityScope) {
				t.Fatalf("conflicting scope accepted: %v", err)
			}
			actual, err := os.ReadFile(path)
			if err != nil || string(actual) != contents {
				t.Fatal("scope refusal changed file")
			}
		})
	}
}

func TestVerifiedIdentityNoOpPreservesBytesAndRefusesChangedSnapshot(t *testing.T) {
	t.Parallel()
	const original = "# exact authored bytes\naccount_id='native-id'\naccount_id_site='https://example.invalid'\naccount_id_email='person@example.invalid'\naccount_id_cloud_id=''\n"
	path := aliasFixture(t, original)
	target, err := OpenAccountIdentityTarget(t.Context(), path, verifiedIdentityScope())
	if err != nil {
		t.Fatal(err)
	}
	update, err := target.Prepare("native-id")
	if err != nil || update.Receipt().WouldChange {
		t.Fatalf("typed no-op became changed candidate: %v", err)
	}
	if receipt, err := update.Save(t.Context()); err != nil || !receipt.Saved || receipt.Changed || receipt.Status != "unchanged" {
		t.Fatalf("unchanged identity falsely reported: %+v %v", receipt, err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil || string(bytes) != original {
		t.Fatal("no-op normalized authored bytes")
	}
	const changed = "account_id='intervening'\n"
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	if receipt, err := update.Save(t.Context()); !errors.Is(err, ErrFileChanged) || receipt.Saved || receipt.Changed || receipt.Status != "not_saved" {
		t.Fatalf("unchanged candidate claimed stale cached identity was saved: %+v %v", receipt, err)
	}
}

func TestVerifiedIdentityAbsentTargetPreviewCreatesNothingAndPublicationRefusesRacer(t *testing.T) {
	t.Parallel()
	for _, racer := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "racer"}[racer], func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "missing", "settings.toml")
			target, err := OpenAccountIdentityTarget(t.Context(), path, verifiedIdentityScope())
			if err != nil {
				t.Fatal(err)
			}
			update, err := target.Prepare("native-id")
			if err != nil || !update.Receipt().WouldChange {
				t.Fatal("absent candidate not prepared", err)
			}
			if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("preparation created directory")
			}
			if racer {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("theme='dark'\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			receipt, err := update.Save(t.Context())
			if racer {
				if !errors.Is(err, ErrFileChanged) || receipt.Saved || receipt.Changed || receipt.Created || receipt.Status != "not_saved" {
					t.Fatalf("concurrent target was replaced or receipt fabricated: %+v %v", receipt, err)
				}
				contents, readErr := os.ReadFile(path)
				if readErr != nil || string(contents) != "theme='dark'\n" {
					t.Fatal("racer contents overwritten")
				}
			} else if err != nil || !receipt.Saved || !receipt.Changed || !receipt.Created || receipt.Status != "saved" {
				t.Fatalf("absent identity target not created: %+v %v", receipt, err)
			}
		})
	}
}

func TestVerifiedIdentityPreparationAndSaveHonorCancellationAndRejectBlankIDs(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	path := filepath.Join(t.TempDir(), "settings.toml")
	if target, err := OpenAccountIdentityTarget(ctx, path, verifiedIdentityScope()); target != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled preparation continued", err)
	}
	target, err := OpenAccountIdentityTarget(t.Context(), path, verifiedIdentityScope())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", " \t"} {
		if update, err := target.Prepare(id); update != nil || !errors.Is(err, ErrFileInvalid) {
			t.Fatal("blank verified identity prepared", err)
		}
	}
	update, err := target.Prepare("native-id")
	if err != nil {
		t.Fatal(err)
	}
	if receipt, err := update.Save(ctx); !errors.Is(err, context.Canceled) || receipt.Saved || receipt.Changed || receipt.Created || receipt.Status != "not_saved" {
		t.Fatalf("canceled publication fabricated success: %+v %v", receipt, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled save created target")
	}
}
