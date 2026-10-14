//go:build !windows

package config

import (
	"path/filepath"

	xfilepath "github.com/gechr/x/filepath"
)

func absoluteConfigurationPath(path string) (string, error) {
	return filepath.Abs(path)
}

func resolveConfigurationPath(path string) (string, error) {
	return xfilepath.ResolveLenient(path)
}

func joinConfigurationPath(parent, path string) string {
	return filepath.Join(parent, filepath.Base(path))
}
