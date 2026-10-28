//go:build linux

package output

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

type screenFixture struct{}

func (*screenFixture) Init() tea.Cmd { return nil }
func (m *screenFixture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.String() == "q" {
		return m, tea.Quit
	}
	return m, nil
}

func (*screenFixture) View() tea.View {
	v := tea.NewView("SCREEN READY")
	v.AltScreen = true
	return v
}

func TestScreenDrawsOnSelectedTerminalAndRestoresOnQuitAndCancellation(t *testing.T) {
	for _, mode := range []string{"quit", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			master, slave := documentTestTerminal(t)
			before, err := term.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				finished <- RunScreen(ctx, slave, slave, func(context.Context) tea.Model { return &screenFixture{} })
			}()
			var transcript strings.Builder
			for !strings.Contains(transcript.String(), "SCREEN READY") {
				poll := []unix.PollFd{{Fd: int32(master.Fd()), Events: unix.POLLIN}}
				count, err := unix.Poll(poll, 100)
				if err != nil {
					t.Fatal(err)
				}
				if count > 0 {
					buf := make([]byte, 4096)
					n, err := master.Read(buf)
					if err != nil {
						t.Fatal(err)
					}
					transcript.Write(buf[:n])
				}
				select {
				case err := <-finished:
					t.Fatalf("screen exited before drawing: %v, %q", err, transcript.String())
				default:
				}
				if ctx.Err() != nil {
					t.Fatalf("screen never drew: %q", transcript.String())
				}
			}
			during, err := term.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			if *during == *before {
				t.Fatal("terminal never entered interactive mode")
			}
			if mode == "cancel" {
				cancel()
			} else {
				if _, err := master.WriteString("q"); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-finished:
				if mode == "quit" && err != nil {
					t.Fatal(err)
				}
				if mode == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("screen did not join shutdown")
			}
			after, err := term.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			if *after != *before {
				t.Fatal("screen did not restore the borrowed terminal")
			}
		})
	}
}
