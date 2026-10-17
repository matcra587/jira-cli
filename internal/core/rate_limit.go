package core

import (
	"context"
	"sync/atomic"
)

type rateLimitObservationKey struct{}

// WithRateLimitObservation gives one invocation its own concurrent observation
// state. Derived request contexts share it; independent invocations never do.
func WithRateLimitObservation(ctx context.Context) context.Context {
	return context.WithValue(ctx, rateLimitObservationKey{}, &atomic.Bool{})
}

// ObserveRateLimitNear records an authenticated Jira reply's near-limit signal.
// It records a fact only; adapters cannot publish output through this owner.
func ObserveRateLimitNear(ctx context.Context) {
	if observed, ok := ctx.Value(rateLimitObservationKey{}).(*atomic.Bool); ok {
		observed.Store(true)
	}
}

// TakeRateLimitNear consumes the invocation's signal once after request workers
// have joined, including when the operation failed or was later canceled.
func TakeRateLimitNear(ctx context.Context) bool {
	if observed, ok := ctx.Value(rateLimitObservationKey{}).(*atomic.Bool); ok {
		return observed.Swap(false)
	}
	return false
}
