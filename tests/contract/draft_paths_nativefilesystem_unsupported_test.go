//go:build !windows && nativefilesystem

package contract

import "testing"

func TestNativePublicWindowsDraftPaths(t *testing.T) {
	t.Fatal("native public draft path acceptance requires Windows execution")
}
