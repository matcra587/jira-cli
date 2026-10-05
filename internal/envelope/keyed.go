package envelope

// KeyedResults is the shared frame every multi-key (`KEY…`) operation
// puts at the top of its data: one ordered entry per key the caller
// named, whatever happened to it.
//
// PLACEMENT, not shape, is what being keyed changes (contract v3.2). A
// single-key command puts its operation object straight at `data`; a
// keyed one puts the SAME object at `data.results[].data`, so an agent
// that learned the shape once reads it in both places and only has to
// know which nesting it asked for. Nothing about the operation's own
// payload changes.
//
// Ordered by the caller's own argv, never by completion: results[i] is
// the i-th key given. A concurrent fan-out (--parallelism) fills the
// slice by index for exactly this reason — the order a caller can
// predict is the order they typed, and any other would make
// `.data.results[0]` mean something different run to run.
//
// The generic parameter is the per-key operation object. It is a real
// type parameter rather than `any` because the contract's no-`any` law
// applies inside results just as it does at data: each keyed operation
// instantiates this with its OWN payload struct, and SchemaOf publishes
// that instantiation.
type KeyedResults[T any] struct {
	// Results is one entry per requested key, in the order requested.
	// Serialized unconditionally: an empty results array is impossible
	// (a keyed command requires at least one key), so a missing one would
	// only ever mean a broken producer.
	Results []KeyedResult[T] `json:"results"`
}

// KeyedResult is one key's outcome inside KeyedResults.
//
// OK is the branch an agent reads first, and it is serialized even when
// true so the branch never depends on a key's absence. Data and Err are
// the two arms of that branch — exactly one is present — and both are
// omitempty because their presence IS the information: a result carrying
// neither would be a producer bug, and one carrying both a contradiction.
//
// A failed key is a PARTIAL RESULT, not a failed command: the other keys
// keep their data, the envelope stays ok, and the failure is reported
// here rather than as a whole-command error. The exception is total
// failure — when NO key succeeded there is no partial result to report,
// so the command fails with the most severe per-key error. The error frame can
// retain these entries: per-key failures still identify their requested keys,
// while the frame's ok=false and nonzero exit classify the whole run.
// That rule is one decision for every keyed
// command, so it lives with the construction path that enforces the rest
// of them (command.KeyedFailure), not in whichever command reached it
// first; this package declares the SHAPE, and deliberately depends on
// nothing above it to do so.
type KeyedResult[T any] struct {
	// Issue is the key this entry answers for, as the same IssueRef
	// object every issue-scoped envelope carries — never a bare string,
	// so `.data.results[].issue.key` reads like `.data.issue.key` does
	// everywhere else. A failed key resolves nothing beyond the key
	// itself, so ID and Self are absent there.
	Issue IssueRef `json:"issue"`
	// OK reports whether this key's operation succeeded.
	OK bool `json:"ok"`
	// Data is the operation's own payload for this key, present only when
	// OK. A pointer so its absence on a failed key is genuine absence
	// rather than a zero-valued payload that would read as a real (if
	// empty) result.
	Data *T `json:"data,omitempty"`
	// Err is why this key failed, present only when OK is false. Named
	// Err rather than Error because a field called Error on a struct
	// reads as the error interface's own method at every call site that
	// touches it.
	Err *KeyError `json:"error,omitempty"`
}

// Verdict exposes the shared failure policy without depending on issue identity.
func (r KeyedResult[T]) Verdict() (bool, *KeyError) { return r.OK, r.Err }

// ResourceResult retains a metadata resource's plan and any observed values
// alongside its failure. Resource names are not issue references. Successful
// rows have data and no error; failed rows retain data and identify the error.
type ResourceResult[T any] struct {
	Resource string    `json:"resource"`
	OK       bool      `json:"ok"`
	Data     *T        `json:"data,omitempty"`
	Err      *KeyError `json:"error,omitempty"`
}

// Verdict lets resource operations use the same total/partial failure policy.
func (r ResourceResult[T]) Verdict() (bool, *KeyError) { return r.OK, r.Err }

// KeyError is one key's failure, carrying the same three things the
// envelope's own errors[] entries do: the stable code an agent branches
// on, what failed, and what to do next.
//
// It is a distinct type from Error rather than a reuse of it. The
// envelope-level Error carries transport and recovery context
// (retry_after_seconds, attempts, suggestions) that belongs to the
// COMMAND's failure; a per-key failure inside a successful command has
// no process exit to report and no retry budget of its own to publish,
// and reusing the wider type would promise fields this position can
// never fill.
type KeyError struct {
	// Code is the stable snake_case taxonomy code (internal/cli's
	// registry) — the same vocabulary errors[].code uses, so a caller
	// branches on one set of codes wherever a failure appears.
	Code string `json:"code"`
	// Message says what failed for this key.
	Message string `json:"message"`
	// Hint says what to do about it. Non-empty for every code the
	// taxonomy knows, exactly as errors[].hint is.
	Hint string `json:"hint"`
	// JiraResponse retains an actual native response from the joined producer,
	// including a successful read followed by a local refusal.
	// It does not change the public error shape or the winning process error.
	JiraResponse bool `json:"-"`
}
