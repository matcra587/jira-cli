package command_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"testing/synctest"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

func TestInputCancellationJoinsCloseBeforeReturn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		reader := &delayedInputClose{closed: make(chan struct{}), release: make(chan struct{})}
		finished := make(chan error, 1)
		go func() {
			_, err := command.ReadInputFileContext(ctx, "file", command.StdinPath, reader, false)
			finished <- err
		}()
		synctest.Wait()
		cancel()
		synctest.Wait()
		early := false
		var err error
		select {
		case err = <-finished:
			early = true
		default:
		}
		close(reader.release)
		synctest.Wait()
		if !early {
			err = <-finished
		}
		if early {
			t.Fatal("input returned while its cancellation close callback was still running")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("input cancellation error=%v", err)
		}
	})
}

type delayedInputClose struct {
	closed  chan struct{}
	release chan struct{}
}

func (r *delayedInputClose) Read([]byte) (int, error) {
	<-r.closed
	return 0, io.ErrClosedPipe
}

func (r *delayedInputClose) Close() error {
	close(r.closed)
	<-r.release
	return nil
}
