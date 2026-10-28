package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	xfilepath "github.com/gechr/x/filepath"
)

// SaveQuery publishes a new literal .jql name through the shared no-clobber
// file owner. Existing searches and symlinks are never replaced by a TUI save.
func SaveQuery(ctx context.Context, dir, name, jql string) (bool, error) {
	if err := ValidateQueryName(name); err != nil {
		return false, err
	}
	dir = xfilepath.Expand(dir)
	if strings.TrimSpace(dir) == "" {
		return false, errors.New("queries_path must name a saved-query directory")
	}
	validate := func(data []byte) error { _, err := parseQuery(string(data), name); return err }
	data := []byte(strings.TrimSpace(jql) + "\n")
	if err := validate(data); err != nil {
		return false, err
	}
	file, err := OpenNewFile(ctx, filepath.Join(dir, name+".jql"))
	if err != nil {
		if errors.Is(err, ErrFileChanged) {
			return false, fmt.Errorf("saved query %q already exists; choose a new name: %w", name, err)
		}
		return false, err
	}
	return file.createValidated(ctx, data, validate, fileCreationOps{
		resolve: resolveCreationPath, link: os.Link, syncDirectory: syncCreationDirectory,
	})
}
