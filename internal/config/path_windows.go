package config

import "github.com/matcra587/jira-cli/internal/nativepath"

func absoluteConfigurationPath(path string) (string, error) { return nativepath.Abs(path) }

// Creation walks absent descendants to the nearest existing native ancestor.
func resolveConfigurationPath(path string) (string, error) {
	return resolveNativeConfigurationPath(path, nativepath.ResolveExisting)
}

func joinConfigurationPath(parent, path string) string { return nativepath.JoinLeaf(parent, path) }
