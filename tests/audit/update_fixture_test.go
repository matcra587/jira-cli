//go:build outputaudit

package audit

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestAuditManagedUpdateFixturesExecute(t *testing.T) {
	bin, err := jiraBinary()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"update-managed-success", "update-managed-preview"} {
		t.Run(id, func(t *testing.T) {
			index := slices.IndexFunc(scenarios, func(s Scenario) bool { return s.ID == id })
			if index < 0 {
				t.Fatal("missing owned managed-update scenario")
			}
			scenario := scenarios[index]
			preview := id == "update-managed-preview"
			for _, mode := range modesFor(scenario.Category) {
				t.Run(string(mode), func(t *testing.T) {
					capture, err := runScenario(bin, scenario, mode, "")
					if err != nil {
						t.Fatal("owned managed-update command did not execute", err)
					}
					if capture.ExitCode != 0 || capture.Stderr != "" || capture.WroteFiles {
						t.Fatalf("owned managed-update command did not finish without writes: exit=%d stderr=%q wrote_files=%v", capture.ExitCode, capture.Stderr, capture.WroteFiles)
					}
					if mode == ModeHuman || mode == ModeHumanColor {
						if !strings.Contains(capture.Stdout, "mise up ") || strings.Contains(capture.Stdout, "Preview only;") != preview {
							t.Fatalf("owned managed-update output lost its installation-owner result: %q", capture.Stdout)
						}
						return
					}
					data := []byte(capture.Stdout)
					if mode == ModeJSON {
						var frame struct {
							OK   bool `json:"ok"`
							Meta struct {
								Command string `json:"command"`
							} `json:"meta"`
							Data json.RawMessage `json:"data"`
						}
						if err := json.Unmarshal(data, &frame); err != nil || !frame.OK || frame.Meta.Command != "update" {
							t.Fatalf("owned managed-update command lost its success frame: %v; %q", err, data)
						}
						data = frame.Data
					}
					var result struct {
						Channel   string `json:"channel"`
						Managed   *bool  `json:"managed"`
						Attempted *bool  `json:"attempted"`
						Updated   *bool  `json:"updated"`
						DryRun    *bool  `json:"dry_run"`
						Hint      string `json:"hint"`
					}
					if err := json.Unmarshal(data, &result); err != nil || result.Channel != "mise" || result.Managed == nil || !*result.Managed || result.Attempted == nil || *result.Attempted || result.Updated == nil || *result.Updated || result.DryRun == nil || *result.DryRun != preview || !strings.HasPrefix(result.Hint, "mise up ") {
						t.Fatalf("owned managed-update command lost native installation ownership: %v; %q", err, data)
					}
				})
			}
		})
	}
}
