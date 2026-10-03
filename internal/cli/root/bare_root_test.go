package root

import (
	"bytes"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

type brokenHelpDestination struct{}

func (brokenHelpDestination) Write([]byte) (int, error) {
	return 0, errors.New("help destination unavailable")
}

func TestBareRootHelpDestinationFailureIsNotSuccess(t *testing.T) {
	var stdout bytes.Buffer
	program, sink := newProgram(testDeps(t), &stdout, brokenHelpDestination{})
	if exit := execute(t.Context(), program, sink, []string{"--output=human"}); exit != cli.ExitOutput {
		t.Fatalf("native help destination failure exited %d, want %d", exit, cli.ExitOutput)
	}
	if stdout.Len() != 0 || program.Root.OutOrStdout() != &stdout {
		t.Fatal("native help failure changed the owned stdout destination")
	}
}
