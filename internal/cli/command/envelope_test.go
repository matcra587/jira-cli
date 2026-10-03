package command_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// sampleData is a minimal typed Output struct standing in for a real
// command's data shape.
type sampleData struct {
	Value string `json:"value"`
}

// brokenWriter always fails, standing in for a destination failure
// (broken pipe, full disk).
type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

// newCmd returns a cobra command wired the way root's PersistentPreRunE
// wires it: an OutOrStdout writer and, unless withoutFormat, a resolved
// format riding the context.
func newCmd(t *testing.T, buf *bytes.Buffer, format output.Format, withoutFormat bool) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "test"}
	cmd.SetOut(buf)
	if !withoutFormat {
		cmd.SetContext(cli.WithFormat(t.Context(), format))
	} else {
		cmd.SetContext(t.Context())
	}
	return cmd
}

var requestIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// TestWriteEnvelopeMissingFormat pins the wiring-bug posture: a command
// context that never went through root's PersistentPreRunE fails loud
// rather than silently defaulting to some format, and writes nothing to
// the destination.
func TestWriteEnvelopeMissingFormat(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	cmd := newCmd(t, &buf, "", true)

	err := command.WriteEnvelope(cmd, "thing.get", sampleData{Value: "x"})
	if err == nil {
		t.Fatal("expected an error when the context carries no resolved format")
	}
	const want = "no output format in the command context: root's output resolution did not run"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %q to the destination despite the wiring failure", buf.String())
	}
}

// TestWriteEnvelopeJSONSuccess pins the full-frame contract in json mode:
// op becomes meta.command, data rides data, ok is true, no exit_code
// appears on a success envelope (only failures set it — WriteEnvelope
// only ever assembles success), and request_id is a fresh 32-hex id.
func TestWriteEnvelopeJSONSuccess(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	cmd := newCmd(t, &buf, output.FormatJSON, false)

	if err := command.WriteEnvelope(cmd, "thing.get", sampleData{Value: "x"}); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	var frame struct {
		OK   bool `json:"ok"`
		Meta struct {
			Command   string `json:"command"`
			RequestID string `json:"request_id"`
			ExitCode  *int   `json:"exit_code"`
		} `json:"meta"`
		Data     sampleData         `json:"data"`
		Errors   []envelope.Error   `json:"errors"`
		Warnings []envelope.Warning `json:"warnings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &frame); err != nil {
		t.Fatalf("stdout is not a frame: %v\nstdout: %q", err, buf.String())
	}
	if !frame.OK {
		t.Error("ok = false, want true")
	}
	if frame.Meta.Command != "thing.get" {
		t.Errorf("meta.command = %q, want %q", frame.Meta.Command, "thing.get")
	}
	if frame.Data != (sampleData{Value: "x"}) {
		t.Errorf("data = %+v, want {Value: x}", frame.Data)
	}
	if frame.Meta.ExitCode != nil {
		t.Errorf("meta.exit_code = %v, want absent on a success envelope", *frame.Meta.ExitCode)
	}
	if len(frame.Errors) != 0 {
		t.Errorf("errors = %+v, want empty", frame.Errors)
	}
	if len(frame.Warnings) != 0 {
		t.Errorf("warnings = %+v, want empty (none supplied)", frame.Warnings)
	}
	if !requestIDPattern.MatchString(frame.Meta.RequestID) {
		t.Errorf("request_id = %q, want a 32-char hex id", frame.Meta.RequestID)
	}
}

// TestWriteEnvelopeCompactSuccessIsBareData pins the design-C1 rule
// spelled out in the doc comment: compact success is bare data, not the
// envelope frame — there is no "ok" or "meta" key at all on success.
func TestWriteEnvelopeCompactSuccessIsBareData(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	cmd := newCmd(t, &buf, output.FormatCompact, false)

	if err := command.WriteEnvelope(cmd, "thing.get", sampleData{Value: "x"}); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	var data sampleData
	if err := json.Unmarshal(buf.Bytes(), &data); err != nil {
		t.Fatalf("stdout is not bare data: %v\nstdout: %q", err, buf.String())
	}
	if data.Value != "x" {
		t.Errorf("data.value = %q, want %q", data.Value, "x")
	}
	var probe map[string]any
	if err := json.Unmarshal(buf.Bytes(), &probe); err != nil {
		t.Fatalf("unmarshal into map: %v", err)
	}
	if _, has := probe["ok"]; has {
		t.Errorf("compact success carries an %q key; want bare data only: %q", "ok", buf.String())
	}
}

// TestWriteEnvelopeWithWarningsJSONIncludesThem pins that warnings ride
// the frame in json mode.
func TestWriteEnvelopeWithWarningsJSONIncludesThem(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	cmd := newCmd(t, &buf, output.FormatJSON, false)
	warnings := []envelope.Warning{{Type: "truncated", Message: "field truncated", Lossy: true}}

	if err := command.WriteEnvelopeWithWarnings(cmd, "thing.get", sampleData{Value: "x"}, warnings); err != nil {
		t.Fatalf("WriteEnvelopeWithWarnings: %v", err)
	}

	var frame struct {
		Warnings []envelope.Warning `json:"warnings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &frame); err != nil {
		t.Fatalf("stdout is not a frame: %v\nstdout: %q", err, buf.String())
	}
	if len(frame.Warnings) != 1 || frame.Warnings[0] != warnings[0] {
		t.Errorf("warnings = %+v, want %+v", frame.Warnings, warnings)
	}
}

// TestWriteEnvelopeWithWarningsCompactDropsThem pins the documented
// asymmetry (design C1): compact success is bare data, so warnings ride
// nowhere — a fact that changed what the caller got must be reflected in
// the data itself, never only in a warning that compact then discards.
func TestWriteEnvelopeWithWarningsCompactDropsThem(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	cmd := newCmd(t, &buf, output.FormatCompact, false)
	warnings := []envelope.Warning{{Type: "truncated", Message: "field truncated", Lossy: true}}

	if err := command.WriteEnvelopeWithWarnings(cmd, "thing.get", sampleData{Value: "x"}, warnings); err != nil {
		t.Fatalf("WriteEnvelopeWithWarnings: %v", err)
	}
	if bytes.Contains(buf.Bytes(), []byte("truncated")) {
		t.Errorf("compact output carries the warning text; want it silently dropped: %q", buf.String())
	}
}

// TestWriteEnvelopeDestinationFailure pins the write-ownership contract:
// a destination failure (broken pipe, full disk) comes back to the
// command as a *output.WriteError, the type internal/cli's taxonomy maps
// to the local output_write_failed exit — never a swallowed error.
func TestWriteEnvelopeDestinationFailure(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	cmd.SetOut(brokenWriter{})
	cmd.SetContext(cli.WithFormat(t.Context(), output.FormatJSON))

	err := command.WriteEnvelope(cmd, "thing.get", sampleData{Value: "x"})
	if err == nil {
		t.Fatal("expected an error from a broken destination")
	}
	var writeErr *output.WriteError
	if !errors.As(err, &writeErr) {
		t.Errorf("error = %v (%T), want an *output.WriteError", err, err)
	}
}
