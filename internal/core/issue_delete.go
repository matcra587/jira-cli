package core

import "errors"

// ErrIssueDeleteResponseInvalid reports an uncertain issue deletion response.
// Inspect the requested issue before retrying; a write may already have applied.
var ErrIssueDeleteResponseInvalid = errors.New("invalid issue deletion response")
