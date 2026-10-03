package worklog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
)

const nativeComment = `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"<>&"}]}]}`

func parsedInput(t *testing.T, args ...string) inputFlags {
	t.Helper()
	var input inputFlags
	input.register(&cobra.Command{Use: "add"})
	if err := input.cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	if err := input.validate(); err != nil {
		t.Fatal(err)
	}
	return input
}

func fixedDeps() Deps {
	return Deps{Now: func() time.Time { return time.Date(2026, 1, 2, 12, 30, 0, 0, time.UTC) }, Location: time.UTC}
}

func TestWorklogInputNativeValuesAndOverrides(t *testing.T) {
	t.Parallel()
	properties := `[{ "key":"future", "value": {"empty":[],"null":null,"false":false,"zero":0,"large":9007199254740993,"markup":"<>&"} }]`
	input := parsedInput(t, "--json-input=-", "--time-spent=1d30m", "--started=2026-01-03T10:00:00+02:30")
	raw := `{"timeSpentSeconds":null,"time_spent":false,"started":false,"comment":` + nativeComment + `,"properties":` + properties + `,"visibility":null,"future":false}`
	req, route, err := input.compose(t.Context(), fixedDeps(), command.Invocation{Stdin: strings.NewReader(raw)}, 21600)
	if err != nil || req.TimeSpentSeconds != 23400 || req.Started != "2026-01-03T10:00:00.000+0230" || route != command.RouteADF {
		t.Fatalf("native overrides or configured workday lost: %+v %q %v", req, route, err)
	}
	if string(req.Comment) != nativeComment || string(req.Extra["properties"]) != properties || string(req.Extra["visibility"]) != "null" || string(req.Extra["future"]) != "false" || len(req.Extra) != 3 {
		t.Fatalf("native values changed or convenience members leaked: %+v", req)
	}
	body, err := req.MarshalJSON()
	if err != nil || !strings.Contains(string(body), properties) || !strings.Contains(string(body), nativeComment) || strings.Contains(string(body), "time_spent") || strings.Contains(string(body), "timeSpent\"") {
		t.Fatalf("request lost raw bytes or retained input-only members: %s %v", body, err)
	}
}

func TestWorklogInputLegacyAndNativeEffort(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, raw string
		seconds   int
		route     string
	}{
		{"native seconds", `{"timeSpentSeconds":1}`, 1, ""},
		{"native text", `{"timeSpent":"2h30m"}`, 9000, ""},
		{"legacy markdown", `{"time_spent":"1d","comment_markdown":"investigation"}`, 28800, command.RouteMarkdown},
		{"explicit null comment", `{"timeSpentSeconds":60,"comment":null}`, 60, command.RouteADF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := parsedInput(t, "--json-input=-")
			req, route, err := input.compose(t.Context(), fixedDeps(), command.Invocation{Stdin: strings.NewReader(tc.raw)}, 28800)
			if err != nil || req.TimeSpentSeconds != tc.seconds || req.Started != "2026-01-02T12:30:00.000+0000" || route != tc.route {
				t.Fatalf("effort, default time or body route changed: %+v %q %v", req, route, err)
			}
			if tc.route == command.RouteMarkdown && (!json.Valid(req.Comment) || !strings.Contains(string(req.Comment), "investigation")) {
				t.Fatalf("legacy Markdown was not converted: %s", req.Comment)
			}
			if tc.name == "explicit null comment" && string(req.Comment) != "null" {
				t.Fatal("explicit null comment vanished")
			}
		})
	}
}

func TestWorklogInputRejectsMalformedAndAmbiguousPayloads(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"timeSpentSeconds":60} {}`, `{"timeSpentSeconds":60} garbage`,
		`{"timeSpentSeconds":60,"timeSpentSeconds":120}`, `{"timeSpentSeconds":60,"\u0074imeSpentSeconds":120}`,
		`{"TimeSpentSeconds":60}`, `{"timeSpentSeconds":null}`, `{"timeSpentSeconds":1.5}`, `{"timeSpentSeconds":"60"}`,
		`{"timeSpentSeconds":60,"TimeSpentSeconds":120}`,
		`{"timeSpentSeconds":60,"Started":"2026-01-02T12:30:00Z"}`, `{"timeSpentSeconds":60,"Comment":null}`,
		`{"timeSpentSeconds":0}`, `{"timeSpentSeconds":-1}`, `{"timeSpentSeconds":9223372036854775808}`,
		`{"timeSpentSeconds":60,"time_spent":"1m"}`, `{"timeSpent":"1h","time_spent":"1m"}`,
		`{"time_spent":null}`, `{"time_spent":false}`, `{"time_spent":"0m"}`,
		`{"timeSpentSeconds":60,"started":null}`, `{"timeSpentSeconds":60,"started":"not a date"}`,
		`{"timeSpentSeconds":60,"started":""}`,
		`{"timeSpentSeconds":60,"comment":false}`, `{"timeSpentSeconds":60,"comment":{"type":"paragraph"}}`,
		`{"timeSpentSeconds":60,"comment":null,"comment_markdown":"a"}`, `{"timeSpentSeconds":60,"comment_markdown":null}`,
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			input := parsedInput(t, "--json-input=-")
			if _, _, err := input.compose(t.Context(), fixedDeps(), command.Invocation{Stdin: strings.NewReader(raw)}, 28800); err == nil || cli.MapError(err).Code != cli.CodeFlagValueInvalid {
				t.Fatalf("unsafe worklog input accepted or mistyped: %s %v", raw, err)
			}
		})
	}
}

func TestWorklogInputAliasesAndConflicts(t *testing.T) {
	t.Parallel()
	for _, alias := range []string{"from-json", "body-adf"} {
		input := parsedInput(t, "--"+alias+"=-")
		req, route, err := input.compose(t.Context(), fixedDeps(), command.Invocation{Stdin: strings.NewReader(`{"timeSpentSeconds":60,"comment":` + nativeComment + `}`)}, 28800)
		if err != nil || route != command.RouteADF || string(req.Comment) != nativeComment {
			t.Fatalf("native alias %s did not reach whole-payload input: %+v %s %v", alias, req, route, err)
		}
	}
	input := parsedInput(t, "--comment-markdown=investigated", "--time-spent=1m")
	req, route, err := input.compose(t.Context(), fixedDeps(), command.Invocation{}, 28800)
	if err != nil || route != command.RouteMarkdown || !strings.Contains(string(req.Comment), "investigated") {
		t.Fatalf("legacy comment flag lost: %+v %q %v", req, route, err)
	}
	for _, args := range [][]string{
		{"--json-input=-", "--markdown=a"},
		{"--json-input=-", "--comment-markdown=a"},
		{"--from-json=-", "--markdown-file=-"},
		{"--markdown=a", "--markdown-file=-"},
		{"--comment-markdown=a", "--markdown=b"},
		{"--from-json=a", "--json-input=b"},
	} {
		var input inputFlags
		input.register(&cobra.Command{Use: "add"})
		if err := input.cmd.ParseFlags(args); err != nil {
			t.Fatal(err)
		}
		if err := input.validate(); err == nil || cli.MapError(err).Code != cli.CodeFlagConflict {
			t.Fatalf("ambiguous routes accepted: %v %v", args, err)
		}
	}
}

func TestWorklogInputCanceledAndTerminalStdin(t *testing.T) {
	t.Parallel()
	input := parsedInput(t, "--json-input=-")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := input.compose(ctx, fixedDeps(), command.Invocation{Stdin: strings.NewReader(`{"timeSpentSeconds":60}`)}, 28800); !strings.Contains(cli.MapError(err).Code, "canceled") {
		t.Fatalf("canceled input was consumed: %v", err)
	}
	if _, _, err := input.compose(t.Context(), fixedDeps(), command.Invocation{StdinIsTerminal: true}, 28800); err == nil {
		t.Fatal("terminal stdin accepted without an input pipe")
	}
}
