package contract

import (
	"slices"
	"sync"
)

// requestCapture synchronizes HTTP handler writes with test assertions.
// Waiting for a subprocess does not synchronize goroutines in the test server.
type requestCapture[T any] struct {
	mu     sync.Mutex
	values []T
}

func (c *requestCapture[T]) Add(value T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values = append(c.values, value)
}

func (c *requestCapture[T]) Values() []T {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.values)
}

func (c *requestCapture[T]) Last() T {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.values) == 0 {
		var zero T
		return zero
	}
	return c.values[len(c.values)-1]
}

func (c *requestCapture[T]) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values = nil
}
