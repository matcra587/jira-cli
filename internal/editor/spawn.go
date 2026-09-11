package editor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gechr/x/shell"
)

// knownNonBlockingEditors maps binary basename → wait flag names. Each
// editor here forks a child process and returns to the shell
// immediately by default, racing the caller's tempfile cleanup. If the
// user's command line includes any of the listed flags, we trust they
// configured it for blocking mode; otherwise Run refuses to spawn it.
//
// The first entry in the wait flags slice is the "canonical" form
// surfaced in the refusal message.
var knownNonBlockingEditors = map[string][]string{
	"code":          {"--wait", "-w"},
	"code-insiders": {"--wait", "-w"},
	"subl":          {"--wait", "-w"},
	"mate":          {"--wait", "-w"},
	"gvim":          {"-f", "--nofork"},
}

// editorBaseName returns the editor binary's basename without
// extension, suitable for matching against knownNonBlockingEditors.
// Handles absolute paths (`/usr/bin/code`) and Windows extensions
// (`code.exe`).
func editorBaseName(arg string) string {
	base := filepath.Base(arg)
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	return base
}

// refuseIfNonBlocking checks parts (the parsed editor command line)
// against knownNonBlockingEditors and returns a non-nil error if the
// editor would race the caller's cleanup. The error message names the
// canonical wait flag so the user has a one-line fix.
func refuseIfNonBlocking(parts []string) error {
	if len(parts) == 0 {
		return nil
	}
	base := editorBaseName(parts[0])
	waitFlags, ok := knownNonBlockingEditors[base]
	if !ok {
		return nil
	}
	for _, arg := range parts[1:] {
		if slices.Contains(waitFlags, arg) {
			return nil
		}
	}
	canonical := waitFlags[0]
	return fmt.Errorf(
		"editor %q is non-blocking by default and will race the cleanup of your edit; set EDITOR='%s %s' (or pass --json-input / --description-file to skip the editor)",
		base, base, canonical,
	)
}

// Resolve returns the editor command to launch, given a configured
// editor (typically pulled from profile.editor or global config.editor).
//
// Precedence:
//
//	JIRA_EDITOR → configured → $EDITOR → $VISUAL → "vi"
//
// JIRA_EDITOR is the per-invocation override; configured is the user's
// pinned preference in jira-cli config; $EDITOR/$VISUAL are the
// platform defaults; "vi" is the last-resort fallback. An empty value
// at any level falls through to the next.
func Resolve(configured string) string {
	if v := strings.TrimSpace(os.Getenv("JIRA_EDITOR")); v != "" {
		return v
	}
	if v := strings.TrimSpace(configured); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("EDITOR")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("VISUAL")); v != "" {
		return v
	}
	return "vi"
}

// ResolveEditor is the no-arg variant for callers that don't have
// access to the config layer. Equivalent to Resolve("").
func ResolveEditor() string { return Resolve("") }

// splitEditorCommand parses an editor command line with POSIX shell
// grammar so a quoted binary path with spaces (e.g.
// EDITOR='"/opt/My Editor/bin/edit" --wait') stays one argument.
// strings.Fields would shatter such a path on every space.
func splitEditorCommand(editorCommand string) ([]string, error) {
	return shell.Split(editorCommand)
}

// Run resolves editorCommand (see Resolve), then spawns it on path with the
// user's terminal wired through so an interactive editor can read keystrokes.
// It refuses a known non-blocking editor invoked without a wait flag, since
// that would race the caller's cleanup. exec.Command is used directly — no
// shell — so path and args are never re-interpreted.
func Run(ctx context.Context, editorCommand, path string) error {
	editorCommand = Resolve(editorCommand)
	if editorCommand == "" {
		editorCommand = "vi"
	}
	parts, err := splitEditorCommand(editorCommand)
	if err != nil {
		return fmt.Errorf("parse editor command %q: %w", editorCommand, err)
	}
	if len(parts) == 0 {
		return fmt.Errorf("editor command is required")
	}
	if err := refuseIfNonBlocking(parts); err != nil {
		return err
	}
	args := append(parts[1:], path)
	cmd := exec.CommandContext(ctx, parts[0], args...) //nolint:gosec // Editor command is explicit user configuration and exec.Command does not invoke a shell.
	// Editor subprocess needs the user's terminal stdin to function (vim,
	// nano, etc. read keystrokes). This is NOT the CLI reading stdin —
	// it's plumbing the terminal through to the child. Exempt from the
	// stdin-discipline guard via the suppressing comment below.
	cmd.Stdin = os.Stdin //nolint:forbidigo // editor child process; not a CLI stdin read (stdin-exempt)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run editor %q: %w", parts[0], err)
	}
	return nil
}
