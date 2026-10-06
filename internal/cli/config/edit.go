package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	xos "github.com/gechr/x/os"
	"github.com/gechr/x/shell"
	"github.com/gechr/x/terminal"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	cfg "github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// configTemplate seeds a config file that does not exist yet. Comments
// carry the vocabulary — a reader who opens an empty file learns
// nothing, and the keys are not guessable.
const configTemplate = `# jira CLI settings.
#
# Store credentials through ` + "`jira auth`" + `. Scalar settings can
# also be given as environment variables (read_only -> JIRA_READ_ONLY),
# which takes precedence over this file. Run ` + "`jira config list`" + ` to see
# the effective value of each setting and where it came from.

# External editor command; JIRA_EDITOR overrides this preference.
# Use a wait flag for editors that launch another application.
# editor = "code --wait"

# Refuse every write to Jira, at the transport, for every command.
# read_only = false

# Seconds in a logged workday: 1d in worklog add uses this duration.
# This does not change elapsed-time dates such as --started '1d ago'.
# workday_seconds = 28800

# Color theme for human output and prompts. Unset reads COLORFGBG when
# available, otherwise uses dark. Not every name is known to both renderers: one only this
# CLI's help knows will color the help and leave errors plain. Run
# ` + "`jira config list`" + ` after changing it, and look at an error.
# theme = "catppuccin-mocha"
# Optional custom TOML palette; relative paths start beside this settings file.
# theme_path = "palette.toml"

# Which glyphs the human output may emit: "default" (every font renders
# it) or "nerd" (adds Nerd Font Powerline caps to status pills). Unlike
# theme, an unrecognized value here fails to load rather than falling
# back quietly — a font choice has no safe "close enough".
# glyphs = "nerd"
`

// renderEditHuman says what happened, in one line, and nothing else.
//
// The interesting outcomes are the quiet ones: a preview that opened
// nothing, and an editor closed without saving. Both would otherwise
// look identical to a successful write, so each gets its own sentence
// rather than a shared "done".
func renderEditHuman(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.ConfigEditOutput)
	if !ok {
		return fmt.Errorf("config.edit: unexpected payload %T", env.Data)
	}
	switch {
	case out.DryRun:
		v.Line(fmt.Sprintf("would open %s in %s", cli.Text(out.Path), cli.Text(strings.Join(out.Editor, " "))))
	case out.Created:
		v.Line("created " + cli.Text(out.Path))
	case out.Changed:
		v.Line("updated " + cli.Text(out.Path))
	default:
		v.Line("no changes")
	}
	return nil
}

// newEdit returns `jira config edit`.
func newEdit() *cobra.Command {
	return command.MustNew(command.Spec[envelope.ConfigEditOutput]{
		Op:                "config.edit",
		MutatesLocalState: true,
		Use:               "edit",
		Short:             "Open the config file in your editor",
		Long: "Open the config file in your editor, creating it if it does not " +
			"exist yet.\n\n" +
			"The editor is taken from `JIRA_EDITOR`, the stored `editor` setting, " +
			"`VISUAL`, or `EDITOR`, in that order. There is no fallback: an editor " +
			"nobody asked for is " +
			"harder to leave than to configure.\n\n" +
			"Set `JIRA_EDITOR` explicitly to repair a file that cannot be decoded. " +
			"Editor commands are parsed as arguments and run without a shell.\n\n" +
			"Edits are validated before they take effect. The file is edited " +
			"through a copy, so an invalid change leaves the live configuration " +
			"exactly as it was rather than breaking every later command.\n\n" +
			"Concurrent changes, removals and redirected config links refuse " +
			"the save. Rejected edits are kept beside the target file with a " +
			"`.rejected` suffix; the error reports whether that recovery copy " +
			"was saved.\n\n" +
			"The editor must leave a regular saved file; symlinks to regular " +
			"files are supported.\n\n" +
			"On Unix, cancellation interrupts the editor, allowing it to save " +
			"before exiting; an ignored interrupt triggers a kill after two " +
			"seconds. Other platforms kill it immediately. Saved changes are " +
			"retained as rejected edits, and terminal " +
			"modes are restored before returning. Configure editors that launch " +
			"another application to wait for it.\n\n" +
			"An editor needs a terminal, so this refuses when there is none " +
			"rather than hanging. `--dry-run` needs no terminal: it reports the " +
			"file and the editor it would open, and changes nothing.",
		Example: "# open the settings file\n" +
			"$ jira config edit\n\n" +
			"# show the file and editor without opening anything\n" +
			"$ jira config edit --dry-run\n\n" +
			"# use a specific editor for this one command\n" +
			"$ JIRA_EDITOR=nano jira config edit",
		Flags: func(c *cobra.Command) {
			command.AddDryRunFlag(c)
		},
		Human: renderEditHuman,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.ConfigEditOutput, error) {
			return runEdit(ctx, inv.DryRun)
		},
	})
}

// runEdit resolves the file, edits a copy, validates it, and only then
// replaces the original.
func runEdit(ctx context.Context, dryRun bool) (envelope.ConfigEditOutput, error) {
	var zero envelope.ConfigEditOutput

	if err := ctx.Err(); err != nil {
		return zero, err
	}
	path, err := cfg.SelectedPath()
	if err != nil {
		return zero, err
	}

	// The editor is resolved before the preview branches, so --dry-run
	// answers the question it is asked for: a preview that skipped the
	// lookup could not report which editor would run, and would report
	// success for a machine that has none configured.
	configured := ""
	// An explicit override can repair invalid TOML without loading it. Read
	// only the stored preference otherwise; editing needs no credential store.
	if os.Getenv("JIRA_EDITOR") == "" {
		file, openErr := cfg.OpenFile(ctx, path)
		if openErr != nil {
			return zero, localIO("read config editor", openErr)
		}
		setting, settingErr := file.Setting("editor")
		if settingErr != nil {
			return zero, configError(cli.CodeConfigInvalid, "cannot resolve the stored editor; set JIRA_EDITOR to repair the file: "+settingErr.Error())
		}
		configured = setting.Value
	}
	editor, err := resolveEditor(configured)
	if err != nil {
		return zero, err
	}

	// A preview launches no editor, so it needs no terminal — which makes it
	// the form this command actually has for an agent. Everything above
	// has already run: the answer is real, not a stub.
	if dryRun {
		return envelope.ConfigEditOutput{
			MutationState: envelope.MutationState{DryRun: true},
			Path:          path,
			Editor:        editor,
		}, nil
	}

	if err := requireInteractive(path); err != nil {
		return zero, err
	}

	result, err := editFile(ctx, path, func(scratch string) error {
		return command.RunEditor(ctx, editor, scratch)
	})
	if err != nil {
		return zero, err
	}
	result.Editor = editor

	return result, nil
}

// editFile edits an isolated copy and saves through the shared file owner.
// The editor callback is the terminal-independent test boundary.
func editFile(ctx context.Context, path string, edit func(scratch string) error) (envelope.ConfigEditOutput, error) {
	var zero envelope.ConfigEditOutput
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	file, err := cfg.OpenFile(ctx, path)
	if err != nil {
		return zero, localIO("open config", err)
	}
	original := file.Contents()
	if !file.Exists() {
		original = []byte(configTemplate)
	}
	scratchPath, err := writeScratch(original)
	if err != nil {
		return zero, err
	}
	defer os.RemoveAll(filepath.Dir(scratchPath)) //nolint:errcheck // scratch cleanup must not replace the edit outcome

	editorErr := edit(scratchPath)
	// Capture one regular-file snapshot, even after cancellation, so saved work
	// remains recoverable. The shared owner refuses Unix FIFOs without waiting
	// for a writer. Publication continues to require the command's live context.
	// This cannot interrupt arbitrary regular-file filesystem I/O.
	snapshot, err := cfg.ReadFile(context.WithoutCancel(ctx), scratchPath)
	if ctxErr := ctx.Err(); ctxErr != nil && !errors.Is(editorErr, ctxErr) {
		editorErr = errors.Join(ctxErr, editorErr)
	}
	if err != nil {
		return zero, errors.Join(editorErr, localIO("read edited file", err))
	}
	edited := snapshot.Contents()
	if editorErr != nil {
		if !bytes.Equal(edited, original) {
			return zero, rejectEdit(file.Path(), edited, editorErr)
		}
		return zero, editorErr
	}
	if err := ctx.Err(); err != nil {
		if !bytes.Equal(original, edited) {
			return zero, rejectEdit(file.Path(), edited, err)
		}
		return zero, err
	}
	if bytes.Equal(original, edited) {
		// The seed differs from a missing file's empty snapshot. Validate an
		// untouched seed without creating the file or moving its metadata.
		if err := cfg.ValidateFile(edited); err != nil {
			return zero, configError(cli.CodeConfigInvalid, err.Error())
		}
		return envelope.ConfigEditOutput{Path: file.Path()}, nil
	}
	changed, err := file.Save(ctx, edited)
	if err != nil {
		switch {
		case errors.Is(err, cfg.ErrFileChanged):
			err = configError(cli.CodeConfigChanged, fmt.Sprintf("%s changed while the editor was open; nothing was overwritten", file.Path()))
		case errors.Is(err, cfg.ErrFileInvalid):
			err = configError(cli.CodeConfigInvalid, fmt.Sprintf("%v — %s was not changed", err, file.Path()))
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			// Preserve cancellation classification and the user's saved work.
		default:
			err = localIO("save config; inspect the file before retrying", err)
		}
		return zero, rejectEdit(file.Path(), edited, err)
	}
	return envelope.ConfigEditOutput{Path: file.Path(), Created: !file.Exists() && changed, Changed: changed}, nil
}

// rejectEdit keeps the candidate even when validation, cancellation or a
// snapshot conflict prevents publication. It retains the original error type.
func rejectEdit(path string, contents []byte, cause error) error {
	kept, err := keepRejected(path, contents)
	if err != nil {
		return fmt.Errorf("%w; your edit could NOT be saved either (%w), so re-apply it before re-running", cause, err)
	}
	return fmt.Errorf("%w; your edit is saved at %s", cause, kept)
}

// writeScratch puts contents in a temp file for the editor to open.
//
// The file goes in a private 0700 directory of its own, not loose in the
// shared temp directory. Creating it at 0600 is not enough on its own:
// an editor that saves by writing a new file and renaming it over the
// target chooses its OWN mode — commonly 0644 — and the result would sit
// world-readable in a directory every user can list. A private parent
// means the mode of the file inside stops mattering.
//
// The path is made absolute because it is handed to another program as
// an argument: a relative TMPDIR could produce one starting with "-",
// which an editor would read as an option rather than a file.
func writeScratch(contents []byte) (string, error) {
	dir, err := os.MkdirTemp("", "jira-config-")
	if err != nil {
		return "", localIO("create scratch directory", err)
	}
	ready := false
	defer func() {
		if !ready {
			_ = os.RemoveAll(dir) //nolint:errcheck // scratch setup cleanup must retain the original filesystem failure
		}
	}()
	// MkdirTemp asks for 0700, but the umask can take bits back off it,
	// and the owner has to be able to enter the directory to reach the
	// file inside. 0700 rather than 0600 for exactly that reason: entering
	// a directory needs the execute bit.
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // directory, not file; owner-only is the tightest usable mode
		return "", localIO("secure scratch directory", err)
	}
	name, err := filepath.Abs(filepath.Join(dir, "config.toml"))
	if err != nil {
		return "", localIO("resolve scratch path", err)
	}
	if err := os.WriteFile(name, contents, 0o600); err != nil {
		return "", localIO("write scratch file", err)
	}
	ready = true
	return name, nil
}

// keepRejected parks a rejected edit beside the config file, so a failed
// validation costs the user their command, not their work.
//
// Beside the config, not in the system temp directory, and at a fixed
// name rather than a random one: the copy is findable without reading
// the error message, a retry overwrites it instead of leaving a new file
// behind on every failure, and it is not somewhere a reboot silently
// clears. This is the one place an abandoned edit may create the config
// directory — saving the user's work earns it.
func keepRejected(configPath string, contents []byte) (string, error) {
	dir := filepath.Dir(configPath)
	exists, err := xos.IsDir(dir)
	if err != nil {
		return "", err
	}
	if !exists {
		if err := xos.EnsureDir(dir, 0o700); err != nil {
			return "", err
		}
	}
	kept := configPath + ".rejected"
	// Replace the name atomically instead of following a pre-existing link
	// or retaining a previous recovery file's permissive mode.
	if err := xos.AtomicWrite(kept, contents, 0o600); err != nil {
		return "", err
	}
	return kept, nil
}

// resolveEditor returns the editor command, or an error naming what to
// set. There is deliberately no fallback: dropping someone into an
// editor they did not choose is harder to escape than to configure, and
// the error can say the exact thing to type.
func resolveEditor(configured string) ([]string, error) {
	for _, choice := range []struct{ name, raw string }{
		{"JIRA_EDITOR", os.Getenv("JIRA_EDITOR")},
		{"editor", configured},
		{"VISUAL", os.Getenv("VISUAL")},
		{"EDITOR", os.Getenv("EDITOR")},
	} {
		name, raw := choice.name, choice.raw
		if raw == "" {
			continue
		}
		parts, splitErr := shell.Split(raw)
		if splitErr != nil || len(parts) == 0 {
			// The reason travels: an unbalanced quote is the usual cause,
			// and "could not be read as a command" alone does not say
			// which character to go and look at.
			detail := "it is empty once parsed"
			if splitErr != nil {
				detail = splitErr.Error()
			}
			return nil, editorError(name,
				fmt.Sprintf("%s is set to %q, which could not be read as a command: %s", name, raw, detail))
		}
		// Resolve the executable HERE, not at launch. Otherwise
		// `--dry-run` reports success for an editor that cannot run —
		// which is the one thing a preview exists to check — and the real
		// invocation fails inside exec with an untyped error that lands
		// on generic validation advice instead of naming the editor.
		resolved, lookErr := exec.LookPath(parts[0])
		if lookErr != nil {
			return nil, editorError(name,
				fmt.Sprintf("%s is set to %q, which was not found as an executable: %v", name, parts[0], lookErr))
		}
		parts[0] = resolved
		return parts, nil
	}
	// Nothing is set at all, so no variable is the offender — EDITOR is
	// named as the one to reach for.
	return nil, editorError("EDITOR", "no editor configured")
}

// requireInteractive refuses to launch an editor with nothing to drive
// it. An agent or a CI job has no terminal, and an editor waiting on
// input that will never arrive hangs the caller — so this fails fast and
// says where the file is, which is what a non-interactive caller needed
// anyway.
//
// Stdin and STDERR, deliberately not stdout: stderr is the stream the
// editor is given, so it is the one that has to be a terminal. Testing
// stdout instead would refuse `jira config edit --output=json > out.json`
// run from a real terminal — the exact invocation the stderr routing
// exists to make work.
func requireInteractive(path string) error {
	if terminal.Is(os.Stdin) && terminal.Is(os.Stderr) {
		return nil
	}
	return configError(cli.CodeTerminalRequired,
		fmt.Sprintf("an editor needs a terminal, and this is not one; the file is %s", path))
}

// configError types a failure whose subject is the config FILE rather
// than anything the caller typed, which is why Field is always "config":
// that is the thing an agent would have to go and look at.
func configError(code, msg string) *cli.InputError {
	e := cli.NewInputError(code, msg)
	e.Field = "config"
	return e
}

// editorError types a failure about the editor setting, carrying the
// variable the caller has to change in Field — which is not always the
// one they set, since the last resort is to name EDITOR.
func editorError(variable, msg string) *cli.InputError {
	e := cli.NewInputError(cli.CodeEditorMissing, msg)
	e.Field = variable
	return e
}

// localIO distinguishes invalid configuration input from a native filesystem
// failure, preserving the original cause alongside its actionable category.
func localIO(what string, err error) error {
	code := cli.CodeLocalIO
	if errors.Is(err, cfg.ErrFileInvalid) {
		code = cli.CodeConfigInvalid
	}
	return fmt.Errorf("%w: %w", configError(code, what), err)
}
