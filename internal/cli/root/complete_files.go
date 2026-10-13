package root

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/matcra587/jira-cli/internal/cli"
)

const maxCompletionDirectoryEntries = 32 * 1024

// localPathCandidates reads names in one literal directory, never file contents,
// configuration, credentials or Jira. The selected x/os has no bounded directory
// enumeration/prefix primitive. Do not use filepath.Glob: authored metacharacters
// are literal path bytes. Failed or incomplete inventory supplies no candidates.
func localPathCandidates(ctx context.Context, prefix string) []string {
	if ctx.Err() != nil || !utf8.ValidString(prefix) || cli.Text(prefix) != prefix || strings.ContainsAny(prefix, "\t\r\n") {
		return nil
	}
	head, tail := filepath.Split(prefix)
	directory := head
	if directory == "" {
		directory = "."
	}
	file, err := os.Open(directory) //nolint:gosec // The literal completion directory is intentional; only entry names are read.
	if err != nil {
		return nil
	}
	entries, readErr := file.ReadDir(maxCompletionDirectoryEntries + 1)
	closeErr := file.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil || len(entries) > maxCompletionDirectoryEntries || ctx.Err() != nil {
		return nil
	}
	var candidates []string
	for _, entry := range entries {
		if ctx.Err() != nil {
			return nil
		}
		name := entry.Name()
		if !strings.HasPrefix(name, tail) || !utf8.ValidString(name) || cli.Text(name) != name || strings.ContainsAny(name, "\t\r\n") {
			continue
		}
		candidate := head + name
		directory := entry.IsDir()
		if entry.Type()&os.ModeSymlink != 0 {
			info, err := os.Stat(candidate)
			if err == nil {
				directory = info.IsDir()
			}
		}
		if directory {
			candidate += string(filepath.Separator)
		}
		candidates = append(candidates, candidate)
	}
	if ctx.Err() != nil {
		return nil
	}
	slices.Sort(candidates)
	return candidates
}
