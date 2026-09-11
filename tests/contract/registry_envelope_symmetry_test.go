package contract

import (
	"encoding/json"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The ADF registry and customfield registry MUST emit rows with the
// same envelope shape so an agent can parse both surfaces with one
// schema. Adding a new shared key MUST land in both.
func TestADFAndFieldTypesShareEnvelope(t *testing.T) {
	bin := buildJiraBinary(t)

	adfRows := loadAgentRows(t, bin, "adf-matrix")
	cfRows := loadAgentRows(t, bin, "fieldtypes")

	if len(adfRows) == 0 || len(cfRows) == 0 {
		t.Fatalf("expected non-empty rows; got adf=%d cf=%d", len(adfRows), len(cfRows))
	}

	// Required shared envelope keys.
	required := []string{"kind", "name", "status", "capabilities", "submit_description"}
	for _, key := range required {
		if _, has := adfRows[0][key]; !has {
			t.Errorf("adf-matrix row missing required envelope key %q", key)
		}
		if _, has := cfRows[0][key]; !has {
			t.Errorf("fieldtypes row missing required envelope key %q", key)
		}
	}

	// Symmetric optional keys: the OFFSET between the two sets MUST be a
	// strict subset relationship — every key in one is either also in the
	// other, or part of the registry-specific tail. We disallow novel
	// "shared-looking" keys appearing in only one registry.
	adfKeys := keysOf(adfRows[0])
	cfKeys := keysOf(cfRows[0])
	sort.Strings(adfKeys)
	sort.Strings(cfKeys)

	for _, key := range adfKeys {
		if !contains(cfKeys, key) {
			t.Errorf("envelope drift: %q present in adf-matrix but not fieldtypes — new shared keys MUST land in both", key)
		}
	}
	for _, key := range cfKeys {
		if !contains(adfKeys, key) {
			t.Errorf("envelope drift: %q present in fieldtypes but not adf-matrix — new shared keys MUST land in both", key)
		}
	}
}

func loadAgentRows(t *testing.T, bin, sub string) []map[string]any {
	t.Helper()
	cmd := exec.Command(bin, "agent", sub, "--output=json")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v\nstderr: %s", sub, err, exitStderr(err))
	}
	var env map[string]any
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("%s output not JSON: %v\n%s", sub, err, strings.TrimSpace(string(out)))
	}
	rawRows, _ := env["data"].([]any)
	rows := make([]map[string]any, 0, len(rawRows))
	for _, r := range rawRows {
		if m, ok := r.(map[string]any); ok {
			rows = append(rows, m)
		}
	}
	return rows
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func contains(s []string, v string) bool {
	return slices.Contains(s, v)
}
