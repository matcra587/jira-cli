//go:build outputaudit

package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"github.com/matcra587/jira-cli/tests/internal/nativeaudit"
	"github.com/matcra587/jira-cli/tests/internal/nativejob"
)

const terminalCaptureLimit = 1 << 20

type auditTerminalReceipt struct {
	Observation  nativeaudit.Observation
	ModesIntact  bool
	OutputSHA256 string
	OutputBytes  int
}

// The coordinator owns the only outer job handle. The helper waits for its
// admission record before constructing a CLI; every descendant then inherits
// job membership without inheriting a keeper that could survive the parent.
func runAuditTerminal(ctx context.Context, cmd *exec.Cmd, _ *bytes.Buffer) (code int, err error) {
	defer func() {
		if cause := context.Cause(ctx); cause != nil && !errors.Is(err, cause) {
			err = errors.Join(err, cause)
		}
	}()
	if len(cmd.Args) == 0 || cmd.Env == nil || cmd.Stdin != nil || cmd.Stdout == nil {
		return 0, errors.New("native audit terminal requires explicit argv, environment, stdout and terminal input")
	}
	directory, err := os.MkdirTemp("", "jira-audit-terminal-")
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(directory)) }()
	self, err := os.Executable()
	if err != nil {
		return 0, err
	}
	job, err := nativejob.New()
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, job.Close()) }()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}
	args := []string{"--audit-terminal", directory, strconv.FormatInt(deadline.UnixNano(), 10)}
	args = append(args, cmd.Args...)
	helper := exec.CommandContext(ctx, self, args...)
	helper.Dir, helper.Env = cmd.Dir, slices.Clone(cmd.Env)
	helper.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	helper.WaitDelay = 2 * time.Second
	// Cancellation before assignment must also terminate the waiting helper.
	// After assignment, the job terminates both the debugger and its descendants.
	helper.Cancel = func() error {
		killErr := helper.Process.Kill()
		if errors.Is(killErr, os.ErrProcessDone) {
			killErr = nil
		}
		return errors.Join(job.Terminate(1), killErr)
	}
	var stdout, diagnostics boundedAuditBuffer
	helper.Stdout, helper.Stderr = &stdout, &diagnostics
	if err := helper.Start(); err != nil {
		return 0, fmt.Errorf("native audit helper did not start: %w", err)
	}
	finished := make(chan error, 1)
	go func() { finished <- helper.Wait() }()
	joined := false
	defer func() {
		if terminateErr := job.Terminate(1); terminateErr != nil {
			err = errors.Join(err, terminateErr)
		}
		if !joined {
			killErr := helper.Process.Kill()
			if !errors.Is(killErr, os.ErrProcessDone) {
				err = errors.Join(err, killErr)
			}
			<-finished
		}
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		err = errors.Join(err, job.WaitEmpty(cleanupContext))
	}()
	if err := job.AssignPID(uint32(helper.Process.Pid)); err != nil {
		return 0, fmt.Errorf("native audit helper was not admitted: %w", err)
	}
	if err := context.Cause(ctx); err != nil {
		return 0, err
	}
	readyTicker := time.NewTicker(5 * time.Millisecond)
	defer readyTicker.Stop()
	for {
		if _, statErr := os.Stat(filepath.Join(directory, "console.ready")); statErr == nil {
			break
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return 0, statErr
		}
		select {
		case <-ctx.Done():
			return 0, context.Cause(ctx)
		case earlyErr := <-finished:
			joined = true
			return 0, errors.Join(errors.New("native audit console never became ready: "+diagnostics.String()), earlyErr)
		case <-readyTicker.C:
		}
	}
	baseline, err := job.Snapshot()
	if err != nil || baseline.Total == 0 || baseline.Active == 0 {
		return 0, errors.Join(errors.New("native audit console has no live admitted supervisor"), err)
	}
	if err := context.Cause(ctx); err != nil {
		return 0, err
	}
	if err := publishAuditTerminalFile(directory, "admitted", nil); err != nil {
		return 0, err
	}
	runErr := <-finished
	joined = true
	if cause := context.Cause(ctx); cause != nil {
		return 0, cause
	}
	if runErr != nil {
		return 0, fmt.Errorf("native audit helper failed before validated capture: %w; %s", runErr, diagnostics.String())
	}
	if diagnostics.Len() != 0 || stdout.failed || diagnostics.failed {
		return 0, errors.New("native audit supervisor polluted or exceeded its control streams")
	}
	receiptFile, err := os.Open(filepath.Join(directory, "receipt.json"))
	if err != nil {
		return 0, fmt.Errorf("native audit helper published no receipt: %w", err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(receiptFile, terminalCaptureLimit+1))
	if err := errors.Join(readErr, receiptFile.Close()); err != nil {
		return 0, err
	}
	if len(raw) > terminalCaptureLimit {
		return 0, errors.New("native audit receipt exceeds its bound")
	}
	var receipt auditTerminalReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return 0, err
	}
	observation := receipt.Observation
	if !observation.Admitted || !observation.Joined || !observation.CollectorJoined || !observation.Armed || !observation.LoaderHandled || !observation.JobEmpty || !receipt.ModesIntact || observation.Error != "" || observation.Refusal != "" || observation.NativeExit != 0 || len(observation.Processes) == 0 || observation.EventFilesOpened == 0 || observation.EventFilesOpened != observation.EventFilesClosed {
		return 0, errors.New("native audit receipt did not establish silent terminal execution")
	}
	for _, process := range observation.Processes {
		if process == nil || !process.Armed || !process.LoaderHandled || !process.Exited || process.Exit != 0 {
			return 0, errors.New("native audit receipt contains an unverified process")
		}
	}
	// Native console hosts belong to setup, before CLI admission. Terminate any
	// remaining owned host after the debugger and observed Go processes join.
	if err := job.Terminate(1); err != nil {
		return 0, err
	}
	cleanupContext, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	cleanupErr := job.WaitEmpty(cleanupContext)
	cancelCleanup()
	if cleanupErr != nil {
		return 0, cleanupErr
	}
	accounting, err := job.Snapshot()
	if err != nil || accounting.Active != 0 || accounting.Total != baseline.Total+uint32(len(observation.Processes)) {
		return 0, errors.Join(fmt.Errorf("native audit parent job inventory did not join: total=%d active=%d baseline=%d observed=%d", accounting.Total, accounting.Active, baseline.Total, len(observation.Processes)), err)
	}
	if receipt.OutputBytes != stdout.Len() || receipt.OutputSHA256 != fmt.Sprintf("%x", sha256.Sum256(stdout.Bytes())) {
		return 0, errors.New("native audit stdout differs from its independently captured receipt")
	}
	if cause := context.Cause(ctx); cause != nil {
		return 0, cause
	}
	written, err := cmd.Stdout.Write(stdout.Bytes())
	if err != nil {
		return 0, fmt.Errorf("publish native audit stdout: %w", err)
	}
	if written != stdout.Len() {
		return 0, io.ErrShortWrite
	}
	return int(observation.NativeExit), nil
}

type boundedAuditBuffer struct {
	buffer bytes.Buffer
	failed bool
}

func (b *boundedAuditBuffer) Len() int       { return b.buffer.Len() }
func (b *boundedAuditBuffer) Bytes() []byte  { return b.buffer.Bytes() }
func (b *boundedAuditBuffer) String() string { return b.buffer.String() }

func (b *boundedAuditBuffer) Write(data []byte) (int, error) {
	if len(data) > terminalCaptureLimit-b.Len() {
		b.failed = true
		return 0, errors.New("native audit control stream exceeds its bound")
	}
	return b.buffer.Write(data)
}

func publishAuditTerminalFile(directory, name string, data []byte) error {
	file, err := os.OpenFile(filepath.Join(directory, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	return errors.Join(writeErr, file.Close())
}

func runAuditTerminalProcess(args []string) error {
	if len(args) < 3 || !filepath.IsAbs(args[0]) || !filepath.IsAbs(args[2]) {
		return errors.New("native audit helper requires an owned directory, deadline and executable")
	}
	nanos, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, nanos))
	defer cancel()
	receipt, err := captureAuditConsole(ctx, args[2:], args[0])
	output := receipt.Observation.Output
	receipt.Observation.Output = nil
	receipt.OutputBytes = len(output)
	receipt.OutputSHA256 = fmt.Sprintf("%x", sha256.Sum256(output))
	encoded, encodeErr := json.Marshal(receipt)
	if err := errors.Join(err, encodeErr); err != nil {
		return err
	}
	if !receipt.Observation.Admitted || !receipt.ModesIntact {
		return fmt.Errorf("native audit silence refused: %s%s", receipt.Observation.Error, receipt.Observation.Refusal)
	}
	if _, err := os.Stdout.Write(output); err != nil {
		return err
	}
	return publishAuditTerminalFile(args[0], "receipt.json", encoded)
}

func captureAuditConsole(ctx context.Context, argv []string, controlDirectory string) (receipt auditTerminalReceipt, err error) {
	input, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		return receipt, err
	}
	defer func() { err = errors.Join(err, input.Close()) }()
	output, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		return receipt, err
	}
	defer func() { err = errors.Join(err, output.Close()) }()
	var beforeInput, beforeOutput uint32
	if err := windows.GetConsoleMode(windows.Handle(input.Fd()), &beforeInput); err != nil {
		return receipt, err
	}
	if err := windows.GetConsoleMode(windows.Handle(output.Fd()), &beforeOutput); err != nil {
		return receipt, err
	}
	defer func() {
		err = errors.Join(err, windows.SetConsoleMode(windows.Handle(input.Fd()), beforeInput), windows.SetConsoleMode(windows.Handle(output.Fd()), beforeOutput))
	}()
	if err := publishAuditTerminalFile(controlDirectory, "console.ready", nil); err != nil {
		return receipt, err
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	admissionDeadline := time.NewTimer(10 * time.Second)
	defer admissionDeadline.Stop()
	for {
		if _, err := os.Stat(filepath.Join(controlDirectory, "admitted")); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return receipt, err
		}
		select {
		case <-ctx.Done():
			return receipt, context.Cause(ctx)
		case <-admissionDeadline.C:
			return receipt, errors.New("native audit helper job admission timed out")
		case <-ticker.C:
		}
	}
	directory, err := os.Getwd()
	if err != nil {
		return receipt, err
	}
	receipt.Observation = nativeaudit.Observe(ctx, argv, directory, windows.Handle(input.Fd()), windows.Handle(output.Fd()))
	var afterInput, afterOutput uint32
	err = errors.Join(windows.GetConsoleMode(windows.Handle(input.Fd()), &afterInput), windows.GetConsoleMode(windows.Handle(output.Fd()), &afterOutput))
	receipt.ModesIntact = err == nil && afterInput == beforeInput && afterOutput == beforeOutput
	return receipt, err
}
