package core

// IssueActionReceipt distinguishes a write attempt from a Jira acknowledgement.
// Applied is not an independent readback or remote compare-and-swap.
type IssueActionReceipt struct {
	Attempted bool
	Applied   bool
	CommentID string
}
