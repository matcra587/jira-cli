package core

import (
	"context"
	"sync"
	"testing"
)

func TestRateLimitObservationIsInvocationLocal(t *testing.T) {
	t.Parallel()
	parent := t.Context()
	first := WithRateLimitObservation(parent)
	second := WithRateLimitObservation(parent)
	child, cancel := context.WithCancel(first)
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() { ObserveRateLimitNear(child) })
	}
	workers.Wait()
	cancel()
	if TakeRateLimitNear(parent) || TakeRateLimitNear(second) {
		t.Fatal("observation escaped its invocation")
	}
	if !TakeRateLimitNear(first) || TakeRateLimitNear(child) {
		t.Fatal("completed observations must survive cancellation and publish once")
	}
	ObserveRateLimitNear(parent)
	if TakeRateLimitNear(parent) {
		t.Fatal("a context without an invocation collector recorded state")
	}
}
