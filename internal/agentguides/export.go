package agentguides

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"

	xos "github.com/gechr/x/os"
	"github.com/matcra587/docent"
	docentexport "github.com/matcra587/docent/export"
)

// ExportArtifact is a validated, rendered file relative to an export root.
type ExportArtifact struct {
	Slug    string
	Path    string
	Content []byte
}

// ExportReceipt retains published files and the last attempted relative path.
type ExportReceipt struct {
	Written   []string
	Attempted string
}

func exportRenderers() map[string]docentexport.ValidatingRenderer {
	return map[string]docentexport.ValidatingRenderer{
		"agent-skill":  docentexport.AgentSkill{NameQualifier: "jira"},
		"claude-skill": docentexport.ClaudeSkill{NameQualifier: "jira"},
	}
}

// ExportFormats lists the formats this host renders, in canonical order.
func ExportFormats() []string {
	return slices.Sorted(maps.Keys(exportRenderers()))
}

// PlanExport validates and renders the whole corpus before filesystem changes.
func PlanExport(guides *docent.GuideSet, format string) ([]ExportArtifact, error) {
	renderer, ok := exportRenderers()[format]
	if !ok {
		return nil, fmt.Errorf("unsupported guide export format %q", format)
	}
	artifacts := make([]ExportArtifact, 0, guides.Len())
	for guide := range guides.All() {
		if err := renderer.Validate(guide); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, ExportArtifact{Slug: guide.Slug, Path: renderer.RelPath(guide), Content: []byte(renderer.Render(guide))})
	}
	return artifacts, nil
}

// WriteExport publishes artifacts in order, retaining completed paths on error.
// The destination itself is caller-selected; descendant access is root-confined.
func WriteExport(ctx context.Context, directory string, artifacts []ExportArtifact, force bool) (receipt ExportReceipt, err error) {
	receipt = ExportReceipt{Written: []string{}}
	seen := make(map[string]bool, len(artifacts))
	for _, artifact := range artifacts {
		if !filepath.IsLocal(filepath.FromSlash(artifact.Path)) || seen[artifact.Path] {
			return receipt, fmt.Errorf("invalid or duplicate guide export path %q", artifact.Path)
		}
		seen[artifact.Path] = true
	}
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return receipt, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return receipt, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	// Check every known collision before publishing the first file. Link below
	// also refuses a destination created after this preflight, without a race.
	if !force {
		for _, artifact := range artifacts {
			if _, err := root.Lstat(filepath.FromSlash(artifact.Path)); !errors.Is(err, os.ErrNotExist) {
				receipt.Attempted = artifact.Path
				if err == nil {
					err = os.ErrExist
				}
				return receipt, err
			}
		}
	}
	for _, artifact := range artifacts {
		if err := ctx.Err(); err != nil {
			return receipt, err
		}
		receipt.Attempted = artifact.Path
		published, err := publishExportArtifact(ctx, root, artifact, force)
		if published {
			receipt.Written = append(receipt.Written, artifact.Path)
		}
		if err != nil {
			return receipt, err
		}
	}
	receipt.Attempted = ""
	return receipt, nil
}

// publishExportArtifact differs from x/os.AtomicWrite and docent/export.Write:
// publication must be confined to an os.Root and refuse existing files unless
// acknowledged. A same-directory temporary file keeps partial bytes private;
// Link publishes exclusively, while Rename replaces the entry without following
// a symlink or changing a hard-linked file elsewhere.
func publishExportArtifact(ctx context.Context, root *os.Root, artifact ExportArtifact, force bool) (published bool, err error) {
	rel := filepath.FromSlash(artifact.Path)
	directory := filepath.Dir(rel)
	if err := root.MkdirAll(directory, 0o750); err != nil {
		return false, err
	}
	temp := filepath.Join(directory, ".jira-export-"+rand.Text())
	file, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, err
	}
	defer func() {
		if cleanupErr := root.Remove(temp); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			err = errors.Join(err, cleanupErr)
		}
	}()
	n, writeErr := file.Write(artifact.Content)
	if writeErr == nil && n != len(artifact.Content) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if force {
		err = root.Rename(temp, rel)
	} else {
		err = root.Link(temp, rel)
	}
	if err != nil {
		return false, err
	}
	if !xos.IsUnix() {
		return true, nil
	}
	dir, err := root.Open(directory)
	if err != nil {
		return true, err
	}
	return true, errors.Join(dir.Sync(), dir.Close())
}
