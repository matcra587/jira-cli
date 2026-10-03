package command_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

type shortHelpWriter struct{}

func (shortHelpWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestNativeHumanHelpUsesFrameworkAndPropagatesWrites(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		w      io.Writer
		failed bool
	}{
		{"healthy", &bytes.Buffer{}, false},
		{"failed", brokenWriter{}, true},
		{"short", shortHelpWriter{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			cmd := command.MustNew(command.Spec[envelope.AgentSchemaOutput]{
				Op: "root", Use: "probe", Short: "probe",
				HumanHelp: func(w io.Writer) error {
					called = true
					// Simulate framework help discarding a destination error.
					_, _ = io.WriteString(w, "native help\n")
					return nil
				},
				Run: func(context.Context, command.Invocation) (envelope.AgentSchemaOutput, error) {
					t.Fatal("native help invoked the discovery handler")
					return envelope.AgentSchemaOutput{}, nil
				},
			})
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(tc.w)
			cmd.SetContext(cli.WithFormat(t.Context(), output.FormatHuman))
			err := cmd.RunE(cmd, nil)
			var writeErr *output.WriteError
			if !called || stdout.Len() != 0 || cmd.OutOrStdout() != &stdout || !command.DeclaresHuman(cmd) || errors.As(err, &writeErr) != tc.failed {
				t.Fatal("native help lost its writer, restoration or destination failure")
			}
			if tc.name == "short" && !errors.Is(err, io.ErrShortWrite) {
				t.Fatal("native help discarded a short write")
			}
			if !tc.failed && (err != nil || tc.w.(*bytes.Buffer).String() != "native help\n") {
				t.Fatal("native help did not reach the selected help stream")
			}
		})
	}
}

func TestNativeHumanHelpKeepsTypedMachineOutput(t *testing.T) {
	t.Parallel()
	for _, format := range []output.Format{output.FormatJSON, output.FormatCompact} {
		var stdout, stderr bytes.Buffer
		cmd := command.MustNew(command.Spec[envelope.AgentSchemaOutput]{
			Op: "root", Use: "probe", Short: "probe",
			HumanHelp: func(io.Writer) error { t.Fatal("machine mode invoked native help"); return nil },
			Run: func(context.Context, command.Invocation) (envelope.AgentSchemaOutput, error) {
				return envelope.AgentSchemaOutput{Schema: json.RawMessage(`{"path":"fixture"}`)}, nil
			},
		})
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetContext(cli.WithFormat(t.Context(), format))
		if err := cmd.RunE(cmd, nil); err != nil || stderr.Len() != 0 {
			t.Fatal("native help declaration blocked machine discovery")
		}
		var doc map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		if format == output.FormatJSON {
			if doc["ok"] != true || doc["meta"].(map[string]any)["command"] != "root" {
				t.Fatal("root lost its shared invocation frame")
			}
			doc = doc["data"].(map[string]any)
		}
		if doc["schema"].(map[string]any)["path"] != "fixture" {
			t.Fatal("machine mode discarded discovery data")
		}
	}
}

func TestNewRejectsAmbiguousNativeHumanHelp(t *testing.T) {
	t.Parallel()
	for _, mutation := range []bool{false, true} {
		spec := command.Spec[envelope.AgentSchemaOutput]{
			Op: "root", Use: "probe", Short: "probe", HumanHelp: func(io.Writer) error { return nil }, MutatesLocalState: mutation,
			Run: func(context.Context, command.Invocation) (envelope.AgentSchemaOutput, error) {
				return envelope.AgentSchemaOutput{}, nil
			},
		}
		if !mutation {
			spec.Human = func(*output.View, envelope.Envelope) error { return nil }
		}
		if _, err := command.New(spec); err == nil {
			t.Fatal("native help accepted a mutation or conflicting human renderer")
		}
	}
}
