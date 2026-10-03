package envelope

// SelectorState is the shared embed for a write whose targets can be
// named by a SELECTOR (`--jql`, `--filter`, `--from-file`) instead of
// positionally.
//
// It lives INSIDE the operation's data for PageState's reason, which
// applies here with more force: compact prints bare data and `--jq`
// filters exactly that document, so a selector recorded in meta would be
// invisible to the two output modes agent detection actually selects.
// The one fact a caller most needs to audit afterwards — "this run
// deleted issues I never named, and here is the query that chose them"
// — must not be the fact only the human renderer can see.
//
// Absent entirely when the caller named their keys positionally. A
// selector block on every write would make `selector: null` the common
// case and teach a consumer to skip the field.
type SelectorState struct {
	Selector *SelectorSummary `json:"selector,omitempty"`
}

// SelectorSummary is what one selector-driven run publishes about how it
// chose its targets.
//
// **The enumerated keys are deliberately NOT here.** They are already
// `results[]`, one entry per key, and a second copy would be a list that
// could disagree with the list of things actually acted on — the exact
// disagreement this block exists to let a caller audit. The design's
// rule that "the count and the keys are published either way" is
// satisfied by results[] on every path where resolution happened; the
// one path where it did not is Resolved=false, which has no keys to
// publish and says so.
type SelectorSummary struct {
	// Kind is which selector was used: "jql", "filter" or "from-file".
	// Serialized unconditionally so a consumer branches on the value
	// rather than on which of three optional fields is populated.
	Kind string `json:"kind"`
	// Query is the value the caller typed — the JQL, the filter id or
	// name, or the file path.
	Query string `json:"query"`
	// JQL is the query a `--filter` resolved TO, absent for the other two
	// kinds.
	//
	// This is the one genuinely new fact in this block: a caller who
	// passed `--filter 10042` cannot otherwise see what that filter
	// actually asked for, and a saved filter can be edited by anyone it is
	// shared with. Publishing the resolved query is what makes a
	// filter-driven run auditable after the fact, when the filter itself
	// may since have changed.
	JQL string `json:"jql,omitempty"`
	// Resolved reports whether the selector was turned into a key list on
	// this run.
	//
	// False only on a bare `--dry-run` of a selector that needs the site,
	// where results[] is empty because nothing was enumerated. Serialized
	// unconditionally: a consumer reading an empty results[] has to be
	// able to tell "the query matched nothing" (impossible — that is
	// refused) from "the query was never run", and absence would leave
	// them guessing.
	Resolved bool `json:"resolved"`
}
