package config

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/google/uuid"
	koanftoml "github.com/knadh/koanf/parsers/toml"

	"github.com/matcra587/jira-cli/internal/core"
)

// Profile contains public authored metadata, never credential contents.
type Profile struct {
	Name          string
	Default       bool
	Selected      bool
	BaseURL       string
	Email         string
	SecretBackend core.CredentialBackend
	CredentialID  string
}

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

// Profiles lists local metadata even when the selected name does not exist,
// allowing a caller to discover and repair an invalid selection.
func (f *File) Profiles() ([]Profile, error) {
	doc, err := decodeProfileDocument(f.original, true)
	if err != nil {
		return nil, err
	}
	profiles := []Profile{}
	selected := f.profileSelection
	if selected == "" {
		selected = doc.defaultProfile
	}
	for _, name := range slices.Sorted(maps.Keys(doc.profiles)) {
		fields, _, err := doc.selected(name)
		if err != nil {
			return nil, err
		}
		decoded, err := unmarshalFields(fields)
		if err != nil {
			return nil, err
		}
		backend := decoded.SecretBackend
		if backend == "" {
			backend = core.CredentialBackendKeyring
		}
		profiles = append(profiles, Profile{
			Name: name, Default: name == doc.defaultProfile,
			Selected: name == selected, BaseURL: Redacted("base_url", decoded.BaseURL),
			Email: decoded.Email, SecretBackend: backend, CredentialID: decoded.CredentialID,
		})
	}
	return profiles, nil
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

// PrepareCredentialID creates a durable public locator before a new named
// profile can write a token. Reopening reuses that locator after interruption.
func (f *File) PrepareCredentialID() (*SettingsUpdate, error) {
	_, _, decoded, err := f.selectedFields()
	if err != nil {
		return nil, err
	}
	id := decoded.CredentialID
	if id == "" {
		id = uuid.NewString()
	}
	return f.PrepareSettings(map[string]string{"credential_id": id})
}

// ProfileUpdate is a prepared local document change. Credentials are neither
// read nor removed; deleting a profile does not log out its stored account.
type ProfileUpdate struct {
	Name           string
	PreviousName   string
	DefaultProfile string
	file           *File
	candidate      []byte
	changed        bool
}

func (u *ProfileUpdate) WouldChange() bool { return u.changed }

func (u *ProfileUpdate) Save(ctx context.Context) (bool, error) {
	if !u.file.Exists() {
		return u.file.Create(ctx, u.candidate)
	}
	return u.file.Save(ctx, u.candidate)
}

// Contents returns a caller-owned candidate for migration and preview checks.
func (u *ProfileUpdate) Contents() []byte { return bytes.Clone(u.candidate) }

func (f *File) prepareProfileUpdate(doc *profileDocument, name, previous string, changed bool) (*ProfileUpdate, error) {
	candidate := f.original
	if changed {
		var err error
		candidate, err = koanftoml.Parser().Marshal(doc.fields)
		if err != nil {
			return nil, fmt.Errorf("encode profiles: %w", err)
		}
		if err := ValidateFile(candidate); err != nil {
			return nil, err
		}
	}
	return &ProfileUpdate{
		Name: name, PreviousName: previous, DefaultProfile: doc.defaultProfile,
		file: f, candidate: candidate, changed: changed,
	}, nil
}

func (f *File) namedDocument() (*profileDocument, error) {
	doc, err := decodeProfileDocument(f.original, true)
	if err != nil {
		return nil, err
	}
	if doc.named {
		return doc, nil
	}
	profiles := map[string]any{}
	globals := doc.globals()
	if len(doc.fields) != 0 {
		profile := map[string]any{}
		for key, value := range doc.fields {
			if !globalSetting(key) {
				profile[key] = value
			}
		}
		if _, exists := profile["credential_id"]; !exists {
			profile["credential_id"] = "default"
		}
		profiles["default"] = profile
		doc.defaultProfile = "default"
	}
	globals["format_version"] = ProfileFormatVersion
	globals["default_profile"] = doc.defaultProfile
	globals["profiles"] = profiles
	doc.fields, doc.profiles, doc.named = globals, profiles, true
	return doc, nil
}

// PrepareProfileAdd prepares an independent profile and its stable locator.
// Converting a nonempty flat file keeps its authored settings under default.
func (f *File) PrepareProfileAdd(name, baseURL, email string) (*ProfileUpdate, error) {
	if err := ValidateProfileName(name); err != nil {
		return nil, err
	}
	if err := ValidateBaseURL(baseURL); baseURL != "" && err != nil {
		return nil, err
	}
	doc, err := f.namedDocument()
	if err != nil {
		return nil, err
	}
	if _, exists := doc.profiles[name]; exists {
		return nil, fmt.Errorf("%w: profile %q already exists", ErrFileInvalid, name)
	}
	doc.profiles[name] = map[string]any{"base_url": baseURL, "email": email, "credential_id": uuid.NewString()}
	if doc.defaultProfile == "" {
		doc.defaultProfile = name
		doc.fields["default_profile"] = name
	}
	return f.prepareProfileUpdate(doc, name, "", true)
}

func (f *File) PrepareProfileRename(oldName, newName string) (*ProfileUpdate, error) {
	if err := ValidateProfileName(newName); err != nil {
		return nil, err
	}
	doc, err := decodeProfileDocument(f.original, true)
	if err != nil {
		return nil, err
	}
	profile, exists := doc.profiles[oldName]
	if !exists {
		return nil, fmt.Errorf("%w: profile %q does not exist", ErrFileInvalid, oldName)
	}
	if oldName != newName {
		if _, exists := doc.profiles[newName]; exists {
			return nil, fmt.Errorf("%w: profile %q already exists", ErrFileInvalid, newName)
		}
		delete(doc.profiles, oldName)
		doc.profiles[newName] = profile
		if doc.defaultProfile == oldName {
			doc.defaultProfile = newName
			doc.fields["default_profile"] = newName
		}
	}
	return f.prepareProfileUpdate(doc, newName, oldName, oldName != newName)
}

func (f *File) PrepareProfileDefault(name string) (*ProfileUpdate, error) {
	doc, err := decodeProfileDocument(f.original, true)
	if err != nil {
		return nil, err
	}
	if _, exists := doc.profiles[name]; !exists {
		return nil, fmt.Errorf("%w: profile %q does not exist", ErrFileInvalid, name)
	}
	changed := doc.defaultProfile != name
	doc.defaultProfile = name
	doc.fields["default_profile"] = name
	return f.prepareProfileUpdate(doc, name, "", changed)
}

func (f *File) PrepareProfileRemove(name string) (*ProfileUpdate, error) {
	doc, err := decodeProfileDocument(f.original, true)
	if err != nil {
		return nil, err
	}
	if _, exists := doc.profiles[name]; !exists {
		return nil, fmt.Errorf("%w: profile %q does not exist", ErrFileInvalid, name)
	}
	if doc.defaultProfile == name && len(doc.profiles) > 1 {
		return nil, fmt.Errorf("%w: select another default profile before removing %q", ErrFileInvalid, name)
	}
	delete(doc.profiles, name)
	if doc.defaultProfile == name {
		doc.defaultProfile = ""
		doc.fields["default_profile"] = ""
	}
	return f.prepareProfileUpdate(doc, name, "", true)
}

func captureFileProfile() (string, error) {
	variables := slices.Clone(os.Environ())
	normalizeTokenEnvironmentNames(variables)
	return selectedProfile(Options{}, currentFlagLayer(), variables)
}

// CaptureOptions freezes local file selection, authored bytes and environment
// before lazy providers run. It opens no credentials and publishes no files.
func CaptureOptions(ctx context.Context, opts Options) (Options, error) {
	if !opts.captured {
		path, err := resolvePath(opts.Path)
		if err != nil {
			return Options{}, err
		}
		opts.authoredPath = path
		opts.explicitPath = opts.Path != "" || currentFlagLayer().Path != ""
		opts.captured = true
	}
	environ := opts.Environ
	if environ == nil {
		environ = os.Environ
	}
	variables := slices.Clone(environ())
	normalizeTokenEnvironmentNames(variables)
	opts.Environ = func() []string { return slices.Clone(variables) }
	selection, err := selectedProfile(opts, currentFlagLayer(), variables)
	if err != nil {
		return Options{}, err
	}
	file := opts.Snapshot
	if file == nil {
		file, err = OpenFile(ctx, opts.Path)
		if err != nil {
			return Options{}, err
		}
	}
	copy := *file
	copy.profileSelection, copy.profileSelectionErr = selection, nil
	opts.Path, opts.Snapshot = file.Path(), &copy
	resolved, err := Load(opts)
	if err != nil {
		return Options{}, err
	}
	opts.Profile = resolved.Profile
	opts.Snapshot.profileSelection = resolved.Profile
	return opts, nil
}
