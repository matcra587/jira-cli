package jira

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	xstrings "github.com/gechr/x/strings"
	"github.com/matcra587/jira-cli/internal/adf"
)

// CommentService groups the Jira-issue comment endpoints. Split out of
// IssueService per plan.md "Service Architecture" — comments deserve a
// service of their own now that the lifecycle covers list / add / edit /
// delete instead of just add.
//
// All methods use the underlying *Client's auth, debug, retry, and
// rate-limit hooks. Pagination on List follows the same ListOptions shape
// the rest of pkg/jira already exposes.
type CommentService interface {
	List(ctx context.Context, key string, opts *ListCommentsOptions) ([]*Comment, *Response, error)
	ListAll(ctx context.Context, key string, opts CommentDrainOptions) (CommentDrainResult, error)
	Add(ctx context.Context, key string, body *CommentBody) (*Comment, *Response, error)
	AddWithVisibility(ctx context.Context, key string, body *CommentBody, vis VisibilityChange) (*Comment, *Response, error)
	Edit(ctx context.Context, key, commentID string, body *CommentBody, vis VisibilityChange) (*Comment, *Response, error)
	Delete(ctx context.Context, key, commentID string) (*Response, error)
}

// CommentDrainOptions configures CommentService.ListAll. Mirrors
// DrainOptions on the search side so consumer-side opt-in walks have
// consistent runaway protection. Defaults: PageSize 50, MaxPages 100,
// MaxResults 10_000. Set Unbounded to disable both bounds (the CLI
// requires --unbounded; the TUI never sets it).
type CommentDrainOptions struct {
	PageSize   int
	MaxPages   int
	MaxResults int
	Unbounded  bool
}

// CommentDrainResult carries the outcome of ListAll: every comment
// fetched, the final page's response (for envelope pagination metadata),
// and the rate-limit APIError that aborted the walk if one fired
// (partial-success semantics — exit 0 with a structured warning).
// Truncated reports whether one of the configured bounds stopped the
// walk short of isLast.
type CommentDrainResult struct {
	Comments        []*Comment
	LastResp        *Response
	PagesFetched    int
	RateLimitHit    *APIError
	Truncated       bool
	TruncatedReason string
}

type commentService struct {
	client *Client
}

// NewCommentService constructs a CommentService bound to the given client.
func NewCommentService(client *Client) CommentService {
	return &commentService{client: client}
}

// ListCommentsOptions controls pagination and ordering for comment list.
// Atlassian's `/issue/{key}/comment` endpoint returns oldest-first by
// default when `orderBy=created` is supplied; we send it explicitly so
// the contract is stable across instances.
type ListCommentsOptions struct {
	ListOptions
	// OrderBy lets callers override the default ordering. Empty string =
	// "created" (oldest-first), matching Atlassian's native default.
	OrderBy string
}

// CommentBody carries the comment payload. Exactly one of ADF or Markdown
// must be supplied; the cmd-layer binding converts Markdown → ADF before
// constructing the body.
type CommentBody struct {
	ADF *adf.Document
	// DryRun short-circuits the HTTP call. The service returns a synthetic
	// {ID: "DRY-RUN"} Comment so callers can keep their envelope flow.
	DryRun bool
}

// VisibilityMode is the discriminator on a VisibilityChange.
type VisibilityMode int

const (
	// VisibilityKeep leaves the existing visibility untouched. The wire
	// body MUST omit the visibility key entirely so Atlassian preserves
	// whatever was previously set.
	VisibilityKeep VisibilityMode = iota
	// VisibilityReplace sets a new restriction (role or group). The wire
	// body sends `"visibility": {"type": ..., "value": ...}`.
	VisibilityReplace
	// VisibilityClear removes the existing restriction. The wire body
	// sends `"visibility": null` explicitly so Atlassian clears it.
	VisibilityClear
)

// VisibilityChange is the typed, validated input to CommentService.Edit.
// Construct via ParseVisibilityChange so the mutex-flag rules are enforced
// before the wire body is built.
type VisibilityChange struct {
	Mode  VisibilityMode
	Type  string // "role" or "group"; only meaningful when Mode == VisibilityReplace
	Value string // role/group name; only meaningful when Mode == VisibilityReplace
}

// VisibilityFlags is the cmd-layer's view of the three CLI flags. The
// *Set fields distinguish "flag was supplied" from "flag was supplied
// with empty value" — cobra's IsChanged() drives the booleans.
type VisibilityFlags struct {
	RoleSet  bool
	Role     string
	GroupSet bool
	Group    string
	Clear    bool
}

// ParseVisibilityChange validates the flag combination and produces a
// VisibilityChange. Returns an error (mapped to exit 3 by the cmd
// layer) when:
//   - --visibility-role and --visibility-group are both supplied
//   - --clear-visibility is combined with either visibility-role or
//     visibility-group
//   - --visibility-role is supplied with an empty value
//   - --visibility-group is supplied with an empty value
//
// All three flags omitted → VisibilityKeep (preserve-when-omitted).
func ParseVisibilityChange(flags VisibilityFlags) (VisibilityChange, error) {
	supplied := 0
	if flags.RoleSet {
		supplied++
	}
	if flags.GroupSet {
		supplied++
	}
	if flags.Clear {
		supplied++
	}
	if supplied > 1 {
		// Build a precise error so the cmd layer can route to exit 3 and
		// the user can see exactly which flags collided.
		var which []string
		if flags.RoleSet {
			which = append(which, "--visibility-role")
		}
		if flags.GroupSet {
			which = append(which, "--visibility-group")
		}
		if flags.Clear {
			which = append(which, "--clear-visibility")
		}
		return VisibilityChange{}, fmt.Errorf("validation: %s are mutually exclusive", strings.Join(which, " / "))
	}
	switch {
	case flags.RoleSet:
		if xstrings.IsBlank(flags.Role) {
			return VisibilityChange{}, errors.New("validation: --visibility-role requires a non-empty role name")
		}
		return VisibilityChange{Mode: VisibilityReplace, Type: "role", Value: flags.Role}, nil
	case flags.GroupSet:
		if xstrings.IsBlank(flags.Group) {
			return VisibilityChange{}, errors.New("validation: --visibility-group requires a non-empty group name")
		}
		return VisibilityChange{Mode: VisibilityReplace, Type: "group", Value: flags.Group}, nil
	case flags.Clear:
		return VisibilityChange{Mode: VisibilityClear}, nil
	default:
		return VisibilityChange{Mode: VisibilityKeep}, nil
	}
}

// LossyCommentWarning is the structured warning shape emitted under
// envelope.warnings[] when a comment's ADF body lost fidelity during the
// Markdown render path. CommentID lets the consumer cross-reference the
// affected comment in data.comments[]; LossyConstructs lists the dropped
// node/mark types in sorted-unique order.
type LossyCommentWarning struct {
	Type            string   `json:"type"`
	CommentID       string   `json:"comment_id"`
	LossyConstructs []string `json:"lossy_constructs"`
}

// CollectLossyCommentWarnings walks comments[] and builds one warning per
// comment whose ADF body included nodes/marks the Markdown renderer
// couldn't fully express. Comments without a body are skipped silently
// — they're not lossy, just empty.
func CollectLossyCommentWarnings(comments []*Comment) []LossyCommentWarning {
	if len(comments) == 0 {
		return nil
	}
	var out []LossyCommentWarning
	for _, c := range comments {
		if c == nil || c.Body == nil {
			continue
		}
		res := adf.ToMarkdownLossy(*c.Body)
		if len(res.LossyConstructs) == 0 {
			continue
		}
		id := ""
		if c.ID != nil {
			id = *c.ID
		}
		out = append(out, LossyCommentWarning{
			Type:            "adf-lossy-comment",
			CommentID:       id,
			LossyConstructs: res.LossyConstructs,
		})
	}
	return out
}

// validateCommentBody enforces that the request body must carry a
// non-empty ADF document. Empty or nil docs would round-trip as a
// Jira 400 — local validation gives a faster, clearer signal.
func validateCommentBody(body *CommentBody) error {
	if body == nil || body.ADF == nil {
		return errors.New("comment body is required")
	}
	if len(body.ADF.Content) == 0 {
		return errors.New("comment body is required: ADF document has no content")
	}
	return nil
}

// addPayload builds the POST/PUT body for create+update endpoints.
// vis controls how visibility is serialized:
//   - VisibilityKeep  → omit the `visibility` key (preserve)
//   - VisibilityReplace → `{"type":...,"value":...}`
//   - VisibilityClear → JSON `null`
//
// The result is `map[string]any` (not a struct) because we need an explicit
// `null` for Clear; encoding/json drops nil pointers as if absent unless we
// use `*Visibility` plus omitempty tricks. Map values are clearer.
func commentPayload(body *CommentBody, vis VisibilityChange) map[string]any {
	payload := map[string]any{"body": body.ADF}
	switch vis.Mode {
	case VisibilityReplace:
		payload["visibility"] = map[string]string{"type": vis.Type, "value": vis.Value}
	case VisibilityClear:
		payload["visibility"] = nil
	case VisibilityKeep:
		// no-op: omit the key
	}
	return payload
}

// List paginates `/rest/api/3/issue/{key}/comment`. opts.MaxResults caps the
// page size; opts.StartAt selects the page. Response.IsLast / Response.Total
// / Response.MaxResults / Response.StartAt mirror Atlassian's native
// pagination keys for downstream consumers (matching SearchService's shape).
func (s *commentService) List(ctx context.Context, key string, opts *ListCommentsOptions) ([]*Comment, *Response, error) {
	q := url.Values{}
	orderBy := "created"
	if opts != nil {
		if opts.OrderBy != "" {
			orderBy = opts.OrderBy
		}
		if opts.MaxResults > 0 {
			q.Set("maxResults", strconv.Itoa(opts.MaxResults))
		}
		if opts.StartAt > 0 {
			q.Set("startAt", strconv.Itoa(opts.StartAt))
		}
	}
	q.Set("orderBy", orderBy)
	path := withQuery(RESTPath("issue", key, "comment"), q)
	req, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, nil, err
	}
	var page struct {
		Comments   []*Comment `json:"comments"`
		StartAt    int        `json:"startAt"`
		MaxResults int        `json:"maxResults"`
		Total      int        `json:"total"`
		IsLast     bool       `json:"isLast"`
	}
	resp, err := s.client.Do(req, &page)
	if resp != nil {
		resp.StartAt = page.StartAt
		resp.MaxResults = page.MaxResults
		resp.Total = page.Total
		resp.TotalKnown = true
		// Real tenants omit isLast on this endpoint, decoding to a false
		// that reads as "more pages" even when startAt+returned covers the
		// whole set. Compute the boundary instead of trusting the wire.
		resp.IsLast = page.IsLast || page.StartAt+len(page.Comments) >= page.Total
	}
	return page.Comments, resp, err
}

// ListAll paginates the entire comment thread for an issue. Cursor
// management lives here so command code never sees raw
// startAt/nextPageToken. A rate-limit (HTTP 429) mid-walk is treated as
// partial success — already-fetched comments come back along with the
// triggering APIError captured in result.RateLimitHit; the caller maps
// that to an envelope warning and exit 0. Any other error short-circuits
// and is returned directly. The configured bounds (max pages / max
// results) guard against runaway walks; opts.Unbounded disables them.
func (s *commentService) ListAll(ctx context.Context, key string, opts CommentDrainOptions) (CommentDrainResult, error) {
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	maxPages := opts.MaxPages
	if maxPages <= 0 {
		maxPages = defaultMaxPages
	}
	maxResults := opts.MaxResults
	if maxResults <= 0 {
		maxResults = defaultMaxResults
	}
	var (
		out    CommentDrainResult
		offset int
	)
	for {
		comments, resp, err := s.List(ctx, key, &ListCommentsOptions{
			StartAt: offset, MaxResults: pageSize,
		})
		if err != nil {
			var apiErr *APIError
			if out.PagesFetched > 0 && errors.As(err, &apiErr) && apiErr.Type == ErrorTypeRateLimit {
				out.RateLimitHit = apiErr
				return out, nil
			}
			return out, err
		}
		out.PagesFetched++
		out.LastResp = resp
		out.Comments = append(out.Comments, comments...)
		serverDone := resp == nil || resp.IsLast || len(comments) == 0
		if !opts.Unbounded {
			if out.PagesFetched >= maxPages && !serverDone {
				out.Truncated = true
				out.TruncatedReason = "max_pages"
				return out, nil
			}
			if len(out.Comments) > maxResults || (len(out.Comments) == maxResults && !serverDone) {
				out.Truncated = true
				out.TruncatedReason = "max_results"
				if len(out.Comments) > maxResults {
					out.Comments = out.Comments[:maxResults]
				}
				return out, nil
			}
		}
		if serverDone {
			return out, nil
		}
		// Advance the offset by the page we received. Tracking our own
		// offset (rather than reading resp.StartAt back from the wire)
		// keeps the loop deterministic against quirky servers and
		// confines cursor mechanics to pkg/jira.
		offset += len(comments)
	}
}

// Add creates a new comment via POST `/issue/{key}/comment`. body.ADF must
// be non-nil and non-empty. vis follows Replace/Clear semantics; on Add,
// Keep is functionally equivalent to omitting the flag entirely (a
// brand-new comment has no prior visibility to preserve).
func (s *commentService) Add(ctx context.Context, key string, body *CommentBody) (*Comment, *Response, error) {
	if err := validateCommentBody(body); err != nil {
		return nil, nil, err
	}
	if body.DryRun {
		return &Comment{ID: new("DRY-RUN")}, &Response{IsLast: true}, nil
	}
	// Add never carries visibility-clear in normal use, but the cmd layer
	// MAY pass a VisibilityChange to plumb a Replace through — accepted via
	// the optional path below. Today's Add signature doesn't accept vis;
	// the caller composes payload manually if visibility is needed.
	payload := commentPayload(body, VisibilityChange{Mode: VisibilityKeep})
	req, err := s.client.NewRequest(ctx, http.MethodPost, RESTPath("issue", key, "comment"), payload)
	if err != nil {
		return nil, nil, err
	}
	var c Comment
	resp, err := s.client.Do(req, &c)
	return &c, resp, err
}

// AddWithVisibility is the visibility-aware variant of Add. The cmd layer
// uses this when --visibility-role / --visibility-group is supplied; the
// plain Add stays minimal for callers that don't care about restrictions.
func (s *commentService) AddWithVisibility(ctx context.Context, key string, body *CommentBody, vis VisibilityChange) (*Comment, *Response, error) {
	if err := validateCommentBody(body); err != nil {
		return nil, nil, err
	}
	if body.DryRun {
		return &Comment{ID: new("DRY-RUN")}, &Response{IsLast: true}, nil
	}
	payload := commentPayload(body, vis)
	req, err := s.client.NewRequest(ctx, http.MethodPost, RESTPath("issue", key, "comment"), payload)
	if err != nil {
		return nil, nil, err
	}
	var c Comment
	resp, err := s.client.Do(req, &c)
	return &c, resp, err
}

// Edit updates an existing comment via PUT
// `/issue/{key}/comment/{id}`. The wire body never carries `author` —
// Atlassian preserves the original author server-side per , and
// any caller-supplied author would just be ignored.
func (s *commentService) Edit(ctx context.Context, key, commentID string, body *CommentBody, vis VisibilityChange) (*Comment, *Response, error) {
	if err := validateCommentBody(body); err != nil {
		return nil, nil, err
	}
	if body.DryRun {
		return &Comment{ID: &commentID}, &Response{IsLast: true}, nil
	}
	payload := commentPayload(body, vis)
	req, err := s.client.NewRequest(ctx, http.MethodPut, RESTPath("issue", key, "comment", commentID), payload)
	if err != nil {
		return nil, nil, err
	}
	var c Comment
	resp, err := s.client.Do(req, &c)
	return &c, resp, err
}

// Delete removes a comment via DELETE `/issue/{key}/comment/{id}`.
// Returns 204 No Content on success.
func (s *commentService) Delete(ctx context.Context, key, commentID string) (*Response, error) {
	if commentID == "" {
		return nil, errors.New("comment id is required")
	}
	req, err := s.client.NewRequest(ctx, http.MethodDelete, RESTPath("issue", key, "comment", commentID), nil)
	if err != nil {
		return nil, err
	}
	return s.client.Do(req, nil)
}
