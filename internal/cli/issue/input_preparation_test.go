package issue

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

type preparationPipeReader struct {
	*io.PipeReader
	started chan struct{}
	once    sync.Once
}

func (reader *preparationPipeReader) Read(p []byte) (int, error) {
	reader.once.Do(func() { close(reader.started) })
	return reader.PipeReader.Read(p)
}

func TestIssueInputPreparationCancellationInterruptsOwnedMetadataInput(t *testing.T) {
	t.Parallel()
	pipe, writer := io.Pipe()
	t.Cleanup(func() {
		if err := pipe.Close(); err != nil {
			t.Error("close owned metadata reader", err)
		}
		if err := writer.Close(); err != nil {
			t.Error("close owned metadata writer", err)
		}
	})
	reader := &preparationPipeReader{PipeReader: pipe, started: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		out, err := runInputPreparation(ctx, command.Invocation{Stdin: reader}, "input-must-not-open", "-", false)
		if len(out.Payload) != 0 {
			err = errors.New("canceled input published a candidate")
		}
		result <- err
	}()
	select {
	case <-reader.started:
	case <-time.After(2 * time.Second):
		t.Fatal("metadata read never entered its owned stream")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost to input or publication: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled metadata input left its handler blocked")
	}
}
