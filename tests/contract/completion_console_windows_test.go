package contract

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// Completion encoding changes belong to an owned console, not the caller's.
func powerShellCompletionConsole(process *exec.Cmd) {
	process.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
}
