package envelope

// ContractVersion is the published shape version of the machine output
// contract, carried in meta.contract_version of every envelope. It is
// deliberately not the CLI release version: agents pin against it and
// branch when the contract's shape changes, which happens rarely and
// deliberately — a CLI release that changes no shape leaves it alone.
// 0.16's greenfield contract starts the count at "1".
const ContractVersion = "1"

// Envelope is the universal machine frame. json mode emits it whole;
// compact mode emits bare Data on success but keeps the full frame on
// failure, so a failed command is never invisible to an agent reading
// the lean mode (design R2).
//
// Data is `any` here because the frame is the one generic slot every
// operation shares — the contract's "no any" law lives one level down:
// each operation puts a typed Output struct in this slot, and SchemaOf
// publishes that struct's shape. Nothing untyped ever rides in Data.
type Envelope struct {
	OK       bool      `json:"ok"`
	Meta     Meta      `json:"meta"`
	Data     any       `json:"data"`
	Errors   []Error   `json:"errors"`
	Warnings []Warning `json:"warnings"`
}

// Meta is the envelope's invocation-context block.
//
// RequestID is a locally generated correlation id, always present —
// it is injected into Assemble, never read from the environment.
// ExitCode appears only on failure envelopes: its absence means success,
// so the field is a pointer with omitempty rather than a zero-valued
// int that would read as "exit 0" on every failure that forgot to set it.
type Meta struct {
	Command         string `json:"command"`
	ContractVersion string `json:"contract_version"`
	Timestamp       string `json:"timestamp"`
	RequestID       string `json:"request_id"`
	ExitCode        *int   `json:"exit_code,omitempty"`
}

// Error is one structured failure in the errors slice. Agents branch on
// Code (stable snake_case, from the internal/cli taxonomy), never on
// Message. Message says what failed; Hint says what to do next — the two
// never trade places. Next, Doc, and Field are the resolve-not-report
// trio: the action to take, where the contract is written up, and the
// machine-readable thing that broke. They are omitted when empty because
// their absence carries no meaning beyond "nothing recorded" — unlike
// Hint, which the taxonomy guarantees non-empty for every code.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
	Next    string `json:"next,omitempty"`
	Doc     string `json:"doc,omitempty"`
	Field   string `json:"field,omitempty"`
	// Suggestions carries "did you mean" candidates for input typos —
	// command names, flag spellings, foreign-CLI flag equivalents — so an
	// agent recovering from a mistake can branch on values instead of
	// re-deriving them from the message.
	Suggestions []string `json:"suggestions,omitempty"`
	// RetryAfterSeconds carries the server's Retry-After when it sent one:
	// a rate-limit hint that says "wait out the reported pause" owes the
	// machine reader the number, not just the prose.
	RetryAfterSeconds int `json:"retry_after_seconds,omitempty"`
	// Attempts and WaitedMS report what the transport already did before
	// giving up — backoff is hidden wall-clock, so an agent deciding
	// whether to retry needs to know how much has been spent.
	Attempts int `json:"attempts,omitempty"`
	WaitedMS int `json:"waited_ms,omitempty"`
}

// Warning is a non-fatal best-effort diagnostic emitted alongside data.
// Lossy is serialized even when false so consumers branch on it without
// presence checks — a warning that dropped data must never be
// indistinguishable from one that did not.
type Warning struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Lossy   bool   `json:"lossy"`
}

// Identity is the shared embed for issue-scoped operations: the one
// fragment that genuinely repeats verbatim across their Output structs.
// It places the issue's ref at data.issue, so `.data.issue.key` reads
// identically across every issue-scoped operation.
type Identity struct {
	Issue IssueRef `json:"issue"`
}

// MutationState is the shared embed for mutations. DryRun is emitted on
// both paths — true on preview, false on the live write — so an agent
// never has to infer from a missing field whether anything happened.
type MutationState struct {
	DryRun bool `json:"dry_run"`
}

// PageState is the shared embed for a paginated read. It lives INSIDE
// the operation's data, never in meta, and that placement is the whole
// point rather than a stylistic preference.
//
// Two shipped decisions depend on data being self-sufficient: compact
// prints bare data on success, and --jq filters exactly the document the
// chosen mode would print. Put this block in meta and a compact caller —
// which is what agent detection selects — cannot see is_last or
// next_cursor at all, and a --jq caller cannot reach them either, BY
// CONSTRUCTION rather than by oversight. The keyed commands already nest
// their pagination inside results[].data, so this keeps the single-target
// path agreeing with them instead of diverging.
//
// The failure it prevents is the quiet kind: a caller running
// `--jq '.issues | length'` against a truncated drain gets a plausible
// number with no signal anywhere in its input that more exists.
type PageState struct {
	// IsLast reports whether this is the final page. Serialized even when
	// false, so a consumer branches on the value rather than on absence.
	IsLast bool `json:"is_last"`
	// NextCursor is the token that fetches the following page, absent on
	// the last one.
	NextCursor string `json:"next_cursor,omitempty"`
	// Truncated reports that a multi-page walk stopped at a cap rather
	// than at the end of the results. A consumer that ignores this is
	// counting a subset while believing it counted everything, so it is
	// serialized unconditionally alongside the reason.
	Truncated bool `json:"truncated"`
	// TruncatedReason names the cap that stopped the walk (for example
	// max_pages or max_results); empty when Truncated is false.
	TruncatedReason string `json:"truncated_reason,omitempty"`
}

// WarningPaginationTruncated is the warning type emitted alongside a
// truncated PageState. The warning is the HUMAN-facing half; it rides
// outside data, so a compact consumer never sees it — which is exactly
// why the machine-facing half has to live in PageState.
const WarningPaginationTruncated = "pagination_truncated"

// WarningBrowserOpenFailed is the warning type `--web` emits when it
// resolved a URL but could not launch a browser for it (see
// internal/cli/issue/web.go's openForHuman) — a missing or failing
// opener, never Jira. The URL itself still rides data.web_url on the
// same envelope this warning travels with, so this warns about the SIDE
// EFFECT failing, not the operation: the caller asked to see something,
// and can still see its URL whether or not a browser actually opened.
const WarningBrowserOpenFailed = "browser_open_failed"

// WebLink is the shared embed for an operation `--web` can target: the
// URL this invocation resolved for what it is looking at — an issue's
// browse link, or a search's issue-navigator link. Populated only when
// `--web` was given; omitted (never an empty string) otherwise, the same
// "absence, not a zero value" contract IssueListOutput.Count already
// gives `--count` — a machine reader must never mistake "nobody asked
// for this" for "this genuinely has no URL".
type WebLink struct {
	WebURL string `json:"web_url,omitempty"`
}
