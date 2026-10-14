//go:build !windows && nativefilesystem

package contract

import "testing"

func TestNativePublicWindowsAttachmentDirectories(t *testing.T) {
	t.Fatal("native attachment directory acceptance requires Windows execution")
}
