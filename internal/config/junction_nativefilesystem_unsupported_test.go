//go:build !windows && nativefilesystem

package config_test

import "testing"

func TestNativeWindowsConfigurationJunctions(t *testing.T) {
	t.Fatal("native junction configuration acceptance requires Windows execution")
}
