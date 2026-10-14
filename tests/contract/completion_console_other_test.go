//go:build !windows

package contract

import "os/exec"

func powerShellCompletionConsole(_ *exec.Cmd) {}
