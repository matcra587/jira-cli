package jira

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

// maxRedirectRequests preserves net/http's default ten-request chain bound
// after installing our own policy. Caller policy may refuse sooner but cannot
// allow an unbounded chain. Verified against Go 1.27.1 net/http/client.go.
const maxRedirectRequests = 10

// scopedCookieJar contains both directions of jar state within the configured
// origin and API prefix. net/http's jar lookup happens after CheckRedirect, so
// deleting copied Cookie headers cannot establish this boundary by itself.
type scopedCookieJar struct {
	base *url.URL
	jar  http.CookieJar
}

func (j scopedCookieJar) Cookies(u *url.URL) []*http.Cookie {
	if !withinBase(j.base, u) {
		return nil
	}
	return j.jar.Cookies(u)
}

func (j scopedCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if withinBase(j.base, u) {
		j.jar.SetCookies(u, cookies)
	}
}

// guardRedirect checks both the original operation and its next destination.
// A safe POST cannot turn into a write, and request bodies stay in API scope.
// Redirect userinfo is refused rather than replacing the stored credential.
func (c *Client) guardRedirect(req, first *http.Request) error {
	// Go 1.27 looks up jar cookies using req.Host when it is set. A caller
	// callback must not name our authority there while routing the URL elsewhere.
	if req.Host != "" && req.Host != req.URL.Host {
		return fmt.Errorf("%w: redirect Host does not match its URL authority", ErrUnsafeRedirect)
	}
	if req.URL.User != nil {
		return fmt.Errorf("%w: refusing to follow a redirect with embedded credentials", ErrUnsafeRedirect)
	}
	if err := requireEncrypted(req.URL); err != nil {
		return fmt.Errorf("refusing to follow a redirect to %s: %w", req.URL.Redacted(), err)
	}
	if !c.safeToRetry(first) {
		return fmt.Errorf("%w: refusing to follow a redirect for %s %s: replaying a write can duplicate it", ErrUnsafeRedirect, first.Method, first.URL.Path)
	}
	if !c.safeToRetry(req) {
		return fmt.Errorf("%w: refusing to follow a redirect to %s %s: the destination is not a read", ErrUnsafeRedirect, req.Method, req.URL.Path)
	}
	if (first.Method == http.MethodPost || (first.Body != nil && first.Body != http.NoBody)) && !withinBase(c.base, req.URL) {
		return fmt.Errorf("%w: refusing to follow a redirect carrying a request body outside the configured API scope", ErrUnsafeRedirect)
	}
	return nil
}

// ErrReadOnly marks a write refused because the client is in read-only
// mode. ErrUnsafePath marks a path segment rejected before it could
// reach the wire. Both are sentinels so the command layer can give them
// their own taxonomy codes without importing this package's types.
var (
	ErrReadOnly   = errors.New("read-only mode: write blocked")
	ErrUnsafePath = errors.New("unsafe path segment")
	// ErrUnsafeRedirect is a server-directed request refused by the safety
	// policy. The original request may have reached Jira before this refusal.
	ErrUnsafeRedirect = errors.New("unsafe redirect")
	// ErrInsecureScheme marks a base URL, or a redirect target, that
	// would carry the credential in cleartext.
	ErrInsecureScheme = errors.New("not encrypted")
)

// validatePathSegment rejects a segment that would mean something other
// than "one literal path element".
//
// This is the transport's half of "the agent is not a trusted operator".
// Segments are built from values an agent may have taken straight out of
// Jira data or a model's own output, and url.URL.JoinPath does NOT make
// them inert:
//
//   - JoinPath RESOLVES dot segments, so {"issue", "..", "..", "admin"}
//     leaves /rest/api/3/issue entirely and lands on /rest/admin. The
//     caller asked for an issue and addressed something else.
//   - JoinPath does NOT escape "/", so a single segment "a/b" silently
//     becomes two path elements — a different endpoint, no error.
//   - Percent-encoded dots ("%2e%2e") pass through verbatim for the
//     SERVER to decode, which is where the traversal then happens.
//
// Rejecting is deliberately blunt: every case here is a value that was
// never a legitimate single path element, so there is nothing to
// sanitize into. A caller that genuinely needs a slash or a query is
// asking for a different endpoint or the query parameter, not this.
func validatePathSegment(seg string) error {
	if seg == "" {
		return fmt.Errorf("%w: empty", ErrUnsafePath)
	}
	if seg == "." || seg == ".." {
		return fmt.Errorf("%w: %q resolves to a different path", ErrUnsafePath, seg)
	}
	for _, r := range seg {
		// C0, DEL and C1. A control character in a URL is either an
		// encoding mistake or an attempt at request smuggling; neither is
		// a resource identifier.
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return fmt.Errorf("%w: %q contains a control character", ErrUnsafePath, seg)
		}
	}
	// "/" and "\" would add path elements; "?" and "#" would start a query
	// or fragment. JoinPath escapes some of these and not others, so the
	// check does not depend on which.
	if i := strings.IndexAny(seg, `/\?#`); i >= 0 {
		return fmt.Errorf("%w: %q contains %q", ErrUnsafePath, seg, seg[i:i+1])
	}
	// Any percent sign at all. Callers pass RAW values and JoinPath does
	// the escaping, so a "%" here means the value was pre-encoded — and
	// pre-encoded input is where this gets dangerous, in three ways a
	// blocklist of specific sequences cannot cover:
	//
	//   - "a%2fb" decodes to a path SEPARATOR: the segment becomes two.
	//   - "a%252e%252e" is double-encoded; it contains no "%2e" for a
	//     blocklist to find, yet decodes toward ".." one hop later.
	//   - "a%b" is a malformed escape, and JoinPath answers by silently
	//     DROPPING the whole segment — the request then addresses a
	//     different resource with no error anywhere.
	//
	// Rejecting the introducer is the complete rule the enumeration of
	// bad sequences was only approximating.
	if strings.ContainsRune(seg, '%') {
		return fmt.Errorf("%w: %q is percent-encoded; pass the raw value and let the encoder escape it", ErrUnsafePath, seg)
	}
	// A segment carrying a scheme would be read as an absolute URL by
	// something downstream, redirecting the call off-site.
	if strings.Contains(strings.ToLower(seg), "://") {
		return fmt.Errorf("%w: %q looks like an absolute URL", ErrUnsafePath, seg)
	}
	// Invalid UTF-8 can carry overlong encodings of characters checked
	// for above, so a byte sequence that is not text is not an identifier.
	if !utf8.ValidString(seg) {
		return fmt.Errorf("%w: %q is not valid UTF-8", ErrUnsafePath, seg)
	}
	return nil
}

// withinBase reports whether joined still addresses something under
// base — same host, and a path that has not climbed out of the base
// prefix.
//
// Defense in depth, and the part that does not depend on predicting the
// attack: validatePathSegment enumerates what is known to be dangerous,
// while this asserts the property that actually matters. A segment that
// slips through the first check still cannot move the request off the
// site or above the configured prefix, which is what a proxy-prefixed
// deployment relies on — a traversal there escapes /proxy, not merely
// the endpoint.
func withinBase(base, joined *url.URL) bool {
	if joined.Host != base.Host || joined.Scheme != base.Scheme {
		return false
	}
	prefix := strings.TrimSuffix(base.EscapedPath(), "/")
	if prefix == "" {
		return true
	}
	got := joined.EscapedPath()
	return got == prefix || strings.HasPrefix(got, prefix+"/")
}

// validatePathSegments checks every segment, naming the first offender.
func validatePathSegments(segments []string) error {
	for _, seg := range segments {
		if err := validatePathSegment(seg); err != nil {
			return err
		}
	}
	return nil
}
