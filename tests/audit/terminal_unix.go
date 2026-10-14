//go:build outputaudit && (linux || darwin)

package audit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/gechr/x/terminal"
	"golang.org/x/sys/unix"
)

const auditDiagnosticLimit = 1 << 20

func runAuditTerminal(ctx context.Context, cmd *exec.Cmd, stderr *bytes.Buffer) (code int, err error) {
	defer func() {
		if cause := context.Cause(ctx); cause != nil && !errors.Is(err, cause) {
			err = errors.Join(err, cause)
		}
	}()
	master, slave, err := openAuditPTY()
	if err != nil {
		return 0, fmt.Errorf("terminal invocation is unrun: %w", err)
	}
	defer func() { err = errors.Join(err, master.Close()) }()
	if !terminal.Is(slave) {
		return 0, errors.Join(errors.New("native audit slave is not a terminal"), slave.Close())
	}
	readContext, cancelRead := context.WithCancelCause(ctx)
	defer cancelRead(nil)
	cmd.Stdin, cmd.Stderr = slave, slave
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = 2 * time.Second
	}
	collected := make(chan error, 1)
	go func() { collected <- collectAuditTerminal(readContext, int(master.Fd()), stderr) }()
	if startErr := cmd.Start(); startErr != nil {
		closeErr := slave.Close()
		cancelRead(startErr)
		return 0, errors.Join(startErr, closeErr, <-collected, context.Cause(ctx))
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	var runErr, captureErr error
	collectorJoined := false
	select {
	case runErr = <-finished:
	case captureErr = <-collected:
		collectorJoined = true
		if captureErr != nil {
			killErr := cmd.Process.Kill()
			if !errors.Is(killErr, os.ErrProcessDone) {
				captureErr = errors.Join(captureErr, killErr)
			}
		}
		runErr = <-finished
	}
	closeErr := slave.Close()
	if !collectorJoined {
		// A descendant can retain the slave after the command exits. Neither
		// that handle nor an unfinished read may keep the audit alive forever.
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case captureErr = <-collected:
		case <-timer.C:
			cancelRead(errors.New("terminal diagnostic collector did not reach EOF"))
			captureErr = <-collected
		}
	}
	code, runErr = auditCommandExit(runErr)
	return code, errors.Join(runErr, captureErr, closeErr, context.Cause(ctx))
}

// The nonblocking master is owned until this reader joins. Polling avoids a
// blocking read that survives cancellation and does not rely on kqueue support
// for PTYs. Only Linux's documented final-slave EIO is an ordinary end of stream.
func collectAuditTerminal(ctx context.Context, descriptor int, stderr *bytes.Buffer) error {
	var buffer [32 << 10]byte
	for {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		n, err := unix.Read(descriptor, buffer[:])
		if n > 0 {
			if n > auditDiagnosticLimit-stderr.Len() {
				return errors.New("terminal diagnostics exceed their byte bound")
			}
			_, _ = stderr.Write(buffer[:n]) // bytes.Buffer.Write always succeeds.
		}
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EAGAIN):
			if _, err := unix.Poll([]unix.PollFd{{Fd: int32(descriptor), Events: unix.POLLIN}}, 25); err != nil && !errors.Is(err, unix.EINTR) {
				return err
			}
		case runtime.GOOS == "linux" && errors.Is(err, unix.EIO):
			return nil
		case err != nil:
			return err
		case n == 0:
			return nil
		}
	}
}
