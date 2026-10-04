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

func TestADFRichActualTerminalWrapsAndHonorsColor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, color string
		noColor     bool
		styled      bool
	}{
		{name: "native color", color: "always", styled: true},
		{name: "disabled color", color: "never"},
		{name: "NO_COLOR", color: "auto", noColor: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			master, slave := editorPTY(t)
			if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: 24}); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			input := filepath.Join(dir, "rich.json")
			if err := os.WriteFile(input, []byte(richADFDocument), 0o600); err != nil {
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
			if !strings.Contains(plain, "Ready") || !strings.Contains(plain, "東京") || !strings.Contains(plain, "Warning") {
				t.Fatalf("terminal lost native content: %q", plain)
			}
			if tc.styled {
				if !strings.Contains(string(raw), "48;2;170;85;204") {
					t.Fatalf("native hex status background lost: %q", raw)
				}
			} else if strings.ContainsAny(string(raw), "\x1b\a") {
				t.Fatalf("disabled color leaked controls: %q", raw)
			}
			body := strings.Split(plain, "Markdown losses")[0]
			for _, line := range strings.Split(strings.ReplaceAll(body, "\r", ""), "\n") {
				if ansi.StringWidth(line) > 24 {
					t.Errorf("terminal body overflowed 24 cells: %q", line)
				}
			}
		})
	}
}
