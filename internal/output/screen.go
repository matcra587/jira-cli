package output

import (
	"context"
	"errors"
	"io"

	tea "charm.land/bubbletea/v2"

	inputio "github.com/matcra587/jira-cli/internal/cli/input"
)

// RunScreen owns Bubble Tea's borrowed streams and cancellation scope. Like
// the document pager, it retains destination failures hidden by Tea's renderer.
// Root already owns process signals; a second signal handler must not compete.
func RunScreen(ctx context.Context, input io.Reader, destination io.Writer, model func(context.Context) tea.Model) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	session, cancel := context.WithCancel(ctx)
	defer cancel()
	prepared, cleanup, err := inputio.PrepareScreen(input)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer func() { err = errors.Join(err, cleanup()) }()
	}
	tracked, writer := newTrackingWriter(destination)
	_, err = tea.NewProgram(model(session), tea.WithContext(session), tea.WithInput(prepared),
		tea.WithOutput(writer), tea.WithoutSignalHandler()).Run()
	if tracked.err != nil {
		return errors.Join(err, &WriteError{Err: tracked.err})
	}
	return errors.Join(ctx.Err(), err)
}
