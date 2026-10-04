//go:build linux

package output

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/gechr/clog"
	"github.com/gechr/x/ansi"
	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestViewTerminalPreservesAlignedAndBlankRows(t *testing.T) {
	for _, tc := range []struct {
		name  string
		width uint16
		want  string
	}{
		{name: "aligned", width: 20, want: "Body:  first\n       \n         last\n       \n\n"},
		{name: "whole row", width: 6, want: "Body:\nfirst\n\n  last\n\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			master, terminal := documentTestTerminal(t)
			if err := unix.IoctlSetWinsize(int(terminal.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: tc.width}); err != nil {
				t.Fatal(err)
			}
			v := NewView(terminal, clog.ColorNever)
			v.Fields(Field{Label: "Body", Value: "first\r\n\r\n  last\r\n"})
			v.Line()
			if err := terminal.Close(); err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(master)
			if err != nil && !errors.Is(err, unix.EIO) {
				t.Fatal(err)
			}
			got := strings.ReplaceAll(string(raw), "\r", "")
			if got != tc.want {
				t.Fatalf("terminal changed line endings or blank rows: %q, want %q", got, tc.want)
			}
		})
	}
}

func TestViewTerminalWrappedLinksKeepGraphemesAndStyle(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field bool
	}{
		{name: "line"},
		{name: "field", field: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			master, terminal := documentTestTerminal(t)
			if err := unix.IoctlSetWinsize(int(terminal.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: 2}); err != nil {
				t.Fatal(err)
			}
			v := NewView(terminal, clog.ColorAlways)
			const text = "東京👩🏽‍💻é🙂last"
			link := v.Output().Hyperlink("https://example.com", text)
			styled := "\x1b[31m" + link + "\x1b[0m"
			want := text
			if tc.field {
				v.Fields(Field{Label: "User", Value: styled})
				want = "User:" + text
			} else {
				v.Line(styled)
			}
			if err := terminal.Close(); err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(master)
			if err != nil && !errors.Is(err, unix.EIO) {
				t.Fatal(err)
			}
			plain := ansi.Strip(string(raw))
			if strings.Join(strings.Fields(plain), "") != want {
				t.Fatalf("wrapped link changed graphemes or order: %q", plain)
			}
			for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r", ""), "\n") {
				if ansi.StringWidth(line) > 2 {
					t.Errorf("wrapped link exceeded width: %q", line)
				}
				if !strings.Contains(line, "\x1b]8;;https://example.com") {
					continue
				}
				closedLink := strings.Contains(line, "\x1b]8;;\x1b\\") || strings.Contains(line, "\x1b]8;;\a")
				resetStyle := strings.Contains(line, "\x1b[0m") || strings.Contains(line, "\x1b[m")
				if !closedLink || !resetStyle || !strings.Contains(line, "\x1b[31m") {
					t.Errorf("wrapped link failed to restore and close style: %q", line)
				}
			}
			if strings.Count(string(raw), "\x1b]8;;https://example.com") < 3 {
				t.Fatal("wrapped link was not restored across rows")
			}
		})
	}
}

type failingViewTerminal struct {
	*os.File
	err error
}

func (w failingViewTerminal) Write([]byte) (int, error) { return 0, w.err }

func TestViewWrappedFieldReportsDestinationFailure(t *testing.T) {
	_, terminal := documentTestTerminal(t)
	if err := unix.IoctlSetWinsize(int(terminal.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: 5}); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("destination unavailable")
	res := Result{Envelope: envelope.Envelope{OK: true}, Human: func(v *View, _ envelope.Envelope) error {
		v.Fields(Field{Label: "Assignee", Value: "東京🙂"})
		return nil
	}}
	err := Render(t.Context(), failingViewTerminal{File: terminal, err: cause}, FormatHuman, res)
	var writeError *WriteError
	if !errors.As(err, &writeError) || !errors.Is(err, cause) {
		t.Fatalf("wrapped field swallowed destination failure: %v", err)
	}
}
