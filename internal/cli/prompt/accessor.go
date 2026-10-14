package prompt

import (
	"context"

	"charm.land/huh/v2"
)

// WithContextAccessor retains Huh's native binding and refuses publication after
// cancellation. Huh's accessible reader can return a rejected earlier answer
// after EOF, including when a native read was interrupted by the caller.
func WithContextAccessor[T any](ctx context.Context, accessor huh.Accessor[T]) huh.Accessor[T] {
	return contextAccessor[T]{
		Accessor: accessor,
		publish: func(value T) {
			if ctx.Err() == nil {
				accessor.Set(value)
			}
		},
	}
}

type contextAccessor[T any] struct {
	huh.Accessor[T]
	publish func(T)
}

func (a contextAccessor[T]) Set(value T) { a.publish(value) }
