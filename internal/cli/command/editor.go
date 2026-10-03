package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"time"

	xos "github.com/gechr/x/os"
	"golang.org/x/term"

	"github.com/matcra587/jira-cli/internal/cli"
)

// RunEditor runs the configured editor with its screen on stderr, preserving
// stdout for the command's result. The command boundary owns the borrowed
// terminal descriptors and restores their modes before returning.
//
// Cancellation interrupts the supervised process on Unix, allowing it to save
// work and exit. An editor that ignores cancellation is killed after two
// seconds. Editors that launch another application must be configured to wait
// for it; this owner does not supervise independent background applications.
func RunEditor(ctx context.Context, editor []string, path string) error {
	return runEditor(ctx, editor, path, os.Stdin, os.Stderr)
}

func runEditor(ctx context.Context, editor []string, path string, stdin, diagnostics *os.File) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(editor) == 0 || editor[0] == "" {
		return cli.NewInputError(cli.CodeEditorMissing, "no editor configured")
	}

	// gechr/x/terminal v0.6.0 detects terminals but does not capture or restore
	// modes. Use its already-selected golang.org/x/term v0.45.0 dependency for
	// this ownership boundary. Input and diagnostics can be different terminals.
	for _, stream := range []struct {
		name string
		file *os.File
	}{{"stdin", stdin}, {"stderr", diagnostics}} {
		if stream.file == nil {
			return cli.NewInputError(cli.CodeTerminalRequired, "an editor needs a terminal on "+stream.name)
		}
		descriptor := int(stream.file.Fd())
		state, captureErr := term.GetState(descriptor)
		if captureErr != nil {
			return fmt.Errorf("%w: %w", cli.NewInputError(cli.CodeTerminalRequired, "capture editor terminal on "+stream.name), captureErr)
		}
		defer func() {
			if restoreErr := term.Restore(descriptor, state); restoreErr != nil {
				failure := cli.NewInputError(cli.CodeTerminalRestoreFailed,
					"restore editor terminal on "+stream.name+"; restore the terminal before continuing")
				err = errors.Join(fmt.Errorf("%w: %w", failure, restoreErr), err)
			}
		}()
	}

	// Clone the arguments so appending the scratch path cannot mutate the
	// configured editor value retained in a receipt. No shell evaluates it.
	args := append(slices.Clone(editor[1:]), path)
	process := exec.CommandContext(ctx, editor[0], args...) //nolint:gosec // the editor is the user's configured executable
	process.Stdin, process.Stdout, process.Stderr = stdin, diagnostics, diagnostics
	process.Cancel = func() error {
		if xos.IsUnix() {
			return process.Process.Signal(os.Interrupt)
		}
		return process.Process.Kill()
	}
	process.WaitDelay = 2 * time.Second
	if runErr := process.Run(); runErr != nil {
		failure := cli.NewInputError(cli.CodeEditorFailed, fmt.Sprintf("editor %q failed", editor[0]))
		err = fmt.Errorf("%w: %w", failure, runErr)
	}
	// os/exec can prefer an unsuccessful process exit over context cancellation.
	// Retain the context cause even when the editor exits unsuccessfully, or
	// successfully in response to the interrupt, so saved work is never published.
	return errors.Join(ctx.Err(), err)
}
