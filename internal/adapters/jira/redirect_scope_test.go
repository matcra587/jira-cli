package jira

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestRedirectChainIsBoundedWithoutReplay(t *testing.T) {
	t.Parallel()
	for _, override := range []bool{false, true} {
		t.Run(fmt.Sprintf("caller_policy=%t", override), func(t *testing.T) {
			t.Parallel()
			const base = "https://example.invalid"
			hits := 0
			transport := redirectTransport(func(req *http.Request) (*http.Response, error) {
				hits++
				status := http.StatusFound
				// Terminate even with the defect so the regression can fail rather
				// than waiting for the command's context to expire.
				if hits > 12 {
					status = http.StatusOK
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Location": {fmt.Sprintf("%s/rest/api/3/myself?page=%d", base, hits)}}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
			})
			hc := &http.Client{Transport: transport}
			if override {
				hc.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
			}
			c := newTestClient(t, base, hc)
			c.retry = core.DefaultRetryPolicy()
			req, err := c.newRequest(t.Context(), http.MethodGet, []string{"rest", "api", "3", "myself"}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, info, err := c.doWithRetry(req)
			if resp != nil && err == nil {
				defer resp.Body.Close() //nolint:errcheck // test response body
			}
			if !errors.Is(err, ErrUnsafeRedirect) || hits != 10 || info.Attempts != 1 {
				t.Fatalf("redirect chain: refused=%t requests=%d attempts=%d, want refusal at ten requests without replay", errors.Is(err, ErrUnsafeRedirect), hits, info.Attempts)
			}
		})
	}
}

func TestCookieJarStaysWithinAPIScope(t *testing.T) {
	t.Parallel()
	const base = "https://example.invalid/ex/jira/cloud-a"
	for _, tc := range []struct {
		name, target string
		retain       bool
	}{
		{name: "same scope", target: base + "/rest/api/3/myself", retain: true},
		{name: "another port", target: "https://example.invalid:9443/ex/jira/cloud-a/rest/api/3/myself"},
		{name: "another cloud", target: "https://example.invalid/ex/jira/cloud-b/rest/api/3/myself"},
		{name: "prefix lookalike", target: base + "-other/rest/api/3/myself"},
		{name: "outside prefix", target: "https://example.invalid/content"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			baseURL, err := url.Parse(base)
			if err != nil {
				t.Fatal(err)
			}
			jar.SetCookies(baseURL, []*http.Cookie{{Name: "fixture_session", Value: "original", Path: "/"}})
			hits := 0
			transport := redirectTransport(func(req *http.Request) (*http.Response, error) {
				hits++
				status := http.StatusOK
				headers := http.Header{}
				cookie, err := req.Cookie("fixture_session")
				if hits == 1 {
					if err != nil || cookie.Value != "original" {
						t.Error("initial request lost the in-scope jar cookie")
					}
					status = http.StatusFound
					headers.Set("Location", tc.target)
				} else {
					if (err == nil) != tc.retain {
						t.Errorf("jar cookie reached redirected request=%t, want %t", err == nil, tc.retain)
					}
					headers.Set("Set-Cookie", "fixture_session=injected; Path=/")
				}
				return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
			})
			hc := &http.Client{Transport: transport, Jar: jar}
			c := newTestClient(t, base, hc)
			req, err := c.newRequest(t.Context(), http.MethodGet, []string{"rest", "api", "3", "myself"}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := c.http.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close() //nolint:errcheck // test response body
			want := "original"
			if tc.retain {
				want = "injected"
			}
			cookies := jar.Cookies(baseURL)
			if len(cookies) != 1 || cookies[0].Value != want {
				t.Fatal("redirect response wrote a cookie outside its allowed scope")
			}
			if hc.Jar != jar || hits != 2 {
				t.Fatal("client copy mutated the caller's jar configuration or did not follow the safe read")
			}
		})
	}
}

func TestCallerHostOverrideCannotRouteJarCredentials(t *testing.T) {
	t.Parallel()
	const base = "https://example.invalid"
	baseURL, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(baseURL, []*http.Cookie{{Name: "fixture_session", Value: "original", Path: "/"}})
	hits := 0
	hc := &http.Client{Jar: jar, Transport: redirectTransport(func(req *http.Request) (*http.Response, error) {
		hits++
		status := http.StatusOK
		if hits == 1 {
			status = http.StatusFound
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Location": {"https://media.invalid/content"}}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
	}), CheckRedirect: func(req *http.Request, _ []*http.Request) error { req.Host = baseURL.Host; return nil }}
	c := newTestClient(t, base, hc)
	req, err := c.newRequest(t.Context(), http.MethodGet, []string{"rest", "api", "3", "myself"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.http.Do(req)
	if err == nil {
		defer resp.Body.Close() //nolint:errcheck // test response body
	}
	if !errors.Is(err, ErrUnsafeRedirect) || hits != 1 {
		t.Fatal("caller-controlled Host routed jar credentials to another URL authority")
	}
}

func TestRealRedirectDoesNotForwardCookieJarAcrossPorts(t *testing.T) {
	t.Parallel()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("credentials reached another port")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":"ABC-1"}`))
	}))
	t.Cleanup(target.Close)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") == "" {
			t.Error("initial request lost its cookie")
		}
		http.Redirect(w, r, target.URL+"/content", http.StatusFound)
	}))
	t.Cleanup(source.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(u, []*http.Cookie{{Name: "fixture_session", Value: "original", Path: "/"}})
	c := newTestClient(t, source.URL, &http.Client{Jar: jar})
	read, err := c.GetIssue(t.Context(), "ABC-1", nil)
	if err != nil || read.Issue == nil || read.Issue.Key.Value != "ABC-1" {
		t.Fatalf("cookie-free media read failed: %v", err)
	}
}
