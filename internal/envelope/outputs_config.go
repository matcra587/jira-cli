package envelope

// ConfigGetOutput reports one file-only setting. Present distinguishes an
// absent key from an explicitly stored zero, false or empty string.
type ConfigGetOutput struct {
	Profile    string `json:"profile,omitempty"`
	Path       string `json:"path"`
	FileExists bool   `json:"file_exists"`
	Key        string `json:"key"`
	Value      string `json:"value"`
	Present    bool   `json:"present"`
	Secret     bool   `json:"secret"`
}

func init() {
	Register("config.get", ConfigGetOutput{}, map[string]any{
		"description": "One stored configuration value, without effective-setting overlays; secrets are redacted.",
		"properties": map[string]any{
			"present": map[string]any{"description": "Whether the selected file explicitly declares this key."},
			"value":   map[string]any{"description": "Stored value as text, redacted when necessary; empty when absent."},
		},
	})
}

// ConfigListOutput is the `config.list` operation's data payload: every
// setting, its EFFECTIVE value, and the layer that supplied it.
//
// Effective, not file contents — the distinction is the whole point.
// Another widely used CLI documents that its config reader "does not use
// any configuration values from environment variables", so its output
// disagrees with what the tool actually does the moment an env var is
// set. A settings view that can contradict the running command is worse
// than no settings view, because it is believed.
type ConfigListOutput struct {
	// Profile is the selected settings table, independent of environment overrides.
	Profile        string `json:"profile,omitempty"`
	DefaultProfile string `json:"default_profile,omitempty"`
	// Path is the config file consulted, whether or not it exists — the
	// answer to "where do I edit this?", so no separate path command has
	// to exist.
	Path string `json:"path"`
	// FileExists distinguishes "no file" from "empty file". Both resolve
	// to defaults, but only one is worth telling someone about.
	FileExists bool `json:"file_exists"`
	// Settings is one row per key, in a stable order.
	Settings []ConfigSetting `json:"settings"`
}

// ConfigSetting is one resolved setting.
type ConfigSetting struct {
	Key string `json:"key"`
	// Value is the effective value, rendered. A secret is replaced with a
	// marker; the raw value never appears here.
	Value string `json:"value"`
	// Source names the layer that supplied Value: default, file, env, or
	// flag. It is the reason this output exists — "why is it this?" is
	// the question a surprising setting raises.
	Source string `json:"source"`
	// Secret marks a value that was withheld, so a consumer can tell a
	// redacted setting from one that is genuinely the literal text.
	Secret bool `json:"secret"`
}

// The overrides carry prose only; the shape derives from the struct.
func init() {
	Register("config.list", ConfigListOutput{}, map[string]any{
		"description": "Effective configuration with the layer that supplied each value.",
		"properties": map[string]any{
			"path": map[string]any{
				"description": "Config file consulted, whether or not it exists.",
			},
			"file_exists": map[string]any{
				"description": "Whether the config file was present; absence is normal, not an error.",
			},
			"settings": map[string]any{
				"description": "One row per setting: key, effective value, and the layer that set it.",
			},
		},
	})
}

// ConfigEditOutput is the `config.edit` operation's data payload: what
// the edit did, stated so a scripted caller does not have to diff the
// file to find out.
type ConfigEditOutput struct {
	// MutationState carries dry_run on both paths, so a caller never has
	// to infer from a missing field whether anything happened.
	MutationState
	// Path is the file that was edited.
	Path string `json:"path"`
	// Editor is the command that was launched, or would be. It is the
	// request context that survives both paths: "which editor did it
	// pick?" is the question a surprising result raises, and a preview
	// that withheld it would answer nothing useful.
	Editor []string `json:"editor"`
	// Created reports that the file did not exist and was seeded.
	Created bool `json:"created"`
	// Changed reports whether the contents actually differ. Serialized
	// even when false: "I opened the editor and saved nothing" is a real
	// outcome, and absence would read as unknown.
	Changed bool `json:"changed"`
}

func init() {
	Register("config.edit", ConfigEditOutput{}, map[string]any{
		"description": "Result of opening the config file in an editor.",
		"properties": map[string]any{
			"dry_run": map[string]any{"description": "Whether this was a preview; no file is touched when true."},
			"path":    map[string]any{"description": "Config file that was edited."},
			"editor":  map[string]any{"description": "Editor command that was launched, or would be."},
			"created": map[string]any{"description": "Whether the file was created by this command."},
			"changed": map[string]any{"description": "Whether the edit changed the file."},
		},
	})
}
