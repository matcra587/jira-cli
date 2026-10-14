//go:build !windows && nativeconsole

package prompt_test

import "testing"

func TestNativeWindowsAccessibleConsole(t *testing.T) {
	t.Fatal("native accessible console acceptance requires Windows execution")
}

func TestNativeWindowsConsoleOwner(t *testing.T) {
	t.Fatal("native console input ownership acceptance requires Windows execution")
}
