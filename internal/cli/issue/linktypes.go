package issue

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	metacache "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// DefaultLinkTypeTTL is the link types' own name for the shared
// configuration lifetime.
//
// Kept as its own exported constant rather than replaced by
// DefaultConfigTTL at the call sites: it is referenced by this command's
// help text and by tests as "how long a link-type list lives", and a
// reader chasing that number should land on a name that says what it is
// about. It is an alias, not a second policy — if the two ever need to
// differ, that is a decision to make here rather than a drift to
// discover.
const DefaultLinkTypeTTL = DefaultConfigTTL

// linkTypeCacheKey is where one site's link types are stored.
//
// **Keyed by SITE, and that is a correctness requirement rather than
// tidiness.** Link types are per-tenant configuration, so a key that
// omitted the site would serve one tenant's types to another the moment a
// caller switched profiles — and the failure would look like Jira having
// forgotten a type rather than like a cache bug.
//
// The resource key remains site-specific; the composition root additionally
// scopes storage by configured account for permission-sensitive consumers.
// All metadata shares that namespace rather than maintaining separate caches.
//
// The version prefix is what lets the STORED SHAPE change without a
// mysterious decode failure: bump it and every old entry becomes a miss
// rather than a corrupt read.
func linkTypeCacheKey(site string) string {
	return metacache.LinkTypesPrefix + site
}

// addLinkTypeCacheFlags registers the two flags that govern the cached
// type list, on every command that reads it.
//
// Both commands get both, deliberately. `link types` needs them because
// it is the command a caller runs to SEE the list, and `link create` needs
// them because it is the command that REFUSES on a stale one — a caller
// told their type does not exist must be able to bust the cache from where
// they are standing rather than being sent to a sibling command first.
func addLinkTypeCacheFlags(cmd *cobra.Command, opts *linkTypeOptions) {
	addCacheFlags(cmd, &opts.cacheOptions, "link-type list")
}

// linkTypeRead is one resolution of the site's link types, with enough
// provenance for a caller to say where the answer came from.
type linkTypeRead struct {
	types []jira.IssueLinkType
	// cached reports that this answer came off disk rather than the wire.
	cached bool
	// age is how long ago the answer was written; zero on a fresh read.
	age time.Duration
}

// linkTypeOptions is how a caller asks for the list. The cache half is
// the shared one, embedded rather than restated so this command cannot
// acquire its own idea of what --ttl-minutes means.
type linkTypeOptions struct {
	cacheOptions
}

// readLinkTypes returns the site's link types, from cache when one is
// fresh enough and from Jira otherwise.
//
// The whole cache policy — miss, expiry, corruption, clock skew, write
// failure — is readCached's, shared with the board resolver. What stays
// here is the part that is genuinely about link types: the cache key,
// and what makes a cached list worth serving.
func readLinkTypes(
	ctx context.Context,
	client core.JiraClient,
	cache core.Cache,
	site string,
	opts linkTypeOptions,
) (linkTypeRead, error) {
	read, err := readCached(ctx, cache, linkTypeCacheKey(site), opts.cacheOptions,
		// An empty list is indistinguishable from a bad decode for the
		// caller's purposes, and serving one would report every type the
		// caller names as unknown. The site has at least one link type or
		// the feature is off; either way, re-read.
		func(types []jira.IssueLinkType) bool { return len(types) > 0 },
		func(ctx context.Context) ([]jira.IssueLinkType, error) {
			return fetchLinkTypes(ctx, client)
		})
	if err != nil {
		return linkTypeRead{}, err
	}
	return linkTypeRead{types: read.value, cached: read.cached, age: read.age}, nil
}

// fetchLinkTypes reads the list from Jira.
func fetchLinkTypes(ctx context.Context, client core.JiraClient) ([]jira.IssueLinkType, error) {
	read, err := command.Spin(ctx, "Reading the link types", func(ctx context.Context) (core.LinkTypeRead, error) {
		return client.GetIssueLinkTypes(ctx)
	})
	if err != nil {
		return nil, err
	}
	if read.Types == nil {
		// GetIssueLinkTypes' contract is a non-nil Types whenever it
		// returns a nil error. Failing loudly beats trusting it: an empty
		// list would report every type the caller names as unknown — and
		// would then be CACHED, turning one bad response into a day of
		// them.
		return nil, fmt.Errorf("issue.link.types: empty link type list after a successful read")
	}
	return val(read.Types.IssueLinkTypes), nil
}

// linkTypeViews narrows the wire types to the CLI's own contract shape.
func linkTypeViews(types []jira.IssueLinkType) []envelope.IssueLinkTypeView {
	out := make([]envelope.IssueLinkTypeView, 0, len(types))
	for _, t := range types {
		out = append(out, envelope.IssueLinkTypeView{
			ID:      t.ID.Value,
			Name:    t.Name.Value,
			Inward:  t.Inward.Value,
			Outward: t.Outward.Value,
		})
	}
	return out
}
