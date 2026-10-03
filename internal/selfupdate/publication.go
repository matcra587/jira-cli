package selfupdate

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	xos "github.com/gechr/x/os"
	"github.com/google/uuid"
)

// MaxExecutableBytes bounds staging and executable snapshot reads to 256 MiB.
const MaxExecutableBytes int64 = 256 << 20

// ErrTargetChanged prevents publishing over an observed intervening replacement.
// It is a pre-publication check, not a filesystem compare-and-swap guarantee.
var ErrTargetChanged = errors.New("installed executable changed during update")

// Publication records filesystem facts even when durability or rollback fails.
type Publication struct {
	Published bool
	Durable   bool
	Uncertain bool
	Backup    string
	Bytes     int64
	SHA256    string
}

// Target retains the directory and original executable across release discovery
// and staging. Close it after the update; it owns no candidate descriptors.
type Target struct {
	mu            sync.Mutex
	root          *os.Root
	original      *os.File
	info          os.FileInfo
	name          string
	digest        [sha256.Size]byte
	syncDirectory func(*os.Root) error
}

// OpenTarget captures an existing regular executable without modifying it.
// Resolve an installation symlink before calling this function; publication
// refuses a symlink at the actual target rather than following a replaced one.
func OpenTarget(ctx context.Context, path string) (*Target, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("open installation directory: %w", err)
	}
	name := filepath.Base(path)
	file, info, err := openRegular(root, name)
	if err != nil {
		root.Close() //nolint:errcheck // no file writes occurred
		return nil, err
	}
	digest, err := executableDigest(ctx, file)
	if err != nil {
		file.Close() //nolint:errcheck // read-only executable
		root.Close() //nolint:errcheck // no file writes occurred
		return nil, err
	}
	return &Target{root: root, original: file, info: info, name: name, digest: digest, syncDirectory: syncInstallationDirectory}, nil
}

// Close releases the retained installation descriptors.
func (t *Target) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return errors.Join(t.original.Close(), t.root.Close())
}

// Publish stages a verified candidate and publishes only after a final context
// and original-file check. The caller supplies the candidate's SHA-256 after
// release integrity verification; copying rechecks it before any replacement.
//
// x/os.AtomicWrite in 0.6.0 cannot express descriptor-bound streaming, a final
// cancellation check, or a receipt after rename. Clive 0.4.9's internal Apply
// overwrites fixed .new/.old paths and does not check context before rename.
// This owner keeps those semantics explicit without changing release discovery.
func (t *Target) Publish(ctx context.Context, candidate *os.File, expectedSHA256 string) (Publication, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out Publication
	if err := ctx.Err(); err != nil {
		return out, err
	}
	info, err := candidate.Stat()
	if err != nil {
		return out, fmt.Errorf("inspect update candidate: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxExecutableBytes {
		return out, fmt.Errorf("update candidate must be a nonempty regular file of at most %d bytes", MaxExecutableBytes)
	}
	if _, err := candidate.Seek(0, io.SeekStart); err != nil {
		return out, fmt.Errorf("read update candidate from its start: %w", err)
	}
	stage := ".jira-update-" + uuid.NewString() + ".new"
	file, err := t.root.OpenFile(stage, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return out, fmt.Errorf("create update stage: %w", err)
	}
	defer t.root.Remove(stage) //nolint:errcheck // only our unique stage; absent after publication
	defer file.Close()         //nolint:errcheck // successful close is checked before publication
	hash := sha256.New()
	out.Bytes, err = io.Copy(io.MultiWriter(file, hash), io.LimitReader(readerWithContext(ctx, candidate), MaxExecutableBytes+1))
	if err != nil {
		return out, fmt.Errorf("copy update candidate: %w", err)
	}
	out.SHA256 = fmt.Sprintf("%x", hash.Sum(nil))
	if out.Bytes != info.Size() || out.Bytes > MaxExecutableBytes || out.SHA256 != expectedSHA256 {
		return out, fmt.Errorf("update candidate size or SHA-256 changed before publication")
	}
	if err := file.Chmod(t.info.Mode().Perm()); err != nil {
		return out, fmt.Errorf("set staged executable permissions: %w", err)
	}
	if err := file.Sync(); err != nil {
		return out, fmt.Errorf("sync staged executable: %w", err)
	}
	if err := file.Close(); err != nil {
		return out, fmt.Errorf("close staged executable: %w", err)
	}
	if err := t.checkOriginal(ctx); err != nil {
		return out, err
	}
	out, err = replaceExecutable(ctx, xos.IsWindows(), t.root.Rename, stage, t.name, out)
	if err != nil || xos.IsWindows() {
		return out, err
	}
	if xos.IsUnix() {
		if err := t.syncDirectory(t.root); err != nil {
			return out, err
		}
		out.Durable = true
	}
	return out, ctx.Err()
}

// syncInstallationDirectory follows x/os's Unix durability policy through the
// retained directory. Errors here describe an already published executable.
func syncInstallationDirectory(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("executable published but directory sync unavailable: %w", err)
	}
	defer directory.Close() //nolint:errcheck // directory descriptor has no buffered writes
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("executable published but directory sync failed: %w", err)
	}
	return nil
}

func (t *Target) checkOriginal(ctx context.Context) error {
	file, info, err := openRegular(t.root, t.name)
	if err != nil {
		return errors.Join(ErrTargetChanged, err)
	}
	defer file.Close() //nolint:errcheck // read-only executable
	if !os.SameFile(t.info, info) || t.info.Mode() != info.Mode() {
		return ErrTargetChanged
	}
	digest, err := executableDigest(ctx, file)
	if err != nil {
		return err
	}
	if digest != t.digest {
		return ErrTargetChanged
	}
	return nil
}

func openRegular(root *os.Root, name string) (*os.File, os.FileInfo, error) {
	entry, err := root.Lstat(name)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect executable: %w", err)
	}
	if !entry.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("executable is not a regular file")
	}
	file, err := root.OpenFile(name, executableReadFlags(), 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open executable: %w", err)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(entry, info) {
		file.Close() //nolint:errcheck // read-only executable
		return nil, nil, errors.Join(ErrTargetChanged, err)
	}
	return file, info, nil
}

func executableDigest(ctx context.Context, file *os.File) ([sha256.Size]byte, error) {
	var out [sha256.Size]byte
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(readerWithContext(ctx, file), MaxExecutableBytes+1))
	if err != nil {
		return out, fmt.Errorf("hash executable: %w", err)
	}
	if count <= 0 || count > MaxExecutableBytes {
		return out, fmt.Errorf("executable must be nonempty and contain at most %d bytes", MaxExecutableBytes)
	}
	copy(out[:], hash.Sum(nil))
	return out, ctx.Err()
}

func replaceExecutable(ctx context.Context, windows bool, rename func(string, string) error, stage, name string, out Publication) (Publication, error) {
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if windows {
		return replaceWindows(ctx, rename, stage, name, out)
	}
	if err := rename(stage, name); err != nil {
		return out, fmt.Errorf("publish staged executable: %w", err)
	}
	out.Published = true
	return out, nil
}

// replaceWindows leaves a uniquely named backup because Windows cannot replace
// a running executable directly. A failed publication restores the old name;
// a failed restoration exposes the backup and uncertain outcome to the caller.
// Windows directory durability is not claimed by a successful pair of renames.
func replaceWindows(ctx context.Context, rename func(string, string) error, stage, name string, out Publication) (Publication, error) {
	backup := ".jira-update-" + uuid.NewString() + ".old"
	if err := rename(name, backup); err != nil {
		return out, fmt.Errorf("retain previous executable: %w", err)
	}
	out.Backup = backup
	err := ctx.Err()
	if err == nil {
		err = rename(stage, name)
	}
	if err != nil {
		if rollbackErr := rename(backup, name); rollbackErr != nil {
			out.Uncertain = true
			return out, fmt.Errorf("publication failed and rollback failed: %w", errors.Join(err, rollbackErr))
		}
		out.Backup = ""
		return out, fmt.Errorf("publication failed; previous executable restored: %w", err)
	}
	out.Published = true
	return out, ctx.Err()
}

func readerWithContext(ctx context.Context, reader io.Reader) io.Reader {
	return readerFunc(func(bytes []byte) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return reader.Read(bytes)
	})
}

type readerFunc func([]byte) (int, error)

func (read readerFunc) Read(bytes []byte) (int, error) { return read(bytes) }
