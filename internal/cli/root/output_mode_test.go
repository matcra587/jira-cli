package root

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gechr/clog"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/cmdutil"
	"github.com/matcra587/jira-cli/internal/cli/issue"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/spf13/cobra"
)

func TestTTYCommandsDefaultToHumanClogOutput(t *testing.T) {
	cmd, stdout, _ := outputModeTestCommand(cli.ModePlain)

	if err := cmdutil.WriteEnvelope(cmd, "issue.list", map[string]any{"issues": []any{}}); err != nil {
		t.Fatalf("cmdutil.WriteEnvelope() error = %v", err)
	}
	got := stdout.String()
	if strings.Contains(got, `"meta"`) || strings.Contains(got, `"errors"`) {
		t.Fatalf("TTY default command output should be human data, not JSON envelope:\n%s", got)
	}
	if !strings.Contains(got, "Listed issues") || !strings.Contains(got, "count=0") {
		t.Fatalf("TTY default command output should use clog rich data rendering:\n%s", got)
	}
}

func TestNonTTYCommandsDefaultToJSONEnvelope(t *testing.T) {
	cmd, stdout, _ := outputModeTestCommand(cli.ModeJSON)

	if err := cmdutil.WriteEnvelope(cmd, "issue.list", map[string]any{"issues": []any{}}); err != nil {
		t.Fatalf("cmdutil.WriteEnvelope() error = %v", err)
	}
	got := stdout.String()
	if !strings.Contains(got, `"meta"`) || !strings.Contains(got, `"errors"`) {
		t.Fatalf("non-TTY command output is not JSON envelope:\n%s", got)
	}
}

// --output=json resolves to ModeJSON, which command output helpers must
// honor by emitting the full envelope even when the terminal is a TTY.
func TestOutputJSONModeForcesEnvelope(t *testing.T) {
	cmd, stdout, _ := outputModeTestCommand(cli.ModeJSON)
	if err := cmdutil.WriteEnvelope(cmd, "issue.list", map[string]any{"issues": []any{}}); err != nil {
		t.Fatalf("cmdutil.WriteEnvelope() error = %v", err)
	}
	got := stdout.String()
	if !strings.Contains(got, `"meta"`) || !strings.Contains(got, `"errors"`) || !strings.Contains(got, `"ok"`) {
		t.Fatalf("--output=json did not force JSON envelope:\n%s", got)
	}
}

// --output=compact resolves to ModeCompact, which drops the envelope
// wrapper and emits the data payload only.
func TestOutputCompactModeDropsEnvelope(t *testing.T) {
	cmd, stdout, _ := outputModeTestCommand(cli.ModeCompact)
	if err := cmdutil.WriteEnvelope(cmd, "issue.list", map[string]any{"issues": []any{}}); err != nil {
		t.Fatalf("cmdutil.WriteEnvelope() error = %v", err)
	}
	got := stdout.String()
	if strings.Contains(got, `"meta"`) {
		t.Fatalf("--output=compact must drop the envelope meta:\n%s", got)
	}
}

// ParseOutputMode rejects every removed legacy flag name so a removed
// flag can never be silently re-aliased into a mode.
func TestRemovedLegacyFlagNamesAreNotOutputModes(t *testing.T) {
	for _, name := range []string{"json", "compact", "plain", "raw"} {
		// json/compact ARE valid --output VALUES; the others must not be.
		if name == "plain" || name == "raw" {
			if _, err := cli.ParseOutputMode(name); err == nil {
				t.Fatalf("removed flag name %q must not be a valid --output mode", name)
			}
		}
	}
}

// A credential-cleanup warning (e.g. a failed revocation of the old
// secret after an auth re-point) must survive compact mode. compact has
// no envelope, so the warning is folded into the data payload — it must
// never be silently dropped, or a stale secret stays invisible.
func TestCompactModePreservesCredentialCleanupWarning(t *testing.T) {
	cmd, stdout, _ := outputModeTestCommand(cli.ModeCompact)
	cmdutil.RecordCredentialWarnings(cmd, []string{"revoking the previous credential failed: keyring locked"})

	if err := cmdutil.WriteEnvelope(cmd, "auth.login", map[string]any{"profile": "work", "logged_in": true}); err != nil {
		t.Fatalf("cmdutil.WriteEnvelope() error = %v", err)
	}
	got := stdout.String()
	if strings.Contains(got, `"meta"`) {
		t.Fatalf("compact output must not carry the envelope:\n%s", got)
	}
	if !strings.Contains(got, "revoking the previous credential failed") {
		t.Fatalf("compact mode dropped the credential cleanup warning:\n%s", got)
	}
}

func TestPlainRawWarningsRouteToStderr(t *testing.T) {
	cmd, stdout, stderr := outputModeTestCommand(cli.ModePlain)

	err := cmdutil.WriteEnvelopeWithRawWarnings(cmd, "cache.boards", map[string]any{"boards": []any{}}, []map[string]any{{
		"type":    "rate-limit-during-paginate",
		"message": "retry later",
	}})
	if err != nil {
		t.Fatalf("cmdutil.WriteEnvelopeWithRawWarnings() error = %v", err)
	}
	if strings.Contains(stdout.String(), "retry later") || strings.Contains(stdout.String(), "rate-limit-during-paginate") {
		t.Fatalf("plain raw warning leaked to stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "retry later") || !strings.Contains(stderr.String(), "rate-limit-during-paginate") {
		t.Fatalf("plain raw warning missing from stderr:\n%s", stderr.String())
	}
}

func TestCommandErrorsUseClogDiagnosticsOnStderr(t *testing.T) {
	cmd, _, stderr := outputModeTestCommand(cli.ModePlain)
	if err := cmd.Root().PersistentFlags().Set("output", "human"); err != nil {
		t.Fatalf("Set(output) error = %v", err)
	}

	if err := writeCommandError(cmd.Context(), cmd, errors.New("jira API failed")); err != nil {
		t.Fatalf("writeCommandError() error = %v", err)
	}
	got := stderr.String()
	if strings.Contains(got, `"errors"`) || strings.Contains(got, `"meta"`) || !strings.Contains(got, "jira API failed") {
		t.Fatalf("command error did not emit clog diagnostic stderr:\n%s", got)
	}
}

func TestCommandFailureRemainsPrimaryWhenErrorEnvelopeWriteFails(t *testing.T) {
	commandErr := cli.NewCLIInputError(cli.InputCommandUnknown, `unknown command "lsit"`)
	writeErr := errors.New("stdout closed")
	stdout := &countingErrorWriter{err: writeErr}
	cmd, _, _ := outputModeTestCommand(cli.ModeJSON)
	cmd.SetOut(stdout)
	cmd.Root().SetOut(stdout)

	renderErr := writeCommandError(cmd.Context(), cmd, commandErr)
	if !errors.Is(renderErr, writeErr) {
		t.Fatalf("writeCommandError() error = %v, want writer failure", renderErr)
	}
	err := preserveCommandError(commandErr, renderErr)
	if !errors.Is(err, commandErr) || !errors.Is(err, writeErr) {
		t.Fatalf("combined error = %v, want command and writer causes", err)
	}
	if _, ok := errors.AsType[*cli.OutputError](err); !ok {
		t.Fatalf("combined error type = %T, want discoverable *cli.OutputError", err)
	}
	mapped := outputErrorFor(err)
	if mapped.Code != "command_unknown" || mapped.Type != "validation" {
		t.Fatalf("combined error mapped as %#v, want original command taxonomy", mapped)
	}
	if got := ExitCode(err); got != 3 {
		t.Fatalf("combined error exit = %d, want 3", got)
	}
	if stdout.writes != 1 {
		t.Fatalf("stdout writes = %d, want one failed envelope attempt", stdout.writes)
	}
}

func TestCommandFailureRemainsPrimaryWhenHumanDiagnosticWriteFails(t *testing.T) {
	commandErr := &jira.APIError{
		StatusCode: 503,
		Type:       jira.ErrorTypeServer,
		Message:    "Jira unavailable",
	}
	writeErr := errors.New("stderr closed")
	stderr := &countingErrorWriter{err: writeErr}
	cmd, _, _ := outputModeTestCommand(cli.ModePlain)
	cmd.SetErr(stderr)
	cmd.Root().SetErr(stderr)
	if err := cmd.Root().PersistentFlags().Set("output", "human"); err != nil {
		t.Fatalf("Set(output) error = %v", err)
	}

	renderErr := writeCommandError(cmd.Context(), cmd, commandErr)
	if !errors.Is(renderErr, writeErr) {
		t.Fatalf("writeCommandError() error = %v, want writer failure", renderErr)
	}
	err := preserveCommandError(commandErr, renderErr)
	if !errors.Is(err, commandErr) || !errors.Is(err, writeErr) {
		t.Fatalf("combined error = %v, want command and writer causes", err)
	}
	mapped := outputErrorFor(err)
	if mapped.Code != "jira_server_error" || mapped.Type != "server" {
		t.Fatalf("combined error mapped as %#v, want original Jira taxonomy", mapped)
	}
	if got := ExitCode(err); got != 5 {
		t.Fatalf("combined error exit = %d, want 5", got)
	}
	if stderr.writes != 1 {
		t.Fatalf("stderr writes = %d, want one failed diagnostic attempt", stderr.writes)
	}
}

func TestExistingOutputFailureIsNotRenderedAgain(t *testing.T) {
	writeErr := errors.New("stdout closed")
	stdout := &countingErrorWriter{err: writeErr}
	cmd, _, _ := outputModeTestCommand(cli.ModeJSON)
	cmd.SetOut(stdout)
	cmd.Root().SetOut(stdout)

	err := writeCommandError(cmd.Context(), cmd, cli.NewOutputError(writeErr))
	if err != nil {
		t.Fatalf("writeCommandError() error = %v, want nil for an already reported output failure", err)
	}
	if stdout.writes != 0 {
		t.Fatalf("stdout writes = %d, want no second write to failed output", stdout.writes)
	}
}

func TestUnsupportedAuthTypeClassifiesAsValidationError(t *testing.T) {
	outErr := outputErrorFor(errors.New(`unsupported auth type "oauth2"`))
	if outErr.Type != string(cli.ErrorTypeValidation) {
		t.Fatalf("unsupported auth type classified as %q, want %q", outErr.Type, cli.ErrorTypeValidation)
	}
}

func TestIssueListPlainOutputHidesJQLUnlessDebug(t *testing.T) {
	cmd, stdout, _ := outputModeTestCommand(cli.ModePlain)
	if err := cmd.Root().PersistentFlags().Set("output", "human"); err != nil {
		t.Fatalf("Set(output) error = %v", err)
	}
	data := issue.IssueListOutputData(cmd, []map[string]any{}, false, "assignee = currentUser() ORDER BY updated DESC")
	if err := cmdutil.WriteEnvelope(cmd, "issue.list", data); err != nil {
		t.Fatalf("cmdutil.WriteEnvelope() error = %v", err)
	}
	if strings.Contains(stdout.String(), "jql=") {
		t.Fatalf("issue list plain output leaked JQL without debug:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Listed issues") || !strings.Contains(stdout.String(), "count=0") || strings.Contains(stdout.String(), "detail=") || strings.Contains(stdout.String(), "No issues") || strings.Contains(stdout.String(), "issues=") {
		t.Fatalf("issue list plain output did not render a clog issue-list result:\n%s", stdout.String())
	}

	cmd, stdout, _ = outputModeTestCommand(cli.ModePlain)
	if err := cmd.Root().PersistentFlags().Set("output", "human"); err != nil {
		t.Fatalf("Set(output) error = %v", err)
	}
	if err := cmd.Root().PersistentFlags().Set("debug", "true"); err != nil {
		t.Fatalf("Set(debug) error = %v", err)
	}
	data = issue.IssueListOutputData(cmd, []map[string]any{}, false, "assignee = currentUser() ORDER BY updated DESC")
	if err := cmdutil.WriteEnvelope(cmd, "issue.list", data); err != nil {
		t.Fatalf("cmdutil.WriteEnvelope(debug) error = %v", err)
	}
	if !strings.Contains(stdout.String(), "jql=") {
		t.Fatalf("issue list debug output omitted JQL:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "detail=") || strings.Contains(stdout.String(), "issues=") || strings.Contains(stdout.String(), "No issues") {
		t.Fatalf("issue list debug output leaked internal fields:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "INF") {
		t.Fatalf("--plain stdout is not clog event output:\n%s", stdout.String())
	}
}

func TestIssueListPlainOutputShowsDetailWhenEnabled(t *testing.T) {
	cmd, stdout, _ := outputModeTestCommand(cli.ModePlain)
	if err := cmd.Root().PersistentFlags().Set("output", "human"); err != nil {
		t.Fatalf("Set(output) error = %v", err)
	}
	data := issue.IssueListOutputData(cmd, []map[string]any{}, true, "")
	if err := cmdutil.WriteEnvelope(cmd, "issue.list", data); err != nil {
		t.Fatalf("cmdutil.WriteEnvelope() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "detail=true") {
		t.Fatalf("issue list detail mode did not surface detail=true:\n%s", stdout.String())
	}
}

func TestPlainOutputExtractsNestedADFText(t *testing.T) {
	cmd, stdout, _ := outputModeTestCommand(cli.ModePlain)
	if err := cmd.Root().PersistentFlags().Set("output", "human"); err != nil {
		t.Fatalf("Set(output) error = %v", err)
	}
	data := map[string]any{
		"comment": map[string]any{
			"body": map[string]any{
				"type":    "doc",
				"version": 1,
				"content": []any{
					map[string]any{
						"type": "paragraph",
						"content": []any{
							map[string]any{"type": "text", "text": "hello world"},
						},
					},
				},
			},
		},
	}
	if err := cmdutil.WriteEnvelope(cmd, "issue.comment", data); err != nil {
		t.Fatalf("cmdutil.WriteEnvelope() error = %v", err)
	}
	got := stdout.String()
	// issue.comment is a mutation, so its completion line logs at the
	// registered success level rather than INF.
	if strings.Contains(got, "comment=1") || !strings.Contains(got, `comment.body="hello world"`) || !strings.Contains(got, "SCS") {
		t.Fatalf("plain output did not extract nested ADF text:\n%s", got)
	}
}

func TestRootInteractiveFlagControlsDashboardLaunchIntent(t *testing.T) {
	cmd, _, _ := outputModeTestCommand(cli.ModePlain)
	interactive, _ := cmd.Root().PersistentFlags().GetBool("interactive")
	if interactive {
		t.Fatal("interactive should default false")
	}
	if err := cmd.Root().PersistentFlags().Set("interactive", "true"); err != nil {
		t.Fatalf("Set(interactive) error = %v", err)
	}
	interactive, _ = cmd.Root().PersistentFlags().GetBool("interactive")
	if !interactive {
		t.Fatal("interactive flag was not set")
	}
}

func TestIssueListPlainOutputMarksNonDefaultParallelism(t *testing.T) {
	cmd, stdout, _ := outputModeTestCommand(cli.ModePlain)
	var parallelism int
	cmdutil.AddParallelismFlag(cmd, &parallelism)
	if err := cmd.Flags().Parse([]string{"-p", "2"}); err != nil {
		t.Fatalf("parse -p 2: %v", err)
	}

	data := issue.IssueListOutputData(cmd, []map[string]any{}, false, "")
	if err := cmdutil.WriteEnvelope(cmd, "issue.list", data); err != nil {
		t.Fatalf("cmdutil.WriteEnvelope() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "threads=2") {
		t.Fatalf("issue list plain output omitted non-default thread count:\n%s", stdout.String())
	}

	cmd, stdout, _ = outputModeTestCommand(cli.ModePlain)
	cmdutil.AddParallelismFlag(cmd, &parallelism)
	data = issue.IssueListOutputData(cmd, []map[string]any{}, false, "")
	if err := cmdutil.WriteEnvelope(cmd, "issue.list", data); err != nil {
		t.Fatalf("cmdutil.WriteEnvelope(default) error = %v", err)
	}
	if strings.Contains(stdout.String(), "threads=") {
		t.Fatalf("issue list plain output should omit default thread count:\n%s", stdout.String())
	}
}

func outputModeTestCommand(mode cli.Mode) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	root := &cobra.Command{Use: "jira"}
	pf := root.PersistentFlags()
	pf.String("profile", "default", "")
	pf.String("config", "", "")
	pf.String("output", "auto", "")
	pf.BoolP("interactive", "i", false, "")
	pf.BoolP("debug", "d", false, "")
	cmd := &cobra.Command{Use: "issue list"}
	root.AddCommand(cmd)
	root.SetOut(stdout)
	root.SetErr(stderr)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	ctx := cmdutil.WithDetector(context.Background(), cli.Detection{Mode: mode, IsTTY: mode == cli.ModePlain})
	ctx = cmdutil.WithCredentialWarnSink(ctx)
	cmd.SetContext(ctx)
	return cmd, stdout, stderr
}

// TestCommandErrorDebugShowsTaxonomyFields pins the --debug enrichment: the
// human ERR line carries the envelope's code and type (and retryable when
// true — OmitZero drops the false) so a debugging human sees the same
// classification an agent gets. The HTTP status is deliberately absent: the
// message and the --debug traffic trace both already carry it. The JSON
// path is untouched — writeCommandError returns to the envelope writer
// before any clog rendering, which the envelope contract tests pin. Not
// parallel: clog.SetVerbose is process-global.
func TestCommandErrorDebugShowsTaxonomyFields(t *testing.T) {
	clog.SetVerbose(true)
	defer clog.SetVerbose(false)

	cmd, _, stderr := outputModeTestCommand(cli.ModePlain)
	if err := cmd.Root().PersistentFlags().Set("output", "human"); err != nil {
		t.Fatalf("Set(output) error = %v", err)
	}
	if err := writeCommandError(cmd.Context(), cmd, &jira.APIError{StatusCode: 503, Type: jira.ErrorTypeServer, Message: "upstream text"}); err != nil {
		t.Fatalf("writeCommandError() error = %v", err)
	}
	got := stderr.String()
	for _, want := range []string{"code=jira_server_error", "type=server", "retryable=true"} {
		if !strings.Contains(got, want) {
			t.Fatalf("debug ERR line missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "http_status=") {
		t.Fatalf("debug ERR line must not duplicate the HTTP status:\n%s", got)
	}

	// A non-retryable failure keeps the line lean: OmitZero drops the
	// false retryable, and no status field appears.
	cmd2, _, stderr2 := outputModeTestCommand(cli.ModePlain)
	if err := cmd2.Root().PersistentFlags().Set("output", "human"); err != nil {
		t.Fatalf("Set(output) error = %v", err)
	}
	if err := writeCommandError(cmd2.Context(), cmd2, &jira.APIError{StatusCode: 404, Type: jira.ErrorTypeNotFound, Message: "upstream text"}); err != nil {
		t.Fatalf("writeCommandError() error = %v", err)
	}
	got2 := stderr2.String()
	for _, want := range []string{"code=jira_not_found", "type=not_found"} {
		if !strings.Contains(got2, want) {
			t.Fatalf("debug ERR line missing %q:\n%s", want, got2)
		}
	}
	if strings.Contains(got2, "retryable=") || strings.Contains(got2, "http_status=") {
		t.Fatalf("non-retryable debug ERR line must omit retryable/http_status:\n%s", got2)
	}
}

// TestCommandErrorSanitizesServerControlledText is the guardrail for the
// human error path: a Jira error message is server-controlled text, so an
// embedded ANSI escape or BEL must never reach the terminal. The JSON
// modes are protected by the JSON encoder; this pins the human-mode
// counterpart at the writeCommandError render boundary.
func TestCommandErrorSanitizesServerControlledText(t *testing.T) {
	cmd, _, stderr := outputModeTestCommand(cli.ModePlain)
	if err := cmd.Root().PersistentFlags().Set("output", "human"); err != nil {
		t.Fatalf("Set(output) error = %v", err)
	}
	if err := writeCommandError(cmd.Context(), cmd, &jira.APIError{
		StatusCode: 404,
		Type:       jira.ErrorTypeNotFound,
		Message:    "The attachment with id '9\x1b[31m9\x079' does not exist",
	}); err != nil {
		t.Fatalf("writeCommandError() error = %v", err)
	}
	got := stderr.String()
	if strings.ContainsAny(got, "\x1b\x07\x00") {
		t.Fatalf("server-controlled control bytes reached the terminal:\n%q", got)
	}
	if !strings.Contains(got, "'999'") {
		t.Fatalf("sanitized message lost its printable text:\n%q", got)
	}
}

// TestCommandErrorWithoutDebugStaysLean pins the default: without --debug
// the ERR line stays message-only, no taxonomy fields.
func TestCommandErrorWithoutDebugStaysLean(t *testing.T) {
	clog.SetVerbose(false)

	cmd, _, stderr := outputModeTestCommand(cli.ModePlain)
	if err := cmd.Root().PersistentFlags().Set("output", "human"); err != nil {
		t.Fatalf("Set(output) error = %v", err)
	}
	if err := writeCommandError(cmd.Context(), cmd, &jira.APIError{StatusCode: 503, Type: jira.ErrorTypeServer, Message: "upstream text"}); err != nil {
		t.Fatalf("writeCommandError() error = %v", err)
	}
	got := stderr.String()
	if strings.Contains(got, "code=") || strings.Contains(got, "http_status=") {
		t.Fatalf("non-debug ERR line leaked taxonomy fields:\n%s", got)
	}
	if !strings.Contains(got, "upstream text") {
		t.Fatalf("ERR line lost the error message:\n%s", got)
	}
}

// TestCommandErrorRateLimitShowsRetryAfter pins the per-instance wait: a
// 429 with Retry-After renders a "retry in Ns" line after the static
// rate-limit hint. The envelope's retry_after_seconds field is pinned
// separately by the error contract tests.
func TestCommandErrorRateLimitShowsRetryAfter(t *testing.T) {
	cmd, _, stderr := outputModeTestCommand(cli.ModePlain)
	if err := cmd.Root().PersistentFlags().Set("output", "human"); err != nil {
		t.Fatalf("Set(output) error = %v", err)
	}
	if err := writeCommandError(cmd.Context(), cmd, &jira.APIError{StatusCode: 429, Type: jira.ErrorTypeRateLimit, Message: "rate limited", RetryAfterSeconds: 42}); err != nil {
		t.Fatalf("writeCommandError() error = %v", err)
	}
	got := stderr.String()
	if !strings.Contains(got, "retry in 42s") {
		t.Fatalf("rate-limit error missing the retry-in line:\n%s", got)
	}
	if !strings.Contains(got, "Jira is rate-limiting you") {
		t.Fatalf("rate-limit error lost its static hint:\n%s", got)
	}
}
