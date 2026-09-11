package cmdutil

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/gechr/clog"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
)

// wireColorMode mirrors what root does per invocation: publish the resolved
// --color mode to the stdout surfaces and flip the process-wide hyperlink
// switch. Both are process globals, so a test that sets them must not run in
// parallel; the cleanup restores the prior values.
func wireColorMode(t *testing.T, mode clog.ColorMode) {
	t.Helper()
	prevMode := cli.ResolvedColorMode()
	prevLinks := clog.Default().FieldFormats().HyperlinkEnabled
	t.Cleanup(func() {
		cli.SetResolvedColorMode(prevMode)
		clog.SetHyperlinkEnabled(prevLinks)
	})
	cli.SetResolvedColorMode(mode)
	clog.SetHyperlinkEnabled(mode != clog.ColorNever)
}

func commandWithDetector(det cli.Detection) *cobra.Command {
	cmd := &cobra.Command{Use: "x"}
	cmd.SetContext(WithDetector(context.Background(), det))
	return cmd
}

var colorTestIssueData = map[string]any{
	"detail": false,
	"issues": []map[string]any{
		{
			"key":      "SAM1-7",
			"summary":  "Create wallet integration",
			"status":   "In Progress",
			"assignee": "Riley Chen",
			"priority": "High",
		},
	},
}

// PlainOptionsForCommand folds the resolved --color mode into the renderer's
// styling switch, so --color=always styles a piped (non-TTY) stdout and
// --color=never leaves a real terminal plain — the flag no longer stops at
// clog.Default() (stderr).
func TestPlainOptionsForCommandHonorsColorMode(t *testing.T) {
	t.Run("always styles a non-tty writer", func(t *testing.T) {
		wireColorMode(t, clog.ColorAlways)
		cmd := commandWithDetector(cli.Detection{Mode: cli.ModePlain, IsTTY: false})

		var buf bytes.Buffer
		if err := cli.WriteCommandPlain(&buf, "issue.list", colorTestIssueData, PlainOptionsForCommand(cmd)...); err != nil {
			t.Fatalf("WriteCommandPlain: %v", err)
		}
		if !strings.Contains(buf.String(), "\x1b[") {
			t.Fatalf("--color=always must style a non-tty writer, got %q", buf.String())
		}
	})

	t.Run("never leaves a tty writer plain", func(t *testing.T) {
		wireColorMode(t, clog.ColorNever)
		cmd := commandWithDetector(cli.Detection{Mode: cli.ModePlain, IsTTY: true})

		var buf bytes.Buffer
		if err := cli.WriteCommandPlain(&buf, "issue.list", colorTestIssueData, PlainOptionsForCommand(cmd)...); err != nil {
			t.Fatalf("WriteCommandPlain: %v", err)
		}
		got := buf.String()
		if strings.Contains(got, "\x1b") {
			t.Fatalf("--color=never must emit no escape bytes on a tty, got %q", got)
		}
		if !strings.Contains(got, "SAM1-7") {
			t.Fatalf("rows must still render as plain text, got %q", got)
		}
	})
}
