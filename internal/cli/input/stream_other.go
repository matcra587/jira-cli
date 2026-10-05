//go:build !unix

package input

import (
	"io"
	"os"
)

// PrepareStream preserves native non-Unix readers. Active interruption of
// borrowed non-Unix files still requires a native implementation and proof.
func PrepareStream(reader io.Reader, _ os.FileMode) (io.Reader, func() error, error) {
	return reader, nil, nil
}
