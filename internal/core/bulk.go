package core

import "github.com/matcra587/jira-cli/internal/jira"

// IssueMoveSubmission distinguishes acceptance from decoding its task receipt.
// Acknowledged does not mean that any issue has moved. A missing receipt never
// permits automatically repeating the POST.
type IssueMoveSubmission struct {
	Task         *jira.SubmittedBulkOperation
	Acknowledged bool
	Retry        RetryInfo
}

// BulkOperationRead retains one validated job observation and retry facts.
type BulkOperationRead struct {
	Progress *jira.BulkOperationProgress
	Retry    RetryInfo
}
