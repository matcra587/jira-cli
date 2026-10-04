//go:build linux

package contract

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gechr/x/ansi"
	"golang.org/x/sys/unix"
)

func TestADFReadableActualTerminalRetainsNativeText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, color string
		native      string
		width       uint16
		noColor     bool
		styled      bool
	}{
		{name: "wide task", color: "never", width: 5, native: `{"type":"doc","version":1,"content":[{"type":"taskList","attrs":{"localId":"list"},"content":[{"type":"taskItem","attrs":{"localId":"item","state":"DONE"},"content":[{"type":"text","text":"東京🙂"}]}]}]}`},
		{name: "prefixed wide task", color: "never", width: 7, native: `{"type":"doc","version":1,"content":[{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph"},{"type":"taskList","attrs":{"localId":"list"},"content":[{"type":"taskItem","attrs":{"localId":"item","state":"TODO"},"content":[{"type":"text","text":"東京🙂"}]}]}]}]}]}`},
		{name: "narrow color", color: "always", width: 5, styled: true, native: `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"東京🙂"}]}]}`},
		{name: "native color", color: "always", styled: true},
		{name: "disabled color", color: "never"},
		{name: "NO_COLOR", color: "auto", noColor: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			width := tc.width
			if width == 0 {
				width = 24
			}
			native := tc.native
			if native == "" {
				native = readableADFDocument
			}
			master, slave := editorPTY(t)
			if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: width}); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			input := filepath.Join(dir, "rich.json")
			if err := os.WriteFile(input, []byte(native), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, jiraBinary(t), "adf", "render", "--input", input, "--output=human", "--color="+tc.color)
			cmd.Dir = dir
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "JIRA_KEYRING=false", "TERM=xterm-256color", "COLORTERM=truecolor", "HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1"}
			if tc.noColor {
				cmd.Env = append(cmd.Env, "NO_COLOR=1")
			}
			cmd.Stdout = slave
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("terminal render failed: %v stderr=%q", err, stderr.String())
			}
			if err := slave.Close(); err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(master)
			if err != nil && !errors.Is(err, unix.EIO) {
				t.Fatal(err)
			}
			plain := ansi.Strip(string(raw))
			if tc.native != "" {
				if !strings.Contains(strings.Join(strings.Fields(plain), ""), "東京🙂") {
					t.Fatal("wide terminal task lost content", plain)
				}
			} else if !strings.Contains(plain, "@Alex") || !strings.Contains(plain, "🙂") || !strings.Contains(plain, "completed") || !strings.Contains(plain, "omega") || !strings.Contains(plain, "Column 2 (50%)") {
				t.Fatalf("terminal lost native content: %q", plain)
			}
			if tc.styled {
				if !strings.Contains(string(raw), "\x1b[") {
					t.Fatalf("shared human heading style lost: %q", raw)
				}
			} else if strings.ContainsAny(string(raw), "\x1b\a") {
				t.Fatalf("disabled color leaked controls: %q", raw)
			}
			// Body, section titles and diagnostics share the terminal budget.
			plain = strings.ReplaceAll(plain, "\r", "")
			heading := "Rich text"
			if width < 9 {
				heading = "Rich\ntext"
			}
			if !strings.Contains(plain, heading+"\n") && !strings.Contains(plain, heading+" ") {
				t.Fatal("missing complete native body heading", plain)
			}
			for _, line := range strings.Split(plain, "\n") {
				if ansi.StringWidth(line) > int(width) {
					t.Errorf("terminal output overflowed %d cells: %q", width, line)
				}
			}
		})
	}
}
