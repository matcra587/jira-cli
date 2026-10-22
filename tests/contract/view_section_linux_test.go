//go:build linux

package contract

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gechr/x/ansi"
	"golang.org/x/sys/unix"
)

// Comment titles exercise the shared Section owner with styled Jira text,
// wide graphemes and words longer than the terminal. Each character must
// survive while every physical row stays inside the measured width.
func TestCommentSectionActualTerminalRetainsTitleWithinWidth(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, color string
		width       uint16
		noColor     bool
		styled      bool
	}{
		{name: "two cells", color: "never", width: 2},
		{name: "five cells", color: "never", width: 5},
		{name: "seven cells", color: "never", width: 7},
		{name: "styled five cells", color: "always", width: 5, styled: true},
		{name: "NO_COLOR seven cells", color: "auto", width: 7, noColor: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issue/KEY-1/comment" {
					t.Errorf("unexpected comment read: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"startAt":0,"maxResults":100,"total":1,"comments":[{"id":"10042","created":"recent","author":{"displayName":"東京🙂LongUnbrokenHeading"},"body":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Body"}]}]}}]}`))
			}))
			t.Cleanup(server.Close)
			master, slave := editorPTY(t)
			if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: tc.width}); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			bin := jiraBinary(t)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, "issue", "comment", "list", "KEY-1", "--limit=100", "--output=human", "--color="+tc.color)
			cmd.Dir = dir
			cmd.Env = append(searchEnv(server.URL), "PATH="+os.Getenv("PATH"), "HOME="+dir, "TERM=xterm-256color", "COLORTERM=truecolor")
			if tc.noColor {
				cmd.Env = append(cmd.Env, "NO_COLOR=1")
			}
			cmd.Stdout = slave
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("terminal comment read failed: %v stderr=%q", err, stderr.String())
			}
			if err := slave.Close(); err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(master)
			if err != nil && !errors.Is(err, unix.EIO) {
				t.Fatal(err)
			}
			plain := strings.ReplaceAll(ansi.Strip(string(raw)), "\r", "")
			if requests.Load() != 1 || strings.Join(strings.Fields(plain), "") != "東京🙂LongUnbrokenHeading•recentBody" {
				t.Fatalf("terminal changed complete title or body: requests=%d output=%q", requests.Load(), plain)
			}
			for _, line := range strings.Split(plain, "\n") {
				if ansi.StringWidth(line) > int(tc.width) {
					t.Errorf("section overflowed %d cells: %q", tc.width, line)
				}
			}
			if tc.styled {
				if !strings.Contains(string(raw), "\x1b[") {
					t.Fatalf("section lost forced style: %q", raw)
				}
			} else if strings.ContainsAny(string(raw), "\x1b\a") {
				t.Fatalf("disabled section style leaked controls: %q", raw)
			}
		})
	}
}
