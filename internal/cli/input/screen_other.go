//go:build !unix

package input

import "io"

// PrepareScreen retains Bubble Tea's native console owner on other platforms.
func PrepareScreen(reader io.Reader) (io.Reader, func() error, error) { return reader, nil, nil }
