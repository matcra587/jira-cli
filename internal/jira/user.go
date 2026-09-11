package jira

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/matcra587/jira-cli/internal/errtax"
)

// ErrUserNotFound is returned by ResolveUser when /user/search yields zero
// matches for the supplied query. The CLI surface maps this to exit 2 per
// the constitution error contract (resource-not-found).
var ErrUserNotFound = errors.New("user not found")

// AmbiguousUserError is returned by ResolveUser when /user/search yields
// 2+ matches and we refuse to pick a winner. Candidates carries every
// returned User so the CLI can render the disambiguation envelope.
type AmbiguousUserError struct {
	Query      string
	Candidates []*User
}

// Error states the query and match count; the candidates themselves travel on
// the struct for the disambiguation envelope (see CandidateRows).
func (e *AmbiguousUserError) Error() string {
	return fmt.Sprintf("ambiguous user %q — %d candidates", e.Query, len(e.Candidates))
}

// Code classifies the failure under the taxonomy's user_ambiguous code.
func (e *AmbiguousUserError) Code() errtax.Code { return errtax.CodeUserAmbiguous }

// CandidateRows flattens the /user/search candidates into envelope
// candidate rows so an agent can pick a winner without re-querying. The
// method is CandidateRows (not Candidates) because the struct carries a
// Candidates field. Nil candidates and absent fields are skipped; the
// returned slice is never nil.
func (e *AmbiguousUserError) CandidateRows() []map[string]any {
	rows := make([]map[string]any, 0, len(e.Candidates))
	for _, c := range e.Candidates {
		if c == nil {
			continue
		}
		row := map[string]any{}
		if c.AccountID != nil {
			row["account_id"] = *c.AccountID
		}
		if c.DisplayName != nil {
			row["display_name"] = *c.DisplayName
		}
		if c.EmailAddress != nil {
			row["email_address"] = *c.EmailAddress
		}
		rows = append(rows, row)
	}
	return rows
}

var (
	_ errtax.Coded      = (*AmbiguousUserError)(nil) //nolint:errcheck // compile-time interface assertion
	_ errtax.Candidated = (*AmbiguousUserError)(nil) //nolint:errcheck // compile-time interface assertion
)

// CurrentUser is the authenticated account's identity from
// /rest/api/3/myself. It is a subset of the full response — only the fields the
// CLI uses for assign-to-me, profile bootstrap, and `whoami`.
type CurrentUser struct {
	Self         string     `json:"self,omitempty"`
	AccountID    string     `json:"accountId,omitempty"`
	AccountType  string     `json:"accountType,omitempty"`
	EmailAddress string     `json:"emailAddress,omitempty"`
	DisplayName  string     `json:"displayName,omitempty"`
	Active       bool       `json:"active,omitempty"`
	TimeZone     string     `json:"timeZone,omitempty"`
	Locale       string     `json:"locale,omitempty"`
	AvatarURLs   AvatarURLs `json:"avatarUrls"`
}

// AvatarURLs maps Jira's standard avatar size keys to URLs.
type AvatarURLs struct {
	Size16 string `json:"16x16,omitempty"`
	Size24 string `json:"24x24,omitempty"`
	Size32 string `json:"32x32,omitempty"`
	Size48 string `json:"48x48,omitempty"`
}

// PermissionEntry is a single result of /rest/api/3/mypermissions.
type PermissionEntry struct {
	ID             string `json:"id,omitempty"`
	Key            string `json:"key,omitempty"`
	Name           string `json:"name,omitempty"`
	Type           string `json:"type,omitempty"`
	Description    string `json:"description,omitempty"`
	HavePermission bool   `json:"havePermission"`
}

// PermissionsResponse wraps the /mypermissions envelope.
type PermissionsResponse struct {
	Permissions map[string]PermissionEntry `json:"permissions"`
}

// UserService exposes user-identity endpoints.
type UserService interface {
	Myself(context.Context) (*CurrentUser, *Response, error)
	MyPermissions(ctx context.Context, projectKey string, keys []string) (*PermissionsResponse, *Response, error)
	Search(ctx context.Context, query string) ([]*User, *Response, error)
	AssignableSearch(ctx context.Context, query, projectKey string) ([]*User, *Response, error)
	ResolveAccountID(ctx context.Context, accountID string) (string, error)
	ResolveUser(ctx context.Context, query string) (string, error)
}

type userService struct {
	client   *Client
	myselfMu sync.Mutex
	myselfID string
}

// NewUserService constructs a UserService bound to the given client.
func NewUserService(client *Client) UserService {
	return &userService{client: client}
}

// Myself returns the authenticated user's identity via /rest/api/3/myself.
func (s *userService) Myself(ctx context.Context) (*CurrentUser, *Response, error) {
	req, err := s.client.NewRequest(ctx, http.MethodGet, RESTPath("myself"), nil)
	if err != nil {
		return nil, nil, err
	}
	var u CurrentUser
	resp, err := s.client.Do(req, &u)
	return &u, resp, err
}

// MyPermissions probes /rest/api/3/mypermissions for the current token.
// projectKey is optional ("" = global context). keys is the comma-joined
// list of permission keys (e.g. BROWSE_PROJECTS,CREATE_ISSUES) — required
// by the v3 endpoint, which rejects unfiltered queries.
func (s *userService) MyPermissions(ctx context.Context, projectKey string, keys []string) (*PermissionsResponse, *Response, error) {
	q := url.Values{}
	q.Set("permissions", strings.Join(keys, ","))
	if projectKey != "" {
		q.Set("projectKey", projectKey)
	}
	req, err := s.client.NewRequest(ctx, http.MethodGet, withQuery(RESTPath("mypermissions"), q), nil)
	if err != nil {
		return nil, nil, err
	}
	var out PermissionsResponse
	resp, err := s.client.Do(req, &out)
	return &out, resp, err
}

// Search runs /rest/api/3/user/search?query=<q> and returns the candidate
// list ordered by Atlassian's relevance. Atlassian does NOT guarantee
// uniqueness for emails — callers needing the resolve-or-fail semantics
// should use ResolveUser instead.
func (s *userService) Search(ctx context.Context, query string) ([]*User, *Response, error) {
	q := url.Values{}
	q.Set("query", query)
	req, err := s.client.NewRequest(ctx, http.MethodGet, withQuery(RESTPath("user", "search"), q), nil)
	if err != nil {
		return nil, nil, err
	}
	var out []*User
	resp, err := s.client.Do(req, &out)
	return out, resp, err
}

// AssignableSearch runs /rest/api/3/user/assignable/search?query=<q>&project=<key>
// and returns the users assignable to issues in that project, in Atlassian's
// relevance order. It is the assignee-suggestion source for the create form's
// assignee picker: unlike Search (which spans the whole directory) this endpoint
// is already scoped to who can be assigned, so the candidates are returned
// as-is — no filtering, the caller renders them in the order Jira ranked them.
func (s *userService) AssignableSearch(ctx context.Context, query, projectKey string) ([]*User, *Response, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("project", projectKey)
	req, err := s.client.NewRequest(ctx, http.MethodGet, withQuery(RESTPath("user", "assignable", "search"), q), nil)
	if err != nil {
		return nil, nil, err
	}
	var out []*User
	resp, err := s.client.Do(req, &out)
	return out, resp, err
}

// ResolveAccountID validates a caller-supplied accountId via Jira's read-only
// user endpoint. Normal ResolveUser keeps accountId:<id> local; this method is
// for explicit remote-validation paths that need active/deleted checks.
func (s *userService) ResolveAccountID(ctx context.Context, accountID string) (string, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return "", errors.New("accountId requires a non-empty id")
	}
	q := url.Values{}
	q.Set("accountId", accountID)
	req, err := s.client.NewRequest(ctx, http.MethodGet, withQuery(RESTPath("user"), q), nil)
	if err != nil {
		return "", err
	}
	var user User
	if _, err := s.client.Do(req, &user); err != nil {
		return "", err
	}
	active := activeUsers([]*User{&user})
	if len(active) == 0 {
		return "", fmt.Errorf("%w: %q", ErrUserNotFound, "accountId:"+accountID)
	}
	if active[0].AccountID == nil || *active[0].AccountID == "" {
		return "", errors.New("user lookup returned empty accountId")
	}
	return *active[0].AccountID, nil
}

// ResolveUser turns the user-supplied identifier into an accountId per
// the resolver contract:
//   - "me"                → cached /myself accountId
//   - "accountId:<id>"    → parsed locally; no /user/search hop
//   - anything else       → /user/search?query=…
//     0 matches  → ErrUserNotFound
//     1 match    → that accountId
//     2+ matches → *AmbiguousUserError carrying every candidate
//
// Caching the /myself response keeps repeated `--user me` invocations
// (e.g. `watch` then `unwatch` in the same process) from hitting the
// network twice.
func (s *userService) ResolveUser(ctx context.Context, query string) (string, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return "", errors.New("user identifier is required")
	}
	if q == "me" {
		// /myself is identical for the lifetime of this Client, so cache
		// the result and re-issue rather than calling once-and-for-all.
		// We don't use sync.Once here because that would bake the first
		// caller's canceled ctx into every subsequent lookup; instead we
		// retry on the next call when the previous attempt failed.
		s.myselfMu.Lock()
		id, cached := s.myselfID, s.myselfID != ""
		s.myselfMu.Unlock()
		if cached {
			return id, nil
		}
		me, _, err := s.Myself(ctx)
		if err != nil {
			return "", err
		}
		if me.AccountID == "" {
			return "", errors.New("me: /myself returned empty accountId")
		}
		s.myselfMu.Lock()
		s.myselfID = me.AccountID
		s.myselfMu.Unlock()
		return me.AccountID, nil
	}
	if id, ok := strings.CutPrefix(q, "accountId:"); ok {
		if id == "" {
			return "", errors.New("accountId: prefix requires a non-empty id")
		}
		return id, nil
	}
	users, _, err := s.Search(ctx, q)
	if err != nil {
		return "", err
	}
	users = activeUsers(users)
	switch len(users) {
	case 0:
		return "", fmt.Errorf("%w: %q", ErrUserNotFound, q)
	case 1:
		if users[0].AccountID == nil || *users[0].AccountID == "" {
			return "", errors.New("user search match returned empty accountId")
		}
		return *users[0].AccountID, nil
	default:
		return "", &AmbiguousUserError{Query: q, Candidates: users}
	}
}

func activeUsers(users []*User) []*User {
	out := make([]*User, 0, len(users))
	for _, user := range users {
		if user == nil {
			continue
		}
		if user.Active != nil && !*user.Active {
			continue
		}
		if user.Deleted != nil && *user.Deleted {
			continue
		}
		out = append(out, user)
	}
	return out
}
