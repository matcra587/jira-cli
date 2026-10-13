package issueedit

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/core"
)

const (
	SourceName   = "source.json"
	MarkdownName = "description.md"
	ManifestName = "manifest.json"
	// The native view and base64 preimage can exceed the response byte cap.
	MaxMemberBytes = 8 * core.DefaultMaxAPIResponseBytes
)

// Target owns one absent child of an already existing, canonical parent.
// Preparing it is local and creates nothing, including on a preview.
type Target struct {
	parent *os.Root
	name   string
	state  core.IssueEditDraftState
}

func draftState(path string) core.IssueEditDraftState {
	return core.IssueEditDraftState{
		Directory: path, SourceFile: filepath.Join(path, SourceName),
		MarkdownFile: filepath.Join(path, MarkdownName), ManifestFile: filepath.Join(path, ManifestName),
		Durability: "unconfirmed",
	}
}

// PrepareTarget refuses existing names and missing/non-directory parents before
// a caller opens credentials. Paths are literal; parent symlinks are resolved.
func PrepareTarget(ctx context.Context, path string) (*Target, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if path == "" {
		return nil, core.ErrIssueEditDraftInvalid
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, draftIO("resolve target", err)
	}
	parentPath, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return nil, draftIO("resolve existing parent", err)
	}
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return nil, draftIO("open existing parent", err)
	}
	name := filepath.Base(abs)
	if _, err := parent.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		closeErr := parent.Close()
		if err == nil {
			err = core.ErrIssueEditDraftChanged
		}
		return nil, errors.Join(err, closeErr)
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, parent.Close())
	}
	return &Target{parent: parent, name: name, state: draftState(filepath.Join(parentPath, name))}, nil
}

func (t *Target) State() core.IssueEditDraftState { return t.state }
func (t *Target) Close() error                    { return t.parent.Close() }

type publicationOps struct {
	link func(*os.Root, string, string) error
	sync func(*os.Root) error
}

func defaultPublicationOps() publicationOps {
	return publicationOps{link: func(root *os.Root, old, name string) error { return root.Link(old, name) }, sync: syncRoot}
}

// publishMember stages a complete private file and publishes without clobber.
// x/os.AtomicWrite replaces names and cannot operate through os.Root. These
// immutable draft members need rooted staging and a no-replacement link instead.
// The bool records link acknowledgment even if later directory sync fails.
func publishMember(ctx context.Context, root *os.Root, name string, data []byte, mode os.FileMode, ops publicationOps) (published bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	stage := ".jira-draft-" + rand.Text()
	file, err := root.OpenFile(stage, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, err
	}
	defer root.Remove(stage) //nolint:errcheck // only this private stage; preserve actual publication and durability outcomes
	defer func() { err = errors.Join(err, file.Close()) }()
	if _, err := file.Write(data); err != nil {
		return false, err
	}
	if err := file.Sync(); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := ops.link(root, stage, name); err != nil {
		return false, err
	}
	// Windows read-only attributes apply to every hard link. Remove the private
	// staging name before making the published inode read-only. The rooted file
	// handle keeps chmod/sync bound to that inode even if its name changes.
	if err := root.Remove(stage); err != nil {
		return true, err
	}
	if err := file.Chmod(mode); err != nil {
		return true, err
	}
	if err := file.Sync(); err != nil {
		return true, err
	}
	return true, ops.sync(root)
}

func syncRoot(root *os.Root) error {
	if !xos.IsUnix() {
		return nil
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func readMember(ctx context.Context, root *os.Root, name string, limit int64) (data []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect %s: %w", core.ErrIssueEditDraftInvalid, name, err)
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s must be a regular file", core.ErrIssueEditDraftInvalid, name)
	}
	file, err := openMember(root, name)
	if err != nil {
		return nil, draftIO("open "+name, err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return nil, draftIO("inspect "+name, err)
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return nil, core.ErrIssueEditDraftChanged
	}
	data, err = io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, draftIO("read "+name, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: %s exceeds the %d-byte draft member limit", core.ErrIssueEditDraftInvalid, name, limit)
	}
	after, err := root.Lstat(name)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(info, after) {
		return nil, errors.Join(core.ErrIssueEditDraftChanged, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

func draftIO(action string, err error) error {
	return fmt.Errorf("%w: %s: %w", core.ErrIssueEditDraftIO, action, err)
}
