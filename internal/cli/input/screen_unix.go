//go:build unix

package input

import (
	"io"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// PrepareScreen: owned duplicate, bounded polling, joined reads before close.
// Hide Name: avoid cancelreader scanner/pipe-close race.
// Preserve flags: stdin/stdout can share open description; O_NONBLOCK breaks redraws.
// Raw-mode read after readiness; no pollable Go wrapper needed.
// gechr/x/terminal: detection only, no read lifetime owner.
func PrepareScreen(reader io.Reader) (io.Reader, func() error, error) {
	prepared, cleanup, err := prepareStream(reader, os.ModeCharDevice|os.ModeNamedPipe|os.ModeSocket, false)
	if err != nil || cleanup == nil {
		return prepared, cleanup, err
	}
	owned := prepared.(*os.File)
	stream := &screenReader{file: owned, descriptor: owned.Fd()}
	return stream, func() error {
		stream.mu.Lock()
		stream.closed = true
		stream.mu.Unlock()
		stream.reads.Wait()
		return cleanup()
	}, nil
}

type screenReader struct {
	file       *os.File
	descriptor uintptr
	mu         sync.Mutex
	reads      sync.WaitGroup
	closed     bool
}

func (r *screenReader) Fd() uintptr { return r.descriptor }

// Tea's terminal identity requires ReadWriteCloser, whereas cancelreader's
// descriptor path additionally requires Name. Close cannot own the borrowed
// terminal; PrepareScreen's cleanup owns the duplicate and joined lifetime.
func (r *screenReader) Write(buffer []byte) (int, error) { return r.file.Write(buffer) }
func (*screenReader) Close() error                       { return nil }

func (r *screenReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return 0, io.EOF
	}
	r.reads.Add(1)
	r.mu.Unlock()
	defer r.reads.Done()
	for {
		r.mu.Lock()
		closed := r.closed
		r.mu.Unlock()
		if closed {
			return 0, io.EOF
		}
		poll := []unix.PollFd{{Fd: int32(r.descriptor), Events: unix.POLLIN}}
		ready, err := unix.Poll(poll, 50)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, err
		}
		if ready == 0 {
			continue
		}
		n, err := unix.Read(int(r.descriptor), buffer)
		if n == 0 && err == nil {
			err = io.EOF
		}
		return n, err
	}
}
