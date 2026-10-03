//go:build !unix

package command

import "io"

// Non-Unix streams retain their native close/cancellation behavior.
func preparePipeInput(input io.Reader) (io.Reader, func() error, error) {
	return input, nil, nil
}
