package root

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

func TestAliasExpansionPreservesFlagArityQuotingAndArgumentOrder(t *testing.T) {
	program, _ := newProgram(testDeps(t), io.Discard, io.Discard)
	table := map[string]string{
		"outer": `--timeout=2s inner --limit=3`,
		"inner": `issue list --jql 'literal #tag' --status 'To Do,In Progress'`,
	}
	input := []string{"--jq-arg", "name=outer", "--output", "json", "outer", "--limit=4"}
	original := slices.Clone(input)
	got, err := expandAliases(program.Root, table, input)
	want := []string{"--jq-arg", "name=outer", "--output", "json", "--timeout=2s", "issue", "list", "--jql", "literal #tag", "--status", "To Do,In Progress", "--limit=3", "--limit=4"}
	if err != nil || !slices.Equal(got, want) || !slices.Equal(input, original) {
		t.Fatalf("alias expansion lost argv identity/order: %q %v", got, err)
	}
	name, index := firstPositionalIndex(program.Root, []string{"--output", "outer", "outer"})
	if name != "outer" || index != 2 {
		t.Fatalf("a flag value became the command token: %q at %d", name, index)
	}
}

func TestAliasExpansionRejectsMalformedUnknownAndCyclicChains(t *testing.T) {
	program, _ := newProgram(testDeps(t), io.Discard, io.Discard)
	for _, table := range []map[string]string{
		{"start": "start"},
		{"start": "middle", "middle": "start"},
		{"start": "missing"},
		{"start": "version '"},
		{"start": "# comment"},
		{"start": "--output=json"},
		{"start": "-- version"},
	} {
		t.Run(fmt.Sprint(table), func(t *testing.T) {
			_, err := expandAliases(program.Root, table, []string{"start"})
			input, ok := errors.AsType[*cli.InputError](err)
			if !ok || input.Code != cli.CodeAliasInvalid {
				t.Fatalf("invalid expansion was not refused with its own code: %v", err)
			}
		})
	}
	if err := validateAlias(program.Root, map[string]string{"start": "issue nonexistent"}, "start"); err == nil {
		t.Fatal("invalid built-in command path passed alias validation")
	}
}

func TestAliasExpansionKeepsBuiltinsTerminatorsAndUnrelatedDefinitions(t *testing.T) {
	program, _ := newProgram(testDeps(t), io.Discard, io.Discard)
	table := map[string]string{"version": "issue delete", "good": "version", "bad": "bad"}
	for _, args := range [][]string{{"version"}, {"--", "good"}, {"unknown"}} {
		got, err := expandAliases(program.Root, table, args)
		if err != nil || !slices.Equal(got, args) {
			t.Fatalf("builtin, terminator or unknown command changed: %q %v", got, err)
		}
	}
	if err := validateAlias(program.Root, table, "good"); err != nil {
		t.Fatalf("an unrelated invalid definition disabled a valid alias: %v", err)
	}
	for _, name := range []string{"", " ", "-name", "version", "help", "bad\nname", "bad\u0085name", string([]byte{0xff})} {
		if err := validateAliasName(program.Root, name); err == nil {
			t.Fatalf("invalid alias name accepted: %q", name)
		}
	}
	for _, name := range []string{"my.inbox", "quote # name", "日本語"} {
		if err := validateAliasName(program.Root, name); err != nil {
			t.Fatalf("literal alias name rejected: %q %v", name, err)
		}
	}
}

func TestAliasExpansionLongChainIsIterativeAndPreservesEveryTail(t *testing.T) {
	program, _ := newProgram(testDeps(t), io.Discard, io.Discard)
	table := map[string]string{}
	const count = 1000
	for i := range count {
		target := fmt.Sprintf("step%d", i+1)
		if i == count-1 {
			target = "version"
		}
		table[fmt.Sprintf("step%d", i)] = target + fmt.Sprintf(" --jq-arg marker%d=value", i)
	}
	got, err := expandAliases(program.Root, table, []string{"step0"})
	if err != nil || len(got) != 1+2*count || got[0] != "version" {
		t.Fatalf("long chain was truncated: %d %v", len(got), err)
	}
	for i := range count {
		if got[1+2*i] != "--jq-arg" || got[2+2*i] != fmt.Sprintf("marker%d=value", count-1-i) {
			t.Fatal("long chain lost argument order")
		}
	}
	if strings.Contains(strings.Join(got, " "), "step") {
		t.Fatal("unexpanded command leaked into final argv")
	}
}
