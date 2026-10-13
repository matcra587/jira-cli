//go:build outputaudit

package audit

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Each migration creates fresh public locators, so its candidate hash and
// target accounts legitimately differ between isolated runs. Only those
// fields are normalized; source locations, hashes and effect receipts remain
// exact. Repeated locator references must agree before comparison.
func normalizeMigrationComparison(data map[string]any) error {
	locations, owners := map[string]string{}, map[string]string{}
	normalizeTarget := func(container map[string]any) error {
		location, ok := container["target_credential"].(map[string]any)
		if !ok || location["backend"] != "keyring" {
			return nil
		}
		id, ok := location["account"].(string)
		if !ok {
			return fmt.Errorf("migration target account must be a string")
		}
		if id == "default" {
			return nil
		}
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			return fmt.Errorf("migration target account must be a canonical UUID")
		}
		profile, ok := container["profile"].(string)
		if !ok || profile == "" {
			return fmt.Errorf("migration target requires its profile name")
		}
		if previous, exists := locations[profile]; exists && previous != id {
			return fmt.Errorf("migration target references disagree for profile %s", profile)
		}
		if previous, exists := owners[id]; exists && previous != profile {
			return fmt.Errorf("migration profiles share a generated credential location")
		}
		locations[profile], owners[id] = id, profile
		location["account"] = "<credential:" + profile + ">"
		return nil
	}
	if err := normalizeTarget(data); err != nil {
		return err
	}
	receipt, ok := data["receipt"].(map[string]any)
	if !ok {
		return nil
	}
	hash, ok := receipt["candidate_sha256"].(string)
	decoded, err := hex.DecodeString(hash)
	if !ok || err != nil || len(decoded) != 32 || hash != strings.ToLower(hash) {
		return fmt.Errorf("migration candidate_sha256 must be a lowercase SHA-256 digest")
	}
	receipt["candidate_sha256"] = "<candidate-sha256>"
	if err := normalizeTarget(receipt); err != nil {
		return err
	}
	if profiles, ok := receipt["profiles"].([]any); ok {
		for _, raw := range profiles {
			profile, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("migration profile receipt must be an object")
			}
			if err := normalizeTarget(profile); err != nil {
				return err
			}
		}
	}
	return nil
}

func TestMigrationComparisonRejectsMalformedOrInconsistentTransientMetadata(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, hash, target, repeated string }{
		{name: "malformed hash", hash: "wrong", target: "b6ff8a17-9f47-4e25-83b8-cf3f38466700"},
		{name: "malformed locator", hash: strings.Repeat("a", 64), target: "wrong"},
		{name: "inconsistent locator", hash: strings.Repeat("a", 64), target: "b6ff8a17-9f47-4e25-83b8-cf3f38466700", repeated: "2a3d34d2-52a8-4dfb-b0da-0d6ff5d22428"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repeated := tc.repeated
			if repeated == "" {
				repeated = tc.target
			}
			data := map[string]any{
				"profile": "work", "target_credential": map[string]any{"backend": "keyring", "account": tc.target},
				"receipt": map[string]any{"profile": "work", "candidate_sha256": tc.hash, "target_credential": map[string]any{"backend": "keyring", "account": repeated}},
			}
			if err := normalizeMigrationComparison(data); err == nil {
				t.Fatal("comparison hid malformed or inconsistent metadata")
			}
		})
	}
}

func TestMigrationComparisonPreservesSourceAndOutcomeDifferences(t *testing.T) {
	t.Parallel()
	capture := func(id, hash, source, status string) Capture {
		t.Helper()
		data := map[string]any{
			"profile": "work", "target_credential": map[string]any{"backend": "keyring", "account": id},
			"source_credential": map[string]any{"backend": "keyring", "account": source},
			"receipt": map[string]any{
				"profile": "work", "candidate_sha256": hash, "status": status,
				"source_sha256": strings.Repeat("c", 64), "target_credential": map[string]any{"backend": "keyring", "account": id},
			},
		}
		body, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		return Capture{Scenario: Scenario{Op: "config.migrate", Category: CategorySuccess}, Mode: ModeCompact, Stdout: string(body)}
	}
	first := capture("b6ff8a17-9f47-4e25-83b8-cf3f38466700", strings.Repeat("a", 64), "legacy/work", "applied")
	for _, tc := range []struct {
		name, source, status string
		want                 bool
	}{
		{name: "independent locators", source: "legacy/work", status: "applied", want: true},
		{name: "changed source", source: "legacy/other", status: "applied"},
		{name: "changed outcome", source: "legacy/work", status: "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			second := capture("2a3d34d2-52a8-4dfb-b0da-0d6ff5d22428", strings.Repeat("b", 64), tc.source, tc.status)
			if ok, detail := checkSuccessDeterminismPair(first, second); ok != tc.want {
				t.Fatal("migration comparison lost a stable field or rejected independent generated metadata", detail)
			}
		})
	}
}
