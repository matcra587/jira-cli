package jira

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	xstrings "github.com/gechr/x/strings"
	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/jql"
)

// lexorankPattern matches Jira Software's lexorank tokens (e.g. "0|i0003z:",
// "0|hzzzzz:r"). On clone POST, Jira reads these as positioning directives
// and rejects with "rankBeforeIssue: expected Object". Drop them and let
// Jira assign a fresh rank to the new issue.
var lexorankPattern = regexp.MustCompile(`^\d+\|[a-z0-9]+:[a-z0-9]*$`)

// IssueService is the primary read/write surface for issues: list and get,
// the create/update/delete/clone/move mutations, the transition pair, and the
// comment and link operations that Jira exposes only through their own
// endpoints. Most mutations honor a DryRun flag on their request, returning a
// synthetic result without contacting Jira; regardless, every write is gated by
// the client's read-only/dry-run transport guard, which is the backstop for any
// path that does not preview locally.
type IssueService interface {
	List(context.Context, *IssueListOptions) ([]*Issue, *Response, error)
	Get(context.Context, string, *IssueGetOptions) (*Issue, *Response, error)
	Create(context.Context, *IssueCreateRequest) (*Issue, *Response, error)
	Update(context.Context, string, *IssueUpdateRequest) (*Issue, *Response, error)
	Delete(context.Context, string, *IssueDeleteOptions) (*Response, error)
	Clone(context.Context, string, *IssueCloneRequest) (*Issue, *Response, error)
	Move(context.Context, string, *IssueMoveRequest) (*Issue, *Response, error)
	Transitions(context.Context, string) ([]*Transition, *Response, error)
	Transition(context.Context, string, *TransitionRequest) (*Response, error)
	AddComment(context.Context, string, *CommentAddRequest) (*Comment, *Response, error)
	// Link creates an issue link between two issues (Blocks, Relates,
	// etc.). Jira's bulk-edit endpoint rejects issuelinks updates;
	// links require this dedicated endpoint.
	Link(context.Context, *IssueLinkRequest) (*Response, error)
	// AddRemoteLink attaches a web link (URL + title) to an issue —
	// the Jira "Web links" feature. Different endpoint from Link.
	AddRemoteLink(context.Context, string, *RemoteLinkRequest) (*Response, error)
}

// IssueDeleteOptions controls deletion behavior. DeleteSubtasks=true
// removes the issue and all its subtasks atomically (Jira refuses the
// delete otherwise when subtasks exist).
type IssueDeleteOptions struct {
	DeleteSubtasks bool
}

// IssueLinkRequest creates an issue link via POST /rest/api/3/issueLink.
// Type is the link-type name (e.g., "Blocks", "Relates", "Cloners").
// InwardIssue and OutwardIssue are issue keys; semantics depend on the
// type ("Blocks" → outwardIssue is blocked by inwardIssue).
type IssueLinkRequest struct {
	Type string
	// TypeID addresses the link type by id instead of name; when both
	// are set the id wins, matching Jira's own precedence.
	TypeID       string
	InwardIssue  string
	OutwardIssue string
	// Comment, when non-empty, is the native REST comment block posted
	// with the link. The CLI validates its ADF body before it gets
	// here; the wire forwards it verbatim.
	Comment map[string]any
}

func (r *IssueLinkRequest) payload() map[string]any {
	linkType := map[string]string{"name": r.Type}
	if r.TypeID != "" {
		linkType = map[string]string{"id": r.TypeID}
	}
	body := map[string]any{
		"type":         linkType,
		"inwardIssue":  map[string]string{"key": r.InwardIssue},
		"outwardIssue": map[string]string{"key": r.OutwardIssue},
	}
	if len(r.Comment) > 0 {
		body["comment"] = cloneJSONMap(r.Comment)
	}
	return body
}

// RemoteLinkRequest creates a "web link" via
// POST /rest/api/3/issue/{key}/remotelink. Title is what the user sees;
// URL is where the link points.
type RemoteLinkRequest struct {
	URL   string
	Title string
}

type issueService struct {
	client *Client
}

var defaultIssueListFields = []string{"key", "summary", "status", "assignee", "priority", "created", "updated"}

// IssueListFields is a package-level copy of the default issue-list field set,
// exposed for callers that read the default without invoking a function. It is
// a mutable slice — prefer DefaultIssueListFields, which hands back a fresh copy
// each call and cannot be mutated out from under other callers.
var IssueListFields = append([]string(nil), defaultIssueListFields...)

// DefaultIssueListFields returns the fields issue list requests when the caller
// selects none: the compact set shown in the default table. A fresh copy is
// returned each call so a caller can append to it without disturbing the
// default.
func DefaultIssueListFields() []string {
	return append([]string(nil), defaultIssueListFields...)
}

// NewIssueService constructs an IssueService bound to the given client.
func NewIssueService(client *Client) IssueService {
	return &issueService{client: client}
}

// IssueListOptions is the input to List. JQL selects the issues (defaulting to
// the standard issue-list query when blank); Fields and Expand shape the
// returned issues; the embedded ListOptions carries paging.
type IssueListOptions struct {
	ListOptions
	JQL    string
	Fields []string
	Expand []string
}

// IssueGetOptions is the input to Get. Expand names Jira expansions to include
// (e.g. transitions, editmeta), which is how a single read can also answer
// "what can I transition to / edit here". Fields narrows the returned field
// set via Jira's fields query parameter; empty means Jira's default (all
// navigable fields).
type IssueGetOptions struct {
	Expand []string
	Fields []string
}

// IssueCreateRequest is the input to Create. Project, IssueType, and Summary are
// the always-required fields, promoted out of the free-form Fields map so
// callers set them by name; Fields carries everything else (including
// spec-input aliases like project_key and assignee_account_id that payload
// translates). DryRun (local-only, `json:"-"`) short-circuits to a synthetic
// issue.
type IssueCreateRequest struct {
	Project   string         `json:"project,omitempty"`
	IssueType string         `json:"issuetype,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	Fields    map[string]any `json:"fields,omitempty"`
	DryRun    bool           `json:"-"`
}

func (r *IssueCreateRequest) payload() map[string]any {
	if r == nil {
		return map[string]any{"fields": map[string]any{}}
	}
	fields := map[string]any{}
	for key, value := range r.Fields {
		switch key {
		case "project_key", "issue_type", "description_markdown", "assignee_account_id":
			// Spec-input aliases — translated below, never sent verbatim.
			continue
		}
		fields[key] = cloneJSONValue(value)
	}
	if r.Summary != "" {
		fields["summary"] = r.Summary
	}
	if r.Project != "" {
		fields["project"] = map[string]string{"key": r.Project}
	}
	if r.IssueType != "" {
		fields["issuetype"] = map[string]string{"name": r.IssueType}
	}
	if id, ok := r.Fields["assignee_account_id"].(string); ok && id != "" {
		fields["assignee"] = map[string]string{"accountId": id}
	}
	return map[string]any{"fields": fields}
}

// IssueUpdateRequest is the input to Update. Fields sets values directly; Update
// carries the native add/set/remove operation block for fields that require it
// (Jira validates the verbs server-side). DryRun (local-only) short-circuits to
// a synthetic result.
type IssueUpdateRequest struct {
	Fields map[string]any `json:"fields"`
	// Update, when non-empty, is the native REST update block of
	// add/set/remove operations, sent as a sibling of fields. Jira
	// validates the operation verbs server-side.
	Update map[string]any `json:"update,omitempty"`
	DryRun bool           `json:"-"`
}

func (r *IssueUpdateRequest) payload() map[string]any {
	if r == nil {
		return map[string]any{"fields": map[string]any{}}
	}
	body := map[string]any{"fields": cloneJSONMap(r.Fields)}
	if len(r.Update) > 0 {
		body["update"] = cloneJSONMap(r.Update)
	}
	return body
}

// IssueCloneRequest is the input to Clone. Fields overrides values on the cloned
// issue, winning over anything carried from the source (see fieldsToClone).
// DryRun (local-only) short-circuits to a synthetic issue.
type IssueCloneRequest struct {
	Fields map[string]any `json:"fields,omitempty"`
	DryRun bool           `json:"-"`
}

// IssueMoveRequest is the input to Move. Fields carries the values a move
// requires — typically the target project and issue type. DryRun (local-only)
// short-circuits to a synthetic result.
type IssueMoveRequest struct {
	Fields map[string]any `json:"fields,omitempty"`
	DryRun bool           `json:"-"`
}

func (r *IssueMoveRequest) payload() map[string]any {
	if r == nil {
		return map[string]any{"fields": map[string]any{}}
	}
	return map[string]any{"fields": cloneJSONMap(r.Fields)}
}

// TransitionRequest is the input to Transition. ID is the target transition;
// Fields and Update carry any values the transition screen collects, and
// Comment posts a comment atomically with the status change. Fields and Update
// are silently dropped by Jira on a screenless transition — see
// Transition.HasScreen. DryRun (local-only) short-circuits to a synthetic
// result.
type TransitionRequest struct {
	ID     string
	Fields map[string]any
	// Comment, when non-nil, is posted atomically with the status change
	// via the transition body's update block.
	Comment *adf.Document
	// Update, when non-empty, is a native REST update block forwarded
	// verbatim into the transition body. The CLI refuses a payload that
	// sets both Update's comment operations and Comment, so the two
	// never collide here.
	Update map[string]any
	DryRun bool
}

// CommentAddRequest is the input to IssueService.AddComment. Body is a required
// ADF document. DryRun (local-only) short-circuits to a synthetic comment. This
// is the issue-service's minimal add path; the richer comment lifecycle
// (edit, delete, visibility) lives on CommentService.
type CommentAddRequest struct {
	Body   adf.Document `json:"body"`
	DryRun bool         `json:"-"`
}

// List fetches one page of issues matching the options' JQL, defaulting the
// query and field set when the caller supplies none. It runs through the same
// /search/jql endpoint as SearchService and stamps the Response with the
// token-paging state so callers can drain further pages.
func (s *issueService) List(ctx context.Context, opts *IssueListOptions) ([]*Issue, *Response, error) {
	body := &SearchRequest{}
	if opts != nil {
		body.JQL = opts.JQL
		body.ListOptions = opts.ListOptions
		body.Fields = opts.Fields
		body.Expand = opts.Expand
	}
	if len(body.Fields) == 0 {
		body.Fields = DefaultIssueListFields()
	}
	if xstrings.IsBlank(body.JQL) {
		body.JQL = jql.DefaultIssueListJQL
	}
	req, err := s.client.NewRequest(ctx, http.MethodPost, RESTPath("search", "jql"), body.payload())
	if err != nil {
		return nil, nil, err
	}
	var result SearchResult
	resp, err := s.client.Do(req, &result)
	if resp != nil {
		resp.MaxResults = body.effectivePageSize()
		resp.IsLast = result.IsLast
		resp.NextPageToken = result.NextPageToken
		resp.TokenPage = true
	}
	return result.Issues, resp, err
}

// Get fetches a single issue, optionally requesting the expansions in opts so
// one read can also carry the issue's valid transitions and edit metadata.
func (s *issueService) Get(ctx context.Context, key string, opts *IssueGetOptions) (*Issue, *Response, error) {
	path := RESTPath("issue", key)
	if opts != nil && (len(opts.Expand) > 0 || len(opts.Fields) > 0) {
		q := url.Values{}
		if len(opts.Expand) > 0 {
			q.Set("expand", strings.Join(opts.Expand, ","))
		}
		if len(opts.Fields) > 0 {
			q.Set("fields", strings.Join(opts.Fields, ","))
		}
		path = withQuery(path, q)
	}
	req, err := s.client.NewRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, nil, err
	}
	var issue Issue
	resp, err := s.client.Do(req, &issue)
	return &issue, resp, err
}

// Create opens a new issue. Summary is required; the payload build translates
// the promoted fields and spec-input aliases into Jira's nested wire shape.
// Under DryRun it returns a synthetic issue keyed "DRY-RUN" without contacting
// Jira.
func (s *issueService) Create(ctx context.Context, reqBody *IssueCreateRequest) (*Issue, *Response, error) {
	if reqBody == nil || xstrings.IsBlank(reqBody.Summary) {
		return nil, nil, errors.New("summary is required")
	}
	if reqBody.DryRun {
		return &Issue{Key: new("DRY-RUN")}, &Response{IsLast: true}, nil
	}
	req, err := s.client.NewRequest(ctx, http.MethodPost, RESTPath("issue"), reqBody.payload())
	if err != nil {
		return nil, nil, err
	}
	var issue Issue
	resp, err := s.client.Do(req, &issue)
	return &issue, resp, err
}

// Update edits an existing issue's fields (and optional operation block). Under
// DryRun it returns a synthetic issue for the key without contacting Jira.
func (s *issueService) Update(ctx context.Context, key string, reqBody *IssueUpdateRequest) (*Issue, *Response, error) {
	if reqBody != nil && reqBody.DryRun {
		return &Issue{Key: new(key)}, &Response{IsLast: true}, nil
	}
	req, err := s.client.NewRequest(ctx, http.MethodPut, RESTPath("issue", key), reqBody.payload())
	if err != nil {
		return nil, nil, err
	}
	var issue Issue
	resp, err := s.client.Do(req, &issue)
	return &issue, resp, err
}

// Transitions lists the workflow moves available from the issue's current
// status — the valid targets a Transition call can name.
func (s *issueService) Transitions(ctx context.Context, key string) ([]*Transition, *Response, error) {
	req, err := s.client.NewRequest(ctx, http.MethodGet, RESTPath("issue", key, "transitions"), nil)
	if err != nil {
		return nil, nil, err
	}
	var result struct {
		Transitions []*Transition `json:"transitions"`
	}
	resp, err := s.client.Do(req, &result)
	return result.Transitions, resp, err
}

// Transition moves the issue through the workflow, optionally setting fields and
// posting a comment in the same request via the transition's update block. Under
// DryRun it returns a synthetic response without contacting Jira.
func (s *issueService) Transition(ctx context.Context, key string, reqBody *TransitionRequest) (*Response, error) {
	if reqBody == nil || reqBody.ID == "" {
		return nil, errors.New("transition id is required")
	}
	if reqBody.DryRun {
		return &Response{IsLast: true}, nil
	}
	payload := map[string]any{"transition": map[string]string{"id": reqBody.ID}}
	if len(reqBody.Fields) > 0 {
		payload["fields"] = cloneJSONMap(reqBody.Fields)
	}
	update := map[string]any{}
	if len(reqBody.Update) > 0 {
		update = cloneJSONMap(reqBody.Update)
	}
	if reqBody.Comment != nil {
		update["comment"] = []map[string]any{{"add": map[string]any{"body": *reqBody.Comment}}}
	}
	if len(update) > 0 {
		payload["update"] = update
	}
	req, err := s.client.NewRequest(ctx, http.MethodPost, RESTPath("issue", key, "transitions"), payload)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req, nil)
	return resp, err
}

// Delete removes an issue. When the issue has subtasks, Jira refuses unless
// deleteSubtasks is set, so a caller wanting the cascade passes
// IssueDeleteOptions.DeleteSubtasks.
func (s *issueService) Delete(ctx context.Context, key string, opts *IssueDeleteOptions) (*Response, error) {
	path := RESTPath("issue", key)
	if opts != nil && opts.DeleteSubtasks {
		q := url.Values{}
		q.Set("deleteSubtasks", "true")
		path = withQuery(path, q)
	}
	req, err := s.client.NewRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return nil, err
	}
	return s.client.Do(req, nil)
}

// Link creates an issue link via POST /rest/api/3/issueLink. Jira's
// bulk-edit endpoint refuses `issuelinks` updates; this is the
// canonical path.
func (s *issueService) Link(ctx context.Context, reqBody *IssueLinkRequest) (*Response, error) {
	if reqBody == nil || xstrings.AllEmpty(reqBody.Type, reqBody.TypeID) || xstrings.AnyEmpty(reqBody.InwardIssue, reqBody.OutwardIssue) {
		return nil, errors.New("Link: type, inwardIssue, and outwardIssue are required")
	}
	req, err := s.client.NewRequest(ctx, http.MethodPost, RESTPath("issueLink"), reqBody.payload())
	if err != nil {
		return nil, err
	}
	return s.client.Do(req, nil)
}

// AddRemoteLink attaches a web link (URL + title) to an issue via
// POST /rest/api/3/issue/{key}/remotelink.
//
// URL must use the http:// or https:// scheme. Other schemes
// (javascript:, file:, ftp:, data:, mailto:, etc.) are rejected
// before any HTTP call. Jira itself
// strips unsafe schemes at render time, but a "web link" command
// shouldn't accept anything other than the two web schemes its
// name implies.
func (s *issueService) AddRemoteLink(ctx context.Context, key string, reqBody *RemoteLinkRequest) (*Response, error) {
	if reqBody == nil || reqBody.URL == "" {
		return nil, errors.New("AddRemoteLink: URL is required")
	}
	if err := validateWebLinkURL(reqBody.URL); err != nil {
		return nil, err
	}
	body := map[string]any{
		"object": map[string]any{
			"url":   reqBody.URL,
			"title": reqBody.Title,
		},
	}
	req, err := s.client.NewRequest(ctx, http.MethodPost, RESTPath("issue", key, "remotelink"), body)
	if err != nil {
		return nil, err
	}
	return s.client.Do(req, nil)
}

// validateWebLinkURL enforces the http/https allowlist for web links.
func validateWebLinkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("AddRemoteLink: URL is malformed: " + err.Error())
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return errors.New("AddRemoteLink: URL scheme must be http or https; got " + strconv.Quote(u.Scheme))
	}
	if u.Host == "" {
		return errors.New("AddRemoteLink: URL is missing host")
	}
	return nil
}

// AddComment posts a comment on the issue. Under DryRun it returns a synthetic
// comment without contacting Jira. For edit/delete/visibility, use
// CommentService.
func (s *issueService) AddComment(ctx context.Context, key string, reqBody *CommentAddRequest) (*Comment, *Response, error) {
	if reqBody == nil || reqBody.Body.Type == "" {
		return nil, nil, errors.New("comment body is required")
	}
	if reqBody.DryRun {
		return &Comment{ID: new("DRY-RUN")}, &Response{IsLast: true}, nil
	}
	req, err := s.client.NewRequest(ctx, http.MethodPost, RESTPath("issue", key, "comment"), reqBody)
	if err != nil {
		return nil, nil, err
	}
	var comment Comment
	resp, err := s.client.Do(req, &comment)
	return &comment, resp, err
}

// fieldsToClone strips server-assigned identifiers, lifecycle state,
// computed rollups, and collections from a raw Jira fields map so the
// result can be POSTed as a new issue without Jira rejecting it.
// project and issuetype are collapsed to their minimal POST shapes
// ({key:…} and {name:…} respectively).  Caller-supplied overrides are
// merged in after sanitisation; the caller always wins on conflicts.
func fieldsToClone(src, overrides map[string]any) map[string]any {
	skip := map[string]bool{
		// Identifiers — server-assigned.
		"id": true, "key": true, "self": true,
		// Lifecycle / audit — server-set, refused on POST.
		"created": true, "updated": true, "creator": true, "reporter": true,
		"status": true, "resolution": true, "resolutiondate": true,
		"statusCategory": true, "statuscategorychangedate": true,
		"lastViewed": true, "issuerestriction": true,
		// Time-tracking — computed; Jira refuses with "not on appropriate
		// screen". Set via the worklog endpoint, not on create.
		"timeestimate": true, "timespent": true, "timeoriginalestimate": true,
		"workratio": true, "progress": true, "timetracking": true,
		// Jira Software ranking — present on GET as scalar/null but POST
		// expects an Object; safest to drop and let Jira assign rank.
		"rankBeforeIssue": true, "rankAfterIssue": true,
		// Collections — need their own endpoints (issue link, comment, etc.).
		"comment": true, "worklog": true, "subtasks": true, "attachment": true,
		"votes": true, "watches": true, "issuelinks": true, "worklogs": true,
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		if skip[k] {
			continue
		}
		if strings.HasPrefix(k, "aggregate") {
			continue
		}
		out[k] = cloneJSONValue(v)
	}
	// Collapse project to {key: …} — only the key is accepted by POST.
	if p, ok := out["project"].(map[string]any); ok {
		if k, ok := p["key"].(string); ok {
			out["project"] = map[string]any{"key": k}
		}
	}
	// Collapse issuetype to {name: …}.
	if it, ok := out["issuetype"].(map[string]any); ok {
		if n, ok := it["name"].(string); ok {
			out["issuetype"] = map[string]any{"name": n}
		}
	}
	// Collapse assignee to {accountId: …}.
	if a, ok := out["assignee"].(map[string]any); ok {
		if id, ok := a["accountId"].(string); ok {
			out["assignee"] = map[string]any{"accountId": id}
		}
	}
	// Collapse priority to {name: …}.
	if pr, ok := out["priority"].(map[string]any); ok {
		if n, ok := pr["name"].(string); ok {
			out["priority"] = map[string]any{"name": n}
		}
	}
	// Drop customfield_* values that look like Jira Software lexorank
	// tokens — Jira rejects them on POST asking for the Object form.
	for k, v := range out {
		if !strings.HasPrefix(k, "customfield_") {
			continue
		}
		if s, ok := v.(string); ok && lexorankPattern.MatchString(s) {
			delete(out, k)
		}
	}
	// Caller overrides win over anything carried from the source.
	for k, v := range overrides {
		out[k] = cloneJSONValue(v)
	}
	return out
}

// Clone creates a new issue from an existing one. It reads the source as raw
// JSON, strips the fields Jira refuses on create (server ids, lifecycle,
// computed rollups, collections — see fieldsToClone), applies the caller's
// overrides, and POSTs the result. Under DryRun it returns a synthetic issue
// after doing the source read but before the create.
func (s *issueService) Clone(ctx context.Context, sourceKey string, reqBody *IssueCloneRequest) (*Issue, *Response, error) {
	// GET the source issue as raw JSON so we can sanitize arbitrary fields.
	getReq, err := s.client.NewRequest(ctx, http.MethodGet, RESTPath("issue", sourceKey), nil)
	if err != nil {
		return nil, nil, err
	}
	var rawSource map[string]any
	if _, err = s.client.Do(getReq, &rawSource); err != nil {
		return nil, nil, err
	}
	srcFields, _ := rawSource["fields"].(map[string]any)
	if srcFields == nil {
		srcFields = map[string]any{}
	}

	var overrides map[string]any
	if reqBody != nil {
		overrides = reqBody.Fields
	}
	merged := fieldsToClone(srcFields, overrides)

	if reqBody != nil && reqBody.DryRun {
		return &Issue{Key: new("DRY-RUN")}, &Response{IsLast: true}, nil
	}

	postReq, err := s.client.NewRequest(ctx, http.MethodPost, RESTPath("issue"), map[string]any{"fields": merged})
	if err != nil {
		return nil, nil, err
	}
	var issue Issue
	resp, err := s.client.Do(postReq, &issue)
	return &issue, resp, err
}

// Move changes an issue's project or type by editing the relevant fields. It is
// a plain issue edit under the hood; the distinct method name keeps the caller's
// intent legible.
func (s *issueService) Move(ctx context.Context, key string, reqBody *IssueMoveRequest) (*Issue, *Response, error) {
	req, err := s.client.NewRequest(ctx, http.MethodPut, RESTPath("issue", key), reqBody.payload())
	if err != nil {
		return nil, nil, err
	}
	var issue Issue
	resp, err := s.client.Do(req, &issue)
	return &issue, resp, err
}
