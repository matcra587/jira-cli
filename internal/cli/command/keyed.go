package command

import (
	"fmt"
	"maps"
	"sync"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// Parallelism bounds: a keyed command runs at most MaxParallelism keys
// at once, and DefaultParallelism is what it runs without being asked.
//
// The default is above one because the whole point of `KEY…` is that a
// caller handed over several keys at once and would rather not wait for
// them serially; it is small because the ceiling that matters is Jira's
// rate limiter, not this machine's cores — the work is network wait, so
// tuning to GOMAXPROCS would be measuring the wrong thing entirely.
// MaxParallelism is a hard cap for the same reason: a caller who asks
// for 200 concurrent reads gets throttled, and the CLI refuses rather
// than helping them find that out.
const (
	MinParallelism     = 1
	MaxParallelism     = 16
	DefaultParallelism = 4
)

// FlagParallelism is the canonical spelling, named here so registration
// and the value-range refusal cannot disagree about it.
const FlagParallelism = "parallelism"

// KeyedFlags is the shared option set every `KEY…` command binds. One
// field today; it is a struct rather than a bare int so a later keyed
// flag joins the bundle without changing every caller's signature — the
// same reason ListFlags is a struct.
type KeyedFlags struct {
	// Parallelism is how many keys may be in flight at once. It is
	// meaningful even for a command that opts OUT of registering the
	// flag: WithoutParallelism pins it to 1, so an executor reads this
	// field unconditionally rather than branching on whether the flag
	// exists.
	Parallelism int
}

// KeyedOption customizes AddKeyedFlags. Functional options rather than a
// fixed set for the reason the mutation registrar will need too: the
// keyed commands do not all carry the same flags, and a registrar that
// applied them uniformly would invent surface on the commands that must
// not have it.
type KeyedOption func(*keyedConfig)

type keyedConfig struct {
	// parallelism reports whether --parallelism is registered.
	parallelism        bool
	defaultParallelism int
	shorthand          string
	// reason is why it is not, for the doc-comment-in-code the opt-out
	// exists to force at the call site.
	reason string
}

// WithParallelismDefault preserves a command's established worker default
// while retaining the shared bounds and validation.
func WithParallelismDefault(value int) KeyedOption {
	return func(c *keyedConfig) { c.defaultParallelism = value }
}

// WithParallelismShorthand restores an established short flag through the
// shared registrar rather than registering another parallelism owner.
func WithParallelismShorthand(value string) KeyedOption {
	return func(c *keyedConfig) { c.shorthand = value }
}

// WithoutParallelism suppresses --parallelism on a keyed command whose
// keys MUST execute one at a time, pinning KeyedFlags.Parallelism to 1.
//
// `issue rank` is the case this exists for: a rank moves an issue
// relative to an anchor, so a batch's outcome depends on the order the
// moves land, and 0.15 gives rank no parallelism for exactly that reason
// ("Preview the submission order" is what its own dry-run help says).
// Registering the flag there and ignoring it would be a lie; honoring it
// would be a correctness bug.
//
// An explicit opt-out rather than inferring one from arity or command
// name: every keyed command must state which it is, so the sequential
// case is a decision somebody made and can be read back, not a default
// that happens to fall out of a heuristic. reason is recorded for the
// same purpose — it is required, and an empty one is a wiring error
// AddKeyedFlags refuses.
func WithoutParallelism(reason string) KeyedOption {
	return func(c *keyedConfig) {
		c.parallelism = false
		c.reason = reason
	}
}

// AddKeyedFlags registers the shared `KEY…` surface on cmd and marks it
// keyed.
//
// The marking is the half that is easy to miss and expensive to lose. A
// command declares Keyed in its Spec (which gates the audit's M3 axis
// and the keyed-results contract), and it calls this to get the flags —
// two statements that must agree and had no mechanism keeping them
// together. New now refuses a disagreement in either direction, so a
// command cannot claim keyed results without the keyed surface, nor bind
// --parallelism while telling the audit it returns a single result.
//
// It panics on a malformed opt-out for the same reason SetMeta panics on
// malformed metadata: this is startup wiring, and a half-declared
// contract must never reach a tree.
func AddKeyedFlags(cmd *cobra.Command, f *KeyedFlags, opts ...KeyedOption) {
	cfg := keyedConfig{parallelism: true, defaultParallelism: DefaultParallelism}
	for _, opt := range opts {
		opt(&cfg)
	}
	if !cfg.parallelism && cfg.reason == "" {
		panic(fmt.Sprintf("command: %q opts out of --parallelism without a reason", cmd.Name()))
	}
	if cfg.defaultParallelism < MinParallelism || cfg.defaultParallelism > MaxParallelism {
		panic(fmt.Sprintf("command: %q has an invalid parallelism default %d", cmd.Name(), cfg.defaultParallelism))
	}

	markKeyed(cmd)
	if !cfg.parallelism {
		// Pinned, not left at zero: an executor asks KeyedFlags for the
		// width to run at and must get a usable answer from a command that
		// registered no flag, rather than a zero every caller has to
		// remember to translate.
		f.Parallelism = MinParallelism
		return
	}
	AddIntVar(cmd, &f.Parallelism, FlagParallelism, cfg.defaultParallelism,
		fmt.Sprintf("How many keys to work on at once (%d-%d)", MinParallelism, MaxParallelism),
		FlagOpts{Terse: "Concurrent keys", Placeholder: "n", Shorthand: cfg.shorthand})
}

// Validate refuses a --parallelism outside the supported range.
//
// Registration cannot do this — pflag parses the value long after
// AddKeyedFlags has run — so a keyed command calls this from its own
// Spec.Validate, which runs before any I/O. Refusing beats clamping: a
// caller who asked for 200 has a wrong expectation about what the
// command will do, and silently doing something else leaves them with
// it.
func (f KeyedFlags) Validate() error {
	if f.Parallelism >= MinParallelism && f.Parallelism <= MaxParallelism {
		return nil
	}
	e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
		"--%s must be between %d and %d, got %d",
		FlagParallelism, MinParallelism, MaxParallelism, f.Parallelism,
	))
	e.Field = FlagParallelism
	return e
}

// KeyedFailure reports the error a keyed command should fail with, or
// nil when it should not fail at all. results are the per-key verdicts
// about to be published; raw carries each key's ORIGINAL error, indexed
// alongside them and nil for a key that succeeded.
//
// The rule, and the reason for it. A failed key is a partial result
// while some other key succeeded: the caller got real data and an honest
// account of what they did not get, which is strictly more useful than
// an error that throws the successes away. When NO key succeeded there
// is no partial result to hand back, and an ok envelope carrying nothing
// but failures would be the CLI reporting success for a command that
// achieved nothing — an agent branching on `ok` would read it as done.
// So total failure is a real failure, and the caller gets the ordinary
// typed error frame with a real exit code: the same one they would have
// got had they passed that single bad key on its own.
//
// The ORIGINAL error comes back, never a rebuild from the published
// code/message/hint. Rebuilding would drop everything the taxonomy reads
// off a typed error — a rate limit's Retry-After, the transport's
// attempt count, the field Jira rejected — and the envelope publishes
// all of them.
//
// Which failure represents the run is decided by CLASS (see
// FailureSeverity), not by position: a run that hit both a missing issue
// and an expired credential should exit on the credential, because that
// is the one the caller has to fix first and the one that likely caused
// the rest. Ties go to the earliest key, so the choice is deterministic
// under a concurrent fan-out.
//
// This function classifies the run without discarding its results. A caller
// that returns those typed results alongside the error can opt into the
// construction path's RetainOnError predicate; the root then publishes a
// failure frame containing both results[] and the most severe original error.
func KeyedFailure[T interface {
	Verdict() (bool, *envelope.KeyError)
}](results []T, raw []error) error {
	// Retain every native response before the severity policy selects one
	// original cause or returns early for a successful row. That selected
	// cause cannot represent all joined work for best-effort local observers.
	for i, r := range results {
		_, failure := r.Verdict()
		if failure != nil && i < len(raw) {
			failure.JiraResponse = failure.JiraResponse || cli.HasJiraResponse(raw[i])
		}
	}
	worst := -1
	var worstError *envelope.KeyError
	for i, r := range results {
		ok, failure := r.Verdict()
		switch {
		case ok:
			return nil
		case failure == nil:
			// A failed key with nothing to say is a producer bug, not a
			// state to paper over: it would otherwise rank at the bottom
			// and could win a run of one.
			return fmt.Errorf("command: keyed result %d failed but carries no error", i)
		case worst < 0, FailureSeverity(failure.Code) > FailureSeverity(worstError.Code):
			worst = i
			worstError = failure
		}
	}
	if worst < 0 || worst >= len(raw) || raw[worst] == nil {
		// worst < 0 means no results at all — unreachable from a real
		// command (a keyed one takes at least one key), and nil keeps
		// this total for a caller that built one by hand. A missing raw
		// error is the producer bug above in its other form; the
		// published message still beats returning nil and reporting
		// success.
		if worst < 0 {
			return nil
		}
		return cli.NewCodedError(worstError.Code, worstError.Message)
	}
	return raw[worst]
}

// FailureSeverity ranks one taxonomy code for KeyedFailure above. Higher
// wins.
//
// An explicit ladder rather than the exit code's numeric value: exit
// codes are an enumeration of classes, not a scale (auth is 1 and server
// is 5, which says nothing about which is worse), so comparing them
// would sort by an accident of the table's order.
func FailureSeverity(code string) int { return keyFailureSeverity[code] }

// KeyFailureRanks returns a copy of the ladder. Exported for the
// guardrail that joins it against the whole taxonomy
// (tests/guardrails/keyed_failure_ladder_test.go): an unranked code
// sorts to the bottom, so without that join a code added to the taxonomy
// could quietly stop being able to win a mixed keyed run.
func KeyFailureRanks() map[string]int { return maps.Clone(keyFailureSeverity) }

// keyFailureSeverity ranks EVERY code in the taxonomy, including the
// many that cannot arise per key at all.
//
// Complete on purpose. A switch with a default would rank an unfamiliar
// code silently, and "silently" is the whole problem: the code that most
// needs ranking is the one nobody thought about. Listing all of them
// makes adding a taxonomy row a decision — the guardrail fails until
// somebody states where it sits — and rank 0 IS a statement, not a
// fallback.
//
// The ordering: an interrupted run outranks everything, because nothing
// else that happened is the whole story. Auth comes next, since a bad
// credential makes every other key's verdict suspect. Then the site's
// own troubles (throttling, then server errors), which are transient and
// worth retrying as a unit. Then the per-key facts — a missing issue, a
// rejected value — which are genuinely about the key that carried them.
var keyFailureSeverity = map[string]int{
	// Withheld work must never outrank the failure that stopped its predecessor.
	cli.CodeRankNotAttempted: 0,
	// 5 — the run was cut short. Whatever any single key reported, this
	// is what happened to the command.
	cli.CodeCanceled: 5,
	"timeout":        5,

	// 4 — the credential. Every other key's verdict is suspect.
	"jira_unauthorized":          4,
	"jira_forbidden":             4,
	cli.CodeAuthMissing:          4,
	cli.CodeAuthStoreUnavailable: 4,

	// 3/2 — site or transport failures. Redirect refusal requires inspection
	// before a mutation is retried; the classification's hint preserves that.
	"jira_rate_limited":                  3,
	"jira_server_error":                  2,
	cli.CodeWorklogResponseInvalid:       2,
	cli.CodeAttachmentResponseInvalid:    2,
	cli.CodeCommentResponseInvalid:       2,
	cli.CodeTransitionResponseInvalid:    2,
	cli.CodeTransitionReadInvalid:        2,
	cli.CodeWeblinkResponseInvalid:       2,
	cli.CodeRankResponseInvalid:          2,
	cli.CodeIssueDeleteResponseInvalid:   2,
	cli.CodeIssueCreateResponseInvalid:   2,
	cli.CodeIssueCreateFollowupFailed:    2,
	cli.CodeIssueMetadataResponseInvalid: 2,
	cli.CodeIssueCloneSourceInvalid:      2,
	cli.CodeIssueEditSourceInvalid:       2,
	cli.CodeBulkOperationResponseInvalid: 2,
	cli.CodeBulkOperationFailed:          2,
	cli.CodeBulkOutcomeUnconfirmed:       2,
	cli.CodeRedirectRefused:              2,
	// Metadata refresh can fail collection or publication independently per resource.
	cli.CodeBoardResponseInvalid:  2,
	cli.CodeSearchResponseInvalid: 2,
	cli.CodeCacheIO:               2,
	cli.CodeCacheResponseInvalid:  2,

	// 1 — genuinely about the key that carried it.
	"jira_not_found":                  1,
	"jira_bad_request":                1,
	"jira_request_failed":             1,
	cli.CodeJiraPreconditionFailed:    1,
	cli.CodeTransitionScreenRequired:  1,
	cli.CodeIssueFieldPreflightFailed: 1,
	// Inputs are inspected once, then reopened per key; replacement or
	// permission changes can therefore produce a local refusal per key.
	cli.CodeAttachmentFileInvalid: 1,

	// 0 — resolved before the fan-out starts (flag and argument
	// validation, jq compilation, config and credential resolution) or
	// after it ends (writing the result), so with one exception below
	// none of them can be one key's verdict. Ranked anyway, and ranked
	// LAST, so the ladder's coverage is total and a future code that CAN
	// arise per key has to be moved deliberately rather than inheriting a
	// default.
	//
	// The exception is CodeReadOnlyBlocked, which the first keyed WRITE
	// (issue.comment.add) made reachable per key: read-only is enforced
	// at the transport, so each key's own POST is refused and every key
	// carries this code. It stays at 0 deliberately. Rank only decides
	// which key REPRESENTS a total failure, and a read-only run fails
	// every key identically — so whichever wins carries the same code,
	// message and exit, and the earliest-key tiebreak settles it. Ranking
	// it higher would change nothing here and would wrongly let a
	// read-only refusal outrank a real auth failure in a mixed run.
	cli.CodeOutputWriteFailed:       0,
	cli.CodeAPIResponseIncomplete:   0,
	cli.CodeAttachmentDownloadLimit: 0,
	cli.CodeSelectorIncomplete:      0,
	// Executable updates are local, single-result operations outside fan-out.
	cli.CodeUpdateUnsupported: 0,
	cli.CodeUpdateFailed:      0,
	cli.CodeUpdateOwnership:   0,
	cli.CodeUpdateIntegrity:   0,
	cli.CodeUpdateChanged:     0,
	cli.CodeUpdateUnavailable: 0,
	// Bound description preparation is a single read, outside issue fan-out.
	cli.CodeIssueEditDraftInvalid:           0,
	cli.CodeIssueEditDraftChanged:           0,
	cli.CodeIssueEditDraftIO:                0,
	cli.CodeIssueDescriptionUnavailable:     0,
	cli.CodeIssueDescriptionResponseInvalid: 0,
	cli.CodeIssueDescriptionScopeChanged:    0,
	// User discovery is a single read, outside issue fan-out.
	// Account discovery is resolved before a keyed issue mutation starts.
	cli.CodeUserNotFound:               0,
	cli.CodeUserAmbiguous:              0,
	cli.CodeUserResolutionIncomplete:   0,
	cli.CodeUserSearchResponseInvalid:  0,
	cli.CodeUserLookupResponseInvalid:  0,
	cli.CodeCurrentUserResponseInvalid: 0,
	// Local metadata capability and type resolution precede issue reads.
	cli.CodeIssueMetadataUnavailable: 0,
	cli.CodeIssueTypeUnknown:         0,
	cli.CodeIssueTypeAmbiguous:       0,
	cli.CodeJQLResponseInvalid:       0,
	cli.CodeBoardCacheUnavailable:    0,
	"validation_failed":              0,
	cli.CodeInsecureScheme:           0,
	cli.CodeCommandUnknown:           0,
	cli.CodeFlagUnknown:              0,
	cli.CodeFlagForeign:              0,
	cli.CodeFlagRequired:             0,
	cli.CodeFlagValueMissing:         0,
	cli.CodeFlagValueInvalid:         0,
	cli.CodeADFInvalid:               0,
	cli.CodeADFTypeUnknown:           0,
	cli.CodeADFContentLoss:           0,
	cli.CodeFlagSyntaxInvalid:        0,
	cli.CodeArgCountInvalid:          0,
	cli.CodeArgValueInvalid:          0,
	cli.CodeFlagNotApplicable:        0,
	cli.CodeFlagConflict:             0,
	cli.CodeJQExpressionInvalid:      0,
	cli.CodeJQEvalFailed:             0,
	cli.CodeJQOutputTooLarge:         0,
	cli.CodeJQOutputConflict:         0,
	cli.CodeReadOnlyBlocked:          0,
	cli.CodeLocalIO:                  0,
	cli.CodeConfigChanged:            0,
	cli.CodeAliasInvalid:             0,
	cli.CodeSavedQueryUnknown:        0,
	cli.CodeSavedQueryInvalid:        0,
	cli.CodeEditorMissing:            0,
	cli.CodeEditorFailed:             0,
	cli.CodeTerminalRestoreFailed:    0,
	cli.CodeTerminalRequired:         0,

	cli.CodeDashboardTerminalRequired: 0,

	cli.CodeConfigInvalid:           0,
	cli.CodeConfigMigrationUnmapped: 0,
	cli.CodeConfigMigrationChanged:  0,
	cli.CodeConfigRecoveryInvalid:   0,
	cli.CodeConfigMigrationFailed:   0,
	cli.CodePathInvalid:             0,
}

// keyedCommands records which commands called AddKeyedFlags, keyed by
// *cobra.Command for the reason SetMeta and humanByCommand are: the tree
// owns identity, and this codebase builds many trees in one process.
var (
	keyedMu       sync.RWMutex
	keyedCommands = map[*cobra.Command]bool{}
)

func markKeyed(cmd *cobra.Command) {
	keyedMu.Lock()
	defer keyedMu.Unlock()
	keyedCommands[cmd] = true
}

// declaresKeyedFlags reports whether cmd was built through
// AddKeyedFlags. Unexported: unlike DeclaresHuman, whose guardrail lives
// in another package, the only consumer is New's own cross-check below.
func declaresKeyedFlags(cmd *cobra.Command) bool {
	keyedMu.RLock()
	defer keyedMu.RUnlock()
	return keyedCommands[cmd]
}
