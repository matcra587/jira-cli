//go:build !windows && nativefilesystem

package issueedit

import "testing"

func TestNativeWindowsDraftPaths(t *testing.T) {
	t.Fatal("native draft path acceptance requires Windows execution")
}
