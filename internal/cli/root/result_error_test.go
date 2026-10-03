package root

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func TestRetainedFailureWarningsReachMachineEnvelope(t *testing.T) {
	for _, format := range []output.Format{output.FormatJSON, output.FormatCompact} {
		t.Run(string(format), func(t *testing.T) {
			var stdout bytes.Buffer
			warning := envelope.Warning{Type: "incomplete", Message: "Validated rows remain incomplete", Lossy: true}
			err := &cli.ResultError{Err: &adapterjira.APIError{StatusCode: http.StatusForbidden}, Op: "version", Data: envelope.VersionOutput{Version: "retained"}, Warnings: []envelope.Warning{warning}}
			sink := &failureSink{stdout: &stdout, format: format, resolved: true, op: "version"}
			if exit := sink.exitCodeContext(t.Context(), err); exit != cli.ExitAuth {
				t.Fatalf("failure changed exit: %d", exit)
			}
			var frame struct {
				OK       bool                   `json:"ok"`
				Warnings []envelope.Warning     `json:"warnings"`
				Data     envelope.VersionOutput `json:"data"`
			}
			if json.Unmarshal(stdout.Bytes(), &frame) != nil || frame.OK || frame.Data.Version != "retained" || !reflect.DeepEqual(frame.Warnings, []envelope.Warning{warning}) {
				t.Fatalf("retained failure lost warning or data: %s", stdout.String())
			}
		})
	}
}

func TestFailureReceiptDestinationErrorOverridesOriginalExit(t *testing.T) {
	for _, format := range []output.Format{output.FormatJSON, output.FormatCompact, output.FormatHuman} {
		t.Run(string(format), func(t *testing.T) {
			reader, writer := io.Pipe()
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = writer.Close() })
			sink := &failureSink{stdout: writer, format: format, resolved: true, op: "version"}
			err := &cli.ResultError{
				Err: &adapterjira.APIError{StatusCode: http.StatusTooManyRequests},
				Op:  "version", Data: envelope.VersionOutput{},
				Human: func(v *output.View, env envelope.Envelope) error {
					if env.OK || env.Meta.ExitCode == nil || *env.Meta.ExitCode != cli.ExitRateLimit {
						t.Error("human receipt was relabeled as a success")
					}
					v.Line("retained receipt")
					return nil
				},
			}
			if exit := sink.exitCodeContext(t.Context(), err); exit != cli.ExitOutput {
				t.Fatalf("broken receipt destination returned exit %d, want output failure", exit)
			}
		})
	}
}
