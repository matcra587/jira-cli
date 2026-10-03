package envelope

// Ref marks a domain identity object — the value an operation places at
// its data.<noun> slot. The marker lets a reflection guardrail verify
// "the identity slot holds a ref, not a bare scalar" generically while
// each ref type stays honest to its resource (issues key on a compound
// key, boards will key on a numeric id). The method is unexported on
// purpose: ref types are contract surface, so they live in this package.
type Ref interface {
	ref()
}

// IssueRef is the contract's issue identity: every issue-scoped envelope
// carries data.issue as an object with at least key, never a bare
// string, so `.data.issue.key` never changes type between operations.
// ID and Self are omitted when unknown — a dry-run preview knows only
// the key, and their absence means exactly that — while Key is always
// present.
type IssueRef struct {
	ID   string `json:"id,omitempty"`
	Key  string `json:"key"`
	Self string `json:"self,omitempty"`
}

func (IssueRef) ref() {}

// String renders the ref as its key, so human output shows the issue key
// where machine output carries the identity object.
func (r IssueRef) String() string {
	return r.Key
}
