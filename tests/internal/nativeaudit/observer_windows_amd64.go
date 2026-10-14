//go:build outputaudit && windows && amd64

// Package nativeaudit admits silent terminal invocations of the owned Go audit images.
package nativeaudit

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/matcra587/jira-cli/tests/internal/nativejob"
)

var (
	kernel        = windows.NewLazySystemDLL("kernel32.dll")
	getContext    = kernel.NewProc("GetThreadContext")
	setContext    = kernel.NewProc("SetThreadContext")
	waitEvent     = kernel.NewProc("WaitForDebugEventEx")
	continueEvent = kernel.NewProc("ContinueDebugEvent")
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func validateImageBuild(info *debug.BuildInfo) error {
	if info.GoVersion != runtime.Version() {
		return errors.New("native audit image toolchain differs from its selected observer")
	}
	for _, setting := range info.Settings {
		if setting.Key == "CGO_ENABLED" && setting.Value == "0" {
			return nil
		}
	}
	return errors.New("native audit image requires explicit CGO_ENABLED=0 build metadata")
}

// The wrapper preserves the selected Windows syscall library's pointer escape
// contract, so native arguments cannot remain on a movable Go stack.
//
//go:uintptrescapes
func call(proc *windows.LazyProc, args ...uintptr) {
	r, _, e := proc.Call(args...)
	if r == 0 {
		panic(fmt.Errorf("%s: %w", proc.Name, e))
	}
}

type event struct {
	Code, PID, TID, Padding uint32
	Data                    [160]byte
}

func (e event) word(offset int) uint32 { return binary.LittleEndian.Uint32(e.Data[offset:]) }
func (e event) quad(offset int) uint64 { return binary.LittleEndian.Uint64(e.Data[offset:]) }

type context64 struct {
	Home                                                                              [6]uint64
	Flags, MxCsr                                                                      uint32
	Segments                                                                          [6]uint16
	EFlags                                                                            uint32
	Dr                                                                                [6]uint64
	Rax, Rcx, Rdx, Rbx, Rsp, Rbp, Rsi, Rdi, R8, R9, R10, R11, R12, R13, R14, R15, Rip uint64
	Rest                                                                              [976]byte
}
type alignedContext struct {
	storage *[1248]byte
	context *context64
}

func threadContext(thread windows.Handle) alignedContext {
	b := new([1248]byte)
	offset := (-uintptr(unsafe.Pointer(&b[0]))) & 15 //nolint:gosec // G103: verified native ABI storage or selected system-module address; never a write payload.
	c := (*context64)(unsafe.Pointer(&b[offset]))    //nolint:gosec // G103: verified native ABI storage or selected system-module address; never a write payload.
	c.Flags = 0x100013
	call(getContext, uintptr(thread), uintptr(unsafe.Pointer(c))) //nolint:gosec // G103: verified native ABI storage or selected system-module address; never a write payload.
	return alignedContext{b, c}
}

func arm(thread windows.Handle, console, file, resume uintptr) {
	c := threadContext(thread)
	c.context.Dr = [6]uint64{uint64(console), uint64(file), uint64(resume), 0, 0, 0x15}
	call(setContext, uintptr(thread), uintptr(unsafe.Pointer(c.context))) //nolint:gosec // G103: verified native ABI storage or selected system-module address; never a write payload.
	runtime.KeepAlive(c.storage)
	verified := threadContext(thread)
	if verified.context.Dr[0] != uint64(console) || verified.context.Dr[1] != uint64(file) || verified.context.Dr[2] != uint64(resume) || verified.context.Dr[5]&0x15 != 0x15 {
		panic("hardware write breakpoint admission unverified")
	}
	runtime.KeepAlive(verified.storage)
}

func moduleName(handle windows.Handle) string {
	if handle == 0 {
		return ""
	}
	b := make([]uint16, 4096)
	n, e := windows.GetFinalPathNameByHandle(handle, &b[0], uint32(len(b)), 0)
	must(e)
	if n == 0 || n >= uint32(len(b)) {
		panic("module filename is unbounded")
	}
	return filepath.Base(windows.UTF16ToString(b[:n]))
}

func readStructure(process windows.Handle, address uintptr, n uint64) []byte {
	if n != 16 && n != 1232 {
		panic("native observation permits only an entry prefix or fixed thread context")
	}
	b := make([]byte, int(n))
	var read uintptr
	must(windows.ReadProcessMemory(process, address, &b[0], uintptr(len(b)), &read))
	if read != uintptr(len(b)) {
		panic("incomplete native structure observation")
	}
	return b
}

// Attempt records an API entry without reading its write payload.
type Attempt struct {
	PID           uint32
	API           string
	Handle, Count uint64
	Thread        uint32
	FileType      uint32
	Target, Error string
}

// Process binds an observed image to its native file identity and actual exit.
type Process struct {
	PID                                      uint32
	Image                                    string
	ImageID                                  [3]uint32
	Armed, LoaderHandled, Exited             bool
	Exit                                     uint32
	process                                  windows.Handle
	console, file, resume, ntBase, writeBase uintptr
	threads                                  map[uint32]windows.Handle
}

// Observation records admission and cleanup independently of the CLI output.
// Output is the separately captured stdout byte stream; it is never serialized
// into a supervisor receipt. A refusal does not reconstruct diagnostics.
type Observation struct {
	NativeExit                                                        uint32
	Joined, Armed, LoaderHandled, Admitted, JobEmpty, CollectorJoined bool
	ThreadsArmed, UnexpectedBreakpoints                               int
	Events                                                            map[uint32]int
	Processes                                                         map[uint32]*Process
	Writes                                                            []Attempt
	Error, Refusal                                                    string
	Canceled, DeadlineExceeded                                        bool
	EventFilesOpened, EventFilesClosed                                int
	Output                                                            []byte `json:"-"`
}

func fileID(handle windows.Handle) [3]uint32 {
	var value windows.ByHandleFileInformation
	must(windows.GetFileInformationByHandle(handle, &value))
	return [3]uint32{value.VolumeSerialNumber, value.FileIndexHigh, value.FileIndexLow}
}

func classify(process, handle, stdout windows.Handle) (target string, kind uint32, err error) {
	var copied windows.Handle
	if err = windows.DuplicateHandle(process, handle, windows.CurrentProcess(), &copied, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return "unclassified", 0, err
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(copied)) }()
	kind, err = windows.GetFileType(copied)
	if err != nil {
		return "unclassified", kind, err
	}
	switch kind {
	case windows.FILE_TYPE_DISK:
		return "disk", kind, nil
	case windows.FILE_TYPE_PIPE:
		same, _, _ := windows.NewLazySystemDLL("kernelbase.dll").NewProc("CompareObjectHandles").Call(uintptr(copied), uintptr(stdout)) //nolint:errcheck // The API returns equality, not a GetLastError outcome; both handles are live owned duplicates.
		if same != 0 {
			return "result_stdout", kind, nil
		}
		return "unmatched_pipe", kind, nil
	case windows.FILE_TYPE_CHAR:
		return "unclassified_character", kind, nil
	default:
		return "unclassified", kind, nil
	}
}

func (o *Observation) closeOwned(name string, closeResource func() error) {
	if err := closeResource(); err != nil {
		o.Error += name + ": " + err.Error() + "; "
		o.Admitted = false
	}
}

// Observe follows only the selected CLI and current helper image in an owned
// process tree. It traps native write entries from startup, admits disk writes
// and exact aliases of the retained stdout pipe, and refuses every other
// nonzero attempt. This is silence admission, not stderr byte reconstruction.
// Call only in a fresh helper console with an explicit scrubbed environment.
func Observe(ctx context.Context, argv []string, directory string, input, diagnostics windows.Handle) (answer Observation) {
	answer.Processes = map[uint32]*Process{}
	answer.Events = map[uint32]int{}
	defer func() {
		if p := recover(); p != nil {
			answer.Error = fmt.Sprint(p) + "; " + answer.Error
			answer.Admitted = false
		}
	}()
	if err := context.Cause(ctx); err != nil {
		panic(err)
	}
	if len(argv) == 0 || !filepath.IsAbs(argv[0]) || !filepath.IsAbs(directory) {
		panic("native audit requires an absolute owned executable and directory")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if runtime.GOARCH != "amd64" || unsafe.Sizeof(context64{}) != 1232 || unsafe.Offsetof(context64{}.Rcx) != 128 || unsafe.Offsetof(context64{}.Rip) != 248 || unsafe.Sizeof(event{}) != 176 {
		panic("native debugger ABI is not verified")
	}
	base := windows.NewLazySystemDLL("kernelbase.dll")
	must(base.NewProc("CompareObjectHandles").Find())
	wc, wf := base.NewProc("WriteConsoleW").Addr(), base.NewProc("WriteFile").Addr()
	var cm, fm windows.Handle
	must(windows.GetModuleHandleEx(6, (*uint16)(unsafe.Pointer(wc)), &cm)) //nolint:gosec,govet // FROM_ADDRESS consumes a selected system-module code address, never a Go allocation.
	must(windows.GetModuleHandleEx(6, (*uint16)(unsafe.Pointer(wf)), &fm)) //nolint:gosec,govet // FROM_ADDRESS consumes a selected system-module code address, never a Go allocation.
	if cm != fm {
		panic("separate native write module bindings required")
	}
	buffer := make([]uint16, 4096)
	n, _, e := kernel.NewProc("GetModuleFileNameW").Call(uintptr(cm), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer))) //nolint:gosec // G103: verified native ABI storage or selected system-module address; never a write payload.
	if n == 0 || n >= uintptr(len(buffer)) {
		panic(e)
	}
	selectedModule := filepath.Base(windows.UTF16ToString(buffer[:n]))
	nt := windows.NewLazySystemDLL("ntdll.dll")
	localResume, ntBase := nt.NewProc("NtContinue").Addr(), nt.Handle()
	var ntInfo windows.ModuleInfo
	must(windows.GetModuleInformation(windows.CurrentProcess(), windows.Handle(ntBase), &ntInfo, uint32(unsafe.Sizeof(ntInfo))))
	resultRead, resultWrite, e := os.Pipe()
	must(e)
	closeRead, closeWrite := sync.OnceValue(resultRead.Close), sync.OnceValue(resultWrite.Close)
	defer answer.closeOwned("close stdout reader", closeRead)
	defer answer.closeOwned("close stdout writer", closeWrite)
	var stdoutKeeper windows.Handle
	must(windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(resultWrite.Fd()), windows.CurrentProcess(), &stdoutKeeper, 0, false, windows.DUPLICATE_SAME_ACCESS))
	keeperClosed := false
	defer func() {
		if !keeperClosed {
			answer.closeOwned("close retained stdout writer", func() error { return windows.CloseHandle(stdoutKeeper) })
		}
	}()
	var inherited [3]windows.Handle
	defer func() {
		for _, handle := range inherited {
			if handle != 0 {
				if err := windows.CloseHandle(handle); err != nil {
					answer.Error += "close owned inherited handle: " + err.Error() + "; "
					answer.Admitted = false
				}
			}
		}
	}()
	for i, handle := range []windows.Handle{input, windows.Handle(resultWrite.Fd()), diagnostics} {
		must(windows.DuplicateHandle(windows.CurrentProcess(), handle, windows.CurrentProcess(), &inherited[i], 0, true, windows.DUPLICATE_SAME_ACCESS))
	}
	attrs, e := windows.NewProcThreadAttributeList(1)
	must(e)
	defer attrs.Delete()
	must(attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&inherited[0]), uintptr(len(inherited))*unsafe.Sizeof(inherited[0]))) //nolint:gosec // G103: verified native ABI storage or selected system-module address; never a write payload.
	startup := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})), Flags: windows.STARTF_USESTDHANDLES | windows.STARTF_USESHOWWINDOW, StdInput: inherited[0], StdOutput: inherited[1], StdErr: inherited[2]}, ProcThreadAttributeList: attrs.List()}
	self, e := os.Executable()
	must(e)
	application, e := windows.UTF16PtrFromString(argv[0])
	must(e)
	command, e := windows.UTF16PtrFromString(windows.ComposeCommandLine(argv))
	must(e)
	expectedImages := map[[3]uint32]bool{}
	for _, path := range []string{self, argv[0]} {
		info, err := buildinfo.ReadFile(path)
		must(err)
		must(validateImageBuild(info))
		func() {
			file, err := os.Open(path) //nolint:gosec // G304: only the selected owned CLI and helper build images are opened.
			must(err)
			defer answer.closeOwned("close selected image", file.Close)
			expectedImages[fileID(windows.Handle(file.Fd()))] = true
		}()
	}
	cwd, e := windows.UTF16PtrFromString(directory)
	must(e)
	job, e := nativejob.New()
	must(e)
	defer func() {
		if err := job.Close(); err != nil {
			answer.Error += "close owned job: " + err.Error() + "; "
			answer.Admitted = false
		}
	}()
	assigned := false
	var root windows.ProcessInformation
	if err := context.Cause(ctx); err != nil {
		panic(err)
	}
	must(windows.CreateProcess(application, command, nil, nil, true, 1|windows.CREATE_UNICODE_ENVIRONMENT|windows.EXTENDED_STARTUPINFO_PRESENT, nil, cwd, &startup.StartupInfo, &root))
	defer answer.closeOwned("close original process", func() error { return windows.CloseHandle(root.Process) })
	defer answer.closeOwned("close original thread", func() error { return windows.CloseHandle(root.Thread) })
	running := true
	alive := map[uint32]bool{root.ProcessId: true}
	var pending *event
	var pendingFile windows.Handle
	closePendingFile := func() error {
		if pendingFile == 0 {
			return nil
		}
		file := pendingFile
		pendingFile = 0
		err := windows.CloseHandle(file)
		if err == nil {
			answer.EventFilesClosed++
		}
		return err
	}
	writerClosed := false
	type captured struct {
		data []byte
		err  error
	}
	drained := make(chan captured, 1)
	collectorStarted, collected := false, false
	var capture captured
	addError := func(err error) {
		if err != nil {
			answer.Error += err.Error() + "; "
		}
	}
	defer func() {
		addError(closePendingFile())
		if running {
			if assigned {
				addError(job.Terminate(1))
			} else {
				addError(windows.TerminateProcess(root.Process, 1))
			}
			until := time.Now().Add(5 * time.Second)
			for running && time.Now().Before(until) {
				if pending == nil {
					var ev event
					r, _, waitErr := waitEvent.Call(uintptr(unsafe.Pointer(&ev)), 100) //nolint:gosec // G103: verified native ABI storage or selected system-module address; never a write payload.
					if r == 0 {
						if !errors.Is(waitErr, windows.ERROR_SEM_TIMEOUT) {
							addError(fmt.Errorf("wait native cleanup event: %w", waitErr))
							break
						}
						continue
					}
					pending = &ev
					answer.Events[ev.Code]++
					if ev.Code == 3 {
						alive[ev.PID] = true
					}
					if (ev.Code == 3 || ev.Code == 6) && ev.quad(0) != 0 {
						answer.EventFilesOpened++
						pendingFile = windows.Handle(ev.quad(0))
						addError(closePendingFile())
					}
				}
				pid, tid := pending.PID, pending.TID

				r, _, err := continueEvent.Call(uintptr(pid), uintptr(tid), 0x10002)
				if r == 0 {
					addError(fmt.Errorf("continue retained cleanup event: %w", err))
					continue
				}

				if pending.Code == 5 {
					if target := answer.Processes[pid]; target != nil {
						target.Exit = pending.word(0)
						target.Exited = true
					}
					if pid == root.ProcessId {
						answer.NativeExit = pending.word(0)
					}
					delete(alive, pid)
					running = len(alive) > 0
				}
				pending = nil
			}
		}
		state, err := windows.WaitForSingleObject(root.Process, 5000)
		addError(err)
		stateJob, jobErr := job.Snapshot()

		addError(jobErr)
		answer.JobEmpty = jobErr == nil && stateJob.Active == 0
		answer.Joined = err == nil && state == windows.WAIT_OBJECT_0 && !running
		if !answer.Joined {
			addError(errors.New("owned child did not join"))
		}
		keeperErr := windows.CloseHandle(stdoutKeeper)
		keeperClosed = keeperErr == nil
		addError(keeperErr)
		var writerErr error
		if !writerClosed {
			writerErr = closeWrite()
			addError(writerErr)
			writerClosed = true
		}
		if collectorStarted {
			if !answer.Joined || keeperErr != nil || writerErr != nil {
				addError(closeRead())
			}
			if !collected {
				capture = <-drained
				collected = true
			}
			answer.CollectorJoined = true

			addError(capture.err)
			answer.Output = capture.data
		}
		answer.Armed = true
		answer.LoaderHandled = true
		for _, node := range answer.Processes {
			answer.Armed = answer.Armed && node.Armed && node.Exited && node.Exit == 0
			answer.LoaderHandled = answer.LoaderHandled && node.LoaderHandled
		}
		if jobErr == nil && stateJob.Total != uint32(len(answer.Processes)) {
			addError(fmt.Errorf("job/process inventory mismatch: total=%d active=%d observed=%d creates=%d exits=%d", stateJob.Total, stateJob.Active, len(answer.Processes), answer.Events[3], answer.Events[5]))
		}
		answer.Admitted = answer.EventFilesOpened > 0 && answer.EventFilesOpened == answer.EventFilesClosed && answer.Joined && answer.JobEmpty && answer.CollectorJoined && len(answer.Processes) > 0 && answer.NativeExit == 0 && answer.Armed && answer.LoaderHandled && answer.Error == ""
		for _, w := range answer.Writes {
			if w.Count != 0 && (w.Error != "" || (w.Target != "disk" && w.Target != "result_stdout")) {
				answer.Admitted = false
				answer.Refusal = "nonzero diagnostic write attempt: " + w.API + "/" + w.Target
			}
		}
	}()
	must(job.AssignPID(root.ProcessId))
	assigned = true
	for i, handle := range inherited {
		inherited[i] = 0
		must(windows.CloseHandle(handle))
	}
	collectorStarted = true
	go func() {
		data, err := io.ReadAll(io.LimitReader(resultRead, (1<<20)+1))
		if len(data) > 1<<20 {
			err = errors.Join(err, errors.New("owned stdout exceeds capture bound"))
		}
		drained <- captured{data, err}
	}()

	must(closeWrite())
	writerClosed = true

	until := time.Now().Add(15 * time.Second)
	for running {
		if err := context.Cause(ctx); err != nil {
			answer.Canceled = errors.Is(err, context.Canceled)
			answer.DeadlineExceeded = errors.Is(err, context.DeadlineExceeded)

			panic(err)
		}

		if time.Now().After(until) {
			panic("native attempt observer deadline")
		}
		if !collected {
			select {
			case capture = <-drained:
				collected = true
				if capture.err != nil {
					panic(capture.err)
				}
			default:
			}
		}
		var ev event
		r, _, err := waitEvent.Call(uintptr(unsafe.Pointer(&ev)), 100) //nolint:gosec // G103: verified native ABI storage or selected system-module address; never a write payload.
		if r == 0 {
			if errors.Is(err, windows.ERROR_SEM_TIMEOUT) {
				continue
			}
			panic(fmt.Errorf("wait native debug event: %w", err))
		}
		pending = &ev
		if ev.Code == 3 || ev.Code == 6 {
			pendingFile = windows.Handle(ev.quad(0))
			if pendingFile != 0 {
				answer.EventFilesOpened++
			}
		}
		answer.Events[ev.Code]++
		status := uintptr(0x10002)
		target := answer.Processes[ev.PID]
		if ev.Code == 3 {
			alive[ev.PID] = true
			if len(answer.Processes) >= 128 {
				panic("native process inventory exceeds bound")
			}
			target = &Process{PID: ev.PID, process: windows.Handle(ev.quad(8)), threads: map[uint32]windows.Handle{}}
			answer.Processes[ev.PID] = target
		} else if target == nil {
			panic("unowned debug event")
		}
		exitPending := false
		switch ev.Code {
		case 3:
			file := windows.Handle(ev.quad(0))
			if file == 0 {
				panic("missing owned executable image handle")
			}
			func() {
				defer func() { must(closePendingFile()) }()
				target.Image = moduleName(file)
				target.ImageID = fileID(file)
			}()
			if !expectedImages[target.ImageID] {
				panic("unapproved executable in owned job")
			}
			target.threads[ev.TID] = windows.Handle(ev.quad(16))
		case 2:
			if len(target.threads) >= 1024 {
				panic("native thread inventory exceeds bound")
			}
			target.threads[ev.TID] = windows.Handle(ev.quad(0))
			if target.Armed {
				arm(target.threads[ev.TID], target.console, target.file, target.resume)
				answer.ThreadsArmed++
			}
		case 4:
			delete(target.threads, ev.TID)
		case 6:
			file := windows.Handle(ev.quad(0))
			name := func() string {
				defer func() { must(closePendingFile()) }()
				name := moduleName(file)

				return name
			}()
			if strings.EqualFold(name, "ntdll.dll") {
				nextBase := uintptr(ev.quad(8))
				if target.ntBase != 0 && target.ntBase != nextBase {
					panic("selected native context module binding changed")
				}
				target.ntBase = nextBase
				target.resume = target.ntBase + (localResume - ntBase)
			}
			if strings.EqualFold(name, selectedModule) {
				b := uintptr(ev.quad(8))
				if target.writeBase != 0 && target.writeBase != b {
					panic("selected native write module binding changed")
				}
				target.writeBase = b
				target.console = b + (wc - uintptr(cm))
				target.file = b + (wf - uintptr(cm))
			}
			if !target.Armed && target.console != 0 && target.file != 0 && target.resume != 0 {
				for _, binding := range []struct{ remote, local uintptr }{{target.console, wc}, {target.file, wf}, {target.resume, localResume}} {
					if !bytes.Equal(readStructure(target.process, binding.remote, 16), readStructure(windows.CurrentProcess(), binding.local, 16)) {
						panic("remote entry does not match selected native module")
					}
				}
				for _, thread := range target.threads {
					arm(thread, target.console, target.file, target.resume)
					answer.ThreadsArmed++
				}
				target.Armed = true
			}
		case 7:
			base := uintptr(ev.quad(0))
			if base == target.writeBase || base == target.ntBase {
				panic("observed native module unloaded")
			}
		case 1:
			switch ev.word(0) {
			case 0x80000003:
				address := uintptr(ev.quad(16))
				if target.LoaderHandled {
					answer.UnexpectedBreakpoints++
					status = 0x80010001
				} else {
					if !target.Armed || ev.word(152) != 1 || address < target.ntBase || address >= target.ntBase+uintptr(ntInfo.SizeOfImage) {
						panic("unexpected loader breakpoint")
					}
					target.LoaderHandled = true
					for _, thread := range target.threads {
						arm(thread, target.console, target.file, target.resume)
						answer.ThreadsArmed++
					}
				}
			case 0x80000004:
				thread := target.threads[ev.TID]
				if thread == 0 {
					panic("unowned observation thread")
				}
				c := threadContext(thread)
				slot := uint64(0)
				switch uintptr(c.context.Rip) {
				case target.console:
					slot = 1
				case target.file:
					slot = 2
				case target.resume:
					slot = 4
				}
				if slot == 0 || c.context.Dr[4]&slot == 0 {
					panic("single-step is not an owned hardware breakpoint")
				}
				switch uintptr(c.context.Rip) {
				case target.resume:
					data := readStructure(target.process, uintptr(c.context.Rcx), 1232)
					if binary.LittleEndian.Uint32(data[48:])&0x10 != 0 {
						values := [6]uint64{uint64(target.console), uint64(target.file), uint64(target.resume), 0, 0xffff0ff0, 0x415}
						patch := make([]byte, 48)
						for i, value := range values {
							binary.LittleEndian.PutUint64(patch[i*8:], value)
						}
						var written uintptr
						must(windows.WriteProcessMemory(target.process, uintptr(c.context.Rcx)+72, &patch[0], uintptr(len(patch)), &written))
						if written != uintptr(len(patch)) {
							panic("incomplete owned debug-register update")
						}
					}
				case target.console, target.file:
					w := Attempt{PID: ev.PID, Handle: c.context.Rcx, Count: uint64(uint32(c.context.R8)), Thread: ev.TID}
					if uintptr(c.context.Rip) == target.console {
						w.API = "WriteConsoleW"
						w.Target = "console_attempt"
					} else {
						w.API = "WriteFile"
						var err error
						w.Target, w.FileType, err = classify(target.process, windows.Handle(w.Handle), stdoutKeeper)
						if err != nil {
							w.Error = err.Error()
						}
					}
					if len(answer.Writes) >= 4096 {
						panic("native attempt inventory exceeds bound")
					}
					answer.Writes = append(answer.Writes, w)
				default:
					panic("unexpected native single-step")
				}
				c.context.EFlags |= 0x10000
				c.context.Dr[4] = 0
				call(setContext, uintptr(thread), uintptr(unsafe.Pointer(c.context))) //nolint:gosec // G103: verified native ABI storage or selected system-module address; never a write payload.
				runtime.KeepAlive(c.storage)
			default:
				status = 0x80010001
			}
		case 5:
			target.Exit = ev.word(0)
			if ev.PID == root.ProcessId {
				answer.NativeExit = target.Exit
			}
			exitPending = true
		}

		call(continueEvent, uintptr(ev.PID), uintptr(ev.TID), status)
		pending = nil
		if exitPending {
			target.Exited = true
			delete(alive, ev.PID)
			running = len(alive) > 0
		}
	}
	return answer
}
