package cli

import (
	"errors"
	"io"
	"testing"
)

type diagnosticErrorWriter struct {
	err error
}

func (w diagnosticErrorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type diagnosticShortWriter struct{}

func (diagnosticShortWriter) Write(p []byte) (int, error) {
	return len(p) - 1, nil
}

func TestFailureDiagnosticsSurfaceDestinationFailures(t *testing.T) {
	data := map[string]any{
		"results": []map[string]any{{
			"key": "TEST-1",
			"ok":  false,
		}},
		"succeeded": 0,
		"failed":    1,
	}
	errorsOut := []Error{{Code: "jira_not_found", Type: "not_found"}}
	writeErr := errors.New("stderr closed")

	tests := []struct {
		name  string
		write func() error
		cause error
	}{
		{
			name: "keyed write error",
			write: func() error {
				return WriteKeyedResultsFailureDiagnostics(diagnosticErrorWriter{err: writeErr}, data, errorsOut)
			},
			cause: writeErr,
		},
		{
			name: "issue view short write",
			write: func() error {
				return WriteIssueViewFailureDiagnostics(diagnosticShortWriter{}, data, errorsOut)
			},
			cause: io.ErrShortWrite,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.write()
			if !errors.Is(err, tt.cause) {
				t.Fatalf("diagnostic error = %v, want %v", err, tt.cause)
			}
			if _, ok := errors.AsType[*OutputError](err); !ok {
				t.Fatalf("diagnostic error type = %T, want *OutputError", err)
			}
		})
	}
}
