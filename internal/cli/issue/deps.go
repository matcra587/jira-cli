package issue

import (
	"github.com/matcra587/jira-cli/internal/cli/prompt"
	"github.com/matcra587/jira-cli/internal/core"
)

// Deps is what the issue commands need from the composition root.
//
// A constructor, not a client: nothing opens a connection until a
// handler that needs one calls for it, so help and completion stay
// network-free. This is the first domain whose Deps carries a Jira
// client — which is what makes the "config list cannot open one"
// property load-bearing rather than theoretical.
type Deps struct {
	// BrowseSite resolves the configured site without credentials or a client.
	// A nil seam retains the supplied client's site for embedded callers.
	BrowseSite func() (string, error)

	// JiraClient returns the configured Jira client.
	JiraClient func() (core.JiraClient, error)

	// IssueMetadata returns the authenticated read-only input-discovery seam.
	// Its constructor stays lazy; inspecting help or schema never opens it.
	IssueMetadata func() (InputDiscoveryClient, error)

	// Cache returns the process's metadata cache, constructed on first use
	// like the client above.
	//
	// A constructor rather than a value for JiraClient's reason: resolving
	// the cache directory is filesystem work, and a command that caches
	// nothing — every write in this package — must neither pay for it nor
	// fail because a home directory is unwritable.
	//
	// A command that can work without it MUST. readLinkTypes treats every
	// cache failure as a miss, so a nil field or a failing constructor
	// degrades to the uncached behavior this CLI had before, never to an
	// error (see issueCache).
	Cache func() (core.Cache, error)

	// Confirm asks a yes/no question and reports the answer.
	//
	// Injected for the reason auth.Deps.Confirm is, which its own doc
	// comment states and which applies here unchanged: called directly it
	// reaches the terminal, and a command whose destructive branch can
	// only be reached by a person typing at one has a destructive branch
	// no test can drive. A test supplies the answer; the composition root
	// supplies the dialog.
	//
	// Only `issue comment delete` uses it today. It is on the shared Deps
	// rather than passed to that one constructor because the delete verbs
	// still to land (issue delete, link delete, attachment delete) all
	// need the same thing, and threading it per command would mean four
	// signatures instead of one field.
	Confirm func(prompt.Confirm) (bool, error)
}

// InputDiscoveryClient joins the existing three-method metadata reader with
// one project-identity read needed by create. It exposes no write operation.
type InputDiscoveryClient interface {
	core.IssueMetadataClient
	core.IssueCreateProjectClient
}
