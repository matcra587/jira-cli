package command_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

func TestContextFactoryPreservesLazyMemoization(t *testing.T) {
	first, cancel := context.WithCancel(t.Context())
	defer cancel()
	cause := errors.New("factory failure")
	var calls atomic.Int64
	factory := command.OnceContextValues(func(ctx context.Context) (int, error) {
		calls.Add(1)
		if ctx != first {
			t.Error("factory did not receive caller context")
		}
		return 17, cause
	})
	if calls.Load() != 0 {
		t.Fatal("factory initialized eagerly")
	}
	var wait sync.WaitGroup
	for range 16 {
		wait.Go(func() {
			value, err := factory(first)
			if value != 17 || !errors.Is(err, cause) {
				t.Errorf("memoized result changed: value=%d error=%v", value, err)
			}
		})
	}
	wait.Wait()
	second, done := context.WithCancel(t.Context())
	defer done()
	value, err := factory(second)
	if calls.Load() != 1 || value != 17 || !errors.Is(err, cause) {
		t.Fatalf("factory did not memoize result/error: calls=%d value=%d error=%v", calls.Load(), value, err)
	}
}

func TestContextFactoryPreservesPanicReplay(t *testing.T) {
	marker := &struct{ label string }{label: "factory panic"}
	calls := 0
	factory := command.OnceContextValues(func(context.Context) (int, error) { calls++; panic(marker) })
	for range 2 {
		func() {
			defer func() {
				if recover() != marker {
					t.Error("factory panic was not replayed")
				}
			}()
			_, _ = factory(t.Context())
		}()
	}
	if calls != 1 {
		t.Fatalf("panicking factory ran %d times", calls)
	}
}
