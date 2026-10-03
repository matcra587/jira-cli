package command

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// Cancellation must stop the animation without detaching the operation that
// owns the result. A task can finish reporting an applied write after it sees
// cancellation; returning first loses that receipt and races its producer.
func TestSpinJoinsCanceledTaskAndKeepsItsReceipt(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	entered := make(chan struct{})
	observedCancel := make(chan struct{})
	unblock := make(chan struct{})
	finished := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(unblock) }) }
	type receipt struct {
		data string
		err  error
	}
	done := make(chan receipt, 1)
	go func() {
		defer close(finished)
		data, err := Spin(ctx, "Recording the write", func(taskCtx context.Context) (string, error) {
			close(entered)
			<-taskCtx.Done()
			close(observedCancel)
			<-unblock
			return "write receipt", taskCtx.Err()
		})
		done <- receipt{data: data, err: err}
	}()
	t.Cleanup(func() {
		release()
		<-finished
	})
	<-entered
	cancel()
	<-observedCancel
	select {
	case <-done:
		t.Fatal("Spin returned while the canceled task still owned an unfinished receipt")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	got := <-done
	if got.data != "write receipt" || !errors.Is(got.err, context.Canceled) {
		t.Fatalf("canceled task receipt was discarded: data=%q err=%v", got.data, got.err)
	}
}

// A fan-out may record cancellation per item and return a nil aggregate error.
// Both the task and cancellation can then be ready for clog's select. Exercise
// that race repeatedly: choosing task completion must not mean success.
func TestSpinCancellationCannotBecomeSuccess(t *testing.T) {
	t.Parallel()
	for attempt := range 128 {
		ctx, cancel := context.WithCancel(t.Context())
		data, err := Spin(ctx, "Recording outcomes", func(context.Context) (int, error) {
			cancel()
			return attempt, nil
		})
		cancel()
		if data != attempt || !errors.Is(err, context.Canceled) {
			t.Fatalf("attempt %d lost cancellation or receipt: data=%d err=%v", attempt, data, err)
		}
	}
}

func TestSpinExpiredDeadlineCannotBecomeSuccess(t *testing.T) {
	t.Parallel()
	for attempt := range 128 {
		ctx, cancel := context.WithDeadline(t.Context(), time.Time{})
		data, err := Spin(ctx, "Recording outcomes", func(context.Context) (int, error) {
			return attempt, nil
		})
		cancel()
		if data != attempt || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("attempt %d lost deadline or receipt: data=%d err=%v", attempt, data, err)
		}
	}
}

func TestSpinKeepsTaskErrorAndSuccessfulReceipt(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("write outcome unknown")
	for _, callErr := range []error{nil, wantErr} {
		data, err := Spin(t.Context(), "Recording outcomes", func(context.Context) (string, error) {
			return "write receipt", callErr
		})
		if data != "write receipt" || !errors.Is(err, callErr) {
			t.Fatalf("task result changed: data=%q err=%v, want %v", data, err, callErr)
		}
	}
}
