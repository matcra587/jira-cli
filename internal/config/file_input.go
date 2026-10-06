package config

import (
	"errors"
	"fmt"
	"io"
)

// readFileInput accepts regular local configuration inputs and follows symlinks.
// Checking the opened descriptor avoids trusting a stale pathname observation.
// Unix opens are nonblocking so a FIFO can be rejected before reading bytes.
// This does not bound regular-file size or interrupt arbitrary filesystem I/O.
func readFileInput(path string) (data []byte, err error) {
	file, err := openFileInput(path)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: configuration input must be a regular file", ErrFileInvalid)
	}
	return io.ReadAll(file)
}
