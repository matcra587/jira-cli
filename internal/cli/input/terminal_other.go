//go:build !windows

package input

import (
	"context"
	"io"
)

// RunTerminalContext preserves other platforms' existing terminal owners.
// Its callers retain their selected stream and password cancellation policies.
func RunTerminalContext(_ context.Context, reader io.Reader, run func(io.Reader) error) (err, cleanupErr error) {
	return run(reader), nil
}
