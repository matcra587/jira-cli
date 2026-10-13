package command

import (
	"slices"
	"strings"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

// ApplyCreateDefaults fills only absent create context. Membership in either
// native half wins even for null, empty or unknown values. Explicit empty flags
// suppress defaults separately; they are not fabricated native payload values.
func ApplyCreateDefaults(request *jira.IssueWriteRequest, preferences core.IssuePreferences, projectSuppressed, typeSuppressed bool) error {
	keys := request.Keys()
	if project := strings.TrimSpace(preferences.DefaultProject); project != "" && !projectSuppressed && !slices.Contains(keys, fieldProject) {
		if err := request.SetField(fieldProject, jira.KeyRef{Key: project}); err != nil {
			return err
		}
	}
	if issueType := strings.TrimSpace(preferences.DefaultIssueType); issueType != "" && !typeSuppressed && !slices.Contains(keys, fieldIssueType) {
		if err := request.SetField(fieldIssueType, jira.NameRef{Name: issueType}); err != nil {
			return err
		}
	}
	return nil
}
