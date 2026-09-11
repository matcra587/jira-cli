package editor

// EditMarkdown is the spawn-and-roundtrip path used by `jira issue edit`
// when an interactive terminal is available. These unit tests exercise
// it directly with a fake-editor shell script so the cmd-layer agent
// gate (which would block this path under `go test`) is out of scope.
//
// The contract tests previously exercised this end-to-end via the CLI
// binary, but the agent gate correctly refuses the editor flow in
// non-TTY contexts, so the spawn machinery has to be tested here.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEditMarkdownRoundTripsThroughFakeEditor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-editor scripts use /bin/sh; not portable to Windows")
	}

	editorPath := filepath.Join(t.TempDir(), "fake-editor.sh")
	editorScript := `#!/bin/sh
# Replace the body section of the temp file with a known sentinel,
# preserving the YAML frontmatter the CLI writes.
cat > "$1" <<'EOF'
---
issue_key: PROJ-1
field_name: description
---

edited body via fake editor
EOF
`
	if err := os.WriteFile(editorPath, []byte(editorScript), 0o700); err != nil {
		t.Fatalf("write fake-editor script: %v", err)
	}

	got, err := EditMarkdown(context.Background(), "PROJ-1", "description", "old body", editorPath)
	if err != nil {
		t.Fatalf("EditMarkdown: %v", err)
	}
	if !strings.Contains(got, "edited body via fake editor") {
		t.Fatalf("EditMarkdown lost the edit; got %q", got)
	}
	// stripFrontmatter MUST have removed the YAML header.
	if strings.Contains(got, "issue_key:") {
		t.Fatalf("EditMarkdown left frontmatter in output: %q", got)
	}
}

func TestEditMarkdownPropagatesEditorFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-editor scripts use /bin/sh; not portable to Windows")
	}

	failingEditor := filepath.Join(t.TempDir(), "fail.sh")
	if err := os.WriteFile(failingEditor, []byte("#!/bin/sh\nexit 9\n"), 0o700); err != nil {
		t.Fatalf("write failing-editor script: %v", err)
	}
	if _, err := EditMarkdown(context.Background(), "PROJ-1", "description", "x", failingEditor); err == nil {
		t.Fatal("EditMarkdown should propagate non-zero editor exit; got nil")
	}
}

func TestEditMarkdownDetectsNonBlockingSpawnAndPreservesFile(t *testing.T) {
	// Simulates `EDITOR=code` (without --wait): the editor binary exits
	// in milliseconds without modifying the file. EditMarkdown MUST
	// detect this (sub-second exit + content unchanged) and:
	//   1. Return an error so the caller doesn't submit an empty edit
	//   2. NOT delete the temp file — VS Code may still hold the buffer,
	//      and the user needs the file on disk to recover any in-flight
	//      edits they made before realizing the parent had moved on.
	if runtime.GOOS == "windows" {
		t.Skip("fake-editor scripts use /bin/sh; not portable to Windows")
	}

	instantEditor := filepath.Join(t.TempDir(), "instant.sh")
	// Just exit cleanly without touching the file. Mirrors the
	// strikethrough-in-VS-Code bug: the launcher returned but the user's
	// edits hadn't been written yet (or aren't going to be).
	if err := os.WriteFile(instantEditor, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write instant-editor script: %v", err)
	}

	_, err := EditMarkdown(context.Background(), "PROJ-1", "description", "original body", instantEditor)
	if err == nil {
		t.Fatal("EditMarkdown should error on instant-exit + unchanged content")
	}
	// The error must name a path the user can recover from.
	if !strings.Contains(err.Error(), "/") {
		t.Errorf("error missing preserved file path: %v", err)
	}

	// Extract the path from the error and confirm the file still exists.
	// The error format is "...preserved at <path>" — assert the file is
	// readable and contains the original markdown so the user can recover.
	preservedPath := extractPreservedPath(err.Error())
	if preservedPath == "" {
		t.Fatalf("could not extract preserved path from error: %v", err)
	}
	defer func() { _ = os.Remove(preservedPath) }()
	body, readErr := os.ReadFile(preservedPath)
	if readErr != nil {
		t.Fatalf("preserved file not readable at %s: %v", preservedPath, readErr)
	}
	if !strings.Contains(string(body), "original body") {
		t.Errorf("preserved file missing original content: %q", body)
	}
}

// extractPreservedPath pulls the temp file path out of the error
// message. Format pinned to "preserved at <path>".
func extractPreservedPath(msg string) string {
	const marker = "preserved at "
	_, after, ok := strings.Cut(msg, marker)
	if !ok {
		return ""
	}
	return strings.TrimSpace(after)
}

func TestEditMarkdownJiraEditorEnvOverridesEditor(t *testing.T) {
	// The Resolve() precedence test already pins the chain at the
	// resolver level; this asserts that the spawn path actually picks
	// the JIRA_EDITOR-resolved binary, not just that Resolve returns
	// the right string. End-to-end check, package-level scope.
	if runtime.GOOS == "windows" {
		t.Skip("fake-editor scripts use /bin/sh; not portable to Windows")
	}

	dir := t.TempDir()
	jiraEditor := filepath.Join(dir, "jira-editor.sh")
	envEditor := filepath.Join(dir, "env-editor.sh")
	make := func(path, marker string) {
		body := "#!/bin/sh\ncat > \"$1\" <<'EOF'\n---\nissue_key: PROJ-1\nfield_name: description\n---\n\n" + marker + "\nEOF\n"
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	make(jiraEditor, "from-jira-editor")
	make(envEditor, "from-editor-env")

	t.Setenv("JIRA_EDITOR", jiraEditor)
	t.Setenv("EDITOR", envEditor)

	got, err := EditMarkdown(context.Background(), "PROJ-1", "description", "old", Resolve(""))
	if err != nil {
		t.Fatalf("EditMarkdown: %v", err)
	}
	if !strings.Contains(got, "from-jira-editor") {
		t.Fatalf("JIRA_EDITOR didn't win the spawn; got %q", got)
	}
	if strings.Contains(got, "from-editor-env") {
		t.Fatalf("$EDITOR sentinel leaked through; got %q", got)
	}
}
