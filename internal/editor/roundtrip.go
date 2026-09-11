package editor

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// nonBlockingSpawnThreshold is the wall-clock floor below which an
// editor exit is considered suspicious (likely a non-blocking launcher
// like vanilla `code` that returns before the user can read, let alone
// edit). Real editors take seconds to load even when the user
// immediately quits without changes.
const nonBlockingSpawnThreshold = 500 * time.Millisecond

// WriteTemp writes markdown to a new jira-edit-*.md temp file, prefixed with
// an issue_key/field_name frontmatter block for context, and returns its path.
// The caller owns cleanup — EditMarkdown deletes it, but preserves it on error.
// A setup write or close failure removes the incomplete file because no editor
// has opened it and it contains no user changes to recover.
func WriteTemp(issueKey, fieldName, markdown string) (string, error) {
	f, err := os.CreateTemp("", "jira-edit-*.md")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err = fmt.Fprintf(f, "---\nissue_key: %s\nfield_name: %s\n---\n\n%s\n", issueKey, fieldName, markdown); err != nil {
		return "", errors.Join(err, f.Close(), os.Remove(name))
	}
	if err := f.Close(); err != nil {
		return "", errors.Join(err, os.Remove(name))
	}
	return name, nil
}

// ReadMarkdown reads path and returns its body with any leading frontmatter
// block (the one WriteTemp adds) stripped and surrounding whitespace trimmed.
func ReadMarkdown(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return stripFrontmatter(string(data)), nil
}

// EditMarkdown writes a temp file, spawns the configured editor, and
// reads the edited content back. The temp file is preserved on every
// error path (per kubectl's pattern) so the user can recover in-flight
// edits — the path is named in the error message.
//
// A sub-second editor exit combined with byte-identical content trips
// the non-blocking-spawn safety check: that's the signature of
// `EDITOR=code` (without --wait) returning before VS Code has finished
// opening the buffer. Treating it as success would race the parent's
// cleanup against the editor's save, causing the strikethrough-on-
// filename + silent-data-loss bug.
func EditMarkdown(ctx context.Context, issueKey, fieldName, markdown, editorCommand string) (string, error) {
	path, err := WriteTemp(issueKey, fieldName, markdown)
	if err != nil {
		return "", err
	}
	originalBytes, err := os.ReadFile(path)
	if err != nil {
		// File missing immediately after WriteTemp shouldn't happen, but
		// preserve the path in the error so a follow-up can investigate.
		return "", fmt.Errorf("read temp file before edit: %w (preserved at %s)", err, path)
	}
	originalHash := sha256.Sum256(originalBytes)

	spawnedAt := time.Now()
	if runErr := Run(ctx, editorCommand, path); runErr != nil {
		return "", fmt.Errorf("editor failed: %w (preserved at %s)", runErr, path)
	}
	elapsed := time.Since(spawnedAt)

	editedBytes, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read temp file after edit: %w (preserved at %s)", err, path)
	}
	editedHash := sha256.Sum256(editedBytes)

	if elapsed < nonBlockingSpawnThreshold && editedHash == originalHash {
		return "", fmt.Errorf(
			"editor returned in %s without modifying the file — likely a non-blocking launcher (e.g. EDITOR=code without --wait); set EDITOR with a blocking flag (e.g. EDITOR='code --wait') and retry, original preserved at %s",
			elapsed.Round(time.Millisecond), path,
		)
	}

	// Safe to delete: editor blocked properly OR the user actually
	// changed something.
	defer os.Remove(path) //nolint:errcheck // successful edit cleanup is best-effort
	return stripFrontmatter(string(editedBytes)), nil
}

func stripFrontmatter(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return strings.TrimSpace(content)
	}
	rest := strings.TrimPrefix(content, "---\n")
	_, after, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return strings.TrimSpace(content)
	}
	return strings.TrimSpace(after)
}
