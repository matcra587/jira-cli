package envelope

// UpdateOutput distinguishes release availability from an observed installation.
// A nil UpdateAvailable means no release comparison was established.
type UpdateOutput struct {
	Channel         string             `json:"channel"`
	Current         string             `json:"current"`
	Latest          string             `json:"latest,omitempty"`
	UpdateAvailable *bool              `json:"update_available,omitempty"`
	Managed         bool               `json:"managed"`
	Attempted       bool               `json:"attempted"`
	Updated         bool               `json:"updated"`
	Uncertain       bool               `json:"uncertain"`
	DryRun          bool               `json:"dry_run"`
	Hint            string             `json:"hint,omitempty"`
	Publication     *UpdatePublication `json:"publication,omitempty"`
	Brew            *BrewUpdate        `json:"brew,omitempty"`
}

// UpdatePublication retains filesystem facts after an archive installation.
type UpdatePublication struct {
	Published bool   `json:"published"`
	Durable   bool   `json:"durable"`
	Uncertain bool   `json:"uncertain"`
	Backup    string `json:"backup,omitempty"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256"`
}

// UpdateInstallation is an independently inspected Homebrew executable.
type UpdateInstallation struct {
	Executable string `json:"executable"`
	Version    string `json:"version"`
	SHA256     string `json:"sha256"`
	Head       bool   `json:"head"`
}

// BrewUpdate retains before/after facts and a PATH copy shadowing the formula.
type BrewUpdate struct {
	Before UpdateInstallation  `json:"before"`
	After  *UpdateInstallation `json:"after,omitempty"`
	Shadow string              `json:"shadow,omitempty"`
}

func init() {
	Register("update", UpdateOutput{}, map[string]any{
		"description": "Release availability and independently retained executable update outcomes.",
	})
}
