package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	cfg "github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// replaceWith returns an edit func that stands in for the editor,
// overwriting the scratch file with what a user might have saved.
func replaceWith(t *testing.T, contents string) func(string) error {
	t.Helper()
	return func(scratch string) error {
		return os.WriteFile(scratch, []byte(contents), 0o600)
	}
}

// TestEditLeavesTheLiveConfigAloneWhenTheEditIsInvalid is the reason the
// command edits a copy at all. A settings file is read by every later
// command, so an edit that does not parse must cost the user that edit —
// not the working configuration they had before it.
func TestEditLeavesTheLiveConfigAloneWhenTheEditIsInvalid(t *testing.T) {
	const good = "base_url = \"https://example.atlassian.net\"\n"
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := editFile(t.Context(), path, replaceWith(t, "base_url = \"not a url\"\n"))
	if err == nil {
		t.Fatal("an unparseable edit was accepted")
	}

	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("config file is gone after a failed edit: %v", readErr)
	}
	if string(after) != good {
		t.Errorf("a rejected edit changed the live config:\ngot:  %q\nwant: %q", after, good)
	}

	var input *cli.InputError
	if !errors.As(err, &input) {
		t.Fatalf("error is %T, want *cli.InputError so it exits as a validation failure", err)
	}
	if input.Code != cli.CodeConfigInvalid {
		t.Errorf("code = %q, want %q", input.Code, cli.CodeConfigInvalid)
	}
	// The rejected text has to be recoverable, or a long edit is simply
	// lost — which teaches people to edit the file directly instead.
	kept := keptPath(t, err.Error())
	body, readErr := os.ReadFile(kept)
	if readErr != nil {
		t.Fatalf("error names %s but nothing is there: %v", kept, readErr)
	}
	if !strings.Contains(string(body), "not a url") {
		t.Errorf("the saved copy is not the rejected edit: %q", body)
	}
}

// TestEditRejectsABrokenFileEvenWhenTheEnvironmentWouldCoverIt pins that
// validation judges the FILE. The environment wins at resolution time,
// so loading with it set would let a variable vouch for a file that is
// broken — and the breakage would surface later, on a machine where that
// variable is not set.
func TestEditRejectsABrokenFileEvenWhenTheEnvironmentWouldCoverIt(t *testing.T) {
	t.Setenv("JIRA_BASE_URL", "https://valid.atlassian.net")
	path := filepath.Join(t.TempDir(), "config.toml")

	if _, err := editFile(t.Context(), path, replaceWith(t, "base_url = \"not a url\"\n")); err == nil {
		t.Fatal("a broken file was accepted because the environment happened to be set")
	}
}

// TestEditSeedsAFileThatParses guards the first run. The template is
// handed to someone who has never seen this file; if it does not survive
// its own validation, saving it unmodified fails and the very first use
// of the command is a defect.
func TestEditSeedsAFileThatParses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")

	// Validate the template directly, so this fails if the template
	// breaks even were the caller to stop validating. Going only through
	// editFile would pass with validation removed entirely.
	if err := cfg.ValidateFile([]byte(configTemplate)); err != nil {
		t.Fatalf("the seeded template does not validate: %v", err)
	}

	out, err := editFile(t.Context(), path, func(string) error { return nil })
	if err != nil {
		t.Fatalf("editFile on an untouched template: %v", err)
	}
	if out.Changed {
		t.Error("Changed = true after an editor that saved nothing")
	}
	// Opening the file and deciding against it must not leave one behind.
	if out.Created {
		t.Error("Created = true although nothing was written")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("a file was created for an edit that changed nothing")
	}
}

// TestEditWritesAnAcceptedChange is the happy path: the file lands, with
// the contents that were saved, and the result says so.
func TestEditWritesAnAcceptedChange(t *testing.T) {
	const edited = "read_only = true\n"
	path := filepath.Join(t.TempDir(), "config.toml")

	out, err := editFile(t.Context(), path, replaceWith(t, edited))
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if !out.Changed || !out.Created {
		t.Errorf("Changed = %v, Created = %v; want both true for a new file with an edit", out.Changed, out.Created)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("nothing was written: %v", err)
	}
	if string(body) != edited {
		t.Errorf("file = %q, want %q", body, edited)
	}
	if out.Path != path {
		t.Errorf("Path = %q, want %q", out.Path, path)
	}
}

// TestEditReportsCreatedOnlyForANewFile keeps the two flags distinct —
// an edit to a file that already existed did not create it.
func TestEditReportsCreatedOnlyForANewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("read_only = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := editFile(t.Context(), path, replaceWith(t, "read_only = true\n"))
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if out.Created {
		t.Error("Created = true for a file that was already there")
	}
	if !out.Changed {
		t.Error("Changed = false although the contents differ")
	}
}

// TestEditLeavesAnUnchangedFileUntouched pins that saving without
// editing is a no-op. Rewriting identical bytes moves the file's mtime,
// which is the one thing a backup or a watcher notices.
func TestEditLeavesAnUnchangedFileUntouched(t *testing.T) {
	const body = "read_only = true\n"
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	out, err := editFile(t.Context(), path, replaceWith(t, body))
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if out.Changed {
		t.Error("Changed = true although the bytes are identical")
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("the file was rewritten despite no change")
	}
}

// TestEditKeepsTheFilePrivate checks POSIX mode without treating Windows file
// attributes as evidence of ACL protection.
func TestEditKeepsTheFilePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if _, err := editFile(t.Context(), path, replaceWith(t, "read_only = true\n")); err != nil {
		t.Fatalf("edit: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	assertEditPOSIXPermissions(t, info.Mode().Perm(), 0o600)
}

func assertEditPOSIXPermissions(t *testing.T, got, want os.FileMode) {
	t.Helper()
	t.Run("posix-permissions", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows file modes do not establish POSIX permissions or ACL protection")
		}
		if got != want {
			t.Fatalf("POSIX mode = %#o, want %#o", got, want)
		}
	})
}

// editorVars name environment overrides and fallbacks for the editor.
//
// The tool-specific override comes first so a quick settings edit can
// use something other than the editor a whole development environment
// is pointed at; VISUAL precedes EDITOR by the long-standing convention
// that separates a full-screen editor from a line editor. Every CLI in
// this space resolves the same way.
var editorVars = []string{"JIRA_EDITOR", "VISUAL", "EDITOR"}

// TestResolveEditorPrefersTheToolSpecificVariable pins the order, so a
// per-tool override is not silently beaten by the environment-wide one.
//
// The values are real executables because resolution now checks PATH: a
// name that cannot run is not an editor, and the test has to exercise
// the same rule the command does.
func TestResolveEditorPrefersTheToolSpecificVariable(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "tool-specific wins", env: map[string]string{"JIRA_EDITOR": "tool", "VISUAL": "visual", "EDITOR": "editor"}, want: "tool"},
		{name: "VISUAL beats EDITOR", env: map[string]string{"VISUAL": "visual", "EDITOR": "editor"}, want: "visual"},
		{name: "EDITOR is the last resort", env: map[string]string{"EDITOR": "editor"}, want: "editor"},
		{name: "an empty value is not a choice", env: map[string]string{"JIRA_EDITOR": "", "EDITOR": "editor"}, want: "editor"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, name := range editorVars {
				choice := tt.env[name]
				if choice != "" {
					choice = shell.Quote(executable) + " --editor-choice=" + choice
				}
				t.Setenv(name, choice)
			}
			got, err := command.ResolveEditor("")
			if err != nil {
				t.Fatalf("resolveEditor: %v", err)
			}
			want := []string{executable, "--editor-choice=" + tt.want}
			if !slices.Equal(got, want) {
				t.Errorf("editor = %q, want %q", got, want)
			}
		})
	}
}

// TestResolveEditorSplitsArguments pins that an editor carrying flags —
// which is how most people configure one — is not treated as a single
// executable with spaces in its name.
func TestResolveEditorSplitsArguments(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range editorVars {
		t.Setenv(name, "")
	}
	t.Setenv("JIRA_EDITOR", shell.Quote(executable)+` --wait '$HOME' '$(must-not-run)'`)

	got, err := command.ResolveEditor("")
	if err != nil {
		t.Fatalf("resolveEditor: %v", err)
	}
	want := []string{executable, "--wait", "$HOME", "$(must-not-run)"}
	if !slices.Equal(got, want) {
		t.Errorf("editor = %q, want %q", got, want)
	}
}

// TestResolveEditorRejectsAnEditorThatCannotRun pins that resolution
// checks PATH rather than only splitting the string. Without it a
// preview reports success for an editor that does not exist — the exact
// question --dry-run is asked — and the real run fails inside exec with
// an untyped error that never names the editor.
func TestResolveEditorRejectsAnEditorThatCannotRun(t *testing.T) {
	for _, name := range editorVars {
		t.Setenv(name, "")
	}
	t.Setenv("EDITOR", "definitely-not-an-editor-on-this-machine")

	_, err := command.ResolveEditor("")
	if err == nil {
		t.Fatal("an editor that is not on PATH was accepted")
	}
	var input *cli.InputError
	if !errors.As(err, &input) || input.Code != cli.CodeEditorMissing {
		t.Errorf("error = %v (code %q), want %q", err, code(input), cli.CodeEditorMissing)
	}
}

// TestResolveEditorRefusesRatherThanGuessing pins the absence of a
// fallback. Dropping someone into an editor they did not choose is
// harder to escape than to configure — so refusing is only defensible if
// the refusal carries the fix, which is the code's job: the message says
// what is wrong, the code's static hint says what to type.
func TestResolveEditorRefusesRatherThanGuessing(t *testing.T) {
	for _, name := range editorVars {
		t.Setenv(name, "")
	}

	_, err := command.ResolveEditor("")
	if err == nil {
		t.Fatal("an editor was chosen although none is configured")
	}
	var input *cli.InputError
	if !errors.As(err, &input) {
		t.Fatalf("error is %T, want *cli.InputError", err)
	}
	// Its own code, not the generic config one: a hint sending the caller
	// to the config file would send them somewhere the fix is not.
	if input.Code != cli.CodeEditorMissing {
		t.Errorf("code = %q, want %q", input.Code, cli.CodeEditorMissing)
	}
	if input.Field != "EDITOR" {
		t.Errorf("field = %q, want EDITOR so the caller knows what to set", input.Field)
	}
}

// TestEditorAndTerminalErrorsCarryTheirOwnAdvice pins the reason those
// two codes exist separately at all. Both are "config edit cannot run",
// and the useful sentence is different for each — a shared code would
// force one of them to carry advice that does not apply.
func TestEditorAndTerminalErrorsCarryTheirOwnAdvice(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ code, wants string }{
		{code: cli.CodeEditorMissing, wants: "EDITOR"},
		{code: cli.CodeTerminalRequired, wants: "--dry-run"},
	} {
		hint := cli.HintFor(tt.code)
		if hint == "" {
			t.Errorf("%s has no hint", tt.code)
			continue
		}
		if !strings.Contains(hint, tt.wants) {
			t.Errorf("%s hint does not mention %q: %s", tt.code, tt.wants, hint)
		}
	}
}

// TestRequireInteractiveRefusesWithoutATerminal pins the agent path. Go
// test binaries run with pipes, so this is the real condition: an editor
// launched here would wait forever for input that cannot arrive.
func TestRequireInteractiveRefusesWithoutATerminal(t *testing.T) {
	const path = "/somewhere/config.toml"

	err := requireInteractive(path)
	if err == nil {
		t.Fatal("an editor would have been launched with no terminal to drive it")
	}
	// The caller that cannot use an editor still needs the file, so the
	// refusal has to carry the path rather than just saying no.
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the refusal does not say where the file is: %v", err)
	}
}

// TestEditIsRegistered guards against a command that exists in the
// source and not in the binary.
func TestEditIsRegistered(t *testing.T) {
	t.Parallel()
	for _, sub := range New(Deps{}).Commands() {
		if sub.Name() == "edit" {
			if _, ok := envelope.Registered("config.edit"); !ok {
				t.Error("config.edit has no registered output shape")
			}
			return
		}
	}
	t.Fatal("config edit is not attached to the config group")
}

// keptPath pulls the saved-copy path back out of the error message, which
// is the only place the user is told about it.
func keptPath(t *testing.T, msg string) string {
	t.Helper()
	const marker = "saved at "
	i := strings.Index(msg, marker)
	if i < 0 {
		t.Fatalf("the error does not say where the rejected edit went: %s", msg)
	}
	return strings.TrimSpace(msg[i+len(marker):])
}

// TestEditWritesThroughASymlink pins the dotfiles case. AtomicWrite ends
// in a rename, and a rename REPLACES a symlink instead of following it —
// so without resolution the link is silently swapped for a regular file
// and the repo the user actually manages keeps the old contents. Nothing
// reports that, which is what makes it worth a test.
func TestEditWritesThroughASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles-config.toml")
	link := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(target, []byte("read_only = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	const edited = "read_only = true\n"
	if _, err := editFile(t.Context(), link, replaceWith(t, edited)); err != nil {
		t.Fatalf("edit: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a regular file — a dotfiles setup would be silently detached")
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != edited {
		t.Errorf("the edit did not reach the symlink target: got %q, want %q", body, edited)
	}
}

// TestEditRefusesToClobberAConcurrentChange pins the lost update. An
// editor can sit open for a long time, so the file may have moved on;
// overwriting it silently is the one outcome with no recovery, since the
// other change would be gone with nothing to say so.
func TestEditRefusesToClobberAConcurrentChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("read_only = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	const meanwhile = "base_url = \"https://someone-else.atlassian.net\"\n"
	_, err := editFile(t.Context(), path, func(scratch string) error {
		// Stand in for a second writer landing while the editor is open.
		if err := os.WriteFile(path, []byte(meanwhile), 0o600); err != nil {
			return err
		}
		return os.WriteFile(scratch, []byte("read_only = true\n"), 0o600)
	})
	if err == nil {
		t.Fatal("a concurrent change was silently overwritten")
	}
	var input *cli.InputError
	if !errors.As(err, &input) || input.Code != cli.CodeConfigChanged {
		t.Errorf("error = %v (code %q), want %q", err, code(input), cli.CodeConfigChanged)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != meanwhile {
		t.Errorf("the other writer's change was lost: file = %q", body)
	}
}

// TestEditLeavesNothingBehindWhenTheEditIsAbandoned pins that opening the
// command and thinking better of it is free. Creating the directory for
// an edit that never lands leaves state nobody asked for.
func TestEditLeavesNothingBehindWhenTheEditIsAbandoned(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created-yet")
	path := filepath.Join(dir, "config.toml")

	if _, err := editFile(t.Context(), path, func(string) error { return nil }); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Error("the config directory was created for an edit that changed nothing")
	}
}

// TestRejectedEditIsKeptBesideTheConfig pins where a rejected edit lands.
// A fixed name beside the config means a retry overwrites it instead of
// leaving a new file per failure, and it is findable without reading the
// error prose.
func TestRejectedEditIsKeptBesideTheConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	const bad = "base_url = \"not a url\"\n"

	for range 2 {
		if _, err := editFile(t.Context(), path, replaceWith(t, bad)); err == nil {
			t.Fatal("an invalid edit was accepted")
		}
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	var rejected []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".rejected") {
			rejected = append(rejected, e.Name())
		}
	}
	if len(rejected) != 1 {
		t.Fatalf("after two failed edits there are %d rejected copies (%v), want exactly 1", len(rejected), rejected)
	}
	body, err := os.ReadFile(path + ".rejected")
	if err != nil {
		t.Fatalf("no rejected copy beside the config: %v", err)
	}
	if string(body) != bad {
		t.Errorf("rejected copy = %q, want the rejected edit %q", body, bad)
	}
}

func code(e *cli.InputError) string {
	if e == nil {
		return ""
	}
	return e.Code
}

// TestEditLeavesTheConfigAloneWhenTheEditorFails pins the path where the
// editor itself exits non-zero. Nothing should be written, and the
// editor's failure should be what the caller hears about.
func TestEditLeavesTheConfigAloneWhenTheEditorFails(t *testing.T) {
	const original = "read_only = false\n"
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("editor exited with status 1")
	_, err := editFile(t.Context(), path, func(scratch string) error {
		// A real editor may well have written before failing.
		if writeErr := os.WriteFile(scratch, []byte("read_only = true\n"), 0o600); writeErr != nil {
			return writeErr
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the editor's own failure", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != original {
		t.Errorf("the config changed although the editor failed: %q", body)
	}
}
