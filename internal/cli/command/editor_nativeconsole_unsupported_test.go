//go:build !windows && nativeconsole

package command_test

import "testing"

func TestNativeWindowsEditorConsole(t *testing.T) {
	t.Fatal("native editor console acceptance requires Windows execution")
}
