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

func TestIssueViewActualTerminalRetainsRowsWithinWidth(t *testing.T) {
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
		{name: "prefix consumes width", color: "never", width: 11},
		{name: "one cell for wide value", color: "never", width: 12},
		{name: "two cells for wide value", color: "never", width: 13},
		{name: "fitting aligned rows", color: "never", width: 96},
		{name: "styled five cells", color: "always", width: 5, styled: true},
		{name: "styled one cell for wide value", color: "always", width: 12, styled: true},
		{name: "styled fitting rows", color: "always", width: 96, styled: true},
		{name: "NO_COLOR seven cells", color: "auto", width: 7, noColor: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issue/KEY-1" {
					t.Errorf("unexpected issue read: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"10001","key":"KEY-1","fields":{"summary":"narrowline東京🙂Retained","status":{"name":"Pending"},"issuetype":{"name":"Task"},"priority":{"name":"Medium"},"assignee":{"displayName":"東京🙂LongUnbrokenName"},"reporter":{"displayName":"Alex"},"labels":["wide_label"]}}`))
			}))
			t.Cleanup(server.Close)
			master, slave := editorPTY(t)
			if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: tc.width}); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, jiraBinary(t), "issue", "view", "KEY-1", "--comments=0", "--output=human", "--color="+tc.color)
			cmd.Dir = dir
			cmd.Env = append(searchEnv(server.URL), "PATH="+os.Getenv("PATH"), "HOME="+dir, "TERM=xterm-256color", "COLORTERM=truecolor")
			if tc.noColor {
				cmd.Env = append(cmd.Env, "NO_COLOR=1")
			}
			cmd.Stdout = slave
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("terminal issue read failed: %v stderr=%q", err, stderr.String())
			}
			if err := slave.Close(); err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(master)
			if err != nil && !errors.Is(err, unix.EIO) {
				t.Fatal(err)
			}
			plain := strings.ReplaceAll(ansi.Strip(string(raw)), "\r", "")
			content := strings.Join(strings.Fields(plain), "")
			for _, retained := range []string{"KEY-1", "narrowline東京🙂Retained", "Pending", "Type:", "Task", "Priority:", "Medium", "Assignee:", "東京🙂LongUnbrokenName", "Reporter:", "Alex", "Labels:", "wide_label"} {
				if !strings.Contains(content, retained) {
					t.Errorf("terminal lost %q: %q", retained, plain)
				}
			}
			if requests.Load() != 1 {
				t.Errorf("issue read requests=%d, want 1", requests.Load())
			}
			for _, line := range strings.Split(plain, "\n") {
				if ansi.StringWidth(line) > int(tc.width) {
					t.Errorf("issue row overflowed %d cells: %q", tc.width, line)
				}
			}
			if tc.width == 96 && !strings.Contains(plain, "Type:      Task\nPriority:  ") {
				t.Errorf("fitting rows lost shared value column: %q", plain)
			}
			if tc.styled {
				if !strings.Contains(string(raw), "\x1b[") || !strings.Contains(string(raw), "\x1b]8;;") {
					t.Fatalf("terminal lost forced style or key link: %q", raw)
				}
			} else if strings.ContainsAny(string(raw), "\x1b\a") {
				t.Fatalf("disabled style leaked controls: %q", raw)
			}
		})
	}
}
