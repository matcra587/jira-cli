package cli

import (
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/matcra587/jira-cli/internal/output"
)

// TestDecideFormatTable pins the auto-resolution rule: agent beats TTY
// beats neither. The probes are environmental; the table is not.
func TestDecideFormatTable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		agent, tty bool
		want       output.Format
	}{
		{name: "agent on tty", agent: true, tty: true, want: output.FormatCompact},
		{name: "agent piped", agent: true, tty: false, want: output.FormatCompact},
		{name: "piped", agent: false, tty: false, want: output.FormatJSON},
		{name: "interactive", agent: false, tty: true, want: output.FormatHuman},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := decideFormat(tt.agent, tt.tty); got != tt.want {
				t.Errorf("decideFormat(%v, %v) = %v, want %v", tt.agent, tt.tty, got, tt.want)
			}
		})
	}
}

// TestResolveOutputModeUnknownPanics pins the loud posture: only
// ParseOutputMode mints modes, so an unknown one is a programming bug.
func TestResolveOutputModeUnknownPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("unknown mode resolved silently")
		}
	}()
	ResolveOutputMode(OutputMode("bogus"), nil)
}

// TestParseOutputMode pins the raw-value contract: an empty string is the
// flag's untouched default and resolves the same as the literal "auto";
// every other value is a specific mode or a loud, named error.
func TestParseOutputMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		want    OutputMode
		wantErr bool
	}{
		{name: "empty string is the untouched default", raw: "", want: OutputAuto},
		{name: "auto", raw: "auto", want: OutputAuto},
		{name: "human", raw: "human", want: OutputHuman},
		{name: "json", raw: "json", want: OutputJSON},
		{name: "compact", raw: "compact", want: OutputCompact},
		{name: "unrecognized value rejected loudly", raw: "yaml", wantErr: true},
		// Case matters: ParseOutputMode does not normalize case, so a
		// capitalized value that a human might type is rejected, not
		// silently folded to the lowercase mode.
		{name: "wrong case rejected, not folded", raw: "JSON", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseOutputMode(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseOutputMode(%q) = %q, nil; want an error", tt.raw, got)
				}
				if !strings.Contains(err.Error(), "invalid --output value") || !strings.Contains(err.Error(), tt.raw) {
					t.Errorf("error = %q, want it to name --output and the rejected value %q", err.Error(), tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseOutputMode(%q): unexpected error %v", tt.raw, err)
			}
			if got != tt.want {
				t.Errorf("ParseOutputMode(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

// newOutputFlagSet mirrors command.AddOutputFlags's registration exactly
// (--output plus every hidden alias bool), without importing command —
// this package cannot: command imports cli, and an internal test file
// (package cli, not cli_test) importing command back would be a cycle.
func newOutputFlagSet() *pflag.FlagSet {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String(FlagOutput, string(OutputAuto), "")
	for _, a := range OutputAliases {
		fs.Bool(a.Flag, false, "")
	}
	return fs
}

// TestResolveSelector pins the single resolution point: how the canonical
// flag and its hidden aliases fold into one mode, including the
// documented edge cases (agreement is fine, an explicit-false alias is
// no signal at all, and disagreement names only mode values).
func TestResolveSelector(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		set     map[string]string
		want    OutputMode
		wantErr string // substring; empty means no error expected
	}{
		{name: "nothing set resolves to auto", set: nil, want: OutputAuto},
		{name: "canonical flag alone", set: map[string]string{FlagOutput: "json"}, want: OutputJSON},
		{name: "alias alone", set: map[string]string{FlagAliasJSON: "true"}, want: OutputJSON},
		{name: "human alias", set: map[string]string{FlagAliasHuman: "true"}, want: OutputHuman},
		{name: "plain alias means human", set: map[string]string{FlagAliasPlain: "true"}, want: OutputHuman},
		{name: "compact alias", set: map[string]string{FlagAliasCompact: "true"}, want: OutputCompact},
		{
			name: "canonical and agreeing alias both set is fine",
			set:  map[string]string{FlagOutput: "json", FlagAliasJSON: "true"},
			want: OutputJSON,
		},
		{
			// Documented oddity, pinned here: --json=false is explicit
			// state (fs.Changed reports true) but carries no signal —
			// ResolveSelector only reads the alias when its bool is true.
			// This is NOT "force away from json"; it is indistinguishable
			// from --json never having been mentioned at all.
			name: "alias explicitly set false is no signal — pins current behavior",
			set:  map[string]string{FlagAliasJSON: "false"},
			want: OutputAuto,
		},
		{
			name:    "canonical and disagreeing alias conflict",
			set:     map[string]string{FlagOutput: "human", FlagAliasJSON: "true"},
			wantErr: "conflicting output modes requested (human and json)",
		},
		{
			name:    "two disagreeing aliases conflict",
			set:     map[string]string{FlagAliasJSON: "true", FlagAliasPlain: "true"},
			wantErr: "conflicting output modes requested (json and human)",
		},
		{
			name:    "invalid canonical value propagates ParseOutputMode's error",
			set:     map[string]string{FlagOutput: "yaml"},
			wantErr: "invalid --output value",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fs := newOutputFlagSet()
			for flag, v := range tt.set {
				if err := fs.Set(flag, v); err != nil {
					t.Fatalf("fs.Set(%q, %q): %v", flag, v, err)
				}
			}
			got, err := ResolveSelector(fs)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ResolveSelector() = %q, nil; want an error containing %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveSelector(): unexpected error %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveSelector() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestResolveSelectorConflictNamesNoHiddenAlias pins the doc comment
// literally: a conflict error must never mention a hidden alias flag
// name (--json, --plain, --human, --compact) — an introspecting agent
// was never shown them in schema or help, so an error referencing one
// would point at a flag it cannot find.
func TestResolveSelectorConflictNamesNoHiddenAlias(t *testing.T) {
	t.Parallel()
	fs := newOutputFlagSet()
	if err := fs.Set(FlagAliasJSON, "true"); err != nil {
		t.Fatal(err)
	}
	if err := fs.Set(FlagAliasPlain, "true"); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveSelector(fs)
	if err == nil {
		t.Fatal("expected a conflict error")
	}
	for _, hidden := range []string{"--" + FlagAliasJSON, "--" + FlagAliasPlain, "--" + FlagAliasHuman, "--" + FlagAliasCompact} {
		if strings.Contains(err.Error(), hidden) {
			t.Errorf("error %q names hidden alias %q", err.Error(), hidden)
		}
	}
	if !strings.Contains(err.Error(), "--output") {
		t.Errorf("error %q does not name the canonical --output flag", err.Error())
	}
}

// agentEnvVars lists every environment variable agent.Is/agent.Detect
// consult. Tests that pin Detect's agent branch must clear all of them
// first — this test suite may itself be running under an AI coding
// agent (CLAUDECODE, AI_AGENT, ...), and a real value already sitting in
// the environment would make the "no agent" case flaky depending on who
// runs it.
var agentEnvVars = []string{
	"AGENT", "AI_AGENT",
	"CLAUDECODE", "CLINE_ACTIVE", "CODEX_SANDBOX", "CURSOR_AGENT",
	"GEMINI_CLI", "OPENCODE", "REPL_ID",
}

// clearAgentEnv neutralizes every agent-detection variable for the
// duration of the test, via t.Setenv (auto-restored). t.Setenv forbids
// t.Parallel, so callers must not parallelize.
func clearAgentEnv(t *testing.T) {
	t.Helper()
	for _, v := range agentEnvVars {
		t.Setenv(v, "")
	}
}

// TestDetectNoAgentNonTTY pins Detect's baseline: a nil *os.File is never
// a terminal (x/terminal's documented nil handling), so with no agent
// signal present the detection is a clean non-interactive, non-agent
// json resolution.
func TestDetectNoAgentNonTTY(t *testing.T) {
	// No t.Parallel: uses t.Setenv.
	clearAgentEnv(t)
	got := Detect(nil)
	want := Detection{Format: output.FormatJSON, IsTTY: false, Agent: false, AgentName: ""}
	if got != want {
		t.Errorf("Detect(nil) = %+v, want %+v", got, want)
	}
}

// TestDetectAgentMarkerNamesVendor pins the marker-variable path: a
// vendor-specific marker (CLAUDECODE) both flips Agent true and names
// the vendor, and the resolved format is compact regardless of TTY state.
func TestDetectAgentMarkerNamesVendor(t *testing.T) {
	// No t.Parallel: uses t.Setenv.
	clearAgentEnv(t)
	t.Setenv("CLAUDECODE", "1")
	got := Detect(nil)
	want := Detection{Format: output.FormatCompact, IsTTY: false, Agent: true, AgentName: "claude"}
	if got != want {
		t.Errorf("Detect(nil) = %+v, want %+v", got, want)
	}
}

// TestDetectBareAgentOptInNamesNothing pins the documented asymmetry: a
// bare `AGENT=1` opts in to agent behavior (Agent true, compact format)
// but names no vendor — AgentName can be empty even when Agent is true.
func TestDetectBareAgentOptInNamesNothing(t *testing.T) {
	// No t.Parallel: uses t.Setenv.
	clearAgentEnv(t)
	t.Setenv("AGENT", "1")
	got := Detect(nil)
	want := Detection{Format: output.FormatCompact, IsTTY: false, Agent: true, AgentName: ""}
	if got != want {
		t.Errorf("Detect(nil) = %+v, want %+v", got, want)
	}
}

// TestDetectFalsyAgentIsExplicitOptOut pins x/agent's escape hatch as
// Detect actually surfaces it: AGENT=0 opts OUT even when a vendor
// marker (CLAUDECODE) is also present — the falsy standard var wins
// over the marker, so Agent is false and the format falls through to
// json (still non-TTY here).
func TestDetectFalsyAgentIsExplicitOptOut(t *testing.T) {
	// No t.Parallel: uses t.Setenv.
	clearAgentEnv(t)
	t.Setenv("AGENT", "0")
	t.Setenv("CLAUDECODE", "1")
	got := Detect(nil)
	want := Detection{Format: output.FormatJSON, IsTTY: false, Agent: false, AgentName: ""}
	if got != want {
		t.Errorf("Detect(nil) = %+v, want %+v", got, want)
	}
}

// TestFormatContextRoundTrip pins WithFormat/FormatFromContext: a format
// seeded onto a context comes back out unchanged, and a context that
// never went through WithFormat reports ok=false rather than a
// plausible-looking zero value — root's PersistentPreRunE wiring must be
// provably absent, never silently defaulted.
func TestFormatContextRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := WithFormat(t.Context(), output.FormatCompact)
	got, ok := FormatFromContext(ctx)
	if !ok {
		t.Fatal("FormatFromContext reported ok=false after WithFormat")
	}
	if got != output.FormatCompact {
		t.Errorf("FormatFromContext = %q, want %q", got, output.FormatCompact)
	}

	_, ok = FormatFromContext(t.Context())
	if ok {
		t.Error("FormatFromContext reported ok=true on a context that never carried a format")
	}
}
