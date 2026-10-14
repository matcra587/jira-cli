//go:build !windows && nativefilesystem

package contract

import "testing"

func TestNativePublicWindowsAttachmentFilenames(t *testing.T) {
	t.Fatal("native attachment filename acceptance requires Windows execution")
}
