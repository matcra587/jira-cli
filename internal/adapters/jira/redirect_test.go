package jira

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// redirectTransport executes net/http's redirect machinery without external
// DNS or TLS. Requests still pass through Client.New's real redirect policy.
type redirectTransport func(*http.Request) (*http.Response, error)

func (f redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestRedirectCredentialsStayWithinBase(t *testing.T) {
	t.Parallel()
	const base = "https://example.invalid/ex/jira/cloud-a"
	tests := []struct {
		name   string
		target string
		retain bool
	}{
		{name: "same API scope", target: base + "/rest/api/3/myself", retain: true},
		{name: "another port", target: "https://example.invalid:9443/ex/jira/cloud-a/rest/api/3/myself"},
		{name: "subdomain", target: "https://media.example.invalid/ex/jira/cloud-a/rest/api/3/myself"},
		{name: "another host", target: "https://media.invalid/content"},
		{name: "another cloud", target: "https://example.invalid/ex/jira/cloud-b/rest/api/3/myself"},
		{name: "prefix lookalike", target: base + "-other/rest/api/3/myself"},
		{name: "escaped prefix", target: "https://example.invalid/ex/%6aira/cloud-a/rest/api/3/myself"},
	}
	for _, tt := range tests {
		for _, override := range []bool{false, true} {
			name := tt.name
			if override {
				name += "/caller policy adds headers"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				var hits int
				var authorization, proxyAuthorization, cookie, referer bool
				transport := redirectTransport(func(req *http.Request) (*http.Response, error) {
					hits++
					status := http.StatusOK
					headers := http.Header{}
					if hits == 1 {
						if req.Header.Get("Authorization") == "" {
							t.Error("initial authenticated request has no authorization")
						}
						status = http.StatusFound
						headers.Set("Location", tt.target)
					} else {
						authorization = req.Header.Get("Authorization") != ""
						proxyAuthorization = req.Header.Get("Proxy-Authorization") != ""
						cookie = req.Header.Get("Cookie") != ""
						referer = req.Header.Get("Referer") != ""
					}
					return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
				})
				hc := &http.Client{Transport: transport}
				if override {
					hc.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
						req.SetBasicAuth("probe@example.com", "test-only")
						req.Header.Set("Proxy-Authorization", "test-only")
						req.Header.Set("Cookie", "test-only")
						req.Header.Set("Referer", base+"/private-query")
						return nil
					}
				}
				c := newTestClient(t, base, hc)
				req, err := c.newRequest(t.Context(), http.MethodGet, []string{"rest", "api", "3", "issue", "ABC-1"}, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Proxy-Authorization", "test-only")
				req.Header.Set("Cookie", "test-only")
				resp, err := c.http.Do(req)
				if err != nil {
					t.Fatalf("safe redirect: %v", err)
				}
				defer resp.Body.Close() //nolint:errcheck // test read-only body
				if hits != 2 {
					t.Fatalf("requests = %d, want one initial request and one redirect", hits)
				}
				if authorization != tt.retain || proxyAuthorization != tt.retain || cookie != tt.retain || referer != tt.retain {
					t.Errorf("redirect headers present: authorization=%t proxy=%t cookie=%t referer=%t; want each %t", authorization, proxyAuthorization, cookie, referer, tt.retain)
				}
			})
		}
	}
}

func TestRedirectReadPOSTCannotBecomeWrite(t *testing.T) {
	t.Parallel()
	const base = "https://example.invalid/proxy"
	tests := []struct {
		name   string
		target string
		allow  bool
	}{
		{name: "same read endpoint", target: base + "/rest/api/3/search/jql", allow: true},
		{name: "another read endpoint", target: base + "/rest/api/3/search/approximate-count", allow: true},
		{name: "write endpoint", target: base + "/rest/api/3/issue"},
		{name: "another host", target: "https://other.invalid/proxy/rest/api/3/search/jql"},
		{name: "outside proxy", target: "https://example.invalid/rest/api/3/search/jql"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var hits int
			transport := redirectTransport(func(req *http.Request) (*http.Response, error) {
				hits++
				status := http.StatusOK
				headers := http.Header{}
				if hits == 1 {
					status = http.StatusTemporaryRedirect
					headers.Set("Location", tt.target)
				}
				return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
			})
			c := newTestClient(t, base, &http.Client{Transport: transport})
			c.readOnly = true
			req, err := c.newRequest(t.Context(), http.MethodPost, []string{"rest", "api", "3", "search", "jql"}, nil, map[string]string{"jql": "project = ABC"})
			if err != nil {
				t.Fatal(err)
			}
			resp, err := c.http.Do(req)
			if err == nil {
				defer resp.Body.Close() //nolint:errcheck // test read-only body
			}
			if tt.allow {
				if err != nil || hits != 2 {
					t.Fatalf("safe read redirect: err=%v requests=%d, want two requests", err, hits)
				}
			} else if err == nil || hits != 1 {
				t.Fatalf("unsafe read POST redirect: refused=%t requests=%d, want refusal before a second request", err != nil, hits)
			}
		})
	}
}

func TestRedirectPolicyCannotWeakenGuards(t *testing.T) {
	t.Parallel()
	const base = "https://example.invalid/proxy"
	userinfoURL := &url.URL{
		Scheme: "https",
		Host:   "example.invalid",
		Path:   "/proxy/rest/api/3/myself",
		User:   url.UserPassword("redirect-user", "redirect-password"),
	}
	tests := []struct {
		name   string
		method string
		target string
		change func(*http.Request)
	}{
		{name: "server userinfo", method: http.MethodGet, target: userinfoURL.String()},
		{name: "caller userinfo", method: http.MethodGet, change: func(req *http.Request) {
			req.URL.User = url.UserPassword("redirect-user", "redirect-password")
		}},
		{name: "caller cleartext", method: http.MethodGet, change: func(req *http.Request) {
			req.URL.Scheme = "http"
		}},
		{name: "caller write", method: http.MethodGet, change: func(req *http.Request) {
			req.Method = http.MethodPost
			req.URL.Path = "/proxy/rest/api/3/issue"
		}},
		{name: "caller moves read POST off-site", method: http.MethodPost, change: func(req *http.Request) {
			req.URL.Host = "other.invalid"
		}},
		{name: "caller moves GET body off-site", method: http.MethodGet, change: func(req *http.Request) {
			req.URL.Host = "other.invalid"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var hits int
			transport := redirectTransport(func(req *http.Request) (*http.Response, error) {
				hits++
				target := tt.target
				if target == "" {
					target = base + "/rest/api/3/search/jql"
				}
				return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": {target}}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
			})
			c := newTestClient(t, base, &http.Client{
				Transport: transport,
				CheckRedirect: func(req *http.Request, _ []*http.Request) error {
					if tt.change != nil {
						tt.change(req)
					}
					return nil
				},
			})
			req, err := c.newRequest(t.Context(), tt.method, []string{"rest", "api", "3", "search", "jql"}, nil, map[string]string{"jql": "project = ABC"})
			if err != nil {
				t.Fatal(err)
			}
			resp, err := c.http.Do(req)
			if err == nil {
				resp.Body.Close() //nolint:errcheck // unexpected success; close the test body
			}
			if err == nil || hits != 1 {
				t.Fatalf("redirect guard: refused=%t requests=%d, want refusal before a second request", err != nil, hits)
			}
		})
	}
}

func TestRedirectGuardsEveryHop(t *testing.T) {
	t.Parallel()
	const base = "https://example.invalid/proxy"
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			var hits int
			transport := redirectTransport(func(req *http.Request) (*http.Response, error) {
				hits++
				status := http.StatusTemporaryRedirect
				target := base + "/rest/api/3/search/approximate-count"
				switch hits {
				case 2:
					if method == http.MethodPost {
						target = base + "/rest/api/3/issue"
					} else {
						target = "https://media.example.invalid/content"
					}
				case 3:
					status = http.StatusOK
					if req.Header.Get("Authorization") != "" {
						t.Error("credential reached the third-hop media host")
					}
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Location": {target}}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
			})
			c := newTestClient(t, base, &http.Client{Transport: transport})
			var body any
			if method == http.MethodPost {
				body = map[string]string{"jql": "project = ABC"}
			}
			req, err := c.newRequest(t.Context(), method, []string{"rest", "api", "3", "search", "jql"}, nil, body)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := c.http.Do(req)
			if err == nil {
				defer resp.Body.Close() //nolint:errcheck // test read-only body
			}
			if method == http.MethodGet {
				if err != nil || hits != 3 {
					t.Fatalf("media redirect chain: err=%v requests=%d, want three requests", err, hits)
				}
			} else if err == nil || hits != 2 {
				t.Fatalf("read POST redirect chain: refused=%t requests=%d, want refusal before a third request", err != nil, hits)
			}
		})
	}
}
