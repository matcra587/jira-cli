package output

import (
	"bytes"
	"testing"

	"github.com/gechr/clog"
)

func TestProgramStatusRedirectedOutputIsUntouched(t *testing.T) {
	t.Setenv("TERM", "xterm")
	var destination bytes.Buffer
	destination.WriteString("existing output\n")
	ctx, finish := WithProgramStatus(t.Context(), &destination)
	StartProgramStatus(ctx)
	WaitForProgramInput(ctx)()
	finish(clog.ProgramStateError)
	if got := destination.String(); got != "existing output\n" {
		t.Fatalf("status changed redirected output: %q", got)
	}
}
