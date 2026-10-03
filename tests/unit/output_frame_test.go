// Package unit holds black-box tests of cross-package behavior that
// need no server: here, the output core's frame contract (design R2)
// and the selector resolution semantics.
package unit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// versionData is a stand-in Output struct: typed, snake_case-tagged,
// the shape any real operation puts in the data slot.
type versionData struct {
	Version string `json:"version"`
}

func assembleSuccess() output.Result {
	return output.Assemble(output.Inputs{
		Op:        "version",
		Data:      versionData{Version: "0.16.0"},
		Now:       time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),
		RequestID: "deadbeefdeadbeefdeadbeefdeadbeef",
	})
}

func assembleFailure() output.Result {
	return output.Assemble(output.Inputs{
		Op: "issue.view",
		Errors: []envelope.Error{{
			Code:    "jira_not_found",
			Message: "issue does not exist",
			Hint:    "Check the identifier — the site answered, but nothing lives at it.",
		}},
		ExitCode:  2,
		Now:       time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),
		RequestID: "deadbeefdeadbeefdeadbeefdeadbeef",
	})
}

// frame is the decode target for structural assertions — never
// string-match whole JSON blobs.
type frame struct {
	OK   *bool `json:"ok"`
	Meta *struct {
		Command         string `json:"command"`
		ContractVersion string `json:"contract_version"`
		Timestamp       string `json:"timestamp"`
		RequestID       string `json:"request_id"`
		ExitCode        *int   `json:"exit_code"`
	} `json:"meta"`
	Data     json.RawMessage  `json:"data"`
	Errors   []envelope.Error `json:"errors"`
	Warnings []map[string]any `json:"warnings"`
}

func decodeFrame(t *testing.T, raw []byte) frame {
	t.Helper()
	var f frame
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("unmarshal frame: %v\nraw: %s", err, raw)
	}
	return f
}

func render(t *testing.T, f output.Format, res output.Result) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := output.Render(t.Context(), &buf, f, res); err != nil {
		t.Fatalf("Render(t.Context(), %s) error = %v", f, err)
	}
	return buf.Bytes()
}

func TestJSONSuccessEmitsFullFrame(t *testing.T) {
	t.Parallel()
	raw := render(t, output.FormatJSON, assembleSuccess())

	if !bytes.HasSuffix(raw, []byte("\n")) || bytes.Count(raw, []byte("\n")) != 1 {
		t.Errorf("json envelope must be exactly one newline-terminated line, got %q", raw)
	}
	f := decodeFrame(t, raw)
	if f.OK == nil || !*f.OK {
		t.Error("ok must be present and true on success")
	}
	if f.Meta == nil {
		t.Fatal("meta missing from the full frame")
	}
	if f.Meta.Command != "version" {
		t.Errorf("meta.command = %q, want %q", f.Meta.Command, "version")
	}
	if f.Meta.ContractVersion != envelope.ContractVersion {
		t.Errorf("meta.contract_version = %q, want %q", f.Meta.ContractVersion, envelope.ContractVersion)
	}
	if f.Meta.Timestamp != "2026-08-02T12:00:00Z" {
		t.Errorf("meta.timestamp = %q, want the injected clock reading", f.Meta.Timestamp)
	}
	if f.Meta.RequestID == "" {
		t.Error("meta.request_id missing")
	}
	if f.Meta.ExitCode != nil {
		t.Error("meta.exit_code must be absent on success")
	}
	// Success still carries errors/warnings as EMPTY ARRAYS, never null:
	// consumers iterate without presence checks.
	if f.Errors == nil || len(f.Errors) != 0 {
		t.Errorf("errors = %v, want empty array", f.Errors)
	}
	if f.Warnings == nil || len(f.Warnings) != 0 {
		t.Errorf("warnings = %v, want empty array", f.Warnings)
	}
	var data versionData
	if err := json.Unmarshal(f.Data, &data); err != nil || data.Version != "0.16.0" {
		t.Errorf("data = %s, want the typed payload", f.Data)
	}
}

func TestCompactSuccessEmitsBareData(t *testing.T) {
	t.Parallel()
	raw := render(t, output.FormatCompact, assembleSuccess())

	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("unmarshal compact data: %v", err)
	}
	if data["version"] != "0.16.0" {
		t.Errorf("data.version = %v, want %q", data["version"], "0.16.0")
	}
	// Bare data means bare: no frame keys wrapping or riding beside it.
	for _, key := range []string{"ok", "meta", "errors", "warnings", "data"} {
		if _, present := data[key]; present {
			t.Errorf("compact success must not carry frame key %q", key)
		}
	}
}

// TestErrorKeepsFullFrameInEveryMachineMode pins design R2: a failure is
// never invisible in the lean mode — compact emits the same full frame
// json does, ok:false, populated errors[], and the contract exit code.
func TestErrorKeepsFullFrameInEveryMachineMode(t *testing.T) {
	t.Parallel()
	for _, format := range []output.Format{output.FormatJSON, output.FormatCompact} {
		t.Run(string(format), func(t *testing.T) {
			t.Parallel()
			f := decodeFrame(t, render(t, format, assembleFailure()))
			if f.OK == nil || *f.OK {
				t.Error("ok must be present and false on failure")
			}
			if f.Meta == nil {
				t.Fatal("meta missing from the failure frame")
			}
			if f.Meta.ExitCode == nil || *f.Meta.ExitCode != 2 {
				t.Errorf("meta.exit_code = %v, want 2", f.Meta.ExitCode)
			}
			if f.Meta.ContractVersion != envelope.ContractVersion {
				t.Errorf("meta.contract_version = %q, want %q", f.Meta.ContractVersion, envelope.ContractVersion)
			}
			if len(f.Errors) != 1 || f.Errors[0].Code != "jira_not_found" {
				t.Errorf("errors = %+v, want the single jira_not_found entry", f.Errors)
			}
			if string(f.Data) != "null" {
				t.Errorf("data = %s, want null on a data-less failure", f.Data)
			}
		})
	}
}

// TestHumanRenderIsGated pins the C4 posture: human is a gated surface,
// so an operation without a registered human renderer fails loudly
// instead of silently showing a person the machine shape.
func TestHumanRenderIsGated(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := output.Render(t.Context(), &buf, output.FormatHuman, assembleSuccess())
	if err == nil {
		t.Fatal("Render(t.Context(), human) with no registered renderer must fail loudly")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error %q should name the operation missing its renderer", err)
	}
	if buf.Len() != 0 {
		t.Errorf("nothing may be written on the failing path, got %q", buf.String())
	}
}

// resolveSelector builds a throwaway command carrying exactly the flags
// command.AddOutputFlags registers on the real root, parses args, and
// returns the resolved mode — the same path root's PersistentPreRunE walks.
func resolveSelector(t *testing.T, args ...string) (cli.OutputMode, error) {
	t.Helper()
	cmd := &cobra.Command{}
	command.AddOutputFlags(cmd)
	if err := cmd.PersistentFlags().Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return cli.ResolveSelector(cmd.PersistentFlags())
}

func TestSelectorAliasesResolveToCanonicalModes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want cli.OutputMode
	}{
		{name: "no signal defaults to auto", args: nil, want: cli.OutputAuto},
		{name: "canonical json", args: []string{"--output=json"}, want: cli.OutputJSON},
		{name: "json alias", args: []string{"--json"}, want: cli.OutputJSON},
		{name: "plain alias maps to human", args: []string{"--plain"}, want: cli.OutputHuman},
		{name: "human alias maps to human", args: []string{"--human"}, want: cli.OutputHuman},
		{name: "compact alias", args: []string{"--compact"}, want: cli.OutputCompact},
		{name: "agreeing signals", args: []string{"--output=json", "--json"}, want: cli.OutputJSON},
		{name: "two aliases for the same surface agree", args: []string{"--plain", "--human"}, want: cli.OutputHuman},
		{name: "alias switched off is no signal", args: []string{"--json=false"}, want: cli.OutputAuto},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveSelector(t, tt.args...)
			if err != nil {
				t.Fatalf("ResolveSelector(%v) error = %v", tt.args, err)
			}
			if got != tt.want {
				t.Errorf("ResolveSelector(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestSelectorConflictNamesOnlyTheCanonicalFlag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{name: "two disagreeing aliases", args: []string{"--json", "--plain"}},
		{name: "canonical and alias disagree", args: []string{"--output=json", "--plain"}},
		{name: "explicit auto disagrees with an alias", args: []string{"--output=auto", "--json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := resolveSelector(t, tt.args...)
			if err == nil {
				t.Fatalf("ResolveSelector(%v) must reject disagreeing signals", tt.args)
			}
			msg := err.Error()
			if !strings.Contains(msg, "--output") {
				t.Errorf("conflict error %q must name the canonical --output", msg)
			}
			// An introspecting agent was never shown the hidden aliases,
			// so the error may not reference them as flags.
			for _, hidden := range []string{"--json", "--plain", "--human", "--compact"} {
				if strings.Contains(msg, hidden) {
					t.Errorf("conflict error %q names hidden alias %s", msg, hidden)
				}
			}
		})
	}
}

func TestInvalidOutputValueIsLoud(t *testing.T) {
	t.Parallel()
	_, err := resolveSelector(t, "--output=yaml")
	if err == nil {
		t.Fatal("an unknown --output value must be rejected")
	}
	if !strings.Contains(err.Error(), "--output") || !strings.Contains(err.Error(), "yaml") {
		t.Errorf("error %q should name the flag and the offending value", err)
	}
}

func TestAliasesAreHiddenButFunctional(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{}
	command.AddOutputFlags(cmd)
	fs := cmd.PersistentFlags()
	if out := fs.Lookup(cli.FlagOutput); out == nil || out.Hidden {
		t.Error("--output must exist and stay discoverable")
	}
	for _, alias := range []string{cli.FlagAliasJSON, cli.FlagAliasPlain, cli.FlagAliasHuman, cli.FlagAliasCompact} {
		flag := fs.Lookup(alias)
		if flag == nil {
			t.Errorf("alias --%s must be registered (functional sugar)", alias)
			continue
		}
		if !flag.Hidden {
			t.Errorf("alias --%s must be hidden from help and completion", alias)
		}
	}
}

// TestWriteEnvelopeOwnershipChain drives the full chain — the command →
// the command package → output → cobra's writer — with the format seeded the way
// root's PersistentPreRunE seeds it, and pins that envelopes land on
// stdout only.
func TestWriteEnvelopeOwnershipChain(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetContext(cli.WithFormat(context.Background(), output.FormatJSON))

	if err := command.WriteEnvelope(cmd, "version", versionData{Version: "0.16.0"}); err != nil {
		t.Fatalf("WriteEnvelope error = %v", err)
	}
	f := decodeFrame(t, stdout.Bytes())
	if f.Meta == nil || f.Meta.Command != "version" || f.Meta.ContractVersion != envelope.ContractVersion {
		t.Errorf("frame meta = %+v, want command and contract_version populated", f.Meta)
	}
	if f.Meta != nil && (f.Meta.RequestID == "" || f.Meta.Timestamp == "") {
		t.Error("the command package must inject request_id and timestamp")
	}
	if stderr.Len() != 0 {
		t.Errorf("machine envelopes are stdout-only; stderr got %q", stderr.String())
	}
}

// TestWriteEnvelopeWithoutResolutionIsLoud pins the wiring guard: a
// command running without root's output resolution is a bug surfaced
// loudly, never papered over with a default format.
func TestWriteEnvelopeWithoutResolutionIsLoud(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	if err := command.WriteEnvelope(cmd, "version", versionData{}); err == nil {
		t.Fatal("WriteEnvelope without a seeded format must fail")
	}
}

// TestWriteEnvelopeReturnsDestinationFailures pins the chain's error
// posture: a failing writer surfaces as a typed output failure (exit 8
// through the taxonomy), not a silent drop.
func TestWriteEnvelopeReturnsDestinationFailures(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{}
	cmd.SetOut(failingWriter{})
	cmd.SetContext(cli.WithFormat(context.Background(), output.FormatJSON))
	err := command.WriteEnvelope(cmd, "version", versionData{})
	if err == nil {
		t.Fatal("a destination failure must propagate up the chain")
	}
	if got := cli.MapError(err); got.Code != "output_write_failed" || got.Exit != cli.ExitOutput {
		t.Errorf("MapError = %+v, want output_write_failed / exit %d", got, cli.ExitOutput)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errWriteRefused
}

var errWriteRefused = &writeRefusedError{}

type writeRefusedError struct{}

func (*writeRefusedError) Error() string { return "write refused" }

// TestSchemaOfDerivesFromTheDeclaration pins the single-source rule:
// the published schema comes from the same struct the wire bytes do —
// embedded structs inline, omitempty maps to optional, snake_case names
// come from the tags.
func TestSchemaOfDerivesFromTheDeclaration(t *testing.T) {
	t.Parallel()
	type transitionOutput struct {
		envelope.Identity
		envelope.MutationState
		Status string `json:"status,omitempty"`
	}
	schema := envelope.SchemaOf(transitionOutput{}, nil)

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema carries no properties: %v", schema)
	}
	for _, name := range []string{"issue", "dry_run", "status"} {
		if _, present := props[name]; !present {
			t.Errorf("property %q missing; embeds must inline and tags must name", name)
		}
	}
	if _, leaked := props["Identity"]; leaked {
		t.Error("embedded struct leaked as a named property instead of inlining")
	}
	required, _ := schema["required"].([]string)
	requiredSet := map[string]bool{}
	for _, r := range required {
		requiredSet[r] = true
	}
	if !requiredSet["issue"] || !requiredSet["dry_run"] {
		t.Errorf("required = %v, want the non-omitempty fields issue and dry_run", required)
	}
	if requiredSet["status"] {
		t.Error("omitempty field status must not be required")
	}
	issue, _ := props["issue"].(map[string]any)
	issueProps, _ := issue["properties"].(map[string]any)
	if _, present := issueProps["key"]; !present {
		t.Errorf("issue ref schema = %v, want nested key property", issue)
	}
}

func TestAssembleFailureWithZeroExitPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("failure Inputs with zero ExitCode assembled silently; want the loud panic")
		}
	}()
	output.Assemble(output.Inputs{
		Errors: []envelope.Error{{Code: "validation_failed", Message: "boom"}},
	})
}

var renderRegistrationID atomic.Uint64

func TestRegisterHumanRendersAndRejectsDuplicates(t *testing.T) {
	// The production registry lives for the process. A fresh key per run
	// lets -count=N exercise duplicate refusal within each invocation.
	op := fmt.Sprintf("unit.test.op.%d", renderRegistrationID.Add(1))
	output.RegisterHuman(op, func(v *output.View, env envelope.Envelope) error {
		_, err := io.WriteString(v.Output().Writer(), "rendered "+env.Meta.Command)
		return err
	})

	var buf bytes.Buffer
	res := output.Assemble(output.Inputs{Op: op, Now: time.Now(), RequestID: "r"})
	if err := output.Render(t.Context(), &buf, output.FormatHuman, res); err != nil {
		t.Fatalf("human render: %v", err)
	}
	if got := buf.String(); got != "rendered "+op {
		t.Errorf("rendered %q", got)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("duplicate registration did not panic")
		}
	}()
	output.RegisterHuman(op, func(*output.View, envelope.Envelope) error { return nil })
}
