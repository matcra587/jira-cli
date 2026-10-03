package cache

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	xstrings "github.com/gechr/x/strings"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

const (
	metadataPageSize = 100
	metadataMaxPages = 100
	metadataMaxItems = 10000
)

// MetadataResource describes a primer's storage identity and freshness policy.
// The catalog does not advertise a CLI command until that producer is wired.
type MetadataResource struct {
	Name       string
	DefaultTTL time.Duration
	endpoint   string
	paged      bool
}

// MetadataResources preserves the baseline TTL ladder for restored primers.
// Existing board/link consumers retain their established one-day read policy;
// primer TTLs decide whether a primer needs to fetch, not another reader's TTL.
func MetadataResources() []MetadataResource {
	return []MetadataResource{
		{Name: "labels", DefaultTTL: time.Hour, endpoint: "/rest/api/3/label", paged: true},
		{Name: "projects", DefaultTTL: 7 * 24 * time.Hour, endpoint: "/rest/api/3/project/search", paged: true},
		{Name: "epics", DefaultTTL: 4 * time.Hour},
		{Name: "fields", DefaultTTL: 14 * 24 * time.Hour, endpoint: "/rest/api/3/field"},
		{Name: "issuetypes", DefaultTTL: 30 * 24 * time.Hour, endpoint: "/rest/api/3/issuetype"},
		{Name: "linktypes", DefaultTTL: 90 * 24 * time.Hour, endpoint: "/rest/api/3/issueLinkType"},
		{Name: "boards", DefaultTTL: 28 * 24 * time.Hour},
		{Name: "statuses", DefaultTTL: 30 * 24 * time.Hour, endpoint: "/rest/api/3/status"},
		{Name: "priorities", DefaultTTL: 90 * 24 * time.Hour, endpoint: "/rest/api/3/priority/search", paged: true},
		{Name: "resolutions", DefaultTTL: 90 * 24 * time.Hour, endpoint: "/rest/api/3/resolution/search", paged: true},
	}
}

// Key reuses existing consumer keys for boards and link types. New resources
// store versioned snapshots, so incomplete walks cannot masquerade as arrays.
func (r MetadataResource) Key(site string) string {
	switch r.Name {
	case "boards":
		return BoardListKey
	case "linktypes":
		return LinkTypesPrefix + site
	default:
		return "v1:metadata:" + r.Name
	}
}

// MetadataValue retains native values and the walk's completion facts. Payload
// is the board owner's native cache snapshot when fetching boards; other kinds
// carry a non-nil Items array. A partial value must not be stored after an error.
type MetadataValue = envelope.CacheMetadata

// MetadataReader uses the existing authenticated transport and enhanced search.
// Capture bounds are explicit; no metadata request follows a response URL.
type MetadataReader interface {
	command.IssueSearcher
	DoAPI(context.Context, core.APIRequest) (*core.APIResponse, error)
}

// BoardMetadataFetcher delegates board collection and enrichment to its owner.
// Composition supplies this callback to avoid a cache-to-boards import cycle.
type BoardMetadataFetcher func(context.Context, bool) (MetadataValue, error)

// FetchMetadata collects one resource without changing local storage. The
// caller owns animation, cache policy and persistence. Native members survive
// validation; later page errors retain only fully validated earlier pages.
func FetchMetadata(ctx context.Context, reader MetadataReader, boards BoardMetadataFetcher, name string, unbounded bool) (MetadataValue, error) {
	out := MetadataValue{Items: []json.RawMessage{}}
	var resource MetadataResource
	for _, candidate := range MetadataResources() {
		if candidate.Name == name {
			resource = candidate
			break
		}
	}
	if resource.Name == "" {
		return out, cli.NewInputError(cli.CodeArgValueInvalid, "unknown cache resource")
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if name == "boards" {
		if boards == nil {
			return out, metadataInvalid("board metadata collector is unavailable")
		}
		return boards(ctx, unbounded)
	}
	if reader == nil {
		return out, metadataInvalid("metadata reader is unavailable")
	}
	if name == "epics" {
		read, err := command.WalkIssues(ctx, reader, "issuetype = Epic", []string{"summary", "status"}, "", command.SearchWalk{All: true, Unbounded: unbounded})
		for _, issue := range read.Issues {
			raw, marshalErr := jira.MarshalNoEscape(issue)
			if marshalErr != nil {
				err = errors.Join(err, fmt.Errorf("encode epic metadata: %w", marshalErr))
				break
			}
			out.Items = append(out.Items, raw)
		}
		err = errors.Join(err, ctx.Err())
		out.Count, out.Pages = len(out.Items), len(read.SearchPages)
		out.Complete = err == nil && read.Pagination.IsLast && !read.Truncated
		out.TruncatedReason, out.NextPageToken = read.TruncatedReason, read.Pagination.NextPageToken
		return out, err
	}
	if resource.paged {
		return fetchMetadataPages(ctx, reader, resource, unbounded)
	}
	raw, err := readMetadataBody(ctx, reader, resource.endpoint, nil)
	if err != nil {
		return out, err
	}
	if name == "linktypes" {
		var response struct {
			Types json.RawMessage `json:"issueLinkTypes"`
		}
		if err := jsonv2.Unmarshal(raw, &response); err != nil {
			return out, metadataInvalid("link type response is not an object")
		}
		raw = response.Types
	}
	items, err := decodeMetadataItems(raw, name)
	if err != nil {
		return out, err
	}
	out.Items, out.Count, out.Pages, out.Complete = items, len(items), 1, true
	return out, ctx.Err()
}

func readMetadataBody(ctx context.Context, reader MetadataReader, endpoint string, query url.Values) ([]byte, error) {
	response, err := reader.DoAPI(ctx, core.APIRequest{
		Method: http.MethodGet, Endpoint: endpoint, Query: query,
		MaxResponseBytes: core.DefaultMaxAPIResponseBytes,
	})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if response == nil || response.StatusCode != http.StatusOK || response.Truncated {
		return nil, metadataInvalid("metadata requires a complete HTTP 200 response")
	}
	return response.Body, nil
}

type metadataPage struct {
	StartAt    *int            `json:"startAt"`
	MaxResults *int            `json:"maxResults"`
	Total      *int            `json:"total"`
	IsLast     *bool           `json:"isLast"`
	Values     json.RawMessage `json:"values"`
}

func fetchMetadataPages(ctx context.Context, reader MetadataReader, resource MetadataResource, unbounded bool) (MetadataValue, error) {
	out := MetadataValue{Items: []json.RawMessage{}}
	seen := map[string]bool{}
	start := 0
	for {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		size := metadataPageSize
		if !unbounded {
			size = min(size, metadataMaxItems-len(out.Items))
		}
		raw, err := readMetadataBody(ctx, reader, resource.endpoint, url.Values{
			"startAt": {strconv.Itoa(start)}, "maxResults": {strconv.Itoa(size)},
		})
		if err != nil {
			return out, err
		}
		var page metadataPage
		if err := jsonv2.Unmarshal(raw, &page); err != nil {
			return out, metadataInvalid("metadata response is not a page object")
		}
		items, err := decodeMetadataItems(page.Values, resource.Name)
		if err != nil {
			return out, err
		}
		next, last, err := validateMetadataPage(page, start, size, len(items))
		if err != nil {
			return out, err
		}
		// Check the whole page before retaining any of its values.
		for _, item := range items {
			identity := metadataIdentity(item, resource.Name)
			if seen[identity] {
				return out, metadataInvalid("metadata walk repeats an identity")
			}
			seen[identity] = true
		}
		out.Items = append(out.Items, items...)
		out.Count, out.Pages = len(out.Items), out.Pages+1
		if last {
			out.Complete = true
			out.NextStart = nil
			return out, ctx.Err()
		}
		out.NextStart = &next
		if !unbounded && (out.Pages >= metadataMaxPages || out.Count >= metadataMaxItems) {
			out.TruncatedReason = "max_pages"
			if out.Count >= metadataMaxItems {
				out.TruncatedReason = "max_results"
			}
			return out, nil
		}
		start = next
	}
}

func validateMetadataPage(page metadataPage, start, requested, count int) (int, bool, error) {
	if page.StartAt == nil || *page.StartAt != start || page.MaxResults == nil || *page.MaxResults < 1 {
		return 0, false, metadataInvalid("metadata page has missing or inconsistent offsets")
	}
	if count > requested || count > *page.MaxResults || start > math.MaxInt-count {
		return 0, false, metadataInvalid("metadata page exceeds its bounds")
	}
	next := start + count
	if page.Total == nil && page.IsLast == nil {
		return 0, false, metadataInvalid("metadata page has no completion fact")
	}
	last := page.IsLast != nil && *page.IsLast
	if page.Total != nil {
		if *page.Total < next || (page.IsLast != nil && last != (next == *page.Total)) {
			return 0, false, metadataInvalid("metadata page has inconsistent total or final-page facts")
		}
		last = next == *page.Total
	}
	if !last && count == 0 {
		return 0, false, metadataInvalid("metadata page makes no progress")
	}
	return next, last, nil
}

func decodeMetadataItems(raw []byte, name string) ([]json.RawMessage, error) {
	var items []json.RawMessage
	// Native page/identity fields match exact JSON names. The selected standard
	// decoder also rejects duplicate names, including within preserved values.
	if err := jsonv2.Unmarshal(raw, &items); err != nil || items == nil {
		return nil, metadataInvalid("metadata values require an explicit array")
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		identity := metadataIdentity(item, name)
		if identity == "" || seen[identity] {
			return nil, metadataInvalid("metadata array has a missing, malformed or repeated identity")
		}
		seen[identity] = true
	}
	return items, nil
}

func metadataIdentity(raw []byte, name string) string {
	if name == "labels" {
		var label string
		if jsonv2.Unmarshal(raw, &label) != nil || xstrings.IsBlank(label) {
			return ""
		}
		return label
	}
	var row struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Key     string `json:"key"`
		Inward  string `json:"inward"`
		Outward string `json:"outward"`
	}
	if jsonv2.Unmarshal(raw, &row) != nil || xstrings.AnyEmpty(strings.TrimSpace(row.ID), strings.TrimSpace(row.Name)) {
		return ""
	}
	if name == "projects" && xstrings.IsBlank(row.Key) {
		return ""
	}
	if name == "linktypes" && (xstrings.IsBlank(row.Inward) || xstrings.IsBlank(row.Outward)) {
		return ""
	}
	return row.ID
}

func metadataInvalid(message string) error {
	return cli.NewCodedError(cli.CodeCacheResponseInvalid, message)
}
