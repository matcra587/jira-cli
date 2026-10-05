package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	xstrings "github.com/gechr/x/strings"
)

// IssueCreateMetadata is one complete, project/type-bound metadata snapshot.
// Native pages retain facts beyond those used for resolution; the field map
// uses actual field IDs and preserves each whole definition verbatim.
type IssueCreateMetadata struct {
	Project     string
	IssueTypeID string
	IssueType   json.RawMessage
	Fields      map[string]json.RawMessage
	TypePages   []json.RawMessage
	FieldPages  []json.RawMessage
}

// IssueTypeResolutionError reports a local unknown or ambiguous type selection
// after complete project-specific discovery. It is not an HTTP 404. Native
// candidates carry the actual identities a caller can use to disambiguate.
type IssueTypeResolutionError struct {
	Requested  string
	Ambiguous  bool
	Candidates []json.RawMessage
}

func (e *IssueTypeResolutionError) Error() string {
	if e.Ambiguous {
		return fmt.Sprintf("issue type %q matches several project issue types; choose an explicit ID", e.Requested)
	}
	return fmt.Sprintf("issue type %q is not available in the project's create metadata", e.Requested)
}

// DiscoverCreateIssueMetadata completely reads project-specific type and field
// pages before returning a snapshot. Exact IDs take precedence; names match
// case-insensitively and must identify one type across every returned page.
// Metadata is an observation, not permission or a guarantee for a later write.
func DiscoverCreateIssueMetadata(ctx context.Context, client IssueMetadataClient, project, requestedType string) (*IssueCreateMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if xstrings.IsBlank(project) || xstrings.IsBlank(requestedType) {
		return nil, fmt.Errorf("project and issue type must be explicit and nonblank")
	}
	types, typePages, err := collectIssueMetadata(ctx, func(ctx context.Context, startAt int) (IssueMetadataRead, error) {
		return client.GetCreateIssueTypes(ctx, project, startAt, issueMetadataPageSize)
	}, DecodeCreateIssueTypes, "id")
	if err != nil {
		return nil, err
	}
	selected, id, err := resolveCreateIssueType(types, requestedType)
	if err != nil {
		return nil, err
	}
	fields, fieldPages, err := collectIssueMetadata(ctx, func(ctx context.Context, startAt int) (IssueMetadataRead, error) {
		return client.GetCreateIssueFields(ctx, project, id, startAt, issueMetadataPageSize)
	}, DecodeCreateIssueFields, "fieldId")
	if err != nil {
		return nil, err
	}
	return &IssueCreateMetadata{Project: project, IssueTypeID: id, IssueType: selected, Fields: fields, TypePages: typePages, FieldPages: fieldPages}, nil
}

const issueMetadataPageSize = 50 // pinned create-metadata endpoint default

func collectIssueMetadata(ctx context.Context, read func(context.Context, int) (IssueMetadataRead, error), decode func([]byte, int, int) (*IssueMetadataPage, error), identity string) (map[string]json.RawMessage, []json.RawMessage, error) {
	entries := make(map[string]json.RawMessage)
	pages := make([]json.RawMessage, 0)
	startAt, total := 0, -1
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		response, err := read(ctx, startAt)
		if err != nil {
			return nil, nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if response.Page == nil {
			return nil, nil, fmt.Errorf("%w: metadata reader returned no page", ErrIssueMetadataInvalid)
		}
		// The native document is the source of truth, including for alternate
		// client implementations. Validate through the same decoder rather
		// than trusting fabricated totals or decoded entries beside it.
		page, err := decode(response.Page.Native, startAt, issueMetadataPageSize)
		if err != nil {
			return nil, nil, err
		}
		if total >= 0 && total != page.Total {
			return nil, nil, fmt.Errorf("%w: metadata total changed during the walk", ErrIssueMetadataInvalid)
		}
		total = page.Total
		for _, entry := range page.Entries {
			// The decoder already established one unique usable identity.
			members, err := metadataObject(entry)
			if err != nil {
				return nil, nil, err
			}
			var id string
			if err := json.Unmarshal(members[identity], &id); err != nil {
				return nil, nil, fmt.Errorf("%w: decoding metadata identity: %w", ErrIssueMetadataInvalid, err)
			}
			if _, duplicate := entries[id]; duplicate {
				return nil, nil, fmt.Errorf("%w: metadata identity repeats across pages", ErrIssueMetadataInvalid)
			}
			entries[id] = entry
		}
		pages = append(pages, page.Native)
		// Advance by actual rows, not maxResults: Jira may return short
		// non-final pages. Decode proved this addition cannot exceed total.
		startAt += len(page.Entries)
		if startAt == total {
			return entries, pages, nil
		}
	}
}

func resolveCreateIssueType(types map[string]json.RawMessage, requested string) (json.RawMessage, string, error) {
	if selected, exists := types[requested]; exists {
		return selected, requested, nil
	}
	candidates := make([]json.RawMessage, 0)
	var selectedID string
	ids := slices.Sorted(maps.Keys(types))
	for _, id := range ids {
		raw := types[id]
		members, err := metadataObject(raw)
		if err != nil {
			return nil, "", err
		}
		var name string
		if value, exists := members["name"]; exists {
			if string(value) == "null" || json.Unmarshal(value, &name) != nil {
				return nil, "", fmt.Errorf("%w: issue type name is not a string", ErrIssueMetadataInvalid)
			}
		}
		if strings.EqualFold(name, requested) {
			candidates = append(candidates, raw)
			selectedID = id
		}
	}
	if len(candidates) != 1 {
		if len(candidates) == 0 {
			for _, id := range ids {
				candidates = append(candidates, types[id])
			}
		}
		return nil, "", &IssueTypeResolutionError{Requested: requested, Ambiguous: selectedID != "", Candidates: candidates}
	}
	return candidates[0], selectedID, nil
}
