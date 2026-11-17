//go:build linux

package root

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"charm.land/huh/v2"
	"github.com/gechr/clog"
	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/prompt"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/output"
)

func TestRuntimeTerminalStatusRespectsOutputAndOutcome(t *testing.T) {
	for _, tt := range []struct {
		name    string
		args    []string
		agent   bool
		dumb    bool
		noColor bool
		failure error
		form    bool
		states  []string
	}{
		{name: "human", args: []string{"version", "--output=human"}, states: []string{"working", "done"}},
		{name: "json", args: []string{"version", "--output=json"}},
		{name: "compact", args: []string{"version", "--output=compact"}},
		{name: "quiet", args: []string{"version", "--output=human", "--quiet"}},
		{name: "plain", args: []string{"version", "--output=human", "--color=never"}},
		{name: "no color", args: []string{"version", "--output=human"}, noColor: true},
		{name: "jq", args: []string{"version", "--jq", ".data.version"}},
		{name: "agent", args: []string{"version", "--output=human"}, agent: true},
		{name: "dumb", args: []string{"version", "--output=human"}, dumb: true},
		{name: "parse failure", args: []string{"version", "--output=human", "--unknown-fixture"}},
		{name: "failure", args: []string{"issue", "view", "DEMO-1", "--output=human"}, failure: errors.New("fixture failed"), states: []string{"working", "error"}},
		{name: "canceled", args: []string{"issue", "view", "DEMO-1", "--output=human"}, failure: context.Canceled, states: []string{"working", "idle"}},
		{name: "form", args: []string{"issue", "view", "DEMO-1", "--output=human"}, failure: context.Canceled, form: true, states: []string{"working", "blocked:kind=question", "working", "idle"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TERM", "xterm")
			t.Setenv("NO_COLOR", "1")
			if !tt.noColor {
				if err := os.Unsetenv("NO_COLOR"); err != nil {
					t.Fatal(err)
				}
			}
			if tt.dumb {
				t.Setenv("TERM", "dumb")
			}
			deps := testDeps(t)
			deps.Detect = func() cli.Detection { return cli.Detection{Format: output.FormatHuman, IsTTY: true, Agent: tt.agent} }
			calls := 0
			deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
				calls++
				if tt.form {
					form := prompt.NewForm(huh.NewInput().Title("Fixture input")).
						WithAccessible(true).WithInput(strings.NewReader("value\n")).WithOutput(io.Discard)
					if err := prompt.RunForm(ctx, form); err != nil {
						return nil, err
					}
				}
				return nil, tt.failure
			}
			master, terminal := openPromptTerminal(t)
			stdoutMaster, stdoutTerminal := openPromptTerminal(t)
			program, sink := newProgram(deps, stdoutTerminal, terminal)
			clog.SetOutput(clog.NewOutput(io.Discard, clog.ColorNever))
			code := execute(t.Context(), program, sink, tt.args)
			if tt.failure != nil && calls != 1 {
				t.Fatalf("failure fixture was not invoked exactly once: %d", calls)
			}
			if tt.name == "parse failure" && code != cli.ExitValidation {
				t.Fatalf("unknown flag returned exit %d", code)
			}
			if tt.failure != nil && code == 0 {
				t.Fatal("failed invocation returned success")
			}
			if tt.failure == nil && tt.name != "parse failure" && code != 0 {
				t.Fatalf("invocation refused: %d", code)
			}
			if err := terminal.Close(); err != nil {
				t.Fatal(err)
			}
			if err := stdoutTerminal.Close(); err != nil {
				t.Fatal(err)
			}
			stdout, err := io.ReadAll(stdoutMaster)
			if err != nil && !errors.Is(err, unix.EIO) {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(master)
			if err != nil && !errors.Is(err, unix.EIO) {
				t.Fatal(err)
			}
			var want strings.Builder
			for _, state := range tt.states {
				want.WriteString("\x1b]7501;state=" + state + ":app=jira\a")
			}
			if string(raw) != want.String() || strings.Contains(string(stdout), "\x1b]7501;") {
				t.Fatalf("status escaped its lifecycle or stream: stderr=%q, stdout=%q, want=%q", raw, stdout, want.String())
			}
		})
	}
}
