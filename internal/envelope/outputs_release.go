package envelope

import changelog "github.com/matcra587/jira-cli"

// ReleaseNotesOutput carries the selected embedded release notes. Markdown is
// the human document; machine consumers receive the structured releases.
type ReleaseNotesOutput struct {
	Releases []changelog.Release `json:"releases"`
	Markdown string              `json:"-"`
}

func init() {
	Register("release.notes", ReleaseNotesOutput{}, map[string]any{
		"description": "Embedded jira-cli release notes, newest first or one selected release.",
	})
}
