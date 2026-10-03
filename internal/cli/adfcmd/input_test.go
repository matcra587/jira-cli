package adfcmd

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
)

type unreadableInput struct{}

func (unreadableInput) Read([]byte) (int, error) { panic("input must not be read") }

func TestADFInputRefusesTerminalBeforeReading(t *testing.T) {
	t.Parallel()
	_, err := readInput(t.Context(), command.Invocation{Stdin: unreadableInput{}, StdinIsTerminal: true}, command.StdinPath)
	var input *cli.InputError
	if !errors.As(err, &input) || input.Code != cli.CodeFlagValueInvalid || input.Field != flagInput {
		t.Fatal("terminal stdin was not refused before reading")
	}
}

func TestADFInputCancellationPreventsIO(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := readInput(ctx, command.Invocation{Stdin: unreadableInput{}}, command.StdinPath)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("canceled local input attempted IO")
	}
}

func TestADFInputEnforcesSharedBodyLimit(t *testing.T) {
	t.Parallel()
	// The public body contract is one MiB, independently pinned here so an
	// accidental growth at the shared owner is detected through this route.
	_, err := readInput(t.Context(), command.Invocation{Stdin: strings.NewReader(strings.Repeat("x", (1<<20)+1))}, command.StdinPath)
	var input *cli.InputError
	if !errors.As(err, &input) || input.Code != cli.CodeFlagValueInvalid || input.Field != flagInput || !strings.Contains(input.Message, "1048576-byte") {
		t.Fatal("oversized local input bypassed the shared body limit")
	}
}

type signaledInput struct {
	io.ReadCloser
	started chan struct{}
	once    sync.Once
}

func (r *signaledInput) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	return r.ReadCloser.Read(p)
}

func TestADFInputCancellationInterruptsBlockedPipe(t *testing.T) {
	t.Parallel()
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	input := &signaledInput{ReadCloser: reader, started: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := readInput(ctx, command.Invocation{Stdin: input}, command.StdinPath)
		done <- err
	}()
	<-input.started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled input changed classification: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation left input blocked until the writer closes")
	}
}
