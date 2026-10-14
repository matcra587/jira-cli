package command

import (
	"io"
	"os"

	"github.com/matcra587/jira-cli/internal/cli/input"
)

// preparePipeInput retains the body/token stream policy: Unix pipes/sockets and
// Windows pipes receive owned duplicates. Terminal readers are a separate
// interactive form capability owned by the prompt package.
func preparePipeInput(reader io.Reader) (io.Reader, func() error, error) {
	return input.PrepareStream(reader, os.ModeNamedPipe|os.ModeSocket)
}
