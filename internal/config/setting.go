package config

import (
	"bytes"
	"context"
	"fmt"
	"slices"
)

// OpenSettingsFile binds publication to the exact authored bytes underlying
// this resolution. A later external file cannot become its save preimage.
func (r *Resolved) OpenSettingsFile(ctx context.Context) (*File, error) {
	if r.fileSnapshot == nil {
		return nil, fmt.Errorf("%w: resolved configuration has no file snapshot", ErrFileInvalid)
	}
	file, err := OpenFile(ctx, r.Path)
	if err != nil {
		return nil, err
	}
	if file.Exists() != r.fileSnapshot.Exists() || !bytes.Equal(file.original, r.fileSnapshot.original) ||
		r.fileSnapshot.requested != "" && file.Path() != r.fileSnapshot.Path() {
		return nil, ErrFileChanged
	}
	return file.SelectProfile(r.Profile)
}

// StoredSetting describes one declared setting. Present distinguishes an
// absent key from an explicitly stored empty string, false or zero. Values
// are redacted before leaving the configuration owner.
type StoredSetting struct {
	Key     string
	Value   string
	Present bool
	Secret  bool
}

// Setting reads one key from this file snapshot, without defaults, environment,
// flags or credentials. A known but absent key has an empty Value and Present
// false. Unknown keys and invalid file syntax/types are refused.
func (f *File) Setting(key string) (StoredSetting, error) {
	if !slices.Contains(Keys(), key) {
		return StoredSetting{}, fmt.Errorf("unknown config key %q", key)
	}
	_, fields, decoded, err := f.selectedFields()
	if err != nil {
		return StoredSetting{}, err
	}
	return storedSetting(key, fields, decoded), nil
}

func storedSetting(key string, fields map[string]any, decoded *Config) StoredSetting {
	_, present := fields[key]
	value := ""
	if present {
		value = decoded.Values()[key]
	}
	rendered := Redacted(key, value)
	return StoredSetting{Key: key, Value: rendered, Present: present, Secret: rendered != value}
}
