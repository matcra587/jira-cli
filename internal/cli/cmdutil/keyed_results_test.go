package cmdutil

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

type keyedDiagnosticErrorWriter struct {
	err error
}

func (w keyedDiagnosticErrorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestWriteKeyedResultsEnvelopeKeepsPartialFailurePrimary(t *testing.T) {
	writeErr := errors.New("stderr closed")
	cmd := commandWithDetector(cli.Detection{Mode: cli.ModePlain})
	cmd.SetContext(WithDetector(context.Background(), cli.Detection{Mode: cli.ModePlain}))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(keyedDiagnosticErrorWriter{err: writeErr})

	err := WriteKeyedResultsEnvelope(
		cmd,
		"issue.delete",
		[]KeyResult[map[string]any]{{
			Key: "TEST-1",
			Err: cli.NewNotFoundError("issue not found", errors.New("missing")),
		}},
		nil,
	)
	if !errors.Is(err, writeErr) {
		t.Fatalf("WriteKeyedResultsEnvelope() error = %v, want writer cause", err)
	}
	if _, ok := errors.AsType[*cli.OutputError](err); !ok {
		t.Fatalf("WriteKeyedResultsEnvelope() error type = %T, want *cli.OutputError", err)
	}
	mapped := cli.MapError(err)
	if mapped.Code != "not_found" || cli.ExitCode(mapped) != 2 {
		t.Fatalf("MapError() = %#v, want primary not-found exit 2", mapped)
	}
}
