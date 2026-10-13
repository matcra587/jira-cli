package command

import (
	"context"
	"sync"
)

// OnceContextValues preserves sync.OnceValues' result, error and panic semantics
// while passing the first invocation's context into a lazy credential-backed
// factory. This adapter is scoped to one command invocation. The standard memoizer
// releases its constructor closure after initialization, including on panic.
func OnceContextValues[T any](factory func(context.Context) (T, error)) func(context.Context) (T, error) {
	var initialize sync.Once
	var value func() (T, error)
	return func(ctx context.Context) (T, error) {
		initialize.Do(func() { value = sync.OnceValues(func() (T, error) { return factory(ctx) }) })
		return value()
	}
}
