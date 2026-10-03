package config

import (
	"fmt"
	"slices"
)

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
