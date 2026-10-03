package cli

// CodedError carries a taxonomy code for a condition the CLI itself
// diagnoses, without claiming the caller's input was wrong.
//
// InputError cannot serve here: every input code exits 3 by design, and
// "you are not logged in" is an authentication state (exit 1). Sharing
// the type would either move that published exit or misreport the class.
type CodedError struct {
	// Code is the taxonomy code; it must have a registry row.
	Code string
	// Message is the human sentence. The remedy lives in the code's
	// static hint, not here.
	Message string
}

// codedCodes lists every code that reaches the mapper through
// CodedError. It exists for the same reason inputCodes does: the
// registry-completeness test walks these lists, so a new code cannot
// ship without a row and a hint.
var codedCodes = []string{
	CodeCacheIO,
	CodeCacheResponseInvalid,
	CodeAuthMissing,
	CodeCanceled,
	CodeSelectorIncomplete,
	CodeUserSearchResponseInvalid,
	CodeCurrentUserResponseInvalid,
	CodeJQLResponseInvalid,
	CodeBoardResponseInvalid,
	CodeBoardCacheUnavailable,
	CodeSearchResponseInvalid,
	CodeWorklogResponseInvalid,
	CodeBulkOperationResponseInvalid,
	CodeBulkOperationFailed,
	CodeBulkOutcomeUnconfirmed,
	CodeIssueCloneSourceInvalid,
	CodeUpdateUnsupported,
	CodeUpdateFailed,
	CodeUpdateOwnership,
	CodeUpdateIntegrity,
	CodeUpdateChanged,
	CodeUpdateUnavailable,
}

// NewCodedError returns a coded error for a condition the CLI diagnosed.
func NewCodedError(code, message string) *CodedError {
	return &CodedError{Code: code, Message: message}
}

func (e *CodedError) Error() string { return e.Message }
