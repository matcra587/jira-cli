package native_test

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// The encoding fixture must not borrow the user's console code page.
func pwshOwnedConsole(process *exec.Cmd) {
	process.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
}
