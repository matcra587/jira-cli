package agentguides

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestExportPlanAndPublication(t *testing.T) {
	t.Parallel()
	guides, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range ExportFormats() {
		t.Run(format, func(t *testing.T) {
			artifacts, err := PlanExport(guides, format)
			if err != nil || len(artifacts) != guides.Len() {
				t.Fatal("export plan lost the validated corpus")
			}
			directory := filepath.Join(t.TempDir(), "skills")
			receipt, err := WriteExport(t.Context(), directory, artifacts, false)
			if err != nil || len(receipt.Written) != len(artifacts) || receipt.Attempted != "" {
				t.Fatal("fresh export did not publish every artifact")
			}
			for i, artifact := range artifacts {
				if artifact.Path != "jira-"+artifact.Slug+"/SKILL.md" || receipt.Written[i] != artifact.Path {
					t.Fatal("qualified skill name, directory or publication order changed")
				}
				file := filepath.Join(directory, filepath.FromSlash(artifact.Path))
				body, err := os.ReadFile(file)
				if err != nil || !slices.Equal(body, artifact.Content) {
					t.Fatal("published artifact differs from the rendered plan")
				}
				info, err := os.Stat(file)
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatal("exported artifact did not retain private file permissions")
				}
				if strings.Contains(string(body), "when_to_use:") != (format == "claude-skill") || !strings.Contains(string(body), "name: jira-"+artifact.Slug+"\n") {
					t.Fatal("export frontmatter lost its selected format or qualified name")
				}
			}
			collision, err := WriteExport(t.Context(), directory, artifacts, false)
			if !errors.Is(err, os.ErrExist) || len(collision.Written) != 0 || collision.Attempted != artifacts[0].Path {
				t.Fatal("unacknowledged existing corpus was overwritten")
			}
		})
	}
}

func TestExportPublicationRefusesConcurrentCollision(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	// Simulate a file appearing after the corpus collision preflight.
	if err := root.Mkdir("jira-guide", 0o750); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile("jira-guide/SKILL.md", []byte("keep this edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	published, err := publishExportArtifact(t.Context(), root, ExportArtifact{Path: "jira-guide/SKILL.md", Content: []byte("new")}, false)
	if published || !errors.Is(err, os.ErrExist) {
		t.Fatal("publication overwrote a file created after preflight")
	}
	body, err := root.ReadFile("jira-guide/SKILL.md")
	if err != nil || string(body) != "keep this edit" {
		t.Fatal("concurrent user edit changed")
	}
	files, err := os.ReadDir(filepath.Join(directory, "jira-guide"))
	if err != nil || len(files) != 1 {
		t.Fatal("failed publication leaked a temporary file")
	}
}

func TestExportPreflightsEntireCorpusBeforePublication(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	artifacts := []ExportArtifact{{Path: "first/SKILL.md", Content: []byte("first")}, {Path: "last/SKILL.md", Content: []byte("last")}}
	if err := os.Mkdir(filepath.Join(directory, "last"), 0o750); err != nil {
		t.Fatal(err)
	}
	last := filepath.Join(directory, "last", "SKILL.md")
	if err := os.WriteFile(last, []byte("keep this edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	receipt, err := WriteExport(t.Context(), directory, artifacts, false)
	if !errors.Is(err, os.ErrExist) || len(receipt.Written) != 0 || receipt.Attempted != artifacts[1].Path {
		t.Fatal("a known later collision allowed partial publication")
	}
	if _, err := os.Stat(filepath.Join(directory, "first")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preflight published an earlier artifact before checking the entire corpus")
	}
	body, err := os.ReadFile(last)
	if err != nil || string(body) != "keep this edit" {
		t.Fatal("preflight changed an existing user edit")
	}
}

func TestExportForceReplacesLinksWithoutChangingTargets(t *testing.T) {
	t.Parallel()
	for _, hard := range []bool{false, true} {
		t.Run(map[bool]string{false: "symlink", true: "hardlink"}[hard], func(t *testing.T) {
			directory := t.TempDir()
			outside := filepath.Join(t.TempDir(), "original")
			if err := os.WriteFile(outside, []byte("keep outside"), 0o600); err != nil {
				t.Fatal(err)
			}
			parent := filepath.Join(directory, "jira-guide")
			if err := os.Mkdir(parent, 0o750); err != nil {
				t.Fatal(err)
			}
			link := os.Symlink
			if hard {
				link = os.Link
			}
			if err := link(outside, filepath.Join(parent, "SKILL.md")); err != nil {
				t.Fatal(err)
			}
			receipt, err := WriteExport(t.Context(), directory, []ExportArtifact{{Path: "jira-guide/SKILL.md", Content: []byte("replacement")}}, true)
			if err != nil || len(receipt.Written) != 1 {
				t.Fatal("acknowledged entry replacement failed")
			}
			body, err := os.ReadFile(outside)
			if err != nil || string(body) != "keep outside" {
				t.Fatal("export replacement changed a link's target")
			}
		})
	}
}

func TestExportRejectsEscapesBeforePublication(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"../outside", "/outside", ""} {
		directory := filepath.Join(t.TempDir(), "not-created")
		_, err := WriteExport(t.Context(), directory, []ExportArtifact{{Path: path}}, false)
		if err == nil {
			t.Fatal("invalid relative export path was accepted")
		}
		if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("invalid export path caused directory creation")
		}
	}
	directory := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(directory, "jira-guide")); err != nil {
		t.Fatal(err)
	}
	receipt, err := WriteExport(t.Context(), directory, []ExportArtifact{{Path: "jira-guide/SKILL.md", Content: []byte("must not escape")}}, true)
	if err == nil || len(receipt.Written) != 0 {
		t.Fatal("export followed a parent symlink outside its root")
	}
	files, err := os.ReadDir(outside)
	if err != nil || len(files) != 0 {
		t.Fatal("export touched the directory outside its root")
	}
}

type cancelAfterPublication struct {
	context.Context //nolint:containedctx // A test context decorator cancels at a deterministic publication boundary.
	cancel          context.CancelFunc
	path            string
}

func (c cancelAfterPublication) Err() error {
	if _, err := os.Stat(c.path); err == nil {
		c.cancel()
	}
	return c.Context.Err()
}

func TestExportCancellationRetainsPublishedFiles(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	artifacts := []ExportArtifact{{Path: "first/SKILL.md", Content: []byte("first")}, {Path: "second/SKILL.md", Content: []byte("second")}}
	observed := cancelAfterPublication{Context: ctx, cancel: cancel, path: filepath.Join(directory, "first", "SKILL.md")}
	receipt, err := WriteExport(observed, directory, artifacts, false)
	if !errors.Is(err, context.Canceled) || !slices.Equal(receipt.Written, []string{"first/SKILL.md"}) {
		t.Fatal("cancellation lost the published prefix")
	}
	if _, err := os.Stat(filepath.Join(directory, "second")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("export continued after cancellation")
	}
}
