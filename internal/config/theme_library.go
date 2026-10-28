package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	koanftoml "github.com/knadh/koanf/parsers/toml"
)

// SavedTheme retains the snapshot used to refuse editing a theme changed on
// disk. Names are display metadata; renaming never changes a selected path.
// Err entries remain visible without preventing other themes from loading.
type SavedTheme struct {
	Name       string
	Path       string
	Definition *ThemeFile
	Active     bool
	Err        error
	file       *File
}

// Managed reports whether the entry has an editable library snapshot.
func (t *SavedTheme) Managed() bool { return t != nil && t.file != nil }

// CloneThemeFile gives an editor independent ownership of every palette role.
func CloneThemeFile(def *ThemeFile) *ThemeFile {
	if def == nil {
		return nil
	}
	return &ThemeFile{
		Name:         def.Name,
		Base:         def.Base,
		Colors:       maps.Clone(def.Colors),
		Syntax:       maps.Clone(def.Syntax),
		EntityColors: slices.Clone(def.EntityColors),
	}
}

// ValidateThemeName accepts a portable, single filename stem and display name.
func ValidateThemeName(name string) error {
	if !validThemeNameText(name) {
		return errors.New("theme name must be 1–64 bytes without path separators, " +
			"reserved punctuation, controls or surrounding spaces")
	}
	if reservedThemeDevice(strings.ToUpper(name)) {
		return errors.New("theme name is reserved on Windows")
	}
	return nil
}

func validThemeNameText(name string) bool {
	if name == "" || strings.TrimSpace(name) != name {
		return false
	}
	if len(name) > 64 || !utf8.ValidString(name) {
		return false
	}
	return !strings.ContainsAny(name, `/\\<>:"|?*.`) && !strings.ContainsFunc(name, unicode.IsControl)
}

func reservedThemeDevice(upper string) bool {
	switch upper {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(upper) != 4 {
		return false
	}
	numberedDevice := strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")
	if !numberedDevice {
		return false
	}
	return upper[3] >= '0' && upper[3] <= '9'
}

// MarshalThemeFile validates editor/import data through the same parser used
// at startup. Omitted roles retain their base; no resolved defaults are saved.
func MarshalThemeFile(def *ThemeFile) ([]byte, error) {
	if def == nil {
		return nil, ErrThemeFileInvalid
	}
	fields := map[string]any{"base": def.Base, "colors": def.Colors, "syntax": def.Syntax}
	if def.Name != "" {
		fields["name"] = def.Name
	}
	if def.EntityColors != nil {
		fields["entity_colors"] = def.EntityColors
	}
	data, err := koanftoml.Parser().Marshal(fields)
	if err != nil {
		return nil, err
	}
	if _, err = ParseThemeFile(data); err != nil {
		return nil, err
	}
	return data, nil
}

// LoadSavedThemes discovers regular TOML files beside the selected config.
// Missing directories are empty libraries. Symlinks are displayed as invalid
// rather than followed during discovery. Each input retains its own failure;
// cancellation aborts discovery without returning a partial library.
func LoadSavedThemes(ctx context.Context, dir string) ([]*SavedTheme, error) {
	return loadSavedThemes(ctx, dir, func(ctx context.Context, path string) (*File, error) {
		return openFileWithReader(ctx, path, readThemeInput)
	})
}

// Inject only snapshot admission so discovery tests can change a file after its
// directory entry is captured while retaining the real bounded reader and parser.
func loadSavedThemes(
	ctx context.Context,
	dir string,
	openSnapshot func(context.Context, string) (*File, error),
) ([]*SavedTheme, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if errors.Is(err, os.ErrNotExist) {
		return []*SavedTheme{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := []*SavedTheme{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".toml") || entry.IsDir() {
			continue
		}
		if len(result) >= 256 {
			return nil, errors.New("theme library exceeds 256 themes")
		}
		saved := &SavedTheme{
			Name: strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())),
			Path: filepath.Join(dir, entry.Name()),
		}
		result = append(result, saved)
		if entry.Type()&os.ModeSymlink != 0 {
			saved.Err = errors.New("theme library entries must not be symlinks")
			continue
		}
		file, err := openSnapshot(ctx, saved.Path)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil {
			saved.Err = err
			continue
		}
		// An absent configuration snapshot can be a future update target, but
		// parsing its empty bytes would invent a default-dark theme here.
		if !file.Exists() {
			saved.Err = fmt.Errorf("read theme %q: %w", saved.Name, os.ErrNotExist)
			continue
		}
		def, err := ParseThemeFile(file.Contents())
		if err != nil {
			saved.Err = err
			continue
		}
		saved.file, saved.Definition = file, def
		if def.Name != "" {
			saved.Name = def.Name
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// SaveTheme publishes a new name without clobbering an existing file, or edits
// the exact captured snapshot. The bool retains publication even if directory
// sync fails afterward. Byte checks do not provide filesystem compare-and-swap.
func SaveTheme(
	ctx context.Context,
	dir, name string,
	def *ThemeFile,
	previous *SavedTheme,
) (*SavedTheme, bool, error) {
	if err := ValidateThemeName(name); err != nil {
		return nil, false, err
	}
	next := CloneThemeFile(def)
	if next == nil {
		return nil, false, ErrThemeFileInvalid
	}
	next.Name = name
	data, err := MarshalThemeFile(next)
	if err != nil {
		return nil, false, err
	}
	path := filepath.Join(dir, name+".toml")
	if previous != nil {
		if previous.file == nil || filepath.Dir(previous.Path) != dir {
			return nil, false, ErrFileChanged
		}
		path = previous.Path
	}
	entries, err := LoadSavedThemes(ctx, dir)
	if err != nil {
		return nil, false, err
	}
	for _, entry := range entries {
		nameConflict := strings.EqualFold(entry.Name, name) ||
			strings.EqualFold(filepath.Base(entry.Path), name+".toml")
		if !nameConflict {
			continue
		}
		if previous != nil && entry.Path == previous.Path {
			continue
		}
		return nil, false, fmt.Errorf("theme %q already exists", name)
	}
	validate := func(data []byte) error {
		_, err := ParseThemeFile(data)
		return err
	}
	var changed bool
	if previous != nil {
		if err := previous.file.checkSnapshot(ctx); err != nil {
			return nil, false, err
		}
		changed, err = previous.file.saveValidated(ctx, data, validate)
	} else {
		var file *File
		file, err = OpenNewFile(ctx, path)
		if err == nil {
			changed, err = file.createValidated(
				ctx,
				data,
				validate,
				fileCreationOps{
					resolve:       resolveCreationPath,
					link:          os.Link,
					syncDirectory: syncCreationDirectory,
				},
			)
		}
	}
	if err != nil {
		return nil, changed, err
	}
	file, err := openFileWithReader(ctx, path, readThemeInput)
	if err != nil {
		return nil, changed, err
	}
	if !bytes.Equal(file.Contents(), data) {
		return nil, changed, ErrFileChanged
	}
	return &SavedTheme{Name: name, Path: path, Definition: next, file: file}, changed, nil
}

// ExportTheme writes only a new explicitly selected file. Existing imports,
// configuration files and other user documents are never replaced.
func ExportTheme(ctx context.Context, path string, def *ThemeFile) (bool, error) {
	data, err := MarshalThemeFile(def)
	if err != nil {
		return false, err
	}
	file, err := OpenNewFile(ctx, path)
	if err != nil {
		return false, err
	}
	validate := func(data []byte) error {
		_, err := ParseThemeFile(data)
		return err
	}
	return file.createValidated(
		ctx,
		data,
		validate,
		fileCreationOps{
			resolve:       resolveCreationPath,
			link:          os.Link,
			syncDirectory: syncCreationDirectory,
		},
	)
}
