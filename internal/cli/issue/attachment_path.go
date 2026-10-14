package issue

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/nativepath"
)

// THE ONLY TRUST BOUNDARY IN THIS CLI WHERE A REMOTE VALUE CHOOSES A
// LOCAL PATH.
//
// Everything else this tree writes goes to a path the CALLER typed. A
// download does not: the filename comes back in the attachment's
// metadata, which means whoever uploaded the file — not necessarily
// whoever is downloading it — picked the name that decides where bytes
// land. An issue on a shared tenant is an open upload form for anyone
// with a Jira account, and an agent running this command is not a
// person who would notice `../../.ssh/authorized_keys` scrolling past.
//
// The defense is two independent layers, and the second is the one that
// actually holds:
//
//  1. NAME HYGIENE (safeAttachmentName). filepath.Base strips every
//     directory component, so a name carrying separators, a traversal,
//     or an absolute path is reduced to its last element before anything
//     touches the filesystem. Then filepath.IsLocal rejects what is left
//     if it still could not be a plain file in a directory — which on
//     Windows also catches the reserved device names (NUL, CON, COM1)
//     that are not paths at all but still escape a directory's meaning.
//     This layer exists to produce a good ERROR, not to be the guard.
//
//  2. THE SANDBOX (createInDir). The file is created through an os.Root
//     opened on the destination directory, so the kernel — not this
//     package's string handling — enforces that the write lands inside
//     it. That is what makes the guarantee hold against the case string
//     checks cannot see: a SYMLINK already sitting in the destination
//     directory, pointing anywhere, which `filepath.Clean` plus a prefix
//     comparison would follow without complaint. os.Root resolves every
//     component relative to the opened directory and refuses to walk out
//     of it.
//
// Layer 2 alone would be sufficient and layer 1 alone would not, which
// is worth stating in that order so nobody later "simplifies" by keeping
// the readable half.

// safeAttachmentName reduces a server-supplied filename to something
// that can only ever be a plain file inside a directory, or refuses it.
//
// It returns the caller's own taxonomy error rather than a bare one,
// because this is reachable from ordinary use — a file legitimately
// named `.` is not a thing, but a tenant can absolutely hold an
// attachment whose name is hostile — and the caller deserves to be told
// which name was refused rather than watching the command fail obscurely.
func safeAttachmentName(raw string) (string, error) {
	// FromSlash first, so a name using forward slashes is split by Base
	// on the platforms where that is the separator. On Linux this is the
	// identity; on Windows it turns `a/b` into `a\b` so Base sees the
	// component it should.
	name := filepath.Base(filepath.FromSlash(raw))
	switch name {
	case "", ".", "..", string(filepath.Separator):
		// Base maps an empty name to ".", a bare root to the separator,
		// and leaves ".." alone — none of which name a file. Listed
		// explicitly because IsLocal accepts "." and this must not.
		return "", refuseAttachmentName(raw, "it does not name a file")
	}
	if !filepath.IsLocal(name) {
		return "", refuseAttachmentName(raw, "it is not a plain file name")
	}
	return name, nil
}

// refuseAttachmentName builds the refusal, naming the value Jira sent
// rather than the reduced one — the caller needs to see what the SERVER
// said to understand why their download stopped.
//
// The raw name goes through no sink here because this is an error
// message, and the error layer sanitizes what it renders; a sink applied
// twice would strip the very characters that make the refusal
// intelligible.
func refuseAttachmentName(raw, why string) error {
	return cli.NewCodedError(cli.CodePathInvalid, fmt.Sprintf(
		"the attachment's own filename (%q) cannot be written safely: %s — download it under a name you choose with --%s",
		raw, why, flagAs,
	))
}

// createInDir creates name inside dir, refusing anything that would land
// outside it.
//
// It returns the open file, its path, and the root that created it.
// The caller owns both handles; cleanup must use that same root.
//
// **O_EXCL unless the caller asked otherwise.** A download that silently
// replaced an existing file would be the second surprise in a command
// whose first one is that the server chose the name — and the two
// compose badly: an attachment named `notes.md` overwriting the caller's
// own `notes.md` is a data loss nobody authorized. --overwrite is the
// explicit opt-in, and it is a separate flag from --force rather than a
// reuse of it, because --force means "skip a confirmation" everywhere
// else in this tree and overloading it would make both vaguer.
//
// **0600, not 0644.** An attachment can be anything a tenant uploaded,
// including something confidential, and this CLI has no idea which. The
// conservative mode is the one that does not need to know.
func createInDir(dir, name string, overwrite bool) (*os.File, string, *os.Root, error) {
	// os.OpenRoot is the sandbox (see this file's own doc comment). It
	// fails here when dir does not exist or is not a directory, which is
	// the right moment for that to be reported: before a request, rather
	// than after the bytes are already streaming.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "", nil, cli.NewCodedError(cli.CodeLocalIO, fmt.Sprintf(
			"cannot write into %s: %v", dir, err,
		))
	}

	file, err := nativepath.CreateFile(root, name, overwrite, 0o600)
	if err != nil {
		root.Close() //nolint:errcheck // no file was created
		return nil, "", nil, createInDirError(dir, name, overwrite, err)
	}
	return file, nativepath.FilePath(dir, name), root, nil
}

// createInDirError turns a create failure into the caller's own error,
// separating the two cases that need different advice.
//
// An existing file gets its own message naming --overwrite, because that
// is a routine collision with a one-flag fix. Everything else — a
// refused traversal, a permission problem, a read-only filesystem —
// carries the underlying reason, since there is no single remedy to
// suggest.
func createInDirError(dir, name string, overwrite bool, err error) error {
	target := nativepath.FilePath(dir, name)
	if !overwrite && errors.Is(err, os.ErrExist) {
		return cli.NewCodedError(cli.CodeLocalIO, fmt.Sprintf(
			"%s already exists — pass --%s to replace it, or --%s to write under a different name",
			target, flagOverwrite, flagAs,
		))
	}
	return cli.NewCodedError(cli.CodeLocalIO, fmt.Sprintf("cannot write %s: %v", target, err))
}

// previewDownloadDestination checks the resolved entry without creating it.
// This is only a preview: the writer still enforces its own sandbox and
// collision policy because another process can change the directory later.
func previewDownloadDestination(dir, name string, overwrite bool) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return createInDirError(dir, name, overwrite, err)
	}
	defer root.Close() //nolint:errcheck // this read-only check has no buffered contents
	stat := nativepath.Lstat
	if overwrite {
		stat = nativepath.Stat
	}
	info, err := stat(root, name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return createInDirError(dir, name, overwrite, err)
	}
	if !overwrite {
		return createInDirError(dir, name, false, os.ErrExist)
	}
	if !info.Mode().IsRegular() {
		return createInDirError(dir, name, true, fmt.Errorf("overwrite target is not a regular file"))
	}
	return nil
}

// resolveDir turns the caller's --to into an absolute directory.
//
// Absolute, because the envelope publishes it and a relative path in a
// machine-readable result is only meaningful to whoever knows the
// process's working directory — which an agent reading the output later
// does not. It is also what the preview reports, and a preview naming
// "." would tell a caller nothing they did not type.
func resolveDir(to string) (string, error) {
	if to == "" {
		to = "."
	}
	abs, err := nativepath.Abs(to)
	if err != nil {
		return "", cli.NewCodedError(cli.CodePathInvalid, fmt.Sprintf(
			"--%s: cannot resolve %s: %v", flagTo, to, err,
		))
	}
	return abs, nil
}
