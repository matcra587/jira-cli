//go:build !unix

package config

import "os"

func openThemeInput(path string) (*os.File, error) {
	return os.Open(path) //nolint:gosec // explicit custom-theme input; the caller validates metadata and the descriptor
}
