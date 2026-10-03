//go:build unix

package selfupdate

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// configureUpdateProcess owns a separate group so cancellation also stops
// ordinary installer descendants. CommandContext otherwise kills only brew;
// WaitDelay closes pipes but leaves children able to finish a canceled install.
// x/shell 0.6.0 exposes process discovery, not process-group cancellation.
func configureUpdateProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := unix.Kill(-cmd.Process.Pid, unix.SIGKILL); err != nil {
			if errors.Is(err, unix.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
}
