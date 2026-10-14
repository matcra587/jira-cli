//go:build !unix && !windows

package input

import (
	"io"
	"os"
)

// PrepareStream preserves native readers on platforms without a stream owner.
// Active interruption of borrowed files requires a native implementation.
func PrepareStream(reader io.Reader, _ os.FileMode) (io.Reader, func() error, error) {
	return reader, nil, nil
}
