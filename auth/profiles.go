package auth

import (
	"context"
	"fmt"

	"github.com/matcra587/jira-cli/internal/config"
)

// Profile is authored account metadata. Empty Name identifies a flat config;
// it is selected by leaving Options.Profile empty.
type Profile struct {
	Name    string `json:"name"`
	Default bool   `json:"default"`
	BaseURL string `json:"base_url"`
	Email   string `json:"email"`
	CloudID string `json:"cloud_id,omitempty"`
}

// ProfileList retains the saved default independently of environment selection.
type ProfileList struct {
	DefaultProfile string    `json:"default_profile"`
	Profiles       []Profile `json:"profiles"`
}

// ListProfiles reads public file metadata without resolving credentials,
// opening a provider or contacting Jira. Missing default files return an empty
// list; missing explicitly selected files and invalid configurations fail.
func ListProfiles(ctx context.Context, opts Options) (ProfileList, error) {
	result := ProfileList{Profiles: []Profile{}}
	file, err := config.OpenFile(ctx, opts.ConfigPath)
	if err != nil {
		return result, resolutionFailure(ctx, err)
	}
	if !file.Exists() {
		if opts.ConfigPath != "" {
			return result, resolutionFailure(ctx, fmt.Errorf("selected configuration does not exist"))
		}
		return result, nil
	}
	profiles, err := file.Profiles()
	if err != nil {
		return result, resolutionFailure(ctx, err)
	}
	for _, profile := range profiles {
		result.Profiles = append(result.Profiles, Profile{
			Name: profile.Name, Default: profile.Default, BaseURL: profile.BaseURL,
			Email: profile.Email, CloudID: profile.CloudID,
		})
		if profile.Default {
			result.DefaultProfile = profile.Name
		}
	}
	named, err := file.NamedProfiles()
	if err != nil {
		return result, resolutionFailure(ctx, err)
	}
	if !named {
		// Authored flat metadata must not inherit tokens or account overrides
		// from the environment during credential-free discovery.
		local, err := config.Load(config.Options{
			Path: file.Path(), Snapshot: file,
			Environ: func() []string { return []string{} },
		})
		if err != nil {
			return result, resolutionFailure(ctx, err)
		}
		result.Profiles = append(result.Profiles, Profile{
			Default: true,
			BaseURL: local.Config.BaseURL, Email: local.Config.Email, CloudID: local.Config.CloudID,
		})
	}
	if err := ctx.Err(); err != nil {
		return ProfileList{Profiles: []Profile{}}, err
	}
	return result, nil
}
