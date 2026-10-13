package config

import (
	"bytes"
	"context"
	jsonv2 "encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func legacyRecoveryFaultPlan(t *testing.T) (*LegacyRecoveryPlan, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "old.toml")
	if err := os.WriteFile(path, []byte("[[profiles]]\nname='default'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PrepareLegacyRecovery(t.Context(), source, source, "")
	if err != nil {
		t.Fatal(err)
	}
	return plan, path
}

func TestLegacyRecoveryPublicationFailureRetainsConfigurationAndActualEffects(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"link failure", "directory sync failure", "cancellation after link"} {
		t.Run(stage, func(t *testing.T) {
			plan, source := legacyRecoveryFaultPlan(t)
			path := filepath.Join(filepath.Dir(source), "recovery.json")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cause := errors.New("injected recovery publication failure")
			linked, synced := false, false
			recovery, created, err := plan.create(ctx, path, fileCreationOps{
				resolve: resolveCreationPath,
				link: func(staged, target string) error {
					if stage == "link failure" {
						return cause
					}
					if err := os.Link(staged, target); err != nil {
						return err
					}
					linked = true
					if stage == "cancellation after link" {
						cancel()
					}
					return nil
				},
				syncDirectory: func(string) error {
					synced = true
					if stage == "directory sync failure" {
						return cause
					}
					return nil
				},
			})
			wantError := cause
			if stage == "cancellation after link" {
				wantError = context.Canceled
			}
			if recovery != nil || created != linked || !errors.Is(err, wantError) || synced != linked {
				t.Fatalf("recovery failure hid publication: created=%v linked=%v synced=%v err=%v", created, linked, synced, err)
			}
			data, err := os.ReadFile(source)
			if err != nil || !bytes.Equal(data, plan.record.Source.Contents) {
				t.Fatal("failed recovery publication changed source configuration")
			}
			if linked {
				if _, err := LoadLegacyRecovery(t.Context(), path); err != nil {
					t.Fatal("published recovery was lost after failure or cancellation")
				}
			} else if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed publication exposed a partial record")
			}
		})
	}
}

func TestLegacyRecoveryRejectsIncompleteCorruptAndSecretBearingRecords(t *testing.T) {
	t.Parallel()
	plan, _ := legacyRecoveryFaultPlan(t)
	baseline, err := jsonv2.Marshal(plan.record)
	if err != nil {
		t.Fatal(err)
	}
	const private = "synthetic-private-material"
	variants := map[string][]byte{
		"syntax":           []byte("{"),
		"trailing value":   append(bytes.Clone(baseline), []byte(" {}")...),
		"duplicate member": append([]byte(`{"version":1,`), baseline[1:]...),
		"unknown private":  append([]byte(`{"`+private+`":"`+private+`",`), baseline[1:]...),
		"invalid UTF8":     []byte("{\"\xff\":0}"),
	}
	mutations := map[string]func(*legacyRecoveryRecord){
		"version":        func(r *legacyRecoveryRecord) { r.Version++ },
		"source absence": func(r *legacyRecoveryRecord) { r.Source.Exists = false },
		"relative path":  func(r *legacyRecoveryRecord) { r.Target.Requested = "relative.toml" },
		"candidate hash": func(r *legacyRecoveryRecord) { r.CandidateSHA256 = "wrong" },
		"source hash":    func(r *legacyRecoveryRecord) { r.Source.SHA256 = "wrong" },
		"secret preimage": func(r *legacyRecoveryRecord) {
			r.Target.Contents = []byte("api_token='" + private + "'\n")
			r.Target.SHA256 = recoveryDigest(r.Target.Contents)
		},
		"candidate derivation": func(r *legacyRecoveryRecord) {
			r.Candidate = []byte("read_only=true\n")
			r.CandidateSHA256 = recoveryDigest(r.Candidate)
		},
	}
	for name, mutate := range mutations {
		var record legacyRecoveryRecord
		if err := jsonv2.Unmarshal(baseline, &record); err != nil {
			t.Fatal(err)
		}
		mutate(&record)
		variants[name], err = jsonv2.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range variants {
		t.Run(name, func(t *testing.T) {
			if record, err := decodeLegacyRecovery(t.Context(), data); record != nil || !errors.Is(err, ErrLegacyRecovery) || strings.Contains(err.Error(), private) {
				t.Fatal("corrupt recovery became usable or disclosed private input")
			}
		})
	}
	if _, err := decodeLegacyRecovery(t.Context(), []byte("{")); errors.Unwrap(err) == nil {
		t.Fatal("sanitization discarded native JSON failure")
	}
}

func TestLegacyRecoveryRemovalFailureRetainsAcknowledgedOutcome(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"remove failure", "sync failure"} {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "migrated.toml")
			if err := os.WriteFile(path, []byte("read_only=true\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			file, err := OpenFile(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			cause := errors.New("injected config removal failure")
			removed, synced := false, false
			changed, err := file.removeWith(t.Context(), func(path string) error {
				if stage == "remove failure" {
					return cause
				}
				if err := os.Remove(path); err != nil {
					return err
				}
				removed = true
				return nil
			}, func(string) error {
				synced = true
				return cause
			})
			if changed != removed || !errors.Is(err, cause) || synced != removed {
				t.Fatal("removal failure hid its acknowledged effect")
			}
			_, inspectErr := os.Stat(path)
			if errors.Is(inspectErr, os.ErrNotExist) != removed {
				t.Fatal("reported removal did not match the filesystem")
			}
		})
	}
}

func TestLegacyRecoveryRequiresExplicitPathBeforeDefaultSelection(t *testing.T) {
	// Keep the refusal guard safe even if its defect is reintroduced: an empty
	// config path would otherwise select real user settings through the root flags.
	before := currentFlagLayer()
	defaultPath := filepath.Join(t.TempDir(), "missing", "default.toml")
	SetFlagLayer(FlagLayer{Path: defaultPath})
	t.Cleanup(func() { SetFlagLayer(before) })
	plan, _ := legacyRecoveryFaultPlan(t)
	if recovery, created, err := plan.Create(t.Context(), ""); recovery != nil || created || !errors.Is(err, ErrLegacyRecovery) {
		t.Fatal("recovery selected an unrelated default configuration path")
	}
	if _, err := os.Stat(filepath.Dir(defaultPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("empty recovery path created the default configuration directory")
	}
}
