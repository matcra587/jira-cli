//go:build outputaudit && linux

package audit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"golang.org/x/sys/unix"
)

// Real terminal descriptors let the corpus verify editor success instead of
// exempting it. x/terminal detects terminals but does not allocate native PTYs.
func attachAuditTerminal(cmd *exec.Cmd, stderr *bytes.Buffer) (func() error, error) {
	descriptor, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	master := os.NewFile(uintptr(descriptor), "audit-editor-terminal")
	if err := unix.IoctlSetPointerInt(descriptor, unix.TIOCSPTLCK, 0); err != nil {
		return nil, errors.Join(err, master.Close())
	}
	number, err := unix.IoctlGetInt(descriptor, unix.TIOCGPTN)
	if err != nil {
		return nil, errors.Join(err, master.Close())
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR, 0)
	if err != nil {
		return nil, errors.Join(err, master.Close())
	}
	cmd.Stdin, cmd.Stderr = slave, slave
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(stderr, master)
		if errors.Is(err, unix.EIO) {
			err = nil
		} // last slave close ends a Linux PTY with EIO
		done <- err
	}()
	return sync.OnceValue(func() error {
		closeErr := slave.Close()
		copyErr := <-done
		return errors.Join(closeErr, copyErr, master.Close())
	}), nil
}
