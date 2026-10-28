package cli

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"time"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/attachmentfiles"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/output"
)

// Exit codes are a published contract — agents branch on them, so a value
// here never changes meaning. main owns the process exit; conductor routes
// command errors through ExitCode at the composition root.
const (
	ExitOK         = 0
	ExitAuth       = 1
	ExitNotFound   = 2
	ExitValidation = 3
	ExitRateLimit  = 4
	ExitServer     = 5
	ExitCanceled   = 6
	ExitTimeout    = 7
	ExitOutput     = 8
)

// Classification is a failure resolved into what both audiences need: the stable
// snake_case Code and Exit for machines, Message and Hint for humans, and
// the resolve-not-report trio — Next (the action to take), Doc (where the
// contract is written up), Field (the machine-readable thing that broke) —
// so an agent can retry without parsing prose.
type Classification struct {
	Code    string
	Message string
	Hint    string
	Next    string
	Doc     string
	Field   string
	Exit    int
	// Suggestions carries "did you mean" candidates for input typos —
	// command names, flag spellings, foreign-CLI equivalents — so an
	// agent recovering from a mistake branches on values, not on the
	// hint's prose.
	Suggestions []string
	// Retry facts lifted from the transport when the failure carried them
	// — the envelope publishes these so an agent branches on numbers, not
	// on the hint's prose.
	RetryAfterSeconds int
	Attempts          int
	Waited            time.Duration
}

// spec pins a code's static half. Hints never interpolate runtime values —
// the specific belongs in Message or Field; two errors wanting different
// hints get different codes. Next and Doc fill in as the commands and
// guides they would point at land; the fields are contract now so the
// envelope shape never shifts underneath an agent.
type spec struct {
	exit int
	hint string
	next string
	doc  string
}

// CodeOutputWriteFailed is the operation succeeded and its result could
// not be emitted. Named because more than the render path raises it: a
// command layer that cannot resolve an output format is in the same
// position — the work happened, the caller got nothing — and the
// alternative is a bare error taking the generic validation row, whose
// hint asks the reader to check values they never passed.
const CodeOutputWriteFailed = "output_write_failed"

// CodeDashboardTerminalRequired refuses a screen before credentials or input.
const CodeDashboardTerminalRequired = "dashboard_terminal_required"

// CodeAuthStoreUnavailable means a credential lookup failed, so absence and
// validity of a stored credential remain unknown.
const CodeAuthStoreUnavailable = "auth_store_unavailable"

// CodeRedirectRefused is a server-directed request rejected by transport
// safety. It does not establish whether the original mutation took effect.
const CodeRedirectRefused = "redirect_refused"

// CodeSelectorIncomplete means Jira's search did not establish the complete
// target set. The command refuses before confirming or performing any write.
const CodeSelectorIncomplete = "selector_incomplete"

// CodeAPIResponseIncomplete reports a bounded or failed raw response capture.
const CodeAPIResponseIncomplete = "api_response_incomplete"

// CodeJiraPreconditionFailed means Jira refused the supplied HTTP precondition.
const CodeJiraPreconditionFailed = "jira_precondition_failed"

const (
	CodeUserNotFound             = "user_not_found"
	CodeUserAmbiguous            = "user_ambiguous"
	CodeUserResolutionIncomplete = "user_resolution_incomplete"
)

// CodeUserSearchResponseInvalid reports malformed or repeated upstream pages.
const CodeUserSearchResponseInvalid = "user_search_response_invalid"

// CodeUserLookupResponseInvalid reports an unusable or mismatched exact-account reply.
const CodeUserLookupResponseInvalid = "user_lookup_response_invalid"

// CodeCurrentUserResponseInvalid reports an unusable upstream account response.
const CodeCurrentUserResponseInvalid = "current_user_response_invalid"

// CodeCacheIO retains a failed local metadata inventory or removal.
const CodeCacheIO = "cache_io_failed"

// CodeJQLResponseInvalid reports malformed parser or reference metadata.
const CodeJQLResponseInvalid = "jql_response_invalid"

// CodeBoardResponseInvalid reports malformed Agile board or project pages.
const CodeBoardResponseInvalid = "board_response_invalid"

// CodeCacheResponseInvalid reports malformed or inconsistent metadata reads.
const CodeCacheResponseInvalid = "cache_response_invalid"

// CodeBoardCacheUnavailable distinguishes an unusable local snapshot from a
// Jira 404. Local composition never contacts Jira to repair this condition.
const CodeBoardCacheUnavailable = "board_cache_unavailable"

// CodeSearchResponseInvalid reports malformed or repeated issue search pages.
const CodeSearchResponseInvalid = "search_response_invalid"

// CodeWorklogResponseInvalid reports a malformed worklog page or POST receipt.
const CodeWorklogResponseInvalid = "worklog_response_invalid"

// CodeBulkOperationResponseInvalid reports malformed task acknowledgement or
// progress. An accepted move is not necessarily complete or safe to repeat.
const CodeBulkOperationResponseInvalid = "bulk_operation_response_invalid"

// CodeBulkOperationFailed reports a native task or explicit issue failure.
const CodeBulkOperationFailed = "bulk_operation_failed"

// CodeBulkOutcomeUnconfirmed reports a terminal receipt that does not identify
// the requested issue's outcome. It does not claim that no move occurred.
const CodeBulkOutcomeUnconfirmed = "bulk_outcome_unconfirmed"

// CodeAttachmentFileInvalid means local upload inputs failed preflight.
const CodeAttachmentFileInvalid = "attachment_file_invalid"

// CodeIssueHierarchyInvalid reports unusable native parent observations.
const CodeIssueHierarchyInvalid = "issue_hierarchy_response_invalid"

// CodeAttachmentDownloadLimit reports a streamed download exceeding its byte bound.
const CodeAttachmentDownloadLimit = "attachment_download_limit_exceeded"

// CodeAttachmentResponseInvalid reports an unusable attachment response.
const CodeAttachmentResponseInvalid = "attachment_response_invalid"

// CodeCommentResponseInvalid reports an unusable comment page or write receipt.
const CodeCommentResponseInvalid = "comment_response_invalid"

// CodeTransitionResponseInvalid reports an uncertain workflow acknowledgement.
const CodeTransitionResponseInvalid = "transition_response_invalid"

// CodeTransitionReadInvalid identifies unusable native workflow discovery.
const CodeTransitionReadInvalid = "transition_read_invalid"

// CodeWeblinkResponseInvalid reports an unusable remote-link response.
const CodeWeblinkResponseInvalid = "weblink_response_invalid"

// CodeRankResponseInvalid reports an uncertain ranking acknowledgement.
const CodeRankResponseInvalid = "rank_response_invalid"

// CodeRankNotAttempted reports a move withheld after an earlier batch stopped.
const CodeRankNotAttempted = "rank_not_attempted"

// CodeIssueDeleteResponseInvalid reports an uncertain issue deletion acknowledgement.
const CodeIssueDeleteResponseInvalid = "issue_delete_response_invalid"

// CodeIssueCreateResponseInvalid reports an uncertain or unusable creation receipt.
const CodeIssueCreateResponseInvalid = "issue_create_response_invalid"

// CodeIssueCreateFollowupFailed reports creation with a failed native followup.
const CodeIssueCreateFollowupFailed = "issue_create_followup_failed"

// Local draft failures retain recovery paths and distinguish malformed artifacts,
// changed originals and actual filesystem failures.
const (
	CodeIssueEditDraftInvalid = "issue_edit_draft_invalid"
	CodeIssueEditDraftChanged = "issue_edit_draft_changed"
	CodeIssueEditDraftIO      = "issue_edit_draft_io"
)

// CodeIssueDescriptionUnavailable reports a client without bound description reads.
const CodeIssueDescriptionUnavailable = "issue_description_unavailable"

// CodeIssueEditSourceInvalid refuses an incomplete native editing identity.
const CodeIssueEditSourceInvalid = "issue_edit_source_invalid"

// CodeIssueDescriptionResponseInvalid reports an unusable native description snapshot.
const CodeIssueDescriptionResponseInvalid = "issue_description_response_invalid"

// CodeIssueDescriptionScopeChanged refuses a different issue or constructed client.
const CodeIssueDescriptionScopeChanged = "issue_description_scope_changed"

// CodeIssueCloneSourceInvalid reports source or metadata that cannot be copied safely.
const CodeIssueCloneSourceInvalid = "issue_clone_source_invalid"

// Input discovery distinguishes invalid upstream metadata and unavailable local
// readers from project-specific type selection failures. A local unknown type
// is not Jira's HTTP 404; callers can inspect retained native candidates.
const (
	CodeIssueMetadataResponseInvalid = "issue_metadata_response_invalid"
	CodeIssueMetadataUnavailable     = "issue_metadata_unavailable"
	CodeIssueTypeUnknown             = "issue_type_unknown"
	CodeIssueTypeAmbiguous           = "issue_type_ambiguous"
	CodeIssueFieldPreflightFailed    = "issue_field_preflight_failed"
)

// registry maps every code to its exit and hint. The shared not-found hint
// stays resource-neutral on purpose: jira_not_found covers every Jira 404
// (issue, board, user, …), so it says "identifier", never "issue key".
var registry = map[string]spec{
	CodeIssueEditSourceInvalid:          {exit: ExitServer, hint: "Refresh the issue or inspect its native identity and fields. No editor was opened from the incomplete response."},
	CodeIssueFieldPreflightFailed:       {exit: ExitValidation, hint: "Inspect this context with issue create/edit --generate-json and choose an available field and operation. No write was sent for the refused context; metadata does not validate field values or guarantee permission."},
	CodeIssueEditDraftInvalid:           {exit: ExitValidation, hint: "Keep source.json and description.md. Inspect the readiness manifest and native backup; repair authored Markdown with issue edit resume. No candidate or Jira write was accepted."},
	CodeIssueEditDraftChanged:           {exit: ExitValidation, hint: "Retain the original native backup and saved work. Inspect the changed directory or source before reopening; do not replace an existing draft or retry creation over it."},
	CodeIssueEditDraftIO:                {exit: ExitValidation, hint: "Inspect retained recovery paths and acknowledged publication effects before retrying. Check local permissions, free space and hard-link support; partial creation and editor saves may already exist."},
	CodeIssueDescriptionUnavailable:     {exit: ExitValidation, hint: "Use a configured Jira adapter that supports bound native description reads. Help and schema remain available without opening a client."},
	CodeIssueDescriptionResponseInvalid: {exit: ExitServer, hint: "Inspect the native issue description and identity with `jira api get`. No editable snapshot was prepared from incomplete or malformed state."},
	CodeIssueDescriptionScopeChanged:    {exit: ExitValidation, hint: "Use the snapshot's original site and account, or prepare a fresh snapshot. Public timestamps and cached identity do not authorize a Jira write."},
	CodeIssueMetadataResponseInvalid:    {exit: ExitServer, hint: "Retry the metadata read or inspect its native endpoint with `jira api get`. Incomplete or contradictory metadata cannot establish an input skeleton."},
	CodeIssueMetadataUnavailable:        {exit: ExitValidation, hint: "Use a configured Jira adapter that supports live issue input discovery. Help and schema remain available without opening a client."},
	CodeIssueTypeUnknown:                {exit: ExitValidation, hint: "Rerun with --output=json to inspect this project's available issue types in data.issue_types, then choose one with --type."},
	CodeIssueTypeAmbiguous:              {exit: ExitValidation, hint: "Rerun with --output=json to inspect matching issue types in data.issue_types, then use one explicit issue-type ID with --type."},
	CodeUpdateUnsupported:               {exit: ExitValidation, hint: "Use the original installation method, or install a published release; inspect `jira update --help`."},
	CodeUpdateFailed:                    {exit: ExitServer, hint: "Inspect the retained update receipt and installed executable before retrying. Check release availability, installation permissions and package-manager diagnostics."},
	CodeUpdateOwnership:                 {exit: ExitValidation, hint: "Run the executable owned by matcra587/tap/jira, or update the current copy through its original installation method. Other copies are preserved."},
	CodeUpdateIntegrity:                 {exit: ExitServer, hint: "Do not install the rejected download. Inspect the published release assets and checksums before retrying."},
	CodeUpdateChanged:                   {exit: ExitValidation, hint: "The installed executable changed during this update. Inspect its current version before requesting another update."},
	CodeUpdateUnavailable:               {exit: ExitNotFound, hint: "No published update release was found. Check the repository's releases before retrying."},
	CodeIssueCloneSourceInvalid:         {exit: ExitServer, hint: "Inspect the source issue and /rest/api/3/field with `jira api get`. This source was not cloned because its native fields could not be classified safely. Inspect other sources' outcomes before retrying."},
	CodeIssueCreateFollowupFailed:       {exit: ExitServer, hint: "Inspect the retained native receipt and created issue. Retry only the failed transition or watcher operation; repeating create or clone makes another issue."},
	CodeIssueCreateResponseInvalid:      {exit: ExitServer, hint: "Inspect the project's issues and retained creation payload before retrying. Jira may already have created it; repeating create or clone makes duplicates. Creation writes are never automatically replayed."},
	CodeIssueDeleteResponseInvalid:      {exit: ExitServer, hint: "Inspect each requested issue and any subtasks before retrying. Jira may already have deleted them; deletion writes are never automatically replayed."},
	CodeRankResponseInvalid:             {exit: ExitServer, hint: "Inspect the backlog order and native ranking response before retrying. Jira may already have moved some issues; ranking writes are never automatically replayed."},
	CodeRankNotAttempted:                {exit: ExitServer, hint: "This move was not submitted because an earlier ranking batch did not complete. Inspect earlier outcomes and the backlog before choosing an anchor for the remaining moves."},
	CodeWeblinkResponseInvalid:          {exit: ExitServer, hint: "Inspect the issue's web links and native response before retrying a write. Jira may already have stored it; repeating an add without the same global ID creates duplicates."},
	CodeCommentResponseInvalid:          {exit: ExitServer, hint: "Retry reads as needed. Inspect the issue's comment list before retrying a write; Jira may already have stored it."},
	CodeTransitionResponseInvalid:       {exit: ExitServer, hint: "Inspect the issue's current status and comments before retrying; the transition may already have landed."},
	CodeTransitionReadInvalid:           {exit: ExitServer, hint: "Retry transition discovery or inspect the native workflow with `jira api get`. Do not transition using incomplete or contradictory metadata."},
	CodeAttachmentResponseInvalid:       {exit: ExitServer, hint: "Retry reads as needed. Inspect the issue's attachment list before retrying a write; Jira may already have applied it. Repeating an upload creates duplicates."},
	CodeIssueHierarchyInvalid:           {exit: ExitServer, hint: "Retry the hierarchy read or inspect the issue in Jira. Missing or contradictory parent data cannot establish a hierarchy."},
	CodeAttachmentDownloadLimit:         {exit: ExitValidation, hint: "Choose a larger positive --max-download-bytes value if you intend to save this attachment. Partial downloads are removed; existing overwrite targets are preserved."},
	CodeAttachmentFileInvalid:           {exit: ExitValidation, hint: "Inspect the local attachment paths. Use readable regular files, with each file and their combined content no larger than 100 MiB."},
	CodeJiraPreconditionFailed:          {exit: ExitValidation, hint: "Fetch the current resource, reconcile edits, and choose a new precondition before sending another write."},
	CodeBoardCacheUnavailable:           {exit: ExitNotFound, hint: "Run `jira issue list` with an explicit matching --board or --board-id selector and --refresh --as-jql to cache its saved filter, then retry your original command. For default_board, pass its name explicitly as --board NAME. The site and account must match; local composition never refreshes the cache."},
	CodeJQLResponseInvalid:              {exit: ExitServer, hint: "Jira returned malformed JQL response data. Retry the read or inspect the native response with `jira api`; query diagnostics are separate from this protocol failure."},
	CodeBulkOperationFailed:             {exit: ExitServer, hint: "Inspect the retained task progress and per-issue failure reasons. Verify any processed issues before submitting another move."},
	CodeBulkOutcomeUnconfirmed:          {exit: ExitServer, hint: "The task did not confirm this issue's outcome. Inspect its current project/type and the retained native task progress before repeating a move."},
	CodeBulkOperationResponseInvalid:    {exit: ExitServer, hint: "Jira returned a malformed bulk-operation receipt. Retain any task identity and inspect its progress before repeating a write; the move may already be accepted."},
	CodeWorklogResponseInvalid:          {exit: ExitServer, hint: "Jira returned a malformed worklog response. Inspect the issue's worklogs before retrying a write; a worklog may already have been created."},
	"jira_unauthorized":                 {exit: ExitAuth, hint: "Jira rejected the credentials — check the account email, and re-run `jira auth login --with-token` if the token was revoked."},
	CodeAuthMissing:                     {exit: ExitAuth, hint: "Pipe a token in with `jira auth login --with-token` — for example from a password manager."},
	CodeAuthStoreUnavailable:            {exit: ExitAuth, hint: "Unlock or restore access to the system keyring and retry, or supply explicit environment credentials."},
	"jira_forbidden":                    {exit: ExitAuth, hint: "The credentials work but lack permission for this resource — ask a Jira admin, or switch to a profile that has it."},
	"jira_not_found":                    {exit: ExitNotFound, hint: "Check the identifier — the site answered, but nothing lives at it."},
	"jira_bad_request":                  {exit: ExitValidation, hint: "Jira rejected some of the values — fix the fields it flagged and send it again."},
	"jira_request_failed":               {exit: ExitValidation, hint: "Jira refused the request — the message carries its exact status and complaint."},
	"jira_rate_limited":                 {exit: ExitRateLimit, hint: "Jira throttled this call even after automatic retries — wait out the reported pause, then run it again."},
	"jira_server_error":                 {exit: ExitServer, hint: "Jira itself failed — wait and retry, or check the site's status page."},
	CodeRedirectRefused:                 {exit: ExitServer, hint: "Inspect the redirect and configured site/API scope. For a mutation, check the affected resource before retrying; the original request may have reached Jira."},
	CodeSelectorIncomplete:              {exit: ExitServer, hint: "The complete target set could not be resolved; nothing was written. Inspect the search with `jira issue list --output=json`, or review explicit keys and pass them through `--from-file`."},
	CodeOutputWriteFailed:               {exit: ExitOutput, hint: "The result could not be written to the destination — a mutation may still have happened — check the affected Jira resource before running the command again."},
	CodeAPIResponseIncomplete:           {exit: ExitOutput, hint: "Inspect the partial HTTP receipt. Verify mutation outcomes before retrying; for a read, allow a larger --max-response-bytes or fix the interrupted response."},
	CodeUserNotFound:                    {exit: ExitValidation, hint: "No visible active match was returned; permissions or privacy can restrict search. Use jira user search and pass the intended account ID."},
	CodeUserAmbiguous:                   {exit: ExitValidation, hint: "Use jira user search to inspect visible matches, then pass the intended account ID explicitly."},
	CodeUserResolutionIncomplete:        {exit: ExitValidation, hint: "The visible search window could not establish one account. Use jira user search and pass the intended account ID explicitly."},
	CodeUserLookupResponseInvalid:       {exit: ExitServer, hint: "Jira returned an unusable or mismatched account reply. Inspect the intended account ID; the lookup does not establish watcher permissions."},
	CodeUserSearchResponseInvalid:       {exit: ExitServer, hint: "Inspect any retained user matches. Jira returned a malformed or repeated page; narrow the query or inspect the user/search endpoint with `jira api get`."},
	CodeCurrentUserResponseInvalid:      {exit: ExitServer, hint: "Jira returned a malformed current-account response. Inspect it with `jira api get /rest/api/3/myself`; authentication was not confirmed and account-dependent writes were not started."},
	CodeBoardResponseInvalid:            {exit: ExitServer, hint: "Jira returned a malformed board or associated-project page. Inspect the Agile endpoint with `jira api get` before retrying."},
	CodeCacheResponseInvalid:            {exit: ExitServer, hint: "Jira returned malformed or inconsistent metadata. Inspect retained values and the metadata endpoint with `jira api get`; an incomplete read must not replace cached metadata."},
	CodeSearchResponseInvalid:           {exit: ExitServer, hint: "Jira returned a malformed or repeated search page. Inspect any retained issues and the raw response with `jira api post /rest/api/3/search/jql`; selector writes were not started."},
	"canceled":                          {exit: ExitCanceled, hint: "The command was interrupted. Inspect any attempted mutations before retrying; rerun interrupted reads as needed."},
	"timeout":                           {exit: ExitTimeout, hint: "The command's deadline passed. Inspect any attempted mutations before retrying; allow a longer timeout for reads."},
	"validation_failed":                 {exit: ExitValidation, hint: "Check the values you passed and try again."},
	CodeInsecureScheme:                  {exit: ExitValidation, hint: "Use an `https://` site URL — Basic auth rides the first request, so plain http would send the API token in the clear."},
	// Input-failure rows: one class-specific hint per code, so a bad verb,
	// a bad flag, and a bad value each say what to fix. Hints stay static —
	// the offender rides Field and candidates ride Suggestions.
	CodeCommandUnknown:           {exit: ExitValidation, hint: "Check the command's spelling, or run `--help` on its parent to see the ones it accepts."},
	CodeFlagUnknown:              {exit: ExitValidation, hint: "Check the flag's spelling, or run the command with `--help` to see the ones it accepts."},
	CodeFlagForeign:              {exit: ExitValidation, hint: "That flag belongs to a different Jira CLI — use this CLI's equivalent instead."},
	CodeFlagRequired:             {exit: ExitValidation, hint: "Add the flag the command needs — run it with `--help` to see which."},
	CodeFlagValueMissing:         {exit: ExitValidation, hint: "Give the flag a value, like `--flag=value`."},
	CodeFlagValueInvalid:         {exit: ExitValidation, hint: "That value isn't the type the flag expects — run the command with `--help` to see its format."},
	CodeADFInvalid:               {exit: ExitValidation, hint: "Inspect the retained schema diagnostics and correct the native document. Local validation does not prove Jira field acceptance."},
	CodeADFTypeUnknown:           {exit: ExitValidation, hint: "Run `jira agent adf-matrix` to discover supported runtime types and context definitions, then pass an exact name to `jira adf explain-node`."},
	CodeADFContentLoss:           {exit: ExitValidation, hint: "Inspect the retained candidate and warnings; revise the Markdown or explicitly accept loss with --adf-best-effort."},
	CodeFlagSyntaxInvalid:        {exit: ExitValidation, hint: "Write it as `--flag=value` or `--flag value`, with nothing stray around the flag."},
	CodeArgCountInvalid:          {exit: ExitValidation, hint: "Check how many arguments the command takes; its usage line is in `--help`."},
	CodeArgValueInvalid:          {exit: ExitValidation, hint: "That isn't one of the accepted values — run the command with `--help` to see the choices."},
	CodeFlagNotApplicable:        {exit: ExitValidation, hint: "That flag is real but has no effect here — drop it and run the command again."},
	CodeTransitionScreenRequired: {exit: ExitValidation, hint: "Choose a transition with a screen, or submit the native fields, operations and properties separately. Inspect available transition metadata before writing.", next: "inspect", doc: "safe-mutation"},
	CodeFlagConflict:             {exit: ExitValidation, hint: "Those two flags set the same thing — keep the one you meant and drop the other."},
	// jq rows: a syntax fault, a shape fault, and a mode conflict each
	// want a different fix, so each gets its own code and hint.
	CodeJQExpressionInvalid:       {exit: ExitValidation, hint: "Check the jq expression — the message names what the engine rejected."},
	CodeJQEvalFailed:              {exit: ExitValidation, hint: "The expression is valid but does not fit this document — inspect the real shape with `--output=json` and no filter."},
	CodeJQOutputTooLarge:          {exit: ExitValidation, hint: "Narrow the expression — it produced more output than the CLI will buffer."},
	CodeReadOnlyBlocked:           {exit: ExitValidation, hint: "Read-only mode is on, so writes are refused before they reach Jira — clear it to allow this command to mutate."},
	CodeCacheIO:                   {exit: ExitValidation, hint: "Inspect base_url and email with `jira config list`, then check available space and permissions in the metadata cache. Inspect retained observations before retrying the cache operation."},
	CodeLocalIO:                   {exit: ExitValidation, hint: "A local file operation failed — check free space and the permissions on the config directory."},
	CodeConfigChanged:             {exit: ExitValidation, hint: "Inspect the current configuration, then reapply your change on top of it."},
	CodeAliasInvalid:              {exit: ExitValidation, hint: "Inspect `jira alias list`; fix the named alias or delete it with `jira alias delete NAME --force`."},
	CodeSavedQueryUnknown:         {exit: ExitValidation, hint: "Use a listed filename stem from queries_path, or create NAME.jql there; inspect `jira config current`."},
	CodeSavedQueryInvalid:         {exit: ExitValidation, hint: "Inspect queries_path with `jira config current`; repair the named .jql file or its YAML/TOML frontmatter."},
	CodeEditorMissing:             {exit: ExitValidation, hint: "Set EDITOR (or JIRA_EDITOR) to the editor you want — `export EDITOR=nano`."},
	CodeEditorFailed:              {exit: ExitValidation, hint: "Inspect any saved edit and check the configured editor before retrying."},
	CodeTerminalRestoreFailed:     {exit: ExitValidation, hint: "Restore the terminal before continuing, then inspect any saved edit."},
	CodeTerminalRequired:          {exit: ExitValidation, hint: "Run it from a terminal, or use `--dry-run` to see the file and editor without opening anything."},
	CodeDashboardTerminalRequired: {exit: ExitValidation, hint: "Run `jira tui --output=human` in a terminal, or use `jira issue list` for scripts and agents."},
	CodeConfigInvalid:             {exit: ExitValidation, hint: "Fix the setting where it is defined — `jira config list` shows each value and the layer it came from."},
	CodePathInvalid:               {exit: ExitValidation, hint: "That value cannot be part of a request path — check the identifier for separators, dots, or characters that are not part of a key."},
	CodeJQOutputConflict:          {exit: ExitValidation, hint: "`--jq` filters machine output — drop it, or ask for `--output=json` or `--output=compact`."},
}

// HasJiraResponse reports a retained native HTTP failure independently of its
// taxonomy. Local refusals can share an exit code with native request failures,
// so callers must not infer whether Jira answered from that code or prose.
func HasJiraResponse(err error) bool {
	response, ok := errors.AsType[*adapterjira.APIError](err)
	return ok && response.StatusCode != 0
}

// MapError resolves any error to its taxonomy entry. Tier order matters:
// context outcomes come first because a typed chain can wrap a
// cancellation, and the cancellation is the truth of what happened; typed
// adapters follow; the terminal fallback classifies every untyped error as
// validation. Never classify by matching message text — an error that
// needs another class gets typed at its source.
func MapError(err error) Classification {
	// nil maps to the zero Error (exit 0): no error is no error. Without
	// this, the fallback tier would call Error() on a nil interface — a
	// panic waiting for the first caller that doesn't pre-check.
	if err == nil {
		return Classification{}
	}
	// A provider cause or its outer wrapper may contain authentication
	// material. Preserve context classification while formatting only the
	// sanitized provider boundary.
	messageErr := err
	if backend, ok := errors.AsType[*core.CredentialStoreError](err); ok {
		messageErr = backend
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return newError("timeout", messageErr)
	case errors.Is(err, context.Canceled):
		return newError("canceled", messageErr)
	}
	var writeErr *output.WriteError
	if errors.As(err, &writeErr) {
		// The work may have completed; only reporting it failed. Exit 8
		// keeps that distinct from every Jira-side class so an agent
		// never auto-retries a mutation off the back of a broken pipe.
		return newError(CodeOutputWriteFailed, err)
	}
	// Filter faults that happen at RENDER time are sentinel-wrapped by the
	// output layer rather than typed there, because internal/output must
	// not import this package. Compile faults never reach here — the flag
	// layer types them at resolution, before the command runs — so only
	// the render-time sentinels get rows. They sit AFTER the write and
	// context tiers on purpose: a filter killed by a deadline is a
	// timeout, and one that could not be written is a write failure; in
	// neither case is the expression what broke.
	// Transport guards are sentinel-wrapped by the adapter, which must not
	// import this package. They sit ahead of the API tier: a request the
	// gate refused never reached Jira, so it has no status to classify.
	switch {
	case errors.Is(err, core.ErrIssueEditDraftChanged):
		return newError(CodeIssueEditDraftChanged, err)
	case errors.Is(err, core.ErrIssueEditDraftInvalid):
		return newError(CodeIssueEditDraftInvalid, err)
	case errors.Is(err, core.ErrIssueEditDraftIO):
		return newError(CodeIssueEditDraftIO, err)
	case errors.Is(err, core.ErrIssueHierarchyInvalid):
		return newError(CodeIssueHierarchyInvalid, err)
	case errors.Is(err, core.ErrIssueMetadataInvalid):
		return newError(CodeIssueMetadataResponseInvalid, err)
	case errors.Is(err, core.ErrIssueMetadataUnavailable):
		return newError(CodeIssueMetadataUnavailable, err)
	case errors.Is(err, core.ErrIssueDescriptionUnavailable):
		return newError(CodeIssueDescriptionUnavailable, err)
	case errors.Is(err, core.ErrIssueDescriptionInvalid):
		return newError(CodeIssueDescriptionResponseInvalid, err)
	case errors.Is(err, core.ErrIssueDescriptionScopeChanged):
		return newError(CodeIssueDescriptionScopeChanged, err)
	case errors.Is(err, core.ErrIssueCloneSourceInvalid):
		return newError(CodeIssueCloneSourceInvalid, err)
	case errors.Is(err, core.ErrIssueCreateResponseInvalid):
		return newError(CodeIssueCreateResponseInvalid, err)
	case errors.Is(err, core.ErrIssueCreateFollowupFailed):
		return newError(CodeIssueCreateFollowupFailed, err)
	case errors.Is(err, core.ErrIssueDeleteResponseInvalid):
		return newError(CodeIssueDeleteResponseInvalid, err)
	case errors.Is(err, core.ErrRankResponseInvalid):
		return newError(CodeRankResponseInvalid, err)
	case errors.Is(err, core.ErrRankNotAttempted):
		return newError(CodeRankNotAttempted, err)
	case errors.Is(err, core.ErrWeblinkResponseInvalid):
		return newError(CodeWeblinkResponseInvalid, err)
	case errors.Is(err, core.ErrCommentResponseInvalid):
		return newError(CodeCommentResponseInvalid, err)
	case errors.Is(err, core.ErrTransitionResponseInvalid):
		return newError(CodeTransitionResponseInvalid, err)
	case errors.Is(err, core.ErrTransitionReadInvalid):
		return newError(CodeTransitionReadInvalid, err)
	case errors.Is(err, core.ErrAttachmentResponseInvalid):
		return newError(CodeAttachmentResponseInvalid, err)
	case errors.Is(err, attachmentfiles.ErrInvalid):
		return newError(CodeAttachmentFileInvalid, err)
	case errors.Is(err, adapterjira.ErrReadOnly):
		return newError(CodeReadOnlyBlocked, err)
	case errors.Is(err, adapterjira.ErrUnsafePath):
		return newError(CodePathInvalid, err)
	case errors.Is(err, adapterjira.ErrInsecureScheme):
		return newError(CodeInsecureScheme, err)
	case errors.Is(err, adapterjira.ErrUnsafeRedirect):
		return newError(CodeRedirectRefused, err)
	case errors.Is(err, core.ErrUserNotFound):
		return newError(CodeUserNotFound, err)
	case errors.Is(err, core.ErrUserAmbiguous):
		return newError(CodeUserAmbiguous, err)
	case errors.Is(err, core.ErrUserResolutionIncomplete):
		return newError(CodeUserResolutionIncomplete, err)
	case errors.Is(err, core.ErrUserLookupResponseInvalid):
		return newError(CodeUserLookupResponseInvalid, err)
	case errors.Is(err, adapterjira.ErrUserSearchResponseInvalid):
		return newError(CodeUserSearchResponseInvalid, err)
	case errors.Is(err, adapterjira.ErrCurrentUserResponseInvalid):
		return newError(CodeCurrentUserResponseInvalid, err)
	case errors.Is(err, adapterjira.ErrJQLResponseInvalid):
		return newError(CodeJQLResponseInvalid, err)
	case errors.Is(err, adapterjira.ErrBoardResponseInvalid):
		return newError(CodeBoardResponseInvalid, err)
	case errors.Is(err, adapterjira.ErrSearchResponseInvalid):
		return newError(CodeSearchResponseInvalid, err)
	case errors.Is(err, adapterjira.ErrWorklogResponseInvalid):
		return newError(CodeWorklogResponseInvalid, err)
	case errors.Is(err, adapterjira.ErrBulkOperationResponseInvalid):
		return newError(CodeBulkOperationResponseInvalid, err)
	}
	switch {
	case errors.Is(err, output.ErrFilterFailed):
		return newError(CodeJQEvalFailed, err)
	case errors.Is(err, output.ErrFilterTooLarge):
		return newError(CodeJQOutputTooLarge, err)
	}
	// An explicit diagnosis (such as migration recovery) owns its remedy.
	// Provider failures without that diagnosis retain the credential fallback.
	if coded, ok := errors.AsType[*CodedError](err); ok {
		return newError(coded.Code, coded)
	}
	if backend, ok := errors.AsType[*core.CredentialStoreError](err); ok {
		e := newError(CodeAuthStoreUnavailable, backend)
		e.Hint = "Check the selected credential backend, its account and item coordinates, and authorization; or supply explicit environment credentials."
		if backend.Attempted {
			e.Hint = "The credential write was attempted and may have completed. Inspect the selected backend before retrying."
		}
		return e
	}
	if inputErr, ok := errors.AsType[*InputError](err); ok {
		e := newError(inputErr.Code, err)
		e.Field = inputErr.Field
		e.Suggestions = slices.Clone(inputErr.Suggestions)
		return e
	}
	if resolution, ok := errors.AsType[*core.IssueTypeResolutionError](err); ok {
		code := CodeIssueTypeUnknown
		if resolution.Ambiguous {
			code = CodeIssueTypeAmbiguous
		}
		e := newError(code, err)
		e.Field = "type"
		return e
	}
	if preflight, ok := errors.AsType[*core.IssueFieldPreflightError](err); ok {
		e := newError(CodeIssueFieldPreflightFailed, err)
		e.Field = preflight.Field
		return e
	}
	if apiErr, ok := errors.AsType[*adapterjira.APIError](err); ok {
		e := newError(codeForStatus(apiErr.StatusCode), err)
		e.RetryAfterSeconds = apiErr.RetryAfterSeconds
		e.Attempts = apiErr.Retry.Attempts
		e.Waited = apiErr.Retry.Waited
		if len(apiErr.FieldErrors) > 0 {
			// Sorted so the surfaced field is deterministic — map order
			// would make the envelope flap between runs.
			e.Field = slices.Sorted(maps.Keys(apiErr.FieldErrors))[0]
		}
		return e
	}
	if errors.Is(err, adapterjira.ErrAPIResponseTooLarge) || errors.Is(err, adapterjira.ErrAPIResponseIncomplete) {
		return newError(CodeAPIResponseIncomplete, err)
	}
	return newError("validation_failed", err)
}

// ExitCode is the taxonomy → process-exit mapper the composition root
// hands to conductor (concobra.WithExitCode).
func ExitCode(err error) int {
	return MapError(err).Exit
}

// codeForStatus turns a Jira HTTP status into its taxonomy code. Statuses
// gain dedicated codes as commands need them; the catch-alls keep every
// answer classified meanwhile (unmapped 4xx → validation, 5xx → server).
func codeForStatus(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "jira_unauthorized"
	case status == http.StatusForbidden:
		return "jira_forbidden"
	case status == http.StatusNotFound:
		return "jira_not_found"
	case status == http.StatusBadRequest:
		return "jira_bad_request"
	case status == http.StatusPreconditionFailed:
		return CodeJiraPreconditionFailed
	case status == http.StatusTooManyRequests:
		return "jira_rate_limited"
	// 408 is transient (the transport retries it); classifying it as a
	// request problem would tell an agent its input was bad when the
	// network was.
	case status == http.StatusRequestTimeout:
		return "timeout"
	case status >= http.StatusInternalServerError:
		return "jira_server_error"
	default:
		return "jira_request_failed"
	}
}

// HintFor returns a code's static hint, or empty when the code has none.
// Exported so a command's own tests can assert that its failures carry
// advice that fits — the reason two similar failures get two codes.
func HintFor(code string) string { return registry[code].hint }

// Codes returns every taxonomy code, sorted.
//
// The enumeration seam for a guardrail that has to hold something ELSE
// against the whole taxonomy — today, that every code has a rank in the
// keyed-failure severity ladder (command.KeyFailureRanks), so a code
// added here cannot silently sort to the bottom of a mixed keyed run.
// A copy of the keys, never the registry itself: the taxonomy is
// read-only contract surface, and handing out the map would let a caller
// edit the exits and hints every error in the CLI resolves through.
func Codes() []string {
	out := make([]string, 0, len(registry))
	for code := range registry {
		out = append(out, code)
	}
	slices.Sort(out)
	return out
}

// newError joins a code's static registry half with the error's message.
// A code without a registry row is a programmer error; it fails safe to
// the validation exit (never a silent 0), and the registry test keeps the
// case from shipping.
func newError(code string, err error) Classification {
	s, ok := registry[code]
	if !ok {
		return Classification{Code: code, Message: err.Error(), Exit: ExitValidation}
	}
	return Classification{
		Code:    code,
		Message: err.Error(),
		Hint:    s.hint,
		Next:    s.next,
		Doc:     s.doc,
		Exit:    s.exit,
	}
}
