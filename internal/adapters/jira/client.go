package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	xhttp "github.com/gechr/x/http"
	xstrings "github.com/gechr/x/strings"

	"github.com/matcra587/jira-cli/internal/adapters"
	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

// Config carries everything New needs to reach one Jira Cloud site. The
// credential pair rides Basic auth — Jira Cloud's API-token scheme.
type Config struct {
	// BaseURL is the site root, e.g. https://example.atlassian.net.
	BaseURL string
	// CloudID selects the fixed Atlassian gateway while BaseURL remains the
	// browser/site identity. Empty preserves ordinary site/proxy routing.
	CloudID string
	// Email and APIToken form the Basic-auth pair.
	Email    string
	APIToken string
	// HTTPClient overrides the transport; nil gets a fresh default client.
	HTTPClient *http.Client
	// Timeout bounds each request end to end. Zero leaves the client
	// without a deadline, which is the default — see config.Config.Timeout
	// for why a guessed one would break attachment transfers.
	//
	// Applied to this client's own copy, never to a caller's: the shallow
	// copy below exists so a Config carrying an http.DefaultClient cannot
	// have a deadline stamped on it process-wide.
	Timeout time.Duration
	// Retry bounds automatic retry of safe requests; the zero value
	// disables retry entirely (see core.RetryPolicy).
	Retry core.RetryPolicy
	// Clock injects time for retry arithmetic; nil gets the system clock.
	Clock core.Clock
	// ReadOnly refuses every write at the TRANSPORT, so a command that
	// forgets to check still cannot mutate Jira. Placing it here rather
	// than in each command is the point: the guarantee holds for paths
	// nobody remembered to gate, including `jira api`.
	ReadOnly bool
}

// Client is the concrete Jira REST adapter. Construct it with New; the
// composition root hands it to commands as a core.JiraClient.
type Client struct {
	base  *url.URL
	site  *url.URL
	email string
	token string
	http  *http.Client
	retry core.RetryPolicy
	clock core.Clock
	// sleep and jitter are unexported test seams so retry tests run
	// instantly and deterministically; New wires the real ones.
	sleep  func(ctx context.Context, d time.Duration) error
	jitter func() float64
	// readOnly blocks writes at the transport; see Config.ReadOnly.
	readOnly bool
}

var _ core.JiraClient = (*Client)(nil)

// New validates the config and returns a ready client. It performs no I/O
// — the first network touch is the first request — so the composition
// root can construct it lazily without cost.
func New(cfg Config) (*Client, error) {
	if xstrings.AnyEmpty(cfg.Email, cfg.APIToken) {
		return nil, errors.New("jira client: email and api token are required")
	}
	// No error below quotes the raw URL. A base URL can carry userinfo,
	// and echoing it puts a password in a terminal, log, or pasted bug
	// report. Config rejects such a URL
	// first; this stays quiet in case anything ever reaches here without
	// going through it.
	base, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, errors.New("jira client: base url is not a valid URL")
	}
	if xstrings.AnyEmpty(base.Scheme, base.Host) {
		return nil, errors.New("jira client: base url needs a scheme and host")
	}
	if base.User != nil {
		return nil, errors.New("jira client: base url must not embed credentials")
	}
	// The credential rides the FIRST request as Basic auth, so the
	// scheme rule belongs here, at the transport, and not only in
	// configuration validation.
	//
	// Config validation runs on one construction path; `auth login` and
	// `auth status` take another. A rule enforced only there is skipped
	// by the paths that carry the credential — so it lives here, where
	// every client passes whatever route built it.
	if err := requireEncrypted(base); err != nil {
		return nil, err
	}
	site := base
	apiBase, err := core.JiraAPIBaseURL(cfg.BaseURL, cfg.CloudID)
	if err != nil {
		return nil, err
	}
	if cfg.CloudID != "" {
		base, err = url.Parse(apiBase)
		if err != nil {
			return nil, errors.New("jira client: invalid scoped API route")
		}
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: cfg.Timeout}
	} else {
		// A shallow copy, so the redirect policy below lands on OUR client:
		// mutating the caller's (imagine http.DefaultClient) would set the
		// policy process-wide and cross-wire two Clients sharing one.
		clone := *httpClient
		// Only when asked: a zero Timeout here means the flag and the
		// config key were both absent, and stamping it would ERASE a
		// deadline the caller set on the client they passed in.
		if cfg.Timeout != 0 {
			clone.Timeout = cfg.Timeout
		}
		httpClient = &clone
	}
	clock := cfg.Clock
	if clock == nil {
		clock = adapters.SystemClock{}
	}
	c := &Client{
		base:     base,
		site:     site,
		email:    cfg.Email,
		token:    cfg.APIToken,
		http:     httpClient,
		readOnly: cfg.ReadOnly,
		retry:    cfg.Retry,
		clock:    clock,
		sleep:    realSleep,
		jitter:   defaultJitter,
	}
	// Redirect safety is enforced before and after caller policy. The latter
	// may change the target or headers, but cannot weaken transport guarantees.
	//
	// The scheme rule first: requireEncrypted checks the base URL, which
	// is the URL the CALLER chose — not the one a server can name in a
	// Location header. http.Client forwards Authorization across a
	// redirect to the same host, and a scheme change does not make it a
	// different host, so https redirected to http on one host hands the
	// token over in the clear. Proven, not assumed: the test server
	// received `Basic ...` before this check existed.
	//
	// The write-replay rule: NewRequest sets GetBody for every
	// buffered body and http.Client re-sends it when following a
	// 307/308, so a write would silently double-fire against the
	// redirect target, invisible to the retry loop. Safe reads may follow
	// only to another read; a search POST redirected to issue creation
	// must not bypass read-only enforcement.
	//
	// Installed unconditionally, with any caller policy kept and run
	// AFTER. It used to be installed only when the caller had supplied
	// none, which let any caller with its own CheckRedirect opt out of
	// both — the credential included.
	callerPolicy := httpClient.CheckRedirect
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirectRequests {
			return fmt.Errorf("%w: stopped after %d redirect requests", ErrUnsafeRedirect, maxRedirectRequests)
		}
		first := via[0]
		if err := c.guardRedirect(req, first); err != nil {
			return err
		}
		if callerPolicy != nil {
			if err := callerPolicy(req, via); err != nil {
				return err
			}
			if err := c.guardRedirect(req, first); err != nil {
				return err
			}
		}
		// net/http forwards credentials to subdomains and across ports. Our
		// credential belongs to exactly this origin and configured API prefix,
		// including a cloud/proxy scope. Media redirects can still succeed
		// without receiving authentication, cookies, or private query referrers.
		if !withinBase(c.base, req.URL) {
			for _, header := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Referer"} {
				req.Header.Del(header)
			}
		}
		return nil
	}
	if httpClient.Jar != nil {
		// net/http adds jar cookies after CheckRedirect and stores response
		// cookies afterward. Header stripping alone cannot contain that state.
		httpClient.Jar = scopedCookieJar{base: base, jar: httpClient.Jar}
	}
	return c, nil
}

// BaseURL implements core.JiraClient: the site root c was constructed
// with. c.site is already validated by New (scheme+host, no embedded
// credentials), so this needs no re-check of its own — it hands back
// exactly the URL New accepted, stringified.
func (c *Client) BaseURL() string {
	return c.site.String()
}

// Account returns the public Basic-auth account bound at construction. It never
// reads credentials again or contacts Jira; token bytes remain private.
func (c *Client) Account() string { return c.email }

// JiraScope reports the public site, account and API route actually bound at
// construction. A scoped gateway can differ from the browser/site URL.
func (c *Client) JiraScope() core.JiraScope {
	return core.JiraScope{Site: c.site.String(), Account: c.email, APIBaseURL: c.base.String()}
}

var _ core.IssueHierarchyClient = (*Client)(nil)

var _ core.IssueDescriptionClient = (*Client)(nil)

// GetIssue implements core.JiraClient: one issue by key, fields narrowed
// to the requested IDs when given.
func (c *Client) GetIssue(ctx context.Context, key string, fields []string, expand ...string) (core.IssueRead, error) {
	return c.getIssue(ctx, key, fields, nil, expand...)
}

func (c *Client) getIssue(ctx context.Context, key string, fields []string, verification *map[string]json.RawMessage, expand ...string) (core.IssueRead, error) {
	query := url.Values{}
	if len(expand) > 0 {
		query.Set("expand", strings.Join(expand, ","))
	}
	if len(fields) > 0 {
		query.Set("fields", strings.Join(fields, ","))
	}
	req, err := c.newRequest(ctx, http.MethodGet, []string{"rest", "api", "3", "issue", key}, query, nil)
	if err != nil {
		return core.IssueRead{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.IssueRead{}, fmt.Errorf("get issue: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if verification != nil {
		if resp.StatusCode != http.StatusOK {
			return core.IssueRead{}, c.apiError(resp, info)
		}
		if err := c.requireAuthenticatedReply(resp, errIssueVerificationInvalid, "issue verification"); err != nil {
			return core.IssueRead{}, err
		}
		issue, nativeFields, err := decodeVerificationIssue(resp.Body, fields)
		*verification = nativeFields
		return core.IssueRead{Issue: issue, Retry: info}, err
	}
	descriptionSource := len(fields) == 2 && fields[0] == "description" && fields[1] == "updated"
	if descriptionSource {
		if err := c.requireAuthenticatedReply(resp, core.ErrIssueDescriptionInvalid, "description"); err != nil {
			return core.IssueRead{}, err
		}
	}
	attachmentsOnly := len(fields) == 1 && fields[0] == "attachment"
	fullSource := len(fields) == 1 && fields[0] == "*all"
	if resp.StatusCode != http.StatusOK {
		if descriptionSource && resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			return core.IssueRead{}, fmt.Errorf("%w: description read returned HTTP %d instead of 200", core.ErrIssueDescriptionInvalid, resp.StatusCode)
		}
		if fullSource && resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			return core.IssueRead{}, fmt.Errorf("%w: full issue read returned HTTP %d instead of 200", core.ErrIssueCloneSourceInvalid, resp.StatusCode)
		}
		if attachmentsOnly && resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			return core.IssueRead{}, fmt.Errorf("%w: attachment issue read returned HTTP %d instead of 200", core.ErrAttachmentResponseInvalid, resp.StatusCode)
		}
		return core.IssueRead{}, c.apiError(resp, info)
	}
	if descriptionSource {
		issue, err := decodeDescriptionIssue(resp.Body)
		return core.IssueRead{Issue: issue, Retry: info}, err
	}
	if attachmentsOnly {
		issue, err := decodeAttachmentIssue(resp.Body)
		if err != nil {
			return core.IssueRead{}, fmt.Errorf("get issue attachments: %w", err)
		}
		return core.IssueRead{Issue: issue, Retry: info}, nil
	}
	if fullSource {
		issue, err := decodeCloneIssue(resp.Body)
		if err != nil {
			return core.IssueRead{}, err
		}
		return core.IssueRead{Issue: issue, Retry: info}, nil
	}
	var issue wire.IssueBean
	if err := json.NewDecoder(resp.Body).Decode(&issue); err != nil {
		return core.IssueRead{}, fmt.Errorf("get issue: decode response: %w", err)
	}
	return core.IssueRead{Issue: &issue, Retry: info}, nil
}

// ErrCurrentUserResponseInvalid marks an upstream identity protocol failure.
// It must not be mistaken for rejected credentials or a usable empty account.
var ErrCurrentUserResponseInvalid = core.ErrCurrentUserResponseInvalid

// CurrentUser reads the account the credentials belong to. Authentication and
// @me writes share this boundary: success requires one complete native account
// object with a usable identity, not merely HTTP 200 or syntactically valid JSON.
func (c *Client) CurrentUser(ctx context.Context) (core.UserRead, error) {
	req, err := c.newRequest(ctx, http.MethodGet, []string{"rest", "api", "3", "myself"}, nil, nil)
	if err != nil {
		return core.UserRead{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.UserRead{}, fmt.Errorf("get current user: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	// Media reads may follow a redirect without credentials, but such a reply
	// cannot verify the account/token tuple used for authentication publication.
	if err := c.requireAuthenticatedReply(resp, ErrCurrentUserResponseInvalid, "identity"); err != nil {
		return core.UserRead{}, err
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			return core.UserRead{}, fmt.Errorf("%w: unexpected HTTP status %d", ErrCurrentUserResponseInvalid, resp.StatusCode)
		}
		return core.UserRead{}, c.apiError(resp, info)
	}
	var raw json.RawMessage
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&raw); err != nil {
		return core.UserRead{}, fmt.Errorf("%w: decode account: %w", ErrCurrentUserResponseInvalid, err)
	}
	// EOF confirmation shares the configured whole-request timeout and
	// command cancellation. Timeout zero deliberately means no deadline;
	// an account lookup must not replace that choice with a guessed bound.
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err == nil {
		return core.UserRead{}, fmt.Errorf("%w: response contains trailing data", ErrCurrentUserResponseInvalid)
	} else if !errors.Is(err, io.EOF) {
		// Preserve interrupted body reads so cancellation and timeout retain
		// their precedence in the shared CLI taxonomy.
		return core.UserRead{}, fmt.Errorf("%w: finish account response: %w", ErrCurrentUserResponseInvalid, err)
	}
	if _, err := wire.DecodeNativeObject(raw, wire.WireSchemaProperties()["UserDetails"]); err != nil {
		return core.UserRead{}, fmt.Errorf("%w: native account: %w", ErrCurrentUserResponseInvalid, err)
	}
	var user *wire.UserDetails
	if err := json.Unmarshal(raw, &user); err != nil {
		return core.UserRead{}, fmt.Errorf("%w: decode account: %w", ErrCurrentUserResponseInvalid, err)
	}
	if core.RequireCurrentUserIdentity(user) != nil {
		return core.UserRead{}, fmt.Errorf("%w: account lacks an identity", ErrCurrentUserResponseInvalid)
	}
	return core.UserRead{User: user, Retry: info}, nil
}

// GetWatchers implements core.JiraClient: everyone watching key.
//
// No query values at all, which is the endpoint's own shape and not an
// omission — /rest/api/3/issue/{key}/watchers takes no paging pair (see
// the seam's own doc comment), so unlike GetComments below there is
// nothing to put in the query string.
//
// One thing this method deliberately does NOT do is treat a short
// watcher list as an error. Jira returns the `watchers` array only to a
// caller holding the "View voters and watchers" permission, and answers
// everyone else with the same 200 carrying `watchCount` and no array.
// That is a legitimate response about a real issue, not a failure, and
// the count that comes with it is exactly what lets the layer above say
// so out loud (see envelope.WatcherListData.WatchCount).
func (c *Client) GetWatchers(ctx context.Context, key string) (core.WatcherRead, error) {
	req, err := c.newRequest(ctx, http.MethodGet, []string{"rest", "api", "3", "issue", key, "watchers"}, nil, nil)
	if err != nil {
		return core.WatcherRead{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.WatcherRead{}, fmt.Errorf("get watchers: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusOK {
		return core.WatcherRead{}, c.apiError(resp, info)
	}
	var watchers wire.Watchers
	if err := json.NewDecoder(resp.Body).Decode(&watchers); err != nil {
		return core.WatcherRead{}, fmt.Errorf("get watchers: decode response: %w", err)
	}
	return core.WatcherRead{Watchers: &watchers, Retry: info}, nil
}

// GetComments implements core.JiraClient: one page of key's comments.
// startAt and maxResults ride straight through as the query pair; this
// method fetches exactly the page asked for and never walks further
// pages itself.
//
// order becomes the endpoint's own `orderBy`. THIS is where
// core.CommentOrderDefault resolves, and it resolves to newest-first —
// the behavior this method had unconditionally before the parameter
// existed, so no caller that passes nothing sees a change. Resolving it
// here rather than at each call site is what keeps that promise in one
// place: a second default written into a command could drift from this
// one and nothing would notice, because both would produce a valid query.
func (c *Client) GetComments(ctx context.Context, key string, startAt, maxResults int, order core.CommentOrder) (core.CommentRead, error) {
	if order == core.CommentOrderDefault {
		order = core.CommentOrderNewestFirst
	}
	query := url.Values{}
	query.Set("startAt", strconv.Itoa(startAt))
	query.Set("maxResults", strconv.Itoa(maxResults))
	query.Set("orderBy", string(order))
	req, err := c.newRequest(ctx, http.MethodGet, []string{"rest", "api", "3", "issue", key, "comment"}, query, nil)
	if err != nil {
		return core.CommentRead{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.CommentRead{}, fmt.Errorf("get comments: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			return core.CommentRead{Retry: info}, fmt.Errorf("%w: Jira answered the comment read with unexpected HTTP status %d", core.ErrCommentResponseInvalid, resp.StatusCode)
		}
		return core.CommentRead{}, c.apiError(resp, info)
	}
	page, err := decodeCommentPage(resp.Body, startAt, maxResults)
	if err != nil {
		return core.CommentRead{Retry: info}, fmt.Errorf("get comments: %w", err)
	}
	return core.CommentRead{Page: page, Retry: info}, nil
}

// AddComment implements core.JiraClient: posts one comment to key and
// returns the comment Jira created.
//
// THE FIRST WRITE THIS CLIENT MAKES. Three things differ from every
// method above, and none of them is a special case written here — they
// all fall out of machinery that already existed:
//
// Read-only mode blocks it, with no code in this method. newRequest runs
// the check on the BUILT request, and isRead (retry.go) classifies a POST
// to anything but the two search paths as a write, so this call returns
// ErrReadOnly before a byte leaves the process. A later write gets the
// same protection by doing nothing.
//
// It is never replayed. safeToRetry IS isRead, so doWithRetry makes
// exactly one attempt here. That is the correct behavior and worth
// stating out loud: retrying a comment POST whose response was lost to a
// timeout would post the comment twice, and neither Jira nor this client
// has an idempotency key to deduplicate with.
//
// It expects 201, not 200. Jira answers a created comment with
// StatusCreated; accepting 200 as well would mean accepting a response
// shape this endpoint does not produce. A noncanonical success status is
// an unusable acknowledgement; failing HTTP
// statuses retain their ordinary apiError classification.
func (c *Client) AddComment(ctx context.Context, key string, body wire.AddCommentRequest) (core.CommentWrite, error) {
	req, err := c.newRequest(ctx, http.MethodPost, []string{"rest", "api", "3", "issue", key, "comment"}, nil, body)
	if err != nil {
		return core.CommentWrite{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.CommentWrite{}, fmt.Errorf("add comment: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusCreated {
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			return core.CommentWrite{Retry: info}, fmt.Errorf("%w: Jira answered the comment add with unexpected HTTP status %d", core.ErrCommentResponseInvalid, resp.StatusCode)
		}
		return core.CommentWrite{}, c.apiError(resp, info)
	}
	created, err := decodeCommentReceipt(resp.Body, "")
	if err != nil {
		return core.CommentWrite{Retry: info}, err
	}
	return core.CommentWrite{Comment: created, Retry: info}, nil
}

// UpdateComment implements core.JiraClient: replaces one comment's body
// and returns the comment as Jira stored it.
//
// It expects 200, where AddComment expects 201, and the difference is
// not pedantry: 201 means a resource was created and carries a Location,
// and accepting it here would be accepting a response shape this
// endpoint does not produce. A noncanonical success is an unusable
// acknowledgement. Failing HTTP statuses retain apiError, including an
// unknown comment's 404 and Jira's refusal to edit a comment.
//
// Read-only blocking and no-replay come for free from newRequest and
// isRead, exactly as they do for AddComment; there is no code here for
// either.
func (c *Client) UpdateComment(ctx context.Context, key, commentID string, body wire.UpdateCommentRequest, notifyUsers bool) (core.CommentWrite, error) {
	req, err := c.newRequest(ctx, http.MethodPut, []string{"rest", "api", "3", "issue", key, "comment", commentID}, notifyQuery(notifyUsers), body)
	if err != nil {
		return core.CommentWrite{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.CommentWrite{}, fmt.Errorf("update comment: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			return core.CommentWrite{Retry: info}, fmt.Errorf("%w: Jira answered the comment edit with unexpected HTTP status %d", core.ErrCommentResponseInvalid, resp.StatusCode)
		}
		return core.CommentWrite{}, c.apiError(resp, info)
	}
	updated, err := decodeCommentReceipt(resp.Body, commentID)
	if err != nil {
		return core.CommentWrite{Retry: info}, err
	}
	return core.CommentWrite{Comment: updated, Retry: info}, nil
}

// DeleteComment implements core.JiraClient: removes one comment.
//
// It expects 204 and reads no body, which is this endpoint's real
// answer. A 404 — an unknown comment id, or one already deleted — comes
// back as an ordinary apiError rather than being swallowed as "already
// in the desired state": unlike the watcher verbs below, a delete that
// finds nothing is far likelier to mean the caller has the wrong id than
// that they are re-running a success, and quietly reporting success
// would hide the typo.
func (c *Client) DeleteComment(ctx context.Context, key, commentID string) (core.CommentDeleteWrite, error) {
	req, err := c.newRequest(ctx, http.MethodDelete, []string{"rest", "api", "3", "issue", key, "comment", commentID}, nil, nil)
	if err != nil {
		return core.CommentDeleteWrite{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.CommentDeleteWrite{}, fmt.Errorf("delete comment: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusNoContent {
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			return core.CommentDeleteWrite{Retry: info}, fmt.Errorf("%w: Jira answered the comment deletion with unexpected HTTP status %d", core.ErrCommentResponseInvalid, resp.StatusCode)
		}
		return core.CommentDeleteWrite{}, c.apiError(resp, info)
	}
	return core.CommentDeleteWrite{StatusCode: resp.StatusCode, Retry: info}, nil
}

// AddWatcher implements core.JiraClient: makes accountID watch key.
//
// The account id is the whole request body, as a bare JSON string. That
// looks like a mistake and is not — the endpoint's requestBody schema is
// `{"type": "string"}` (see the seam's own doc comment), so handing
// newRequest a plain Go string is exactly right and an object with an
// accountId field would be refused. newRequest marshals it through the
// wire tier's own encoder like every other body.
func (c *Client) AddWatcher(ctx context.Context, key, accountID string) (core.WatcherWrite, error) {
	req, err := c.newRequest(ctx, http.MethodPost, []string{"rest", "api", "3", "issue", key, "watchers"}, nil, accountID)
	if err != nil {
		return core.WatcherWrite{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.WatcherWrite{}, fmt.Errorf("add watcher: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusNoContent {
		return core.WatcherWrite{}, c.apiError(resp, info)
	}
	return core.WatcherWrite{Retry: info}, nil
}

// RemoveWatcher implements core.JiraClient: stops accountID watching
// key.
//
// The account id rides the QUERY here where AddWatcher puts it in the
// body — Jira's own asymmetry, not a choice made here. It is set through
// url.Values rather than pasted into a path segment, so an id containing
// a reserved character is escaped rather than reinterpreted as more
// path.
//
// The `username` parameter the endpoint also declares is deliberately
// never sent: it is the deprecated pre-GDPR identifier, and this CLI
// speaks account ids everywhere.
func (c *Client) RemoveWatcher(ctx context.Context, key, accountID string) (core.WatcherWrite, error) {
	query := url.Values{}
	query.Set("accountId", accountID)
	req, err := c.newRequest(ctx, http.MethodDelete, []string{"rest", "api", "3", "issue", key, "watchers"}, query, nil)
	if err != nil {
		return core.WatcherWrite{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.WatcherWrite{}, fmt.Errorf("remove watcher: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusNoContent {
		return core.WatcherWrite{}, c.apiError(resp, info)
	}
	return core.WatcherWrite{Retry: info}, nil
}

// GetIssueLinkTypes implements core.JiraClient: every link type on the
// site.
//
// An ordinary GET with no parameters at all — the endpoint takes none —
// so read-only mode allows it and the retry loop replays it, which is
// right: re-reading a configuration list changes nothing.
func (c *Client) GetIssueLinkTypes(ctx context.Context) (core.LinkTypeRead, error) {
	req, err := c.newRequest(ctx, http.MethodGet, []string{"rest", "api", "3", "issueLinkType"}, nil, nil)
	if err != nil {
		return core.LinkTypeRead{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.LinkTypeRead{}, fmt.Errorf("get issue link types: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusOK {
		return core.LinkTypeRead{}, c.apiError(resp, info)
	}
	var types wire.IssueLinkTypes
	if err := json.NewDecoder(resp.Body).Decode(&types); err != nil {
		return core.LinkTypeRead{}, fmt.Errorf("get issue link types: decode response: %w", err)
	}
	return core.LinkTypeRead{Types: &types, Retry: info}, nil
}

// LinkIssues implements core.JiraClient: joins two issues.
//
// It expects 201 and reads NO body, which is this endpoint's real
// answer — it creates a link and declines to identify it. The check
// stays exact for AddComment's reason: accepting 200 as well would be
// accepting a response shape this endpoint does not produce, and an
// unexpected status becomes an ordinary apiError, which is where a 404
// for an unknown issue key and the 400 for an unknown link type both
// arrive.
//
// Read-only blocking and no-replay come for free from newRequest and
// isRead. No-replay matters here in a way it does not for a comment: the
// endpoint has no idempotency key, so a replayed POST after a lost
// response would create a SECOND identical link between the same two
// issues, which Jira permits.
func (c *Client) LinkIssues(ctx context.Context, body wire.LinkIssueRequest) (core.LinkWrite, error) {
	req, err := c.newRequest(ctx, http.MethodPost, []string{"rest", "api", "3", "issueLink"}, nil, body)
	if err != nil {
		return core.LinkWrite{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.LinkWrite{}, fmt.Errorf("link issues: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusCreated {
		return core.LinkWrite{}, c.apiError(resp, info)
	}
	return core.LinkWrite{Retry: info}, nil
}

// DeleteIssueLink implements core.JiraClient: removes one link by id.
//
// It accepts 200 AND 204, which is the one place this client is
// deliberately loose about a status — and the spec is the reason rather
// than caution: `deleteIssueLink` documents BOTH as success responses,
// unlike every other delete here, which documents only 204. Insisting on
// one would turn a documented success into an apiError.
func (c *Client) DeleteIssueLink(ctx context.Context, linkID string) (core.LinkDeleteWrite, error) {
	req, err := c.newRequest(ctx, http.MethodDelete, []string{"rest", "api", "3", "issueLink", linkID}, nil, nil)
	if err != nil {
		return core.LinkDeleteWrite{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.LinkDeleteWrite{}, fmt.Errorf("delete issue link: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return core.LinkDeleteWrite{}, c.apiError(resp, info)
	}
	return core.LinkDeleteWrite{Retry: info}, nil
}

// GetRemoteLinks implements core.JiraClient: one issue's remote links.
//
// No query values: the endpoint's globalId filter is a way to fetch ONE
// known link, and this seam exists to list them all, so sending it would
// narrow the very thing the caller asked for.
func (c *Client) GetRemoteLinks(ctx context.Context, key string) (core.RemoteLinkRead, error) {
	req, err := c.newRequest(ctx, http.MethodGet, []string{"rest", "api", "3", "issue", key, "remotelink"}, nil, nil)
	if err != nil {
		return core.RemoteLinkRead{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.RemoteLinkRead{}, fmt.Errorf("get remote links: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusOK {
		return core.RemoteLinkRead{}, c.apiError(resp, info)
	}
	// A bare ARRAY, not an object wrapping one — this endpoint is the
	// only read on this client that answers that way, which is why the
	// result type carries a slice rather than a pointer to a page.
	links, err := decodeRemoteLinks(resp.Body)
	if err != nil {
		return core.RemoteLinkRead{}, fmt.Errorf("get remote links: decode response: %w", err)
	}
	return core.RemoteLinkRead{Links: links, Retry: info}, nil
}

// AddRemoteLink implements core.JiraClient: attaches a URL to an issue,
// or replaces the link already carrying the same globalId.
//
// It accepts 201 AND 200, and unlike DeleteIssueLink's looseness above
// the two mean DIFFERENT things here rather than the same thing twice:
// 201 created a link, 200 replaced one. Both are successes, and which it
// was rides back on the result so the layer above can say so — the
// status code is the only place Jira reports it.
func (c *Client) AddRemoteLink(ctx context.Context, key string, body wire.RemoteLinkRequest) (core.RemoteLinkWrite, error) {
	if _, err := core.ValidateRemoteLinkRequest(body); err != nil {
		return core.RemoteLinkWrite{}, err
	}
	req, err := c.newRequest(ctx, http.MethodPost, []string{"rest", "api", "3", "issue", key, "remotelink"}, nil, body)
	if err != nil {
		return core.RemoteLinkWrite{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.RemoteLinkWrite{}, fmt.Errorf("add remote link: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			return core.RemoteLinkWrite{}, fmt.Errorf("%w: Jira acknowledged the write with unexpected status %d", core.ErrWeblinkResponseInvalid, resp.StatusCode)
		}
		return core.RemoteLinkWrite{}, c.apiError(resp, info)
	}
	link, err := decodeRemoteLinkReceipt(resp.Body)
	if err != nil {
		return core.RemoteLinkWrite{}, err
	}
	return core.RemoteLinkWrite{Created: resp.StatusCode == http.StatusCreated, Retry: info, Link: link}, nil
}

// DeleteRemoteLink implements core.JiraClient: removes one remote link
// from an issue. Expects 204, this endpoint's documented answer.
func (c *Client) DeleteRemoteLink(ctx context.Context, key, linkID string) (core.RemoteLinkDeleteWrite, error) {
	req, err := c.newRequest(ctx, http.MethodDelete, []string{"rest", "api", "3", "issue", key, "remotelink", linkID}, nil, nil)
	if err != nil {
		return core.RemoteLinkDeleteWrite{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.RemoteLinkDeleteWrite{}, fmt.Errorf("delete remote link: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusNoContent {
		return core.RemoteLinkDeleteWrite{}, c.apiError(resp, info)
	}
	return core.RemoteLinkDeleteWrite{Retry: info}, nil
}

// GetTransitions implements core.JiraClient: every transition available
// on key right now.
//
// transitions.fields supplies screen field metadata for discovery and native
// transition inputs. The whole response is decoded and validated before use.
//
// It is an ordinary GET, so read-only mode allows it and the retry loop
// replays it — which is right: a caller in read-only mode may still ask
// what transitions exist, and re-reading a list changes nothing.
func (c *Client) GetTransitions(ctx context.Context, key string) (core.TransitionRead, error) {
	req, err := c.newRequest(ctx, http.MethodGet, []string{"rest", "api", "3", "issue", key, "transitions"}, url.Values{"expand": {"transitions.fields"}}, nil)
	if err != nil {
		return core.TransitionRead{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.TransitionRead{}, fmt.Errorf("get transitions: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			return core.TransitionRead{Retry: info}, fmt.Errorf("%w: Jira answered workflow discovery with unexpected HTTP status %d", core.ErrTransitionReadInvalid, resp.StatusCode)
		}
		return core.TransitionRead{}, c.apiError(resp, info)
	}
	list, err := decodeTransitions(resp.Body)
	if err != nil {
		return core.TransitionRead{Retry: info}, err
	}
	return core.TransitionRead{Transitions: list, Retry: info}, nil
}

// DoTransition implements core.JiraClient: moves key through its
// workflow.
//
// It expects 204, reads no body, and retains that acknowledgement in
// core.TransitionWrite. Other successful statuses leave the write uncertain
// and require inspection. Native rejections use apiError, including workflow
// rejection (400, naming the field Jira would not accept).
//
// Read-only blocking and no-replay both come for free from newRequest
// and isRead, exactly as they do for AddComment; there is no code here
// for either.
func (c *Client) DoTransition(ctx context.Context, key string, body wire.TransitionRequest) (core.TransitionWrite, error) {
	req, err := c.newRequest(ctx, http.MethodPost, []string{"rest", "api", "3", "issue", key, "transitions"}, nil, body)
	if err != nil {
		return core.TransitionWrite{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.TransitionWrite{}, fmt.Errorf("do transition: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices && resp.StatusCode != http.StatusNoContent {
		return core.TransitionWrite{}, fmt.Errorf("%w: Jira answered the transition with unexpected status %d", core.ErrTransitionResponseInvalid, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusNoContent {
		return core.TransitionWrite{}, c.apiError(resp, info)
	}
	return core.TransitionWrite{StatusCode: resp.StatusCode, Retry: info}, nil
}

// CreateIssue implements core.JiraClient: creates one issue and returns
// the identity Jira assigned it.
//
// It expects 201 and DECODES the body, which is the combination neither
// write above has: AddComment decodes a whole created object, DoTransition
// decodes nothing at all, and this one decodes an identity. The exact
// status check is kept for AddComment's reason — Jira answers a created
// issue with StatusCreated, and accepting 200 as well would be accepting a
// response shape this endpoint does not produce.
//
// A decode failure here reads the same way AddComment's does, and must:
// the issue EXISTS once Jira said 201, so the message reports a failure to
// describe what happened rather than a failure to make it happen. It
// matters more here — a caller who retries on this message creates a
// SECOND issue, and unlike a duplicate comment there is no obvious way to
// tell which one anybody meant.
//
// Read-only blocking and no-replay come free from newRequest and isRead
// exactly as they do for AddComment, with no code in this method.
func (c *Client) CreateIssue(ctx context.Context, body wire.IssueWriteRequest) (core.IssueCreateWrite, error) {
	req, err := c.newRequest(ctx, http.MethodPost, []string{"rest", "api", "3", "issue"}, nil, body)
	if err != nil {
		return core.IssueCreateWrite{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.IssueCreateWrite{}, fmt.Errorf("create issue: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusCreated {
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return core.IssueCreateWrite{}, fmt.Errorf("%w: unexpected HTTP %d", core.ErrIssueCreateResponseInvalid, resp.StatusCode)
		}
		return core.IssueCreateWrite{}, c.apiError(resp, info)
	}
	write, err := decodeIssueCreation(resp.Body)
	write.Retry = info
	return write, err
}

// EditIssue implements core.JiraClient: applies field changes to one
// existing issue.
//
// It rides PUT and expects 204, reading no body — which is the endpoint's
// real answer and the reason core.IssueEditWrite carries only retry facts.
//
// PUT is idempotent in the HTTP sense, and this client still does not
// replay it, because isRead classifies every non-GET as a write. That is
// the right answer rather than a missed optimization: this payload's
// `update` half applies OPERATIONS, so replaying an edit that added a
// label would add it twice on a server that does not deduplicate, and the
// method's HTTP semantics say nothing about the body's.
//
// A 400 here is where a workflow or screen rejection arrives, naming the
// field Jira would not accept — which is the failure a caller composing
// fields locally is likeliest to meet, and apiError carries the server's
// own wording for it rather than a guess made here.
func (c *Client) EditIssue(ctx context.Context, key string, body wire.IssueWriteRequest, notifyUsers bool) (core.IssueEditWrite, error) {
	req, err := c.newRequest(ctx, http.MethodPut, []string{"rest", "api", "3", "issue", key}, notifyQuery(notifyUsers), body)
	if err != nil {
		return core.IssueEditWrite{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.IssueEditWrite{}, fmt.Errorf("edit issue: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusNoContent {
		return core.IssueEditWrite{}, c.apiError(resp, info)
	}
	return core.IssueEditWrite{Retry: info}, nil
}

// SearchIssues implements core.JiraClient: one page of a JQL query's
// results. It rides POST — the search/jql endpoint takes JQL bodies too
// large for a GET's query string — which is exactly the exception
// retry.go's searchJQLPath allowance exists for; this handler builds
// that literal path (["rest","api","3","search","jql"]) so the
// allowance and the actual request can never drift apart. Read-only
// mode does not block it: isRead (retry.go) already classifies this
// path's POST as a read, same as every other GET on this client, so no
// separate carve-out is needed here.
func (c *Client) SearchIssues(ctx context.Context, jql string, fields []string, nextPageToken string, maxResults int, expand ...string) (core.IssueSearchRead, error) {
	body := wire.SearchRequest{
		Expand:        strings.Join(expand, ","),
		JQL:           jql,
		Fields:        fields,
		NextPageToken: nextPageToken,
		MaxResults:    maxResults,
	}
	req, err := c.newRequest(ctx, http.MethodPost, []string{"rest", "api", "3", "search", "jql"}, nil, body)
	if err != nil {
		return core.IssueSearchRead{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.IssueSearchRead{}, fmt.Errorf("search issues: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusOK {
		return core.IssueSearchRead{}, c.apiError(resp, info)
	}
	results, err := decodeSearchPage(resp.Body, maxResults)
	if err != nil {
		return core.IssueSearchRead{}, fmt.Errorf("search issues: %w", err)
	}
	return core.IssueSearchRead{Results: results, Retry: info}, nil
}

// CountIssues implements core.JiraClient: an ESTIMATED count of jql's
// matches, from the dedicated POST /rest/api/3/search/approximate-count
// endpoint (core.JiraClient.CountIssues's own doc comment explains why
// this is a separate seam, and why the number can only ever be an
// estimate). Same POST-for-a-read posture as SearchIssues, for the
// identical reason — a JQL body can outgrow a GET's query string —
// which is exactly the allowance retry.go's searchApproximateCountPath
// exists for; this handler builds that literal path so the allowance
// and the actual request can never drift apart. Read-only mode does not
// block it: isRead (retry.go) classifies this path's POST as a read,
// same as SearchIssues', so no separate carve-out is needed here.
func (c *Client) CountIssues(ctx context.Context, jql string) (core.IssueCountRead, error) {
	body := wire.SearchCountRequest{JQL: jql}
	req, err := c.newRequest(ctx, http.MethodPost, []string{"rest", "api", "3", "search", "approximate-count"}, nil, body)
	if err != nil {
		return core.IssueCountRead{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.IssueCountRead{}, fmt.Errorf("count issues: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusOK {
		return core.IssueCountRead{}, c.apiError(resp, info)
	}
	var results wire.SearchCountResults
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return core.IssueCountRead{}, fmt.Errorf("count issues: decode response: %w", err)
	}
	return core.IssueCountRead{Count: results.Count, Retry: info}, nil
}

// GetFilter implements core.JiraClient: one saved filter by id, for the
// JQL a `--filter` selector resolves to.
//
// An ordinary GET, so read-only mode permits it with no carve-out. The
// 404 it can answer with is left to apiError and the taxonomy exactly as
// every other read's is — deliberately NOT special-cased here into "no
// such filter", because Jira answers 404 both for a filter that does not
// exist and for one the caller cannot see (core.JiraClient.GetFilter's
// own doc comment). The command layer words that refusal, where it can
// name the flag.
func (c *Client) GetFilter(ctx context.Context, id string) (core.FilterRead, error) {
	req, err := c.newRequest(ctx, http.MethodGet, []string{"rest", "api", "3", "filter", id}, nil, nil)
	if err != nil {
		return core.FilterRead{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.FilterRead{}, fmt.Errorf("get filter: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusOK {
		return core.FilterRead{}, c.apiError(resp, info)
	}
	var filter wire.Filter
	if err := json.NewDecoder(resp.Body).Decode(&filter); err != nil {
		return core.FilterRead{}, fmt.Errorf("get filter: decode response: %w", err)
	}
	return core.FilterRead{Filter: &filter, Retry: info}, nil
}

// GetBoardConfiguration implements core.JiraClient: one board's
// configuration, for the filter id behind it.
func (c *Client) GetBoardConfiguration(ctx context.Context, boardID int64) (core.BoardConfigurationRead, error) {
	id := strconv.FormatInt(boardID, 10)
	req, err := c.newRequest(ctx, http.MethodGet, []string{"rest", "agile", "1.0", "board", id, "configuration"}, nil, nil)
	if err != nil {
		return core.BoardConfigurationRead{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.BoardConfigurationRead{}, fmt.Errorf("get board configuration: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode != http.StatusOK {
		return core.BoardConfigurationRead{}, c.apiError(resp, info)
	}
	var cfg wire.BoardConfiguration
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return core.BoardConfigurationRead{}, fmt.Errorf("get board configuration: decode response: %w", err)
	}
	return core.BoardConfigurationRead{Configuration: &cfg, Retry: info}, nil
}

// notifyQuery builds the notifyUsers query parameter, or nil when the
// caller wants the default.
//
// **Sent only when FALSE**, deliberately. notifyUsers defaults to true
// on both endpoints that take it, so spelling that out would add a
// parameter to every edit request in the CLI while changing nothing
// about any of them — the wire equivalent of §7's "an alias that changes
// nothing is a lie". A request that carries it is a request that means
// it.
func notifyQuery(notifyUsers bool) url.Values {
	if notifyUsers {
		return nil
	}
	return url.Values{"notifyUsers": {"false"}}
}

// requireEncrypted refuses to build a client that would send the
// credential in the clear.
//
// http is allowed only to this machine, where there is no network to
// intercept. Anything else must be https: Basic auth is the API token
// in base64, which is not encryption.
func requireEncrypted(base *url.URL) error {
	if base.Scheme == "https" {
		return nil
	}
	if base.Scheme == "http" && isLoopbackHost(base.Hostname()) {
		return nil
	}
	// Scheme only, never the URL. A base URL can carry userinfo, and
	// echoing one back is how a password reaches a terminal. New()
	// rejects that case before this runs — which is the reason to keep
	// this message narrow rather than a reason to widen it.
	return fmt.Errorf("jira client: %s is %w — the API token would cross the network in cleartext; use https (http is allowed only for a local instance)", base.Scheme, ErrInsecureScheme)
}

// isLoopbackHost reports whether host addresses this machine.
//
// Deliberately a second copy of config.isLoopback rather than a shared
// helper: an adapter must not import the config package, and the rule
// belongs to both layers independently — config decides what to accept,
// the transport decides what to send. Change one and look at the other.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// newRequest builds an authenticated request from path segments. A body,
// when present, is serialized through wire.MarshalNoEscape to preserve raw
// JSON string spelling. Bytes-backed bodies also hand net/http a GetBody,
// so a safe request stays replayable for retry.
func (c *Client) newRequest(ctx context.Context, method string, segments []string, query url.Values, body any) (*http.Request, error) {
	// Validate BEFORE JoinPath: it resolves dot segments and passes
	// slashes through, so by the time a URL exists the damage is already
	// encoded in it and the original intent is unrecoverable.
	if err := validatePathSegments(segments); err != nil {
		return nil, err
	}
	var reader io.Reader
	contentType := ""
	if body != nil {
		// wire.MarshalNoEscape, not a local encoder: the wire tier already
		// owns "encode without rewriting <, > and & inside a document we
		// captured verbatim", and its own comment explains why that
		// setting has to live at the innermost boundary. A second copy
		// here would be a second place to remember.
		raw, err := wire.MarshalNoEscape(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		reader = bytes.NewReader(raw)
		contentType = "application/json"
	}
	return c.newBodyRequest(ctx, method, segments, query, reader, contentType)
}

// newStreamRequest builds an authenticated request whose body is a
// STREAM rather than a marshaled value — the attachment upload's own
// constructor.
//
// It shares every safety property newRequest has: the same path
// validation, the same within-base assertion, the same read-only check
// on the built request. It does not buffer the body: the attachment pipe
// has no GetBody, and its write is already excluded from retry. A buffered
// reader can still receive GetBody from http.NewRequestWithContext.
//
// contentType is the caller's, because a multipart body's own boundary
// is part of it and only the multipart.Writer knows the boundary.
func (c *Client) newStreamRequest(ctx context.Context, method string, segments []string, body io.Reader, contentType string) (*http.Request, error) {
	req, err := c.newBodyRequest(ctx, method, segments, nil, body, contentType)
	if err != nil {
		return nil, err
	}
	// The one endpoint-specific header this client sets; see xsrfHeader.
	req.Header.Set(xsrfHeader, xsrfNoCheck)
	return req, nil
}

// maxErrorBodyBytes bounds how much of a failure body we read for the
// error envelope: enough for any real Jira error document, a cap against
// a proxy's runaway HTML page.
const maxErrorBodyBytes = 64 << 10

// APIError is a Jira response the CLI could not use: the status, Jira's
// structured complaint when it sent one, and what the retry loop did
// before giving up. internal/cli/errors.go maps it to the stable code and
// exit-code taxonomy via errors.AsType.
type APIError struct {
	// StatusCode is the HTTP status Jira answered with.
	StatusCode int
	// Messages carries Jira's errorMessages[] and FieldErrors its
	// errors{} map (field → reason), both verbatim.
	Messages    []string
	FieldErrors map[string]string
	// RetryAfterSeconds echoes the Retry-After header when the server
	// sent one, so the caller can report or honor the pause itself.
	RetryAfterSeconds int
	// Retry reports the retry loop's attempts and total backoff.
	Retry core.RetryInfo
}

func (e *APIError) Error() string {
	msg := "jira: " + xhttp.Status(e.StatusCode)
	if len(e.Messages) > 0 {
		msg += ": " + e.Messages[0]
	}
	return msg
}

// HTTPStatusCode exposes response classification without coupling consumers to
// the concrete REST adapter or converting malformed replies into auth refusals.
func (e *APIError) HTTPStatusCode() int { return e.StatusCode }

// apiError builds the typed error for a non-2xx response. A body that is
// not Jira's error envelope — an HTML error page, an empty body — is
// fine; the status alone types the failure.
func (c *Client) apiError(resp *http.Response, info core.RetryInfo) error {
	apiErr := &APIError{
		StatusCode:        resp.StatusCode,
		RetryAfterSeconds: int(retryAfterDelay(resp.Header.Get("Retry-After"), c.clock.Now()) / time.Second),
		Retry:             info,
	}
	var body struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxErrorBodyBytes)).Decode(&body); err == nil {
		apiErr.Messages = body.ErrorMessages
		apiErr.FieldErrors = body.Errors
	}
	return apiErr
}
