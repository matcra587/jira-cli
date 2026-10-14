package root

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
)

func TestIssueInputPreparationOutputFailureRetainsSharedExitPolicy(t *testing.T) {
	fixture := t.TempDir()
	input, metadata := filepath.Join(fixture, "input.json"), filepath.Join(fixture, "metadata.json")
	if err := os.WriteFile(input, []byte(`{"fields":{"project":"KEY","issuetype":"Task"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metadata, []byte(`{"fields":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			deps := testDeps(t)
			deps.JiraClient = func(context.Context) (core.JiraClient, error) {
				t.Fatal("local authoring opened Jira")
				return nil, nil
			}
			deps.Credentials = func() (core.CredentialStore, error) { t.Fatal("local authoring opened credentials"); return nil, nil }
			var stderr bytes.Buffer
			program, sink := newProgram(deps, brokenHelpDestination{}, &stderr)
			exit := execute(t.Context(), program, sink, []string{"issue", "input", "prepare", "--create", "--input", input, "--metadata", metadata, "--output=" + mode})
			if exit != cli.ExitOutput {
				t.Fatalf("failed result destination reported success or a different exit: %d stderr=%q", exit, stderr.String())
			}
		})
	}
	// Agent selection remains on the same typed path and still opens no adapters.
	t.Setenv("AGENT", "1")
	deps := testDeps(t)
	deps.JiraClient = func(context.Context) (core.JiraClient, error) {
		t.Fatal("agent authoring opened Jira")
		return nil, nil
	}
	deps.Credentials = func() (core.CredentialStore, error) { t.Fatal("agent authoring opened credentials"); return nil, nil }
	var stdout bytes.Buffer
	program, sink := newProgram(deps, &stdout, io.Discard)
	if exit := execute(t.Context(), program, sink, []string{"issue", "input", "prepare", "--create", "--input", input, "--metadata", metadata}); exit != 0 || !bytes.Contains(stdout.Bytes(), []byte(`"payload"`)) {
		t.Fatalf("agent local authoring failed: %d %s", exit, stdout.String())
	}
}
