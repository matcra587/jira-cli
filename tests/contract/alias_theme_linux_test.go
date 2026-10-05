//go:build linux

package contract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func aliasHelpTerminal(t *testing.T, env, args []string) string {
	t.Helper()
	master, slave := editorPTY(t)
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 40, Col: 120}); err != nil {
		t.Fatal(err)
	}
	type terminalRead struct {
		data []byte
		err  error
	}
	read := make(chan terminalRead, 1)
	go func() {
		data, err := io.ReadAll(master)
		read <- terminalRead{data: data, err: err}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, jiraBinary(t), args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + cmd.Dir,
		"HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1",
		"JIRA_KEYRING=false", "TERM=xterm-256color", "COLORTERM=truecolor", "COLORFGBG=15;0",
	}, env...)
	cmd.Stdout, cmd.Stderr = slave, slave
	runErr := cmd.Run()
	if err := slave.Close(); err != nil {
		t.Fatal(err)
	}
	result := <-read
	if result.err != nil && !errors.Is(result.err, unix.EIO) {
		t.Fatal(result.err)
	}
	if runErr != nil || !strings.Contains(string(result.data), "\x1b[") || strings.Contains(string(result.data), "\x1b]11;") {
		t.Fatalf("terminal help did not render without probing: error=%v terminal=%q", runErr, result.data)
	}
	return string(result.data)
}

func TestAliasActualTerminalUsesFinalConfigTheme(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin.toml")
	target := filepath.Join(dir, "target.toml")
	custom := filepath.Join(dir, "custom.toml")
	files := map[string]string{
		origin:                             fmt.Sprintf("theme='nord'\n[aliases]\nshade=%q\nchain='shade'\npalette=%q\n", "--config="+target+" help", "--config="+custom+" help"),
		target:                             "theme='dracula'\n",
		custom:                             "theme='dracula'\ntheme_path='palette.toml'\n",
		filepath.Join(dir, "palette.toml"): "base='light'\n[colors]\nhelp_command='#123456'\nhelp_section='#123456'\n",
	}
	for path, contents := range files {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	controls := map[string]string{}
	for name, path := range map[string]string{"origin": origin, "target": target, "custom": custom} {
		controls[name] = aliasHelpTerminal(t, nil, []string{"--config", path, "help", "--color=always"})
	}
	if controls["origin"] == controls["target"] || controls["custom"] == controls["target"] || !strings.Contains(controls["custom"], "38;2;18;52;86") {
		t.Fatal("direct terminal controls did not distinguish presets and relative custom palette")
	}
	for _, tc := range []struct {
		name, alias, want string
		tail              []string
	}{
		{name: "alias config", alias: "shade", want: "target"},
		{name: "alias chain", alias: "chain", want: "target"},
		{name: "caller config", alias: "shade", tail: []string{"--config", origin}, want: "origin"},
		{name: "relative custom palette", alias: "palette", want: "custom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--config", origin, tc.alias, "--color=always"}, tc.tail...)
			if got := aliasHelpTerminal(t, nil, args); got != controls[tc.want] {
				t.Fatalf("expanded invocation did not use %s theme", tc.want)
			}
		})
	}
	t.Run("environment precedence", func(t *testing.T) {
		env := []string{"JIRA_THEME=monokai"}
		direct := aliasHelpTerminal(t, env, []string{"--config", target, "help", "--color=always"})
		if direct == controls["target"] || aliasHelpTerminal(t, env, []string{"--config", origin, "shade", "--color=always"}) != direct {
			t.Fatal("alias theme displaced the environment or diverged from direct help")
		}
	})
}
