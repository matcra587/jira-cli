//go:build linux

package root

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/sys/unix"
)

func TestPromptObservationRechecksMarkerAtTimeout(t *testing.T) {
	expired := make(chan time.Time, 1)
	expired <- time.Time{}
	reads := 0
	observation := promptObservation{
		marker: "fixture marker",
		snapshot: func() string {
			reads++
			if reads == 1 {
				return "partial screen"
			}
			return "fixture marker"
		},
		expired: expired,
	}
	if err := observation.wait(); err != nil {
		t.Fatalf("observer rejected a marker captured before timeout handling: %v", err)
	}
}

func TestPromptObservationClassifiesCompletion(t *testing.T) {
	for _, event := range []string{"child exited", "terminal reader stopped"} {
		t.Run(event, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				completed := make(chan struct{})
				expired := make(chan time.Time, 1)
				observation := promptObservation{
					marker:   "fixture marker",
					snapshot: func() string { return "partial screen" },
					expired:  expired,
				}
				if event == "child exited" {
					observation.exited = completed
				} else {
					observation.drained = completed
				}
				finished := make(chan error, 1)
				go func() { finished <- observation.wait() }()
				synctest.Wait()
				close(completed)
				synctest.Wait()
				if event == "child exited" {
					select {
					case err := <-finished:
						t.Fatalf("observer stopped before queued child output could be captured: %v", err)
					default:
					}
					expired <- time.Time{}
					synctest.Wait()
				}
				select {
				case err := <-finished:
					if err == nil || !strings.Contains(err.Error(), event) {
						t.Fatalf("completion diagnostic=%v, want %q", err, event)
					}
				default:
					expired <- time.Time{}
					<-finished
					t.Fatalf("observer ignored %s completion", event)
				}
			})
		})
	}
}

func TestPromptObservationRequiresMarker(t *testing.T) {
	expired := make(chan time.Time, 1)
	expired <- time.Time{}
	observation := promptObservation{
		marker:   "fixture marker",
		snapshot: func() string { return "partial screen" },
		expired:  expired,
	}
	if err := observation.wait(); err == nil || !strings.Contains(err.Error(), "fixture marker") || !strings.Contains(err.Error(), "observation timeout") {
		t.Fatalf("missing-marker diagnostic=%v", err)
	}
}

func TestPromptObservationAcceptsCapturedMarker(t *testing.T) {
	observation := promptObservation{
		marker:   "fixture marker",
		snapshot: func() string { return "prefix fixture marker suffix" },
	}
	if err := observation.wait(); err != nil {
		t.Fatalf("captured marker rejected: %v", err)
	}
}

func TestPromptObservationRechecksMarkerOnCompletion(t *testing.T) {
	for _, event := range []string{"child exited", "terminal reader stopped"} {
		t.Run(event, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				completed := make(chan struct{})
				close(completed)
				expired := make(chan time.Time, 1)
				reads := 0
				observation := promptObservation{
					marker: "fixture marker",
					snapshot: func() string {
						reads++
						if reads == 1 {
							return "partial screen"
						}
						return "fixture marker"
					},
					expired: expired,
				}
				if event == "child exited" {
					observation.exited = completed
				} else {
					observation.drained = completed
				}
				finished := make(chan error, 1)
				go func() { finished <- observation.wait() }()
				synctest.Wait()
				select {
				case err := <-finished:
					if err != nil {
						t.Fatalf("captured marker rejected after %s: %v", event, err)
					}
				default:
					expired <- time.Time{}
					<-finished
					t.Fatalf("observer ignored marker after %s", event)
				}
			})
		})
	}
}

func TestPromptObservationAcceptsChangedScreen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		changed := make(chan struct{}, 1)
		expired := make(chan time.Time, 1)
		screen := "partial screen"
		observation := promptObservation{
			marker:   "fixture marker",
			snapshot: func() string { return screen },
			changed:  changed,
			expired:  expired,
		}
		finished := make(chan error, 1)
		go func() { finished <- observation.wait() }()
		synctest.Wait()
		screen = "fixture marker"
		changed <- struct{}{}
		synctest.Wait()
		select {
		case err := <-finished:
			if err != nil {
				t.Fatalf("changed screen rejected: %v", err)
			}
		default:
			expired <- time.Time{}
			<-finished
			t.Fatal("observer ignored changed screen")
		}
	})
}

// TestPromptObservationFailureProcess deliberately omits the expected marker.
// Its parent verifies diagnostics from the real terminal and joined child.
func TestPromptObservationFailureProcess(t *testing.T) {
	if os.Getenv("JIRA_TEST_PROMPT_OBSERVATION_FAILURE") != "1" {
		return
	}
	runPromptContextProcess(t, "https://example.invalid", "missing fixture marker", false, []string{"auth", "logout"})
}

func TestPromptObservationFailureDiagnostics(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestPromptObservationFailureProcess$")
	child.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "JIRA_TEST_PROMPT_OBSERVATION_FAILURE=1"}
	output, err := child.CombinedOutput()
	if ctx.Err() != nil || err == nil || child.ProcessState == nil || child.ProcessState.ExitCode() != 1 {
		t.Fatalf("diagnostic fixture exit=%v, context=%v, output=%q", err, ctx.Err(), output)
	}
	for _, marker := range []string{"missing fixture marker", "observation timeout", "deadline=false", "forced_cleanup=true", "wait=signal: killed", "reader=", "screen=", "Remove the stored token", "stdout="} {
		if !strings.Contains(string(output), marker) {
			t.Fatalf("failure diagnostic missing %q: %s", marker, output)
		}
	}
}

func TestPromptObservationQueuedOutputProcess(t *testing.T) {
	if os.Getenv("JIRA_TEST_PROMPT_QUEUED_OUTPUT") != "1" {
		return
	}
	if _, err := fmt.Fprint(os.Stderr, "queued fixture marker"); err != nil {
		t.Fatal(err)
	}
}

func TestPromptObservationAcceptsQueuedChildOutput(t *testing.T) {
	master, slave := openPromptTerminal(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestPromptObservationQueuedOutputProcess$")
	child.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "JIRA_TEST_PROMPT_QUEUED_OUTPUT=1"}
	child.Stdout, child.Stderr = io.Discard, slave
	if err := child.Run(); err != nil {
		t.Fatal(err)
	}
	marker := "queued fixture marker"
	queued, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCINQ)
	if err != nil || queued < len(marker) {
		t.Fatalf("native fixture has %d queued bytes, want at least %d: %v", queued, len(marker), err)
	}
	synctest.Test(t, func(t *testing.T) {
		exited, changed := make(chan struct{}), make(chan struct{}, 1)
		close(exited)
		expired := make(chan time.Time, 1)
		screen := ""
		observation := promptObservation{
			marker:   marker,
			snapshot: func() string { return screen },
			exited:   exited,
			changed:  changed,
			expired:  expired,
		}
		finished := make(chan error, 1)
		go func() { finished <- observation.wait() }()
		synctest.Wait()
		select {
		case err := <-finished:
			t.Fatalf("observer rejected actual queued child output before capture: %v", err)
		default:
		}
		buffer := make([]byte, len(marker))
		if _, err := io.ReadFull(master, buffer); err != nil {
			expired <- time.Time{}
			<-finished
			t.Fatal(err)
		}
		screen = string(buffer)
		changed <- struct{}{}
		synctest.Wait()
		select {
		case err := <-finished:
			if err != nil {
				t.Fatalf("queued terminal marker rejected after capture: %v", err)
			}
		default:
			expired <- time.Time{}
			<-finished
			t.Fatal("observer ignored captured native marker")
		}
	})
}
