package config

import (
	"errors"

	"github.com/matcra587/jira-cli/internal/nativepath"
)

// Only the resolver's initial object-open refusal proves absence. A later
// missing-shaped metadata or cleanup failure must retain its actual outcome.
func resolveNativeConfigurationPath(path string, resolve func(string) (string, error)) (string, error) {
	resolved, err := resolve(path)
	if errors.Is(err, nativepath.ErrMissing) {
		return path, nil
	}
	return resolved, err
}
