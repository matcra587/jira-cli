package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	xos "github.com/gechr/x/os"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/v2"
)

// ErrFileChanged refuses a local update when the original bytes, existence or
// resolved target differ from the snapshot.
var ErrFileChanged = errors.New("configuration changed before save")

// ErrFileInvalid identifies a candidate that cannot be saved as configuration.
var ErrFileInvalid = errors.New("invalid configuration file")

// File owns an immutable snapshot of the selected configuration file. It reads
// only authored bytes, without defaults, environment values or credentials.
// It permits opening an invalid file so an editor can repair it. Saves require
// valid configuration and retain exact candidate bytes. Reopen after a save.
// Snapshot checks compare bytes, existence and resolved target, not inode
// identity or a filesystem compare-and-swap against external writers.
type File struct {
	profileSelection    string
	profileSelectionErr error
	requested           string
	path                string
	original            []byte
	exists              bool
	missingCause        error // retained native read failure for an absent explicit path
}

// SelectedPath selects the global --config path or the platform default.
// Path selection itself does not read the file or resolve links.
func SelectedPath() (string, error) { return resolvePath("") }

// OpenFile reads a raw snapshot. A missing file is an empty update target;
// dangling symlinks are refused so an atomic replacement cannot detach them.
func OpenFile(ctx context.Context, path string) (*File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	requested, err := resolvePath(path)
	if err != nil {
		return nil, err
	}
	requested, err = filepath.Abs(requested)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	resolved, err := resolveCreationPath(requested)
	if err != nil {
		return nil, fmt.Errorf("resolve config link: %w", err)
	}
	data, err := readFileInput(resolved)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if !exists {
		// ResolveLenient cannot follow a link whose target is missing. Treat
		// that differently from a new file, or AtomicWrite would detach it.
		if _, linkErr := os.Lstat(resolved); linkErr == nil {
			return nil, fmt.Errorf("config link has no readable target: %w", err)
		} else if !errors.Is(linkErr, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect missing config: %w", linkErr)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	profile, profileErr := captureFileProfile()
	return &File{requested: requested, path: resolved, original: data, exists: exists, missingCause: err, profileSelection: profile, profileSelectionErr: profileErr}, nil
}

// ReadFile permits an absent default file but refuses an absent explicitly
// selected path. Updates use OpenFile because creating their target is valid;
// a read must not silently substitute an empty file for a mistyped --config.
func ReadFile(ctx context.Context, path string) (*File, error) {
	explicit := path != "" || currentFlagLayer().Path != ""
	file, err := OpenFile(ctx, path)
	if err != nil {
		return nil, err
	}
	if explicit && !file.Exists() {
		return nil, fmt.Errorf("read config %s: %w", file.Path(), file.missingCause)
	}
	return file, nil
}

// Path is the resolved file that a save would replace.
func (f *File) Path() string { return f.path }

// Exists reports whether the snapshot contained an existing file.
func (f *File) Exists() bool { return f.exists }

// Contents returns a caller-owned copy of the authored bytes.
func (f *File) Contents() []byte { return bytes.Clone(f.original) }

// Save validates and atomically replaces a changed candidate at mode 0600.
// An unchanged candidate leaves bytes and metadata untouched. On a write error,
// inspect the file before retrying: directory sync can fail after replacement.
func (f *File) Save(ctx context.Context, data []byte) (bool, error) {
	return f.saveValidated(ctx, data, ValidateFile)
}

// Ordinary saves always use ValidateFile. Recovery supplies its verified
// preimage validator through this private boundary, retaining one writer.
func (f *File) saveValidated(ctx context.Context, data []byte, validate func([]byte) error) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := validate(data); err != nil {
		return false, err
	}
	if bytes.Equal(data, f.original) {
		return false, nil
	}
	if err := f.checkSnapshot(ctx); err != nil {
		return false, err
	}
	dir := filepath.Dir(f.path)
	exists, err := xos.IsDir(dir)
	if err != nil {
		return false, fmt.Errorf("inspect config directory: %w", err)
	}
	// An explicit config path may live in a shared directory. EnsureDir also
	// changes existing permissions, so use it only for a missing directory.
	if !exists {
		if err := xos.EnsureDir(dir, 0o700); err != nil {
			return false, fmt.Errorf("create config directory: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := xos.AtomicWrite(f.path, data, 0o600); err != nil {
		return false, fmt.Errorf("save config: %w", err)
	}
	return true, nil
}

func (f *File) checkSnapshot(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	resolved, err := resolveCreationPath(f.requested)
	if err != nil {
		return fmt.Errorf("re-resolve config link: %w", err)
	}
	if resolved != f.path {
		return ErrFileChanged
	}
	current, err := readFileInput(f.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("re-read config: %w", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		if _, linkErr := os.Lstat(f.path); linkErr == nil {
			return ErrFileChanged
		} else if !errors.Is(linkErr, os.ErrNotExist) {
			return fmt.Errorf("inspect missing config: %w", linkErr)
		}
	}
	if (err == nil) != f.exists || !bytes.Equal(current, f.original) {
		return ErrFileChanged
	}
	return ctx.Err()
}

// ValidateFile checks exactly the supplied TOML bytes, without effective
// settings. Partial preference files are valid; unknown keys and invalid
// declared values are refused. Validation never accesses credentials.
func ValidateFile(data []byte) error {
	_, _, err := decodeFile(data)
	return err
}

func decodeFile(data []byte) (map[string]any, *Config, error) {
	doc, err := decodeProfileDocument(data, true)
	if err != nil {
		return nil, nil, err
	}
	selected, _, err := doc.selected("")
	if err != nil {
		return nil, nil, err
	}
	decoded, err := unmarshalFields(selected)
	return doc.fields, decoded, err
}

func unmarshalFields(fields map[string]any) (*Config, error) {
	k := koanf.New(delim)
	if err := k.Load(confmap.Provider(fields, ""), nil); err != nil {
		return nil, fmt.Errorf("load config snapshot: %w", err)
	}
	var decoded Config
	if err := unmarshalFile(k, &decoded); err != nil {
		return nil, err
	}
	return &decoded, nil
}
