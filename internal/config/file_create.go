package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	xos "github.com/gechr/x/os"
)

// OpenNewFile prepares an absent configuration target without reading existing
// contents. Existing names, including dangling links and directories, refuse
// creation. A symlinked parent is resolved through the same owner as OpenFile.
func OpenNewFile(ctx context.Context, path string) (*File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	requested, err := resolvePath(path)
	if err != nil {
		return nil, err
	}
	requested, err = absoluteConfigurationPath(requested)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	resolved, err := resolveCreationPath(requested)
	if err != nil {
		return nil, fmt.Errorf("resolve config link: %w", err)
	}
	if _, err := os.Lstat(resolved); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return nil, ErrFileChanged
		}
		return nil, fmt.Errorf("inspect config target: %w", err)
	}
	return &File{requested: requested, path: resolved}, nil
}

// Create publishes a complete validated configuration without replacing an
// existing name. x/os.AtomicWrite replaces its destination, so it stages the
// private sibling only; os.Link supplies the required atomic no-clobber step.
// Filesystems without hard-link support fail rather than fall back to a
// replacing rename or expose a partially written destination. Directory sync
// can fail after publication; the returned bool still reports that creation.
func (f *File) Create(ctx context.Context, data []byte) (bool, error) {
	return f.create(ctx, data, fileCreationOps{
		resolve: resolveCreationPath, link: os.Link, syncDirectory: syncCreationDirectory,
	})
}

// These concrete filesystem boundaries permit controlled failure tests without
// replacing validation, staging, snapshot ownership or process-global state.
type fileCreationOps struct {
	resolve       func(string) (string, error)
	link          func(string, string) error
	syncDirectory func(string) error
}

func (f *File) create(ctx context.Context, data []byte, ops fileCreationOps) (bool, error) {
	return f.createValidated(ctx, data, ValidateFile, ops)
}

func (f *File) createValidated(ctx context.Context, data []byte, validate func([]byte) error, ops fileCreationOps) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := validate(data); err != nil {
		return false, err
	}
	if f.exists {
		return false, ErrFileChanged
	}
	if err := f.checkCreationTarget(ops.resolve); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	dir := filepath.Dir(f.path)
	exists, err := xos.IsDir(dir)
	if err != nil {
		return false, fmt.Errorf("inspect config directory: %w", err)
	}
	if !exists {
		if err := xos.EnsureDir(dir, 0o700); err != nil {
			return false, fmt.Errorf("create config directory: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	staged, err := os.CreateTemp(dir, ".jira-init-*")
	if err != nil {
		return false, fmt.Errorf("prepare config creation: %w", err)
	}
	name := staged.Name()
	defer os.Remove(name) //nolint:errcheck // private staging cleanup must retain publication and durability outcomes
	if err := staged.Close(); err != nil {
		return false, fmt.Errorf("close config staging file: %w", err)
	}
	if err := xos.AtomicWrite(name, data, 0o600); err != nil {
		return false, fmt.Errorf("stage config creation: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := f.checkCreationTarget(ops.resolve); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := ops.link(name, f.path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, ErrFileChanged
		}
		return false, fmt.Errorf("publish config without replacing existing files: %w", err)
	}
	if err := ops.syncDirectory(dir); err != nil {
		return true, fmt.Errorf("sync directory after config creation: %w", err)
	}
	return true, nil
}

func (f *File) checkCreationTarget(resolve func(string) (string, error)) error {
	resolved, err := resolve(f.requested)
	if err != nil {
		return fmt.Errorf("re-resolve config link: %w", err)
	}
	if resolved != f.path {
		return ErrFileChanged
	}
	if _, err := os.Lstat(f.path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return ErrFileChanged
		}
		return fmt.Errorf("inspect config creation target: %w", err)
	}
	return nil
}

// Walk missing parents back to their resolved existing ancestor before taking
// a snapshot. Platform resolution preserves junction targets and native naming
// syntax so creating children cannot change the snapshot's destination.
func resolveCreationPath(path string) (string, error) {
	resolved, err := resolveConfigurationPath(path)
	if err != nil {
		return resolved, err
	}
	parent := filepath.Dir(resolved)
	if parent == resolved {
		return resolved, nil
	}
	resolvedParent, err := resolveCreationPath(parent)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolvedParent)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err == nil && !info.IsDir() {
		return "", errors.New("configuration ancestor is not a directory")
	}
	return joinConfigurationPath(resolvedParent, resolved), nil
}

func syncCreationDirectory(dir string) error {
	if !xos.IsUnix() {
		return nil
	}
	parent, err := os.Open(dir) //nolint:gosec // resolved configuration parent, opened only for directory sync
	if err != nil {
		return fmt.Errorf("open created config directory for sync: %w", err)
	}
	return errors.Join(parent.Sync(), parent.Close())
}
