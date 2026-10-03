package cli

// Input-failure codes: the class-specific halves of what the terminal
// validation fallback would otherwise flatten into validation_failed. An
// agent recovering from a typo needs to tell a bad verb from a bad flag
// from a bad value — one generic code with one generic hint cannot say
// which, and the recovery differs for each. Every code here classifies as
// validation (exit 3); the registry pins that and the completeness test
// keeps the set reachable.
const (
	// CodeCommandUnknown is a positional token that names no command —
	// caught before flag parsing, so a typo'd verb is never misreported
	// as whatever flag happened to follow it.
	CodeCommandUnknown = "command_unknown"
	// CodeFlagUnknown is a flag this CLI does not define anywhere on the
	// failing command.
	CodeFlagUnknown = "flag_unknown"
	// CodeFlagForeign is a recognized flag from a different Jira CLI —
	// not a typo, so it gets an orientation hint instead of a spelling
	// one; this CLI's equivalents ride the suggestions field.
	CodeFlagForeign = "flag_foreign"
	// CodeFlagRequired is a mandatory flag the caller did not pass at
	// all. Distinct from flag_value_missing, which is a flag that WAS
	// given without its value: "add the flag" and "give the flag a
	// value" are different corrections.
	CodeFlagRequired = "flag_required"
	// CodeFlagValueMissing is a flag that takes a value, given none.
	CodeFlagValueMissing = "flag_value_missing"
	// CodeFlagValueInvalid is a value the flag's type rejected.
	CodeFlagValueInvalid = "flag_value_invalid"
	// CodeADFContentLoss means valid Markdown loses content under conversion.
	// Inspect the candidate before explicitly accepting that loss.
	CodeADFContentLoss = "adf_content_loss"
	// CodeFlagSyntaxInvalid is a token the parser could not read as a
	// flag at all (a bare "-", a malformed shorthand group).
	CodeFlagSyntaxInvalid = "flag_syntax_invalid"
	// CodeArgCountInvalid is a positional-argument arity failure from a
	// command's Args validator.
	CodeArgCountInvalid = "arg_count_invalid"
	// CodeArgValueInvalid is a positional argument outside the command's
	// accepted set (a shell name `completion` has no generator for).
	CodeArgValueInvalid = "arg_value_invalid"
	// CodeFlagNotApplicable is a flag this CLI really does define, used
	// where it cannot take effect — distinct from CodeFlagUnknown, whose
	// "check the spelling" hint would send the reader hunting a typo in a
	// flag they spelled correctly. The recovery is to drop the flag, not
	// to fix it.
	CodeFlagNotApplicable = "flag_not_applicable"
	// CodeTransitionScreenRequired means a native transition payload needs a
	// workflow screen. Preserve the requested changes when choosing a remedy.
	CodeTransitionScreenRequired = "transition_screen_required"
	// CodeFlagConflict is two flags that each work alone and cannot be
	// given together, because they set one thing two ways: the three body
	// routes (--json-input, --markdown, --markdown-file), the two
	// visibility flags, --assignee against --unassign.
	//
	// Its own code for the reason CodeJQOutputConflict is its own: nothing
	// is wrong with either flag or either value, so every flag-value hint
	// misdescribes the fix. flag_value_invalid would say "that value isn't
	// the type the flag expects" when the type was fine, and
	// flag_not_applicable would say the flag "has no effect here" when it
	// would have had one had the other flag been absent. The recovery is
	// neither to correct a value nor to drop a useless flag: it is to
	// choose between two flags that both work.
	//
	// Field carries the flag the caller should probably keep, and the
	// message names both — canonical spellings only, never a hidden alias,
	// since an alias is absent from help and schema and naming one would
	// send a reader hunting a flag this CLI never showed them.
	CodeFlagConflict = "flag_conflict"
	// CodeJQExpressionInvalid is a --jq expression the engine could not
	// parse or compile. Reusing flag_value_invalid would point the reader
	// at `--help`, which will never document jq syntax.
	CodeJQExpressionInvalid = "jq_expression_invalid"
	// CodeJQEvalFailed is a valid expression that failed against the
	// actual document (iterating a null, an explicit error()). The
	// expression is fine; its assumption about the shape is not, so the
	// recovery is to inspect the shape rather than to fix syntax.
	CodeJQEvalFailed = "jq_eval_failed"
	// CodeJQOutputConflict is --jq asked for alongside an explicit human
	// mode. Neither the flag nor the value is wrong on its own — only the
	// combination is, so no flag-value code fits.
	CodeJQOutputConflict = "jq_output_conflict"
	// CodeJQOutputTooLarge is a filter that produced more output than the
	// CLI will buffer. The expression is not wrong — it asked for more
	// than fits — so the recovery is to narrow it, which is a different
	// fix from either jq code above.
	CodeJQOutputTooLarge = "jq_output_too_large"
	// CodeReadOnlyBlocked is a write refused because read-only mode is
	// on. Not a validation mistake — the request was well-formed and the
	// operator chose to forbid it — so the recovery is to turn the mode
	// off, not to fix the input.
	CodeReadOnlyBlocked = "read_only_blocked"
	// CodePathInvalid is a request path segment rejected before it could
	// reach the wire: a traversal, an embedded separator, a control
	// character. Distinct from a bad flag value because the offending
	// value is usually data the caller passed through from elsewhere.
	CodePathInvalid = "path_invalid"

	// CodeInsecureScheme is a base URL, or a redirect target, that would
	// put the API token on the wire in cleartext. Its own code because
	// the fix is a specific one — change the scheme — and the generic
	// "check the values you passed" says nothing about which value or
	// what is wrong with it.
	CodeInsecureScheme = "insecure_scheme"
	// CodeEditorMissing means no editor is configured. Distinct from
	// CodeConfigInvalid because the fix is a different one entirely: this
	// is an environment variable, not a jira setting, so a hint pointing
	// at the config file would send the caller to the wrong place.
	CodeEditorMissing = "editor_missing"
	// CodeEditorFailed means the configured editor could not start or exited
	// unsuccessfully. Inspect saved work before retrying the editor.
	CodeEditorFailed = "editor_failed"
	// CodeTerminalRestoreFailed means borrowed terminal modes could not be
	// restored after the editor. The caller must restore the terminal to proceed.
	CodeTerminalRestoreFailed = "terminal_restore_failed"
	// CodeTerminalRequired means the command needs an interactive
	// terminal and does not have one. Its own code because nothing is
	// wrong with the caller's input — the answer is a different way to
	// invoke, which is what its hint has to say.
	CodeTerminalRequired = "terminal_required"
	// CodeCanceled is the caller stopping the work — an interrupt, or
	// closing an interactive form. It already existed for a canceled
	// context; naming it lets a command report a deliberate cancellation
	// with a message that fits, rather than plumbing a fake context error.
	CodeCanceled = "canceled"
	// CodeAuthMissing means no usable credential is configured at all —
	// distinct from jira_unauthorized, which is Jira rejecting one that
	// exists. Different remedies: log in, versus check the token you
	// already have.
	CodeAuthMissing = "auth_missing"
	// CodeLocalIO means a local filesystem operation failed: no space, a
	// read-only directory, a permission denied. Its own code because the
	// generic validation advice ("check the values you passed") sends
	// automation to retry or rewrite input when the remedy is storage or
	// permissions, and nothing about the input was wrong.
	//
	// It keeps the validation EXIT even so. The published 0-8 contract
	// has no local-I/O class, and inventing an exit here would move a
	// contract to fix a hint — so the code and its hint carry the
	// distinction, which is where the caller reads it.
	CodeLocalIO = "local_io_failed"
	// CodeConfigChanged means the file moved under an in-progress edit.
	// Its own code because the answer is neither "fix your input" nor
	// "try again blindly": the edit still exists, and the caller has to
	// decide which version wins.
	CodeConfigChanged = "config_changed"
	// CodeConfigInvalid means the config file's CONTENT is not usable:
	// it does not parse, or a value in it is malformed. Not
	// flag_value_invalid — nothing was passed on the command line — and
	// deliberately narrow: a missing editor, an absent terminal, and a
	// file that moved mid-edit each have their own code above, because
	// each wants different advice.
	CodeConfigInvalid = "config_invalid"
	// CodeAliasInvalid reports an invalid name, expansion or cyclic chain.
	CodeAliasInvalid = "alias_invalid"
	// CodeSavedQueryUnknown reports a missing local saved-query name.
	CodeSavedQueryUnknown = "saved_query_unknown"
	// CodeSavedQueryInvalid reports invalid local query files or configuration.
	CodeSavedQueryInvalid = "saved_query_invalid"
)

// inputCodes lists every input-failure code, in one place, so the
// registry completeness test can require a row per code without
// re-enumerating them — a code added above joins the test by construction.
var inputCodes = []string{
	CodeCommandUnknown,
	CodeFlagUnknown,
	CodeFlagRequired,
	CodeFlagForeign,
	CodeFlagValueMissing,
	CodeFlagValueInvalid,
	CodeADFContentLoss,
	CodeFlagSyntaxInvalid,
	CodeArgCountInvalid,
	CodeArgValueInvalid,
	CodeFlagNotApplicable,
	CodeTransitionScreenRequired,
	CodeFlagConflict,
	CodeJQExpressionInvalid,
	CodeJQEvalFailed,
	CodeJQOutputConflict,
	CodeJQOutputTooLarge,
	CodeReadOnlyBlocked,
	CodePathInvalid,
	CodeInsecureScheme,
	CodeConfigInvalid,
	CodeAliasInvalid,
	CodeSavedQueryUnknown,
	CodeSavedQueryInvalid,
	CodeEditorMissing,
	CodeEditorFailed,
	CodeTerminalRestoreFailed,
	CodeTerminalRequired,
	CodeConfigChanged,
	CodeLocalIO,
}

// InputError is a command-line input failure typed at the dispatch
// boundary — unknown command, unknown flag, bad value, wrong arity —
// before any client or renderer exists. Field carries the offender (the
// flag name without dashes, the command token, the rejected value) and
// Suggestions carries "did you mean" candidates; both ride the machine
// envelope so an agent branches on data, never on message prose.
type InputError struct {
	Code        string
	Message     string
	Field       string
	Suggestions []string
}

// NewInputError builds an InputError. Callers set Field and Suggestions
// afterward when the failure names an offender.
func NewInputError(code, message string) *InputError {
	return &InputError{Code: code, Message: message}
}

func (e *InputError) Error() string { return e.Message }

// UnknownFlagError classifies an unrecognized long flag. Near-misses
// (computed by the caller, which knows the failing command's flag set)
// make it a spelling problem; with none, a flag borrowed from another
// Jira CLI becomes the orientation code with that CLI's equivalents as
// suggestions — the foreign table only carries flags with no equivalent
// here, so the two branches cannot both apply.
func UnknownFlagError(message, flag string, nearMisses []string) *InputError {
	e := NewInputError(CodeFlagUnknown, message)
	e.Field = flag
	e.Suggestions = nearMisses
	if len(nearMisses) == 0 {
		if foreign := ForeignFlagSuggestions(flag); len(foreign) > 0 {
			e.Code = CodeFlagForeign
			e.Suggestions = foreign
		}
	}
	return e
}
