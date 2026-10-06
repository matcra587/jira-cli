//go:build !unix

package adapters

import "os"

// Preserve native Root opening on other platforms. The shared reader checks
// descriptor regularity; Unix nonblocking and no-follow guarantees need separate
// native equivalents and acceptance before they can be claimed here.
func openCacheEntry(root cacheReadRoot, name string) (*os.File, error) {
	return root.Open(name)
}
