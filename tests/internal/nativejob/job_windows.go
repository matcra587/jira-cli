// Package nativejob owns disposable Windows process trees used by native tests.
package nativejob

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Accounting describes all processes admitted to one owned job.
type Accounting struct {
	Total  uint32
	Active uint32
}

// Job kills its remaining processes when its last handle closes. Its handle is
// never inherited. Callers must admit helpers before allowing descendants and
// join those helpers before closing the job.
type Job struct {
	mu       sync.RWMutex
	handle   windows.Handle
	closed   bool
	closeErr error
}

// New creates an unnamed job with kill-on-close ownership.
func New() (*Job, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = setInformation(handle, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))) //nolint:gosec // G103: fixed native limit structure owned by this call.
	if err != nil {
		return nil, errors.Join(err, windows.CloseHandle(handle))
	}
	return &Job{handle: handle}, nil
}

// AssignPID admits an already-started process. Before admission its owner must
// prevent descendant creation and retain a way to terminate it on failure.
// An error closing the temporary process handle does not undo assignment.
func (j *Job) AssignPID(pid uint32) error {
	j.mu.RLock()
	defer j.mu.RUnlock()
	if j.closed {
		return os.ErrClosed
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return err
	}
	return errors.Join(windows.AssignProcessToJobObject(j.handle, process), windows.CloseHandle(process))
}

// Terminate requests termination of every admitted process. WaitEmpty and the
// caller's process waits establish that termination has completed.
func (j *Job) Terminate(code uint32) error {
	j.mu.RLock()
	defer j.mu.RUnlock()
	if j.closed {
		return os.ErrClosed
	}
	return windows.TerminateJobObject(j.handle, code)
}

// Snapshot reads native accounting without enumerating other jobs or processes.
func (j *Job) Snapshot() (Accounting, error) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	if j.closed {
		return Accounting{}, os.ErrClosed
	}
	// JOBOBJECT_BASIC_ACCOUNTING_INFORMATION has the same 48-byte layout on
	// supported Windows architectures; its four times are native LARGE_INTEGERs.
	var state struct {
		times                             [4]int64
		faults, total, active, terminated uint32
	}
	if unsafe.Sizeof(state) != 48 {
		return Accounting{}, errors.New("invalid native job accounting layout")
	}
	var returned uint32
	err := queryInformation(j.handle, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&state)), uint32(unsafe.Sizeof(state)), &returned) //nolint:gosec // G103: fixed native accounting structure owned by this call.
	if err != nil {
		return Accounting{}, err
	}
	if returned != uint32(unsafe.Sizeof(state)) {
		return Accounting{}, errors.New("incomplete native job accounting")
	}
	return Accounting{Total: state.total, Active: state.active}, nil
}

// The selected Windows wrappers accept opaque uintptr buffers without an escape
// directive. Force their caller-owned storage off movable stacks before entry.
//
//go:uintptrescapes
func setInformation(handle windows.Handle, class uint32, information uintptr, size uint32) (int, error) {
	return windows.SetInformationJobObject(handle, class, information, size)
}

//go:uintptrescapes
func queryInformation(handle windows.Handle, class int32, information uintptr, size uint32, returned *uint32) error {
	return windows.QueryInformationJobObject(handle, class, information, size, returned)
}

// WaitEmpty observes native accounting until no processes remain or the caller
// cancels. It never substitutes cancellation for evidence of an empty job.
func (j *Job) WaitEmpty(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		state, err := j.Snapshot()
		if err != nil {
			return err
		}
		if err := context.Cause(ctx); err != nil {
			return err
		}
		if state.Active == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("native job retained %d active processes: %w", state.Active, context.Cause(ctx))
		case <-ticker.C:
		}
	}
}

// Close releases the non-inherited job handle, retaining the first close outcome.
// It serializes with native operations so a reused handle cannot be targeted.
func (j *Job) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.closed {
		j.closed = true
		j.closeErr = windows.CloseHandle(j.handle)
	}
	return j.closeErr
}
