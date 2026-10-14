//go:build outputaudit

package audit

import (
	"reflect"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

func TestMigrationDeferredNamesConformToOutputContract(t *testing.T) {
	bin, err := jiraBinary()
	if err != nil {
		t.Fatal(err)
	}
	scenario := Scenario{
		ID: "config-migrate-deferred-success", Op: "config.migrate", Category: CategorySuccess,
		Args:  []string{"config", "migrate", "--source=old.toml", "--config=new.toml", "--recovery-dir=recovery", "--defer-tui", "--yes", "--no-input"},
		Files: map[string]string{"old.toml": migrationFixture + "secret_backend='env'\n[tui]\ndefault_tab='issues'\ntabs=['issues','owned-private-value-marker']\n"},
	}
	for _, mode := range []Mode{ModeJSON, ModeCompact} {
		t.Run(string(mode), func(t *testing.T) {
			capture, err := runScenario(bin, scenario, mode, "")
			if err != nil {
				t.Fatal(err)
			}
			if ok, detail := checkSuccessModeContract(capture, command.Meta{Op: "config.migrate"}); !ok {
				t.Fatal("deferred migration did not complete successfully", detail)
			}
			if ok, detail := checkSuccessSchemaTruth(capture, command.Meta{Op: "config.migrate"}); !ok {
				t.Fatal("deferred migration violates the independent output contract", detail)
			}
			data, err := successData(capture)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(data["deferred"], []any{"tui.default_tab", "tui.tabs"}) || strings.Contains(capture.Stdout, "owned-private-value-marker") {
				t.Fatal("deferred migration output lost declaration names or exposed values")
			}
		})
	}
}

// Environment cases reuse the existing operation's axes without inventing a
// command variant or recording a second claimant for the global matrix cells.
func TestEnvironmentMigrationOutputAudit(t *testing.T) {
	members, problems := liveMembers()
	if len(problems) > 0 {
		t.Fatalf("inventory is not clean: %v", problems)
	}
	bin, err := jiraBinary()
	if err != nil {
		t.Fatal(err)
	}
	recipes := []Scenario{
		{ID: "config-migrate-environment-success", Op: "config.migrate", Category: CategorySuccess, Args: []string{"config", "migrate", "--source=old.toml", "--config=new.toml", "--recovery-dir=recovery", "--yes", "--no-input"}, Files: map[string]string{"old.toml": migrationFixture + "secret_backend='env'\n"}},
		{ID: "config-migrate-environment-preview", Op: "config.migrate", Category: CategoryDryRun, Args: []string{"config", "migrate", "--source=old.toml", "--config=new.toml", "--dry-run"}, Files: map[string]string{"old.toml": migrationFixture + "secret_backend='env'\n"}},
		{ID: "config-rollback-environment-success", Op: "config.rollback", Category: CategorySuccess, Args: []string{"config", "rollback", "--journal=recovery/migration.json", "--yes", "--no-input"}, Files: map[string]string{"old.toml": migrationFixture + "secret_backend='env'\n"}, SetupArgs: []string{"config", "migrate", "--source=old.toml", "--config=new.toml", "--recovery-dir=recovery", "--yes", "--no-input"}},
		{ID: "config-rollback-environment-preview", Op: "config.rollback", Category: CategoryDryRun, Args: []string{"config", "rollback", "--journal=recovery/migration.json", "--dry-run"}, Files: map[string]string{"old.toml": migrationFixture + "secret_backend='env'\n"}, SetupArgs: []string{"config", "migrate", "--source=old.toml", "--config=new.toml", "--recovery-dir=recovery", "--yes", "--no-input"}},
	}
	for _, scenario := range recipes {
		t.Run(scenario.ID, func(t *testing.T) {
			member, found := memberFor(members, scenario.Op, scenario.Variant)
			if !found {
				t.Fatal("environment recipe has no operation owner")
			}
			captures := map[string]Capture{}
			for _, mode := range modesFor(scenario.Category) {
				capture, err := runScenario(bin, scenario, mode, "")
				if err != nil {
					t.Fatal(err)
				}
				captures[scenario.ID+"|"+string(mode)] = capture
			}
			for _, cell := range expectedCells([]Member{member}) {
				if cell.Category != scenario.Category {
					continue
				}
				t.Run(cell.Key(), func(t *testing.T) {
					capture, found := captures[scenario.ID+"|"+string(cell.Mode)]
					if !found {
						t.Fatal("environment cell has no capture")
					}
					check := axisChecks[cell.Category][cell.Axis]
					if check == nil {
						t.Fatal("environment cell has no check")
					}
					if cell.Axis == AxisM12 {
						check = func(cap Capture, _ command.Meta) (bool, string) {
							sibling, found := siblingModeCaptureFrom(captures, cap)
							if !found {
								return false, "no sibling machine mode"
							}
							return checkSuccessDeterminismPair(cap, sibling)
						}
					}
					if ok, detail := check(capture, member.Meta); !ok {
						t.Errorf("environment output cell failed: %s", detail)
					}
				})
			}
		})
	}
}
