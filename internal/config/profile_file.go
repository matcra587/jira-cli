package config

import (
	"fmt"
	"os"
)

// SelectProfile returns a copy bound to an existing profile. It never mutates
// the snapshot or the saved default; callers retain the same save preimage.
func (f *File) SelectProfile(name string) (*File, error) {
	doc, err := decodeProfileDocument(f.original, false)
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = f.profileSelection
	}
	_, selected, err := doc.selected(name)
	if err != nil {
		return nil, err
	}
	copy := *f
	copy.profileSelection = selected
	copy.profileSelectionErr = nil
	return &copy, nil
}

// Profile returns the profile selected for authored reads and updates.
func (f *File) Profile() (string, error) {
	if f.profileSelectionErr != nil {
		return "", f.profileSelectionErr
	}
	doc, err := decodeProfileDocument(f.original, false)
	if err != nil {
		return "", err
	}
	_, selected, err := doc.selected(f.profileSelection)
	return selected, err
}

// selectedFields separates the full document from the selected authored view.
// Mutation callers must retain the former when preparing a replacement.
func (f *File) selectedFields() (map[string]any, map[string]any, *Config, error) {
	if f.profileSelectionErr != nil {
		return nil, nil, nil, f.profileSelectionErr
	}
	doc, err := decodeProfileDocument(f.original, false)
	if err != nil {
		return nil, nil, nil, err
	}
	selected, _, err := doc.selected(f.profileSelection)
	if err != nil {
		return nil, nil, nil, err
	}
	decoded, err := unmarshalFields(selected)
	return doc.fields, selected, decoded, err
}

func (f *File) setSelectedField(fields map[string]any, key string, value any) error {
	profiles, named := fields["profiles"].(map[string]any)
	if !named || globalSetting(key) && key != "editor" {
		fields[key] = value
		return nil
	}
	name := f.profileSelection
	if name == "" {
		name, _ = fields["default_profile"].(string)
	}
	profile, ok := profiles[name].(map[string]any)
	if !ok {
		return fmt.Errorf("%w: select an existing profile before changing %s", ErrFileInvalid, key)
	}
	profile[key] = value
	return nil
}

func captureFileProfile() (string, error) {
	return selectedProfile(Options{}, currentFlagLayer(), os.Environ())
}
