//go:build !windows && nativefilesystem

package contract

import "testing"

func TestNativePublicWindowsConfigurationJunctions(t *testing.T) {
	t.Fatal("native public junction configuration acceptance requires Windows execution")
}
