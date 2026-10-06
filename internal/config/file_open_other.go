//go:build !unix

package config

import "os"

func openFileInput(path string) (*os.File, error) {
	return os.Open(path) //nolint:gosec // selected local input; the caller validates the descriptor
}
