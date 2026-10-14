//go:build !windows

package native_test

import "os/exec"

func pwshOwnedConsole(_ *exec.Cmd) {}
