package core

import "github.com/matcra587/jira-cli/internal/jira"

// WorklogRead retains a single server page and its safe-read retry facts.
type WorklogRead struct {
	Page  *jira.PageOfWorklogs
	Retry RetryInfo
}

// WorklogWrite separates an acknowledged creation from decoding its receipt.
// A nil Worklog with Acknowledged true must never invite a blind repeat POST.
type WorklogWrite struct {
	Worklog      *jira.Worklog
	Acknowledged bool
	Retry        RetryInfo
}
