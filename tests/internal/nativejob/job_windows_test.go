package nativejob

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--native-job-child" {
		if err := runJobChild(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runJobChild(args []string) error {
	if len(args) != 2 || args[0] != "parent" && args[0] != "leaf" && args[0] != "owner" {
		return errors.New("native job fixture requires its role and owned directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if args[0] == "owner" {
		return runJobOwner(ctx, args[1])
	}
	if args[0] == "parent" {
		if err := waitOwnedFile(ctx, filepath.Join(args[1], "admitted")); err != nil {
			return err
		}
		self, err := os.Executable()
		if err != nil {
			return err
		}
		child := exec.CommandContext(ctx, self, "--native-job-child", "leaf", args[1])
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		return child.Run()
	}
	encoded, err := json.Marshal(os.Getpid())
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(args[1], "leaf.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(encoded)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return waitOwnedFile(ctx, filepath.Join(args[1], "release"))
}

type ownerReady struct {
	Owner, Root, Leaf uint32
}

func runJobOwner(ctx context.Context, directory string) (err error) {
	job, err := New()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, job.Close()) }()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	child := exec.CommandContext(ctx, self, "--native-job-child", "parent", directory)
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		return err
	}
	defer func() {
		if child.ProcessState == nil {
			killErr := child.Process.Kill()
			if !errors.Is(killErr, os.ErrProcessDone) {
				err = errors.Join(err, killErr)
			}
			err = errors.Join(err, child.Wait())
		}
	}()
	if err := job.AssignPID(uint32(child.Process.Pid)); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "admitted"), nil, 0o600); err != nil {
		return err
	}
	leaf, err := waitFixturePID(ctx, filepath.Join(directory, "leaf.json"))
	if err != nil {
		return err
	}
	state, err := job.Snapshot()
	if err != nil || state.Total != 2 || state.Active != 2 {
		return errors.Join(errors.New("native fixture owner did not admit both processes"), err)
	}
	encoded, err := json.Marshal(ownerReady{Owner: uint32(os.Getpid()), Root: uint32(child.Process.Pid), Leaf: leaf})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "owner.json"), encoded, 0o600); err != nil {
		return err
	}
	return child.Wait()
}

func waitFixturePID(ctx context.Context, path string) (uint32, error) {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			var pid uint32
			if json.Unmarshal(data, &pid) == nil && pid != 0 {
				return pid, nil
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
		select {
		case <-ctx.Done():
			return 0, context.Cause(ctx)
		case <-ticker.C:
		}
	}
}

func waitOwnedFile(ctx context.Context, path string) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-ticker.C:
		}
	}
}

func TestJobEmptyAndClosed(t *testing.T) {
	t.Parallel()
	job, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := job.Close(); err != nil {
			t.Error(err)
		}
	})
	state, err := job.Snapshot()
	if err != nil || state != (Accounting{}) {
		t.Fatalf("new job accounting: %+v; %v", state, err)
	}
	if err := job.WaitEmpty(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	want := errors.New("owned job caller canceled")
	cancel(want)
	if err := job.WaitEmpty(ctx); !errors.Is(err, want) {
		t.Fatalf("empty job lost caller cancellation: %v", err)
	}
	if err := job.Close(); err != nil {
		t.Fatal(err)
	}
	if err := job.Close(); err != nil {
		t.Fatalf("repeated close changed outcome: %v", err)
	}
	if _, err := job.Snapshot(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed accounting: %v", err)
	}
	if err := job.Terminate(1); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed termination: %v", err)
	}
	if err := job.AssignPID(uint32(os.Getpid())); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed assignment: %v", err)
	}
}

func TestJobOwnsDescendants(t *testing.T) {
	for _, mode := range []string{"terminate", "close"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			job, err := New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := job.Close(); err != nil {
					t.Error(err)
				}
			}()
			dir := t.TempDir()
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, self, "--native-job-child", "parent", dir)
			cmd.Dir = dir
			cmd.Env = jobFixtureEnvironment(dir)
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			joined := false
			defer func() {
				if err := job.Terminate(1); err != nil && !errors.Is(err, os.ErrClosed) {
					t.Error(err)
				}
				if !joined {
					if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
						t.Error(err)
					}
					<-done
				}
			}()
			if err := job.AssignPID(uint32(cmd.Process.Pid)); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "admitted"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			leaf := waitJobLeaf(t, ctx, filepath.Join(dir, "leaf.json"))
			defer func() {
				if err := windows.CloseHandle(leaf); err != nil {
					t.Error(err)
				}
			}()
			state, err := job.Snapshot()
			if err != nil || state.Total != 2 || state.Active != 2 {
				t.Fatalf("job does not own both live processes: %+v; %v", state, err)
			}
			canceled, stop := context.WithCancelCause(ctx)
			cause := errors.New("owned live job observation canceled")
			stop(cause)
			if err := job.WaitEmpty(canceled); !errors.Is(err, cause) {
				t.Fatalf("live job observation lost cancellation: %v", err)
			}
			if mode == "close" {
				err = job.Close()
			} else {
				err = job.Terminate(73)
			}
			if err != nil {
				t.Fatal(err)
			}
			runErr := <-done
			joined = true
			var exit *exec.ExitError
			if ctx.Err() != nil || output.Len() != 0 || !cmd.ProcessState.Exited() || runErr != nil && !errors.As(runErr, &exit) {
				t.Fatalf("owned parent did not terminate cleanly: %v; output=%q", runErr, output.String())
			}
			if mode == "terminate" && (exit == nil || exit.ExitCode() != 73) {
				t.Fatalf("explicit termination lost its exit code: %v", runErr)
			}
			// Kill-on-close does not promise a nonzero exit code. Joined native
			// waits and an absent release file establish the termination here.
			wait, err := windows.WaitForSingleObject(leaf, 5000)
			if err != nil || wait != windows.WAIT_OBJECT_0 {
				t.Fatalf("descendant survived owner termination: %d; %v", wait, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "release")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("termination guard unexpectedly released its child: %v", err)
			}
			if mode == "terminate" {
				if err := job.WaitEmpty(ctx); err != nil {
					t.Fatal(err)
				}
				state, err := job.Snapshot()
				if err != nil || state.Total != 2 || state.Active != 0 {
					t.Fatalf("terminated job accounting: %+v; %v", state, err)
				}
			}
		})
	}
}

func waitJobLeaf(t *testing.T, ctx context.Context, path string) windows.Handle {
	t.Helper()
	pid, err := waitFixturePID(ctx, path)
	if err != nil {
		t.Fatal("owned descendant never became ready", err)
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		t.Fatal(err)
	}
	state, err := windows.WaitForSingleObject(handle, 0)
	if err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		closeErr := windows.CloseHandle(handle)
		t.Fatalf("descendant was not live before fault: %d; %v", state, errors.Join(err, closeErr))
	}
	return handle
}

func TestJobOwnerDeath(t *testing.T) {
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	owner := exec.CommandContext(ctx, self, "--native-job-child", "owner", dir)
	owner.Dir, owner.Env = dir, jobFixtureEnvironment(dir)
	owner.WaitDelay = 2 * time.Second
	var output bytes.Buffer
	owner.Stdout, owner.Stderr = &output, &output
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- owner.Wait() }()
	joined := false
	defer func() {
		if !joined {
			cancel()
			<-finished
		}
	}()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	var ready ownerReady
	for ready.Owner == 0 || ready.Root == 0 || ready.Leaf == 0 {
		data, err := os.ReadFile(filepath.Join(dir, "owner.json"))
		if err == nil {
			var candidate ownerReady
			if json.Unmarshal(data, &candidate) == nil {
				ready = candidate
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case err := <-finished:
			joined = true
			t.Fatal("native owner exited before publishing its admitted tree", err)
		case <-ctx.Done():
			t.Fatal("native owner never became ready", context.Cause(ctx))
		case <-ticker.C:
		}
	}
	if ready.Owner != uint32(owner.Process.Pid) || ready.Root == ready.Owner || ready.Leaf == ready.Root || ready.Leaf == ready.Owner {
		t.Fatal("native owner published an unrelated process inventory")
	}
	var handles []windows.Handle
	defer func() {
		for _, handle := range handles {
			if state, err := windows.WaitForSingleObject(handle, 0); err == nil && state == uint32(windows.WAIT_TIMEOUT) {
				if err := windows.TerminateProcess(handle, 1); err != nil {
					t.Error(err)
				}
				if state, err := windows.WaitForSingleObject(handle, 5000); err != nil || state != windows.WAIT_OBJECT_0 {
					t.Errorf("owned orphan cleanup did not join: %d; %v", state, err)
				}
			}
			if err := windows.CloseHandle(handle); err != nil {
				t.Error(err)
			}
		}
	}()
	for _, pid := range []uint32{ready.Root, ready.Leaf} {
		handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, pid)
		if err != nil {
			t.Fatal(err)
		}
		handles = append(handles, handle)
		if state, err := windows.WaitForSingleObject(handle, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
			t.Fatalf("owned descendant was not live before owner death: %d; %v", state, err)
		}
	}
	// TerminateProcess bypasses the helper's Go defers. Only kernel closure of
	// its non-inherited job handle can terminate both admitted descendants.
	if err := owner.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	runErr := <-finished
	joined = true
	var exit *exec.ExitError
	if ctx.Err() != nil || !errors.As(runErr, &exit) || output.Len() != 0 {
		t.Fatalf("native owner did not terminate as requested: %v; output=%q", runErr, output.String())
	}
	for _, handle := range handles {
		if state, err := windows.WaitForSingleObject(handle, 5000); err != nil || state != windows.WAIT_OBJECT_0 {
			t.Fatalf("owned descendant survived abrupt owner death: %d; %v", state, err)
		}
	}
}

func jobFixtureEnvironment(dir string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"), "PATHEXT=" + os.Getenv("PATHEXT"),
		"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"), "WINDIR=" + os.Getenv("WINDIR"),
		"HOME=" + dir, "USERPROFILE=" + dir, "TEMP=" + dir, "TMP=" + dir,
		"APPDATA=" + filepath.Join(dir, "roaming"), "LOCALAPPDATA=" + filepath.Join(dir, "local"),
	}
}
