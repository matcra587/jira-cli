package issue

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

// THE SECURITY TEST FOR THE ONE TRUST BOUNDARY WHERE A REMOTE VALUE
// CHOOSES A LOCAL PATH.
//
// Everything here is about a filename the CLI did not choose. The two
// layers attachment_path.go describes are tested SEPARATELY and then
// together, because they fail differently and a test that only exercised
// the outer one would pass with the inner one deleted:
//
//   - TestSafeAttachmentName covers the name hygiene, which produces the
//     error a caller reads;
//   - TestCreateInDirCannotEscape covers the sandbox, which is what
//     actually holds — including the symlink case that no amount of
//     string checking can see;
//   - TestDownloadHostileNameNeverEscapes drives them together the way
//     the command does.

// hostileNames are the filenames an attachment could plausibly carry on
// a shared tenant, where anyone with an account can upload one. Each is
// either refused outright or reduced to something that lands inside the
// destination — never both refused and written, and never neither.
//
// Built from the same instinct tests/audit/hostile.go applies to
// rendered text: attack the field with what an attacker would actually
// put there, not with a placeholder that merely differs from the happy
// path.
var hostileNames = []struct {
	name string
	raw  string
	// want is the reduced name, or "" when the name must be refused.
	want string
}{
	{"parent traversal", "../evil.txt", "evil.txt"},
	{"deep traversal", "../../../../etc/passwd", "passwd"},
	{"absolute unix path", "/etc/passwd", "passwd"},
	{"absolute into ssh", "/root/.ssh/authorized_keys", "authorized_keys"},
	{"traversal into ssh", "../../.ssh/authorized_keys", "authorized_keys"},
	{"nested relative", "a/b/c.txt", "c.txt"},
	{"trailing traversal", "notes/../../x.txt", "x.txt"},
	{"bare parent", "..", ""},
	{"bare dot", ".", ""},
	{"empty", "", ""},
	{"root", "/", ""},
	{"only separators", "///", ""},
	// A dotfile is NOT hostile and must survive: refusing it would break
	// a legitimate attachment for no security gain.
	{"leading dot is a normal file", ".gitignore", ".gitignore"},
	{"ordinary name", "report.pdf", "report.pdf"},
	{"spaces and unicode", "quarterly report — final.pdf", "quarterly report — final.pdf"},
}

func TestSafeAttachmentName(t *testing.T) {
	for _, tt := range hostileNames {
		t.Run(tt.name, func(t *testing.T) {
			got, err := safeAttachmentName(tt.raw)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("safeAttachmentName(%q) = %q, want a refusal", tt.raw, got)
				}
				if code := cli.MapError(err).Code; code != cli.CodePathInvalid {
					t.Errorf("code = %q, want %q", code, cli.CodePathInvalid)
				}
				return
			}
			if err != nil {
				t.Fatalf("safeAttachmentName(%q) = %v, want %q", tt.raw, err, tt.want)
			}
			if got != tt.want {
				t.Errorf("safeAttachmentName(%q) = %q, want %q", tt.raw, got, tt.want)
			}
			// Whatever came back must be a plain name: no separator, no
			// traversal. This is the property the sandbox then enforces
			// independently, asserted here so a regression in EITHER layer
			// is attributable.
			if strings.ContainsRune(got, filepath.Separator) {
				t.Errorf("reduced name %q still contains a path separator", got)
			}
			if !filepath.IsLocal(got) {
				t.Errorf("reduced name %q is not a local path", got)
			}
		})
	}
}

// TestCreateInDirCannotEscape is the sandbox's own test, and the reason
// os.Root is used instead of a string prefix check.
//
// The symlink case is the one that matters: `filepath.Clean` plus
// `strings.HasPrefix` would see a name with no separators and no `..`,
// conclude it was safe, and follow the link straight out of the
// directory. os.Root resolves every component against the opened
// directory and refuses.
func TestCreateInDirCannotEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows; the sandbox is os.Root either way")
	}
	outside := t.TempDir()
	dir := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("original"), 0o600); err != nil {
		t.Fatalf("seed the file the attack targets: %v", err)
	}
	// A symlink ALREADY inside the destination, pointing out of it —
	// planted by anything with write access to the download directory.
	if err := os.Symlink(secret, filepath.Join(dir, "escape.txt")); err != nil {
		t.Fatalf("plant the symlink: %v", err)
	}

	// --overwrite, so O_EXCL is not what refuses this: the point is that
	// the SANDBOX refuses to follow the link even when the caller has
	// asked to replace a file.
	file, _, root, err := createInDir(dir, "escape.txt", true)
	if err == nil {
		file.Close() //nolint:errcheck // the test has already failed
		root.Close() //nolint:errcheck // test cleanup
		t.Fatal("createInDir followed a symlink out of the destination directory")
	}
	if got, err := os.ReadFile(secret); err != nil || string(got) != "original" {
		t.Errorf("the file outside the sandbox was modified: %q, %v", got, err)
	}
}

// TestCreateInDirRefusesAnExistingFile pins the no-silent-overwrite
// rule, and that --overwrite is what lifts it.
func TestCreateInDirRefusesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(existing, []byte("mine"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	file, _, root, err := createInDir(dir, "report.pdf", false)
	if err == nil {
		file.Close() //nolint:errcheck // the test has already failed
		root.Close() //nolint:errcheck // test cleanup
		t.Fatal("createInDir replaced an existing file without being asked")
	}
	if !strings.Contains(err.Error(), "--"+flagOverwrite) {
		t.Errorf("refusal %q does not name --%s, which is the fix", err, flagOverwrite)
	}
	if got, _ := os.ReadFile(existing); string(got) != "mine" {
		t.Errorf("the existing file was modified: %q", got)
	}

	file, path, root, err := createInDir(dir, "report.pdf", true)
	if err != nil {
		t.Fatalf("--overwrite must replace it: %v", err)
	}
	defer root.Close() //nolint:errcheck // test cleanup
	defer file.Close() //nolint:errcheck // test cleanup
	if path != existing {
		t.Errorf("path = %q, want %q", path, existing)
	}
}

// TestCreateInDirWritesPrivately pins 0600: an attachment can be
// anything a tenant uploaded, and this CLI has no way to know whether it
// is confidential.
func TestCreateInDirWritesPrivately(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits do not apply")
	}
	dir := t.TempDir()
	file, path, root, err := createInDir(dir, "report.pdf", false)
	if err != nil {
		t.Fatalf("createInDir: %v", err)
	}
	defer root.Close() //nolint:errcheck // test cleanup
	file.Close()       //nolint:errcheck // closing before Stat is the point

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600 — an attachment may be confidential and this CLI cannot know", perm)
	}
}

// TestDownloadHostileNameNeverEscapes drives both layers together, the
// way the command does: every hostile name either refuses or lands
// inside the destination, and NOTHING is ever created outside it.
//
// The outside directory is checked after every case rather than once at
// the end, so a failure names the input that caused it.
func TestDownloadHostileNameNeverEscapes(t *testing.T) {
	for _, tt := range hostileNames {
		t.Run(tt.name, func(t *testing.T) {
			outside := t.TempDir()
			dir := filepath.Join(outside, "downloads")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatalf("mkdir: %v", err)
			}

			name, err := safeAttachmentName(tt.raw)
			if err != nil {
				if tt.want != "" {
					t.Fatalf("safeAttachmentName(%q) refused a name that should reduce to %q: %v", tt.raw, tt.want, err)
				}
				assertOnlyDownloadsIn(t, outside)
				return
			}
			file, path, root, err := createInDir(dir, name, false)
			if err != nil {
				t.Fatalf("createInDir(%q) = %v", name, err)
			}
			defer root.Close() //nolint:errcheck // test cleanup
			file.Close()       //nolint:errcheck // test cleanup

			// The written file must be a direct child of the destination.
			if got := filepath.Dir(path); got != dir {
				t.Errorf("wrote to %q, whose directory is %q, want %q", path, got, dir)
			}
			assertOnlyDownloadsIn(t, outside)
		})
	}
}

// assertOnlyDownloadsIn fails if anything appeared in the parent of the
// destination — the directory a traversal would land in.
func assertOnlyDownloadsIn(t *testing.T, outside string) {
	t.Helper()
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatalf("read %s: %v", outside, err)
	}
	for _, e := range entries {
		if e.Name() != "downloads" {
			t.Errorf("%q appeared outside the destination directory", e.Name())
		}
	}
}

// TestResolveDirIsAbsolute pins that the envelope publishes a directory
// a reader can act on: "." tells an agent reading the output later
// nothing at all.
func TestResolveDirIsAbsolute(t *testing.T) {
	for _, in := range []string{"", ".", "downloads"} {
		got, err := resolveDir(in)
		if err != nil {
			t.Fatalf("resolveDir(%q): %v", in, err)
		}
		if !filepath.IsAbs(got) {
			t.Errorf("resolveDir(%q) = %q, want an absolute path", in, got)
		}
	}
}

// TestValidateDownloadNameRefusesAPathAs applies the same check to the
// CALLER's own --as, before any request — a script assembling a name out
// of variables is a real way to end up with `../../x`.
func TestValidateDownloadNameRefusesAPathAs(t *testing.T) {
	for _, bad := range []string{"../evil.txt", "/etc/passwd", "a/b.txt", "..", "."} {
		err := validateDownloadName(bad)
		if err == nil {
			t.Errorf("--%s %q was accepted; it is not a plain file name", flagAs, bad)
			continue
		}
		var e *cli.InputError
		if !errors.As(err, &e) {
			t.Errorf("error for %q is not a *cli.InputError: %v", bad, err)
			continue
		}
		if e.Field != flagAs {
			t.Errorf("field = %q, want %q", e.Field, flagAs)
		}
	}
	for _, good := range []string{"", "report.pdf", ".gitignore"} {
		if err := validateDownloadName(good); err != nil {
			t.Errorf("--%s %q was refused: %v", flagAs, good, err)
		}
	}
}
