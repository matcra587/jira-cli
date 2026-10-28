package root

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestDashboardRefusesHeadlessModesBeforeConstructingDependencies(t *testing.T) {
	for _, args := range [][]string{
		{"tui", "--output=json"},
		{"tui", "--output=compact"},
		{"tui", "--output=human"},
		{"tui", "--output=human", "--no-input"},
		{"tui", "--output=json", "--jq=.data"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			deps := testDeps(t)
			deps.JiraClient = func(context.Context) (core.JiraClient, error) {
				t.Fatal("terminal refusal constructed credentials/client")
				return nil, nil
			}
			deps.BuildPreferences = func() (core.IssuePreferences, error) {
				t.Fatal("terminal refusal loaded settings")
				return core.IssuePreferences{}, nil
			}
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(deps, &stdout, &stderr)
			program.Root.SetIn(strings.NewReader("do not consume"))
			if exit := execute(t.Context(), program, sink, args); exit != cli.ExitValidation {
				t.Fatalf("exit=%d: %s %s", exit, &stdout, &stderr)
			}
			if strings.Contains(strings.Join(args, " "), "--output=human") {
				if stdout.Len() != 0 {
					t.Fatalf("human refusal wrote result stdout: %s", &stdout)
				}
			} else {
				var frame envelope.Envelope
				if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil {
					t.Fatal(err)
				}
				if frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeDashboardTerminalRequired {
					t.Fatalf("wrong failure envelope: %s", &stdout)
				}
			}
		})
	}
}

func TestDashboardHelpIsLocalAndDescribesTheMainPage(t *testing.T) {
	deps := testDeps(t)
	deps.JiraClient = func(context.Context) (core.JiraClient, error) { t.Fatal("help constructed a client"); return nil, nil }
	var stdout, stderr bytes.Buffer
	program, sink := newProgram(deps, &stdout, &stderr)
	if exit := execute(t.Context(), program, sink, []string{"tui", "--help"}); exit != 0 {
		t.Fatalf("exit %d: %s", exit, &stderr)
	}
	text := stdout.String() + stderr.String()
	for _, want := range []string{"dashboard", "--jql", "jira issue list"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help omitted %q", want)
		}
	}
}

func TestDashboardSchemaPublishesScreenOnlyWithoutCredentials(t *testing.T) {
	var stdout bytes.Buffer
	program, sink := newProgram(testDeps(t), &stdout, &bytes.Buffer{})
	if exit := execute(t.Context(), program, sink, []string{"agent", "schema", "--path=tui", "--output=json"}); exit != 0 {
		t.Fatal("dashboard schema failed", exit)
	}
	var frame struct {
		Data struct {
			Schema struct {
				Extensions struct {
					Operations []struct {
						Op          string `json:"op"`
						ScreenOnly  bool   `json:"screen_only"`
						Class       string `json:"class"`
						WritesJira  bool   `json:"screen_writes_jira"`
						WritesLocal bool   `json:"screen_mutates_local_state"`
					} `json:"operations"`
				} `json:"extensions"`
			} `json:"schema"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil {
		t.Fatal(err)
	}
	operations := frame.Data.Schema.Extensions.Operations
	if len(operations) != 1 || operations[0].Op != "tui" || !operations[0].ScreenOnly || operations[0].Class != "interactive" || !operations[0].WritesJira || !operations[0].WritesLocal {
		t.Fatal("schema did not declare its human-only session", stdout.String())
	}
}
