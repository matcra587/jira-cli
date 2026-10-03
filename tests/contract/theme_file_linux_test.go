//go:build linux

package contract

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCustomThemeActualTerminalKeepsHumanPaletteAndPlainMachineOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.toml")
	palette := filepath.Join(dir, "palette.toml")
	if err := os.WriteFile(settings, []byte("theme_path='palette.toml'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(palette, []byte("base='light'\n[colors]\nhelp_command='#123456'\nhelp_section='#123456'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		args  []string
		env   []string
		color bool
	}{
		{name: "human always", args: []string{"--output=human", "--color=always"}, color: true},
		{name: "human never", args: []string{"--output=human", "--color=never"}},
		{name: "machine always", args: []string{"version", "--output=json", "--color=always"}},
		{name: "agent always", args: []string{"version", "--color=always"}, env: []string{"AGENT=1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			master, slave := editorPTY(t)
			env := append([]string{"JIRA_KEYRING=false", "TERM=xterm-256color", "COLORTERM=truecolor"}, tc.env...)
			args := append(append([]string{}, tc.args...), "--config", settings)
			stdout, _, exit := runBinaryIOStarted(t, env, nil, slave, nil, args...)
			if err := slave.Close(); err != nil {
				t.Fatal(err)
			}
			diagnostics, err := io.ReadAll(master)
			if err != nil && !errors.Is(err, unix.EIO) {
				t.Fatal(err)
			}
			if exit != 0 || strings.ContainsAny(stdout, "\x1b\a") {
				t.Fatalf("custom theme corrupted native output: exit=%d stdout=%q stderr=%q", exit, stdout, diagnostics)
			}
			if tc.color {
				if stdout != "" || !strings.Contains(string(diagnostics), "38;2;18;52;86") {
					t.Fatal("terminal help discarded the custom palette")
				}
			} else if strings.ContainsAny(string(diagnostics), "\x1b\a") {
				t.Fatal("disabled or machine theme leaked terminal controls")
			}
			if tc.name == "machine always" || tc.name == "agent always" {
				if stdout == "" || len(diagnostics) != 0 {
					t.Fatal("machine terminal output lost data or printed a human theme diagnostic")
				}
			}
		})
	}
}
