package agent

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/agentguides"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func TestExportRetainsCancellationReceipt(t *testing.T) {
	t.Parallel()
	guides, err := agentguides.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			cmd := newExport(guides, func(_ context.Context, _ string, artifacts []agentguides.ExportArtifact, _ bool) (agentguides.ExportReceipt, error) {
				return agentguides.ExportReceipt{Written: []string{artifacts[0].Path}, Attempted: artifacts[1].Path}, cause
			})
			if err := cmd.ParseFlags([]string{"--format", "agent-skill", "--dir", filepath.Join(t.TempDir(), "skills")}); err != nil {
				t.Fatal(err)
			}
			cmd.SetContext(t.Context())
			err := cmd.RunE(cmd, nil)
			var retained *cli.ResultError
			if !errors.Is(err, cause) || !errors.As(err, &retained) {
				t.Fatal("export discarded its cancellation cause or partial receipt")
			}
			out, ok := retained.Data.(envelope.GuideExportOutput)
			if !ok || !out.Attempted || len(out.Artifacts) != guides.Len() || len(out.WrittenPaths) != 1 || out.WrittenPaths[0] != out.Artifacts[0].Path || out.AttemptedPath != out.Artifacts[1].Path || !out.Artifacts[0].Written || out.Artifacts[1].Written || retained.Op != "agent.export" || !retained.Mutation || retained.Human == nil {
				t.Fatal("export lost its complete plan, published prefix or failure-rendering metadata")
			}
		})
	}
}

func TestExportDryRunNeverInvokesWriter(t *testing.T) {
	t.Parallel()
	guides, err := agentguides.Load()
	if err != nil {
		t.Fatal(err)
	}
	cmd := newExport(guides, func(context.Context, string, []agentguides.ExportArtifact, bool) (agentguides.ExportReceipt, error) {
		t.Fatal("export preview invoked the filesystem writer")
		return agentguides.ExportReceipt{}, nil
	})
	if err := cmd.ParseFlags([]string{"--format", "agent-skill", "--dir", filepath.Join(t.TempDir(), "skills"), "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	cmd.SetContext(cli.WithFormat(t.Context(), output.FormatJSON))
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
}
