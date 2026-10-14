package root

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/gechr/clog"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/output"
)

// testDeps returns fixed non-probing deps: auto resolves to json without
// touching the test process's real environment, and restores the default
// clog logger the program construction clobbers.
func testDeps(t *testing.T) *Deps {
	t.Helper()
	orig := clog.Default()
	t.Cleanup(func() { clog.SetDefault(orig) })
	return &Deps{
		JiraClient: func(ctx context.Context) (core.JiraClient, error) { return nil, nil },
		Detect: func() cli.Detection {
			return cli.Detection{Format: output.FormatJSON}
		},
	}
}

// No t.Parallel in this file: conductor configures clog's package globals
// on every construction, so root tests run serially and restore the
// default logger afterwards.

// runQuiet executes one invocation with counting deps and silenced
// streams, returning the exit code and how often the Jira constructor ran.
func runQuiet(t *testing.T, args []string) (exit, clientConstructions int) {
	t.Helper()
	orig := clog.Default()
	t.Cleanup(func() { clog.SetDefault(orig) })

	calls := 0
	deps := &Deps{
		JiraClient: func(ctx context.Context) (core.JiraClient, error) {
			calls++
			return nil, nil
		},
		// A fixed non-agent, non-TTY environment: auto resolves to json
		// without probing the test process's real stdout or env.
		Detect: func() cli.Detection {
			return cli.Detection{Format: output.FormatJSON}
		},
	}
	program, sink := newProgram(deps, io.Discard, io.Discard)
	return execute(t.Context(), program, sink, args), calls
}

// TestClientFreePathsConstructNothing pins the lazy-DI contract: help and
// a bare invocation must never construct a Jira client — snappiness for
// repeated agent calls depends on it.
func TestClientFreePathsConstructNothing(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "bare invocation", args: nil},
		{name: "help flag", args: []string{"--help"}},
		// The help COMMAND materializes lazily in cobra; the pre-dispatch
		// command check must not mistake it for an unknown verb.
		{name: "help command", args: []string{"help"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exit, constructions := runQuiet(t, tt.args)
			if exit != 0 {
				t.Errorf("exit = %d, want 0", exit)
			}
			if constructions != 0 {
				t.Errorf("jira client constructed %d times on a client-free path, want 0", constructions)
			}
		})
	}
}

// TestOutputSelectorConflictFailsLoud pins the resolution semantics end
// to end: disagreeing mode signals must fail the invocation with the
// validation exit, never silently last-win. The hidden aliases are
// functional (--json parses), so the failure comes from ResolveSelector,
// not from cobra rejecting an unknown flag.
func TestOutputSelectorConflictFailsLoud(t *testing.T) {
	exit, constructions := runQuiet(t, []string{"--json", "--plain"})
	if exit != 3 {
		t.Errorf("exit = %d, want the validation contract code 3", exit)
	}
	if constructions != 0 {
		t.Errorf("jira client constructed %d times on a failing resolution, want 0", constructions)
	}
}

// TestExitCodeMapperIsWired proves WithExitCode is live: an unknown flag
// is a validation failure (exit 3) under the taxonomy, where conductor's
// default mapper would flatten it to 1.
func TestExitCodeMapperIsWired(t *testing.T) {
	exit, constructions := runQuiet(t, []string{"--definitely-not-a-flag"})
	if exit != 3 {
		t.Errorf("exit = %d, want the validation contract code 3", exit)
	}
	if constructions != 0 {
		t.Errorf("jira client constructed %d times on a failing parse, want 0", constructions)
	}
}

// TestFailureEnvelopeReachesStdout pins R2 end-to-end through the real
// conductor program: a failing command in a machine mode must land the
// FULL error frame on stdout — an agent never parses stderr prose. The
// gap this pins: the exit boundary once rendered human-only, so machine
// callers got an exit code and an empty stdout.
func TestFailureEnvelopeReachesStdout(t *testing.T) {
	for _, format := range []string{"json", "compact"} {
		t.Run(format, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(testDeps(t), &stdout, &stderr)
			program.Root.AddCommand(&cobra.Command{
				Use: "boom",
				RunE: func(*cobra.Command, []string) error {
					return errors.New("synthetic failure")
				},
			})

			exit := execute(t.Context(), program, sink, []string{"boom", "--output=" + format})
			if exit != cli.ExitValidation {
				t.Fatalf("exit = %d, want %d", exit, cli.ExitValidation)
			}

			var frame struct {
				OK   bool `json:"ok"`
				Meta struct {
					Command  string `json:"command"`
					ExitCode *int   `json:"exit_code"`
				} `json:"meta"`
				Errors []struct {
					Code string `json:"code"`
				} `json:"errors"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil {
				t.Fatalf("stdout is not a frame: %v\nstdout: %q", err, stdout.String())
			}
			if frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "validation_failed" {
				t.Errorf("frame = %+v", frame)
			}
			if frame.Meta.Command != "boom" {
				t.Errorf("meta.command = %q, want boom", frame.Meta.Command)
			}
			if frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != cli.ExitValidation {
				t.Errorf("meta.exit_code = %v, want %d", frame.Meta.ExitCode, cli.ExitValidation)
			}
		})
	}
}

// errorFrame is the test-side decode of a failure envelope.
type errorFrame struct {
	OK   bool `json:"ok"`
	Meta struct {
		Command  string `json:"command"`
		ExitCode *int   `json:"exit_code"`
	} `json:"meta"`
	Errors []struct {
		Code        string   `json:"code"`
		Message     string   `json:"message"`
		Hint        string   `json:"hint"`
		Field       string   `json:"field"`
		Suggestions []string `json:"suggestions"`
	} `json:"errors"`
}

// runFrame executes args against a fresh program and decodes the failure
// frame from stdout.
func runFrame(t *testing.T, args []string) (int, errorFrame, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	program, sink := newProgram(testDeps(t), &stdout, &stderr)
	exit := execute(t.Context(), program, sink, args)
	var frame errorFrame
	if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil {
		t.Fatalf("stdout is not a frame: %v\nstdout: %q", err, stdout.String())
	}
	return exit, frame, stdout.String()
}

// TestPreParseFailuresEmitMachineFrames pins the uniform-grammar rule at
// the exact failures that fire BEFORE cobra parses or resolves anything:
// an unknown flag, an unresolvable selector, an arity miss. The selector
// is resolved from the raw argv, so every failure the binary can produce
// reaches the machine envelope — "a pre-resolution failure has no known
// format" was the premise that made typos unparseable to agents.
func TestPreParseFailuresEmitMachineFrames(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantCode  string
		wantField string
	}{
		{name: "unknown flag", args: []string{"--output=json", "--definitely-not-a-flag"}, wantCode: "flag_unknown", wantField: "definitely-not-a-flag"},
		{name: "selector conflict", args: []string{"--json", "--plain"}, wantCode: "validation_failed"},
		{name: "invalid selector value", args: []string{"--output=bogus"}, wantCode: "validation_failed"},
		{name: "arity failure", args: []string{"version", "extra", "--output=json"}, wantCode: "arg_count_invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exit, frame, raw := runFrame(t, tt.args)
			if exit != cli.ExitValidation {
				t.Fatalf("exit = %d, want %d", exit, cli.ExitValidation)
			}
			if frame.OK || len(frame.Errors) != 1 {
				t.Fatalf("frame = %s", raw)
			}
			if frame.Errors[0].Code != tt.wantCode {
				t.Errorf("code = %q, want %q", frame.Errors[0].Code, tt.wantCode)
			}
			if frame.Errors[0].Hint == "" {
				t.Error("hint is empty; every code carries one")
			}
			if tt.wantField != "" && frame.Errors[0].Field != tt.wantField {
				t.Errorf("field = %q, want %q", frame.Errors[0].Field, tt.wantField)
			}
			if frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != exit {
				t.Errorf("meta.exit_code = %v, want %d", frame.Meta.ExitCode, exit)
			}
		})
	}
}

// TestUnknownCommandIsItself pins the 0-magic dispatch rule: a typo'd
// verb is reported as an unknown command with candidates — never as
// whatever flag cobra would have parsed first. The trailing-flag case is
// the reproduced failure: cobra parses flags before dispatch, so without
// the pre-dispatch check `verson --foo bar` blamed --foo.
func TestUnknownCommandIsItself(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "bare typo", args: []string{"verson", "--output=json"}},
		{name: "typo with trailing flag", args: []string{"verson", "--foo", "bar", "--output=json"}},
		// pflag answers an UNREGISTERED --help by aborting the parse with
		// ErrHelp before its unknown-flag tolerance applies, so a probe
		// missing that flag stopped scanning here and saw no command token
		// at all — demoting a typo'd verb to a bare arity failure that
		// named nothing.
		{name: "typo behind an explicit-valued help flag", args: []string{"--help=false", "verson", "--output=json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exit, frame, raw := runFrame(t, tt.args)
			if exit != cli.ExitValidation {
				t.Fatalf("exit = %d, want %d", exit, cli.ExitValidation)
			}
			if len(frame.Errors) != 1 || frame.Errors[0].Code != "command_unknown" {
				t.Fatalf("want one command_unknown error, got frame %s", raw)
			}
			e := frame.Errors[0]
			if e.Field != "verson" {
				t.Errorf("field = %q, want the offending token %q", e.Field, "verson")
			}
			if !slices.Contains(e.Suggestions, "version") {
				t.Errorf("suggestions = %v, want them to include %q", e.Suggestions, "version")
			}
		})
	}
}

// TestUnknownFlagCarriesSuggestions pins the machine-readable half of
// suggestion-on-error: a near-miss flag typo offers the real flag in the
// envelope's suggestions, and a flag borrowed from another Jira CLI gets
// the orientation code with this CLI's equivalent.
func TestUnknownFlagCarriesSuggestions(t *testing.T) {
	t.Run("near miss", func(t *testing.T) {
		_, frame, raw := runFrame(t, []string{"--outpt=json"})
		if len(frame.Errors) != 1 || frame.Errors[0].Code != "flag_unknown" {
			t.Fatalf("want one flag_unknown error, got frame %s", raw)
		}
		if !slices.Contains(frame.Errors[0].Suggestions, "--output") {
			t.Errorf("suggestions = %v, want them to include --output", frame.Errors[0].Suggestions)
		}
	})
	t.Run("foreign flag", func(t *testing.T) {
		_, frame, raw := runFrame(t, []string{"version", "--no-headers", "--output=json"})
		if len(frame.Errors) != 1 || frame.Errors[0].Code != "flag_foreign" {
			t.Fatalf("want one flag_foreign error, got frame %s", raw)
		}
		if !slices.Contains(frame.Errors[0].Suggestions, "--output=json") {
			t.Errorf("suggestions = %v, want them to include --output=json", frame.Errors[0].Suggestions)
		}
	})
}

// TestGroupParentStrayPositionalIsUnknownCommand pins the group-parent
// half of the dispatch guard: cobra only errors on unknown commands at
// the root — a pure group swallows a stray positional and renders help
// (exit 0), or dies on the parent's flag parse when a flag trails. A
// synthetic group stands in until the first real command group lands.
func TestGroupParentStrayPositionalIsUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	program, sink := newProgram(testDeps(t), &stdout, &stderr)
	group := &cobra.Command{Use: "issue", Short: "Synthetic group"}
	group.AddCommand(&cobra.Command{
		Use:   "view",
		Short: "Synthetic leaf",
		RunE:  func(*cobra.Command, []string) error { return nil },
	})
	program.Root.AddCommand(group)

	exit := execute(t.Context(), program, sink, []string{"issue", "veiw", "--output=json"})
	if exit != cli.ExitValidation {
		t.Fatalf("exit = %d, want %d", exit, cli.ExitValidation)
	}
	var frame errorFrame
	if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil {
		t.Fatalf("stdout is not a frame: %v\nstdout: %q", err, stdout.String())
	}
	if len(frame.Errors) != 1 || frame.Errors[0].Code != "command_unknown" {
		t.Fatalf("want one command_unknown error, got %q", stdout.String())
	}
	e := frame.Errors[0]
	if e.Field != "veiw" {
		t.Errorf("field = %q, want %q", e.Field, "veiw")
	}
	// The whole point of inheriting the suggestion distance onto group
	// parents: candidates come from the GROUP's children.
	if !slices.Contains(e.Suggestions, "view") {
		t.Errorf("suggestions = %v, want them to include %q", e.Suggestions, "view")
	}
}

// TestHumanModeFailureKeepsStdoutClean pins the other half of the seed:
// resolving early must not leak a JSON frame into human mode — a person
// on a TTY gets the stderr line and hint, and stdout stays empty.
func TestHumanModeFailureKeepsStdoutClean(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := testDeps(t)
	deps.Detect = func() cli.Detection {
		return cli.Detection{Format: output.FormatHuman, IsTTY: true}
	}
	program, sink := newProgram(deps, &stdout, &stderr)

	exit := execute(t.Context(), program, sink, []string{"verson"})
	if exit != cli.ExitValidation {
		t.Fatalf("exit = %d, want %d", exit, cli.ExitValidation)
	}
	if stdout.Len() != 0 {
		t.Errorf("human-mode failure wrote to stdout: %q", stdout.String())
	}
}

// TestHumanStatusCarriesNoLevelColumn pins the human-facing shape of
// stderr: severity is carried by the symbol, not by an "ERR"/"INF"
// column that makes a terminal CLI read like a server log. Asserted on
// the bytes clog actually emits, because clog exposes no way to read the
// configured parts back.
//
// The unknown-command path is used deliberately: it logs from the exit
// boundary AFTER cobra unwinds, so it also pins that the configuration
// happens at construction rather than in a run hook that this path never
// reaches.
func TestHumanStatusCarriesNoLevelColumn(t *testing.T) {
	stderr := captureStderr(t, func() {
		runQuiet(t, []string{"verson"})
	})

	if stderr == "" {
		t.Fatal("no status output captured — the assertion below would pass vacuously")
	}
	for _, label := range []string{"ERR ", "INF ", "WRN ", "HNT ", "DBG "} {
		if strings.Contains(stderr, label) {
			t.Errorf("status output still carries the %q level column:\n%s", label, stderr)
		}
	}
	if !strings.Contains(stderr, "❌") {
		t.Errorf("status output lost its severity symbol:\n%s", stderr)
	}
}

// TestPositionalAfterTerminatorIsStillNamed pins the code/message/hint
// triple on the one path the pre-dispatch check deliberately skips. A
// token after "--" is declared a non-command, so cobra's own Args
// validator is what finally rejects it — and that validator words its
// failure as "unknown command". Stamping the arity code on it left the
// code and hint claiming a miscount while the message named a command,
// the same contradiction the check exists to prevent.
func TestPositionalAfterTerminatorIsStillNamed(t *testing.T) {
	exit, frame, raw := runFrame(t, []string{"--output=json", "--", "verson"})
	if exit != cli.ExitValidation {
		t.Fatalf("exit = %d, want %d", exit, cli.ExitValidation)
	}
	if len(frame.Errors) != 1 || frame.Errors[0].Code != "command_unknown" {
		t.Fatalf("want one command_unknown error, got %s", raw)
	}
	e := frame.Errors[0]
	if e.Field != "verson" {
		t.Errorf("field = %q, want %q", e.Field, "verson")
	}
	if !slices.Contains(e.Suggestions, "version") {
		t.Errorf("suggestions = %v, want them to include %q", e.Suggestions, "version")
	}
}

// TestSuggestionsNeverEchoTheInput pins that no suggestion repeats what
// was typed. Cobra's SuggestionsFor matches on prefix as well as edit
// distance, so a token naming a real command — reachable once "--" makes
// it a positional — would otherwise be offered as the fix for itself:
// noise to a reader, and a loop to an agent following the recovery path.
func TestSuggestionsNeverEchoTheInput(t *testing.T) {
	_, frame, raw := runFrame(t, []string{"--output=json", "--", "version"})
	if len(frame.Errors) != 1 {
		t.Fatalf("want one error, got %s", raw)
	}
	if slices.Contains(frame.Errors[0].Suggestions, "version") {
		t.Errorf("suggestions = %v, want the typed token not offered back", frame.Errors[0].Suggestions)
	}
}

// TestHumanFailureOffersOneRouteForward pins the noise floor for human
// status: an error prints its message and at most ONE line of advice. A
// suggestion wins over the generic hint, because the hint exists to
// recover a reader who has no lead and a suggestion means they have one.
// The machine frame is asserted to still carry both, so this stays a
// rendering choice rather than a contract loss.
func TestHumanFailureOffersOneRouteForward(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantOnly string
	}{
		{name: "suggestion beats the hint", args: []string{"verson"}, wantOnly: "Did you mean version?"},
		{name: "hint stands alone when nothing is close", args: []string{"version", "--output"}, wantOnly: "Give the flag a value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stderr := captureStderr(t, func() { runQuiet(t, tt.args) })
			if hints := strings.Count(stderr, "💡"); hints != 1 {
				t.Errorf("got %d advice lines, want exactly 1:\n%s", hints, stderr)
			}
			if !strings.Contains(stderr, tt.wantOnly) {
				t.Errorf("advice line = %q, want it to carry %q", stderr, tt.wantOnly)
			}
		})
	}

	// The suppressed hint must still reach an agent.
	_, frame, raw := runFrame(t, []string{"--output=json", "verson"})
	if len(frame.Errors) != 1 || frame.Errors[0].Hint == "" || len(frame.Errors[0].Suggestions) == 0 {
		t.Errorf("machine frame lost hint or suggestions: %s", raw)
	}
}

// TestCompletionFlagsDoNotEatTheNextToken pins that clib's hidden
// completion flags cannot swallow the token after them. Both take a
// value, so pflag read a bare `--@shell --output=json` as "shell =
// --output=json": the selector vanished and the command answered in a
// mode nobody asked for, exit 0 and no warning.
//
// clib's own argv scanner already refuses a value starting with "--", so
// this pins the two parsers agreeing on one argv rather than a
// preference. The "=" form must keep working — that is what shells emit.
func TestCompletionFlagsDoNotEatTheNextToken(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "bare complete flag", args: []string{"--@complete", "--output=json", "version"}},
		{name: "bare shell flag", args: []string{"--@shell", "--output=json", "version"}},
		{name: "shell flag with its own value", args: []string{"--@shell=fish", "--output=json", "version"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Detection MUST answer with something other than json here.
			// With the usual json-detecting deps a swallowed --output=json
			// falls back to detection and produces a frame anyway, so the
			// assertion below cannot fail and the guard is decoration —
			// verified by removing the fix and watching this test still
			// pass.
			orig := clog.Default()
			t.Cleanup(func() { clog.SetDefault(orig) })
			deps := &Deps{
				JiraClient: func(ctx context.Context) (core.JiraClient, error) { return nil, nil },
				Detect: func() cli.Detection {
					return cli.Detection{Format: output.FormatCompact}
				},
			}
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(deps, &stdout, &stderr)
			if exit := execute(t.Context(), program, sink, tt.args); exit != 0 {
				t.Fatalf("exit = %d, want 0\nstdout: %s", exit, stdout.String())
			}
			// A swallowed selector leaves the mode unresolved, which
			// detection answers with bare data instead of the frame the
			// caller asked for.
			var frame map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil {
				t.Fatalf("stdout is not JSON: %v\nstdout: %q", err, stdout.String())
			}
			if _, ok := frame["ok"]; !ok {
				t.Errorf("--output=json was swallowed; got bare data: %s", stdout.String())
			}
		})
	}
}

// TestFilterNeverTouchesAnErrorFrame is the load-bearing --jq rule. An
// expression is written against a command's SUCCESS shape; run over an
// error frame it yields nothing useful and can swallow the very report
// the caller needs — including the report that the filter itself is
// wrong. 0.15 filtered failure envelopes and needed a "disarm" hack to
// stop exactly that; here the failure path never carries a filter, so
// there is nothing to disarm.
func TestFilterNeverTouchesAnErrorFrame(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "unknown command", args: []string{"verson", "--output=json", "--jq", ".data"}, want: "command_unknown"},
		{name: "unknown flag", args: []string{"version", "--nope", "--output=json", "--jq", ".data"}, want: "flag_unknown"},
		{name: "compact keeps the frame too", args: []string{"verson", "--output=compact", "--jq", ".data"}, want: "command_unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exit, frame, raw := runFrame(t, tt.args)
			if exit != cli.ExitValidation {
				t.Fatalf("exit = %d, want %d\nstdout: %s", exit, cli.ExitValidation, raw)
			}
			if len(frame.Errors) != 1 || frame.Errors[0].Code != tt.want {
				t.Fatalf("want one %s in an unfiltered frame, got %s", tt.want, raw)
			}
		})
	}
}

// TestFilterResolvesAgainstTheSelector pins how --jq and --output settle
// together: an explicit human mode is a conflict rather than a silent
// override, and an unset selector resolves to json — the only shape an
// expression can be written against.
func TestFilterResolvesAgainstTheSelector(t *testing.T) {
	t.Run("explicit human is a loud conflict", func(t *testing.T) {
		// Asserting the CODE, not only the exit: every input-validation
		// code shares ExitValidation, so an exit-only check stayed green
		// when the code was swapped for an unrelated one.
		var stdout, stderr bytes.Buffer
		program, sink := newProgram(testDeps(t), &stdout, &stderr)
		exit := execute(t.Context(), program, sink, []string{"version", "--output=human", "--jq", ".data"})
		if exit != cli.ExitValidation {
			t.Fatalf("exit = %d, want %d", exit, cli.ExitValidation)
		}
		// Human mode writes no envelope to stdout, so the code is read
		// off the typed error the resolver returns.
		fs := pflag.NewFlagSet("probe", pflag.ContinueOnError)
		fs.String(cli.FlagJQ, "", "")
		fs.StringArray(cli.FlagJQArg, nil, "")
		fs.StringArray(cli.FlagJQArgJSON, nil, "")
		if err := fs.Parse([]string{"--" + cli.FlagJQ + "=.data"}); err != nil {
			t.Fatal(err)
		}
		_, _, err := resolveFilter(fs, cli.OutputHuman)
		var input *cli.InputError
		if !errors.As(err, &input) {
			t.Fatalf("err = %v, want a *cli.InputError", err)
		}
		if input.Code != cli.CodeJQOutputConflict {
			t.Errorf("code = %q, want %q", input.Code, cli.CodeJQOutputConflict)
		}
	})
	t.Run("a bad expression fails before the command runs", func(t *testing.T) {
		exit, frame, raw := runFrame(t, []string{"version", "--output=json", "--jq", ".data.["})
		if exit != cli.ExitValidation {
			t.Fatalf("exit = %d, want %d", exit, cli.ExitValidation)
		}
		if len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeJQExpressionInvalid {
			t.Errorf("want %s, got %s", cli.CodeJQExpressionInvalid, raw)
		}
	})
	t.Run("a shape mismatch is its own code", func(t *testing.T) {
		exit, frame, raw := runFrame(t, []string{"version", "--output=json", "--jq", ".data.nope[]"})
		if exit != cli.ExitValidation {
			t.Fatalf("exit = %d, want %d", exit, cli.ExitValidation)
		}
		if len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeJQEvalFailed {
			t.Errorf("want %s, got %s", cli.CodeJQEvalFailed, raw)
		}
	})
}

// TestFilterAppliesToWhatTheModeWouldPrint pins that a filter sees the
// document its mode advertises — the whole frame under json, bare data
// under compact — so an expression matches what --output documents
// rather than a shape the caller never asked for.
func TestFilterAppliesToWhatTheModeWouldPrint(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "json filters the frame", args: []string{"version", "--output=json", "--jq", ".meta.command"}, want: "version\n"},
		{name: "compact filters bare data", args: []string{"version", "--output=compact", "--jq", "has(\"go_version\")"}, want: "true\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(testDeps(t), &stdout, &stderr)
			if exit := execute(t.Context(), program, sink, tt.args); exit != 0 {
				t.Fatalf("exit = %d, want 0\nstdout: %s", exit, stdout.String())
			}
			if stdout.String() != tt.want {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.want)
			}
		})
	}
}

// TestFilterPinsAutoDeterministically is the contract that keeps a
// filter's meaning independent of the environment it runs in. `auto`
// normally asks detection, which answers compact under an agent and json
// on a pipe — so without this pin, the SAME command line with the SAME
// filter would return data in one setting and null in another, with no
// error either way. That is the failure AWS shipped with `--output text`
// changing `--query` semantics, and it cannot be fixed after the fact.
//
// Detection here deliberately answers compact, so the test fails if the
// selector ever falls back to it.
func TestFilterPinsAutoDeterministically(t *testing.T) {
	orig := clog.Default()
	t.Cleanup(func() { clog.SetDefault(orig) })
	deps := &Deps{
		JiraClient: func(ctx context.Context) (core.JiraClient, error) { return nil, nil },
		Detect: func() cli.Detection {
			return cli.Detection{Format: output.FormatCompact}
		},
	}
	var stdout, stderr bytes.Buffer
	program, sink := newProgram(deps, &stdout, &stderr)
	// No --output at all: the selector is unresolved auto.
	if exit := execute(t.Context(), program, sink, []string{"version", "--jq", ".meta.command"}); exit != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s", exit, stdout.String())
	}
	// .meta exists only on the full frame, so a compact fallback yields
	// null here rather than the command name.
	if got := stdout.String(); got != "version\n" {
		t.Errorf("stdout = %q, want %q — --jq must pin auto to json regardless of detection", got, "version\n")
	}
}

// TestFilterSupportsFormatters pins the @-formatters agents actually
// reach for. @tsv is the reason a tabular output mode was rejected: it
// gives caller-chosen, caller-ordered columns without a fifth --output
// value that would have nothing sensible to say about an error frame.
// @json is the escape hatch for values carrying newlines or tabs, which
// Jira descriptions and comments routinely do.
func TestFilterSupportsFormatters(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "tsv gives ordered columns", expr: "[.data.platform.os, .data.platform.arch] | @tsv", want: runtime.GOOS + "\t" + runtime.GOARCH + "\n"},
		{name: "json re-escapes a raw string", expr: ".data.platform.os | @json", want: "\"" + runtime.GOOS + "\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(testDeps(t), &stdout, &stderr)
			if exit := execute(t.Context(), program, sink, []string{"version", "--output=json", "--jq", tt.expr}); exit != 0 {
				t.Fatalf("exit = %d, want 0\nstdout: %s", exit, stdout.String())
			}
			if stdout.String() != tt.want {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.want)
			}
		})
	}
}

// TestFilterVariablesArePassedAsData is the reason --jq-arg exists. A
// caller filtering on Jira-authored text would otherwise splice it into
// the expression, where a quote, a paren or a backslash stops being
// content and becomes program — changing what the filter means, or
// breaking it, depending on the data. Bound values are handed to the
// engine as data and can never be parsed as jq source.
func TestFilterVariablesArePassedAsData(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "plain string binding",
			args: []string{"--jq", "$want", "--jq-arg", "want=hello"},
			want: "hello\n",
		},
		{
			// Every character here would end or redirect the program if it
			// were spliced into the expression text.
			name: "value that is itself jq syntax stays inert",
			args: []string{"--jq", "$want", "--jq-arg", `want=he said "boom") | .x`},
			want: "he said \"boom\") | .x\n",
		},
		{
			name: "only the first = splits, so values may contain more",
			args: []string{"--jq", "$kv", "--jq-arg", "kv=a=b=c"},
			want: "a=b=c\n",
		},
		{
			name: "argjson decodes into a real value",
			args: []string{"--jq", "$n + 1", "--jq-argjson", "n=41"},
			want: "42\n",
		},
		{
			name: "bound value drives a select",
			args: []string{"--jq", "select(.data.platform.os == $os) | .meta.command", "--jq-arg", "os=" + runtime.GOOS},
			want: "version\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(testDeps(t), &stdout, &stderr)
			args := append([]string{"version", "--output=json"}, tt.args...)
			if exit := execute(t.Context(), program, sink, args); exit != 0 {
				t.Fatalf("exit = %d, want 0\nstdout: %s", exit, stdout.String())
			}
			if stdout.String() != tt.want {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.want)
			}
		})
	}
}

// TestFilterVariableFailures pins that a malformed binding is reported
// as the caller's mistake rather than surfacing as a confusing jq
// compile error, and that a duplicate is rejected instead of one of two
// deliberate bindings being silently dropped.
func TestFilterVariableFailures(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode string
	}{
		{name: "no equals sign", args: []string{"--jq", ".", "--jq-arg", "badpair"}, wantCode: cli.CodeFlagValueInvalid},
		{name: "empty name", args: []string{"--jq", ".", "--jq-arg", "=value"}, wantCode: cli.CodeFlagValueInvalid},
		{name: "argjson given non-json", args: []string{"--jq", ".", "--jq-argjson", "n=notjson"}, wantCode: cli.CodeFlagValueInvalid},
		{name: "duplicate name", args: []string{"--jq", ".", "--jq-arg", "a=1", "--jq-arg", "a=2"}, wantCode: cli.CodeFlagValueInvalid},
		{name: "duplicate across both flags", args: []string{"--jq", ".", "--jq-arg", "a=1", "--jq-argjson", "a=2"}, wantCode: cli.CodeFlagValueInvalid},
		{name: "binding with no expression to bind into", args: []string{"--jq-arg", "a=1"}, wantCode: cli.CodeFlagNotApplicable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"version", "--output=json"}, tt.args...)
			exit, frame, raw := runFrame(t, args)
			if exit != cli.ExitValidation {
				t.Fatalf("exit = %d, want %d\nstdout: %s", exit, cli.ExitValidation, raw)
			}
			if len(frame.Errors) != 1 || frame.Errors[0].Code != tt.wantCode {
				t.Errorf("want one %s, got %s", tt.wantCode, raw)
			}
		})
	}
}

// TestFilterVariableNamesAreValidatedHere pins that a bad binding NAME
// is blamed on the flag that carried it. gojq accepts only
// [A-Za-z_][A-Za-z0-9_]*; passed through, the compile failure surfaced as
// jq_expression_invalid against --jq, telling the caller to fix an
// expression that was correct while the real mistake sat in --jq-arg.
func TestFilterVariableNamesAreValidatedHere(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantCode  string
		wantField string
	}{
		{name: "hyphen is not a jq identifier", args: []string{"--jq", ".", "--jq-arg", "bad-name=x"}, wantCode: cli.CodeFlagValueInvalid, wantField: cli.FlagJQArg},
		{name: "leading digit is not a jq identifier", args: []string{"--jq", ".", "--jq-arg", "1n=x"}, wantCode: cli.CodeFlagValueInvalid, wantField: cli.FlagJQArg},
		{name: "dollar belongs to the syntax, not the name", args: []string{"--jq", ".", "--jq-arg", "$n=x"}, wantCode: cli.CodeFlagValueInvalid, wantField: cli.FlagJQArg},
		{name: "argjson names are held to the same rule", args: []string{"--jq", ".", "--jq-argjson", "bad-name=1"}, wantCode: cli.CodeFlagValueInvalid, wantField: cli.FlagJQArgJSON},
		// Field must name the flag actually passed, not a fixed default.
		{name: "argjson alone reports argjson", args: []string{"--jq-argjson", "n=1"}, wantCode: cli.CodeFlagNotApplicable, wantField: cli.FlagJQArgJSON},
		{name: "arg alone reports arg", args: []string{"--jq-arg", "n=1"}, wantCode: cli.CodeFlagNotApplicable, wantField: cli.FlagJQArg},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"version", "--output=json"}, tt.args...)
			exit, frame, raw := runFrame(t, args)
			if exit != cli.ExitValidation {
				t.Fatalf("exit = %d, want %d\nstdout: %s", exit, cli.ExitValidation, raw)
			}
			if len(frame.Errors) != 1 {
				t.Fatalf("want one error, got %s", raw)
			}
			if frame.Errors[0].Code != tt.wantCode {
				t.Errorf("code = %q, want %q", frame.Errors[0].Code, tt.wantCode)
			}
			if frame.Errors[0].Field != tt.wantField {
				t.Errorf("field = %q, want %q — it must name the flag the caller passed", frame.Errors[0].Field, tt.wantField)
			}
		})
	}
}

// TestArgValidatorFailuresKeepTheirOwnClass pins that only a NoArgs
// rejection is reported as an unknown command. Keying that off "this
// command has subcommands" relabelled every other validator's failure —
// ExactArgs, RangeArgs, OnlyValidArgs — as a bad verb, telling the
// caller to fix a command name that was never wrong.
func TestArgValidatorFailuresKeepTheirOwnClass(t *testing.T) {
	tests := []struct {
		name     string
		args     cobra.PositionalArgs
		give     []string
		wantCode string
	}{
		{name: "NoArgs on a parent means unknown subcommand", args: cobra.NoArgs, give: []string{"nope"}, wantCode: "command_unknown"},
		{name: "ExactArgs miscount stays an arity failure", args: cobra.ExactArgs(2), give: []string{"only-one"}, wantCode: "arg_count_invalid"},
		{name: "RangeArgs miscount stays an arity failure", args: cobra.RangeArgs(2, 3), give: []string{"one"}, wantCode: "arg_count_invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(testDeps(t), &stdout, &stderr)
			parent := &cobra.Command{Use: "grp", Short: "a group", Args: tt.args, RunE: func(*cobra.Command, []string) error { return nil }}
			parent.AddCommand(&cobra.Command{Use: "child", Short: "a child", RunE: func(*cobra.Command, []string) error { return nil }})
			program.Root.AddCommand(parent)
			typeArgErrors(program.Root)

			args := append([]string{"grp", "--output=json", "--"}, tt.give...)
			exit := execute(t.Context(), program, sink, args)
			if exit != cli.ExitValidation {
				t.Fatalf("exit = %d, want %d\nstdout: %s", exit, cli.ExitValidation, stdout.String())
			}
			var frame errorFrame
			if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil {
				t.Fatalf("stdout is not a frame: %v\n%s", err, stdout.String())
			}
			if len(frame.Errors) != 1 || frame.Errors[0].Code != tt.wantCode {
				t.Errorf("want %s, got %s", tt.wantCode, stdout.String())
			}
		})
	}
}

// TestBareInvocationAnswersByMode verifies native human help and nonempty
// machine discovery through the root's registered output contract.
func TestBareInvocationAnswersByMode(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantHelp bool
	}{
		{name: "human mode explains itself", args: []string{"--output=human"}, wantHelp: true},
		{name: "json mode discovers commands", args: []string{"--output=json"}},
		{name: "compact mode discovers commands", args: []string{"--output=compact"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(testDeps(t), &stdout, &stderr)
			if exit := execute(t.Context(), program, sink, tt.args); exit != 0 {
				t.Fatalf("exit = %d, want 0", exit)
			}
			got := stderr.String()
			if !tt.wantHelp {
				if got != "" {
					t.Fatalf("machine discovery wrote stderr: %q", got)
				}
				payload := stdout.Bytes()
				if tt.args[0] == "--output=json" {
					var frame struct {
						OK   bool            `json:"ok"`
						Data json.RawMessage `json:"data"`
						Meta struct {
							Command string `json:"command"`
						} `json:"meta"`
					}
					if err := json.Unmarshal(payload, &frame); err != nil || !frame.OK || frame.Meta.Command != "root" {
						t.Fatal("bare invocation lost its shared success frame")
					}
					payload = frame.Data
				}
				var out struct {
					Schema struct {
						Path     string            `json:"path"`
						Children []json.RawMessage `json:"children"`
					} `json:"schema"`
				}
				if err := json.Unmarshal(payload, &out); err != nil || out.Schema.Path != "jira" || len(out.Schema.Children) == 0 {
					t.Fatal("bare invocation emitted no usable discovery artifact")
				}
				return
			}
			if stdout.Len() != 0 {
				t.Fatal("native human help reached machine stdout")
			}
			if got == "" {
				t.Fatal("human mode wrote nothing — a bare invocation must say what the tool is")
			}
			for _, want := range []string{"jira", "version", "completion"} {
				if !strings.Contains(got, want) {
					t.Errorf("help does not mention %q:\n%s", want, got)
				}
			}
		})
	}
}
