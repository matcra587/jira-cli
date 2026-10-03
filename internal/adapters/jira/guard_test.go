package jira

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

// TestPathSegmentsAreRefusedBeforeTheWire pins the transport's half of
// "the agent is not a trusted operator". Segments are built from values
// an agent may have lifted out of Jira data or produced itself, and
// url.URL.JoinPath does not make them inert — it RESOLVES dot segments
// and passes "/" through unescaped, so a value that looks like an
// identifier can silently address a different endpoint.
func TestPathSegmentsAreRefusedBeforeTheWire(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		seg  string
	}{
		{name: "parent traversal", seg: ".."},
		{name: "current directory", seg: "."},
		{name: "embedded separator splits the path", seg: "a/b"},
		{name: "backslash separator", seg: `a\b`},
		{name: "query starts here", seg: "ABC-1?expand=all"},
		{name: "fragment starts here", seg: "ABC-1#frag"},
		{name: "encoded dots the server will decode", seg: "%2e%2e"},
		{name: "encoded separator", seg: "a%2fb"},
		{name: "encoded backslash", seg: "a%5cb"},
		{name: "null byte", seg: "a%00b"},
		{name: "newline", seg: "a\nb"},
		{name: "carriage return", seg: "a\rb"},
		{name: "tab", seg: "a\tb"},
		{name: "del", seg: "a\x7fb"},
		{name: "absolute url", seg: "https://evil.example/x"},
		{name: "empty", seg: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := validatePathSegment(tt.seg); !errors.Is(err, ErrUnsafePath) {
				t.Errorf("validatePathSegment(%q) = %v, want ErrUnsafePath", tt.seg, err)
			}
		})
	}
}

// TestOrdinaryIdentifiersPass keeps the guard from being a blanket
// refusal — every shape a real Jira path element takes must survive.
func TestOrdinaryIdentifiersPass(t *testing.T) {
	t.Parallel()
	for _, seg := range []string{
		"rest", "api", "3", "issue", "ABC-1", "10002",
		"customfield_10011", "search", "jql",
		"user@example.com", "a.b.c", "with spaces", "unicode-café",
		"557058:f58131cb-b67d-43c7-b30d-6b58d40bd077", // an account id
	} {
		t.Run(seg, func(t *testing.T) {
			t.Parallel()
			if err := validatePathSegment(seg); err != nil {
				t.Errorf("validatePathSegment(%q) = %v, want nil — this is a real path element", seg, err)
			}
		})
	}
}

// TestTraversalWouldEscapeWithoutTheGuard demonstrates WHY the guard
// exists rather than asserting it in the abstract: the same segments,
// joined the way the client joins them, leave the intended prefix
// entirely. If this ever stops being true the guard's rationale is stale
// and should be re-argued, not quietly kept.
func TestTraversalWouldEscapeWithoutTheGuard(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, "https://example.atlassian.net", nil)
	escaped := c.base.JoinPath("rest", "api", "3", "issue", "..", "..", "..", "admin", "secrets").String()
	if !strings.HasSuffix(escaped, "/rest/admin/secrets") {
		t.Fatalf("JoinPath no longer resolves dot segments (got %s); re-argue the guard rather than deleting it", escaped)
	}
	if err := validatePathSegments([]string{"rest", "api", "3", "issue", "..", "admin"}); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("the guard does not stop what JoinPath demonstrably allows")
	}
}

// TestReadOnlyBlocksWritesAtTheTransport pins the guarantee that makes
// read-only worth having: it holds at the TRANSPORT, so a command that
// forgets to check — including a passthrough like `jira api` — still
// cannot mutate Jira. Reads must keep working, or the mode is just an
// outage.
func TestReadOnlyBlocksWritesAtTheTransport(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		method    string
		segments  []string
		wantBlock bool
	}{
		{name: "GET is a read", method: http.MethodGet, segments: []string{"rest", "api", "3", "issue", "ABC-1"}},
		{name: "HEAD is a read", method: http.MethodHead, segments: []string{"rest", "api", "3", "myself"}},
		{name: "OPTIONS is a read", method: http.MethodOptions, segments: []string{"rest", "api", "3", "myself"}},
		{name: "search POST is the whitelisted read", method: http.MethodPost, segments: []string{"rest", "api", "3", "search", "jql"}},
		{name: "POST elsewhere is a write", method: http.MethodPost, segments: []string{"rest", "api", "3", "issue"}, wantBlock: true},
		{name: "PUT is a write", method: http.MethodPut, segments: []string{"rest", "api", "3", "issue", "ABC-1"}, wantBlock: true},
		{name: "DELETE is a write", method: http.MethodDelete, segments: []string{"rest", "api", "3", "issue", "ABC-1"}, wantBlock: true},
		{name: "PATCH is a write", method: http.MethodPatch, segments: []string{"rest", "api", "3", "issue", "ABC-1"}, wantBlock: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newTestClient(t, "https://example.atlassian.net", nil)
			c.readOnly = true
			_, err := c.newRequest(t.Context(), tt.method, tt.segments, nil, nil)
			if tt.wantBlock {
				if !errors.Is(err, ErrReadOnly) {
					t.Errorf("%s %v: err = %v, want ErrReadOnly", tt.method, tt.segments, err)
				}
				return
			}
			if err != nil {
				t.Errorf("%s %v: err = %v, want the read to be allowed", tt.method, tt.segments, err)
			}
		})
	}
}

// TestReadOnlyOffLetsWritesThrough keeps the gate from being a
// permanent block: with the mode off, a write builds normally.
func TestReadOnlyOffLetsWritesThrough(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, "https://example.atlassian.net", nil)
	if _, err := c.newRequest(t.Context(), http.MethodPost, []string{"rest", "api", "3", "issue"}, nil, nil); err != nil {
		t.Errorf("err = %v, want a write to build when read-only is off", err)
	}
}

// TestReadOnlyGateSitsBeforeTheNetwork proves nothing is sent. A blocked
// write that still reached Jira would be the worst possible outcome:
// refused locally, performed remotely.
func TestReadOnlyGateSitsBeforeTheNetwork(t *testing.T) {
	t.Parallel()
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv.URL, srv.Client())
	c.readOnly = true
	if _, err := c.newRequest(t.Context(), http.MethodDelete, []string{"rest", "api", "3", "issue", "ABC-1"}, nil, nil); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("err = %v, want ErrReadOnly", err)
	}
	if hits != 0 {
		t.Errorf("the server saw %d request(s); a blocked write must never leave the process", hits)
	}
}

// newTestClient builds a Client against base with retry disabled.
func newTestClient(t *testing.T, base string, hc *http.Client) *Client {
	t.Helper()
	c, err := New(Config{
		BaseURL:    base,
		Email:      "probe@example.com",
		APIToken:   "token",
		HTTPClient: hc,
		Retry:      core.RetryPolicy{},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

// TestNewRequestValidatesItsSegments pins that the client actually CALLS
// the path guard. Testing validatePathSegment alone proves the rule is
// correct, not that anything applies it: deleting the call from
// newRequest left every other test in this file green, which is the
// gap this closes.
func TestNewRequestValidatesItsSegments(t *testing.T) {
	t.Parallel()
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv.URL, srv.Client())
	// A traversal reaches the guard through the same door every command
	// uses, and is refused before a URL is ever built.
	_, err := c.newRequest(t.Context(), http.MethodGet, []string{"rest", "api", "3", "issue", "..", "..", "admin"}, nil, nil)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("err = %v, want ErrUnsafePath — newRequest must apply the guard, not merely have one", err)
	}
	if hits != 0 {
		t.Errorf("the server saw %d request(s); a rejected path must never leave the process", hits)
	}

	// And a legitimate identifier still builds.
	req, err := c.newRequest(t.Context(), http.MethodGet, []string{"rest", "api", "3", "issue", "ABC-1"}, nil, nil)
	if err != nil {
		t.Fatalf("err = %v, want an ordinary path to build", err)
	}
	if !strings.HasSuffix(req.URL.Path, "/rest/api/3/issue/ABC-1") {
		t.Errorf("path = %q, want it to end with the requested resource", req.URL.Path)
	}
}

// FuzzAcceptedSegmentsStayUnderBase asserts the property the enumerated
// cases only approximate: ANY segment the validator accepts must still
// resolve under the configured base.
//
// The blocklist can only reject what someone thought of — it originally
// missed double-encoding ("%252e%252e" contains no "%2e") and malformed
// escapes ("a%b", which JoinPath answers by dropping the segment
// entirely). A property test does not need the attack enumerated first;
// it fails on whatever the fuzzer finds.
func FuzzAcceptedSegmentsStayUnderBase(f *testing.F) {
	for _, seed := range []string{
		"ABC-1", "..", ".", "a/b", "%2e%2e", "%252e%252e", "a%b",
		"https://evil.example/x", "\x00", "café", "a\\b", "..%2f..",
	} {
		f.Add(seed)
	}
	// A proxy prefix, because that is where a traversal has somewhere to
	// climb to: on a bare host any path is trivially "under" the root.
	base, err := url.Parse("https://example.atlassian.net/proxy")
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, seg string) {
		if err := validatePathSegment(seg); err != nil {
			return // rejected: nothing to prove
		}
		joined := base.JoinPath("rest", "api", "3", seg)
		if !withinBase(base, joined) {
			t.Fatalf("accepted segment %q escaped the base: %s", seg, joined)
		}
		// An accepted segment must also survive as ONE element: if it
		// split or vanished, the request addresses something the caller
		// never asked for.
		if got := strings.Count(joined.EscapedPath(), "/"); got != strings.Count(base.EscapedPath()+"/rest/api/3/x", "/") {
			t.Fatalf("accepted segment %q changed the path shape: %s", seg, joined.EscapedPath())
		}
	})
}

// TestClientRefusesCredentialsInBaseURL pins that the transport neither
// accepts a base URL carrying userinfo nor repeats it. Config rejects
// such a URL first; this is the layer that must not leak it if anything
// ever arrives without passing through config.
func TestClientRefusesCredentialsInBaseURL(t *testing.T) {
	t.Parallel()
	_, err := New(Config{
		BaseURL:  "https://user:hunter2@example.atlassian.net", // betterleaks:allow -- synthetic URL used to test credential rejection and redaction.
		Email:    "probe@example.com",
		APIToken: "token",
	})
	if err == nil {
		t.Fatal("New accepted a base url with embedded credentials")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error leaked the password: %v", err)
	}
}
