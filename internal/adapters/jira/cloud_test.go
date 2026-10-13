package jira

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

func TestScopedClientKeepsSiteIdentityAndGatewayRequests(t *testing.T) {
	t.Parallel()
	for _, site := range []string{"https://example.invalid/", "https://example.invalid/proxy", "https://example.invalid/proxy?public=1#view"} {
		t.Run(site, func(t *testing.T) {
			t.Parallel()
			calls := 0
			transport := redirectTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				if request.URL.String() != "https://api.atlassian.com/ex/jira/fixture-cloud/rest/api/3/myself" {
					t.Fatal("request escaped the fixed selected gateway", request.URL)
				}
				email, token, ok := request.BasicAuth()
				if !ok || email != "person@example.invalid" || token != "synthetic-scoped-token" {
					t.Fatal("gateway lost the exact verified credential")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"accountId":"fixture-account"}`)), Request: request}, nil
			})
			client, err := New(Config{BaseURL: site, CloudID: "fixture-cloud", Email: "person@example.invalid", APIToken: "synthetic-scoped-token", HTTPClient: &http.Client{Transport: transport}})
			if err != nil {
				t.Fatal(err)
			}
			if client.BaseURL() != site || client.Account() != "person@example.invalid" || calls != 0 {
				t.Fatal("API routing replaced browser/site identity")
			}
			if _, err := client.CurrentUser(t.Context()); err != nil || calls != 1 {
				t.Fatal("scoped identity request failed", err)
			}
		})
	}
}

func TestCurrentUserCannotVerifyOutsideAuthenticationScope(t *testing.T) {
	t.Parallel()
	for _, cloud := range []string{"", "fixture-cloud"} {
		t.Run(map[bool]string{true: "scoped", false: "classic"}[cloud != ""], func(t *testing.T) {
			t.Parallel()
			calls := 0
			transport := redirectTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://api.atlassian.com/ex/jira/another-cloud/rest/api/3/myself"}}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
				}
				if request.Header.Get("Authorization") != "" {
					t.Fatal("credential followed a cross-scope redirect")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"accountId":"unverified-account"}`)), Request: request}, nil
			})
			client, err := New(Config{BaseURL: "https://example.invalid", CloudID: cloud, Email: "person@example.invalid", APIToken: "synthetic-scoped-token", HTTPClient: &http.Client{Transport: transport}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.CurrentUser(t.Context()); !errors.Is(err, ErrCurrentUserResponseInvalid) || calls != 2 {
				t.Fatal("unauthenticated redirected reply established a verified account", err)
			}
		})
	}
}

func TestPublicCloudDiscoveryIsCredentialCookieAndRedirectFree(t *testing.T) {
	t.Parallel()
	for _, redirect := range []bool{false, true} {
		t.Run(map[bool]string{false: "metadata", true: "redirect refused"}[redirect], func(t *testing.T) {
			t.Parallel()
			calls := 0
			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			site, err := url.Parse("https://example.invalid")
			if err != nil {
				t.Fatal(err)
			}
			jar.SetCookies(site, []*http.Cookie{{Name: "fixture_session", Value: "synthetic-private-cookie", Path: "/"}})
			transport := redirectTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				if request.URL.String() != "https://example.invalid/_edge/tenant_info" || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" || request.Header.Get("Proxy-Authorization") != "" {
					t.Fatal("public discovery leaked credentials/cookies or changed site")
				}
				status, body, headers := 200, `{"cloudId":" fixture-cloud ","future":{"retained":true}}`, http.Header{}
				if redirect {
					status = 302
					headers.Set("Location", "https://other.invalid/_edge/tenant_info")
				}
				return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})
			caller := &http.Client{Transport: transport, Jar: jar}
			client, err := New(Config{BaseURL: site.String(), Email: "person@example.invalid", APIToken: "synthetic-discovery-token", HTTPClient: caller})
			if err != nil {
				t.Fatal(err)
			}
			id, err := client.DiscoverCloudID(t.Context())
			if redirect {
				if !errors.Is(err, ErrCloudIDResponseInvalid) || id != "" {
					t.Fatal("redirect supplied a tenant identity", err)
				}
			} else if err != nil || id != "fixture-cloud" {
				t.Fatal("public cloud metadata did not resolve", err)
			}
			if calls != 1 || caller.Jar != jar || caller.CheckRedirect != nil || len(jar.Cookies(site)) != 1 {
				t.Fatal("discovery followed redirects or mutated caller state")
			}
		})
	}
}

func TestPublicCloudDiscoveryRejectsAmbiguousInvalidAndOversizedMetadata(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`null`, `[]`, `{}`, `{"cloudId":null}`, `{"cloudId":12}`, `{"cloudId":""}`, `{"cloudId":"."}`, `{"cloudId":".."}`, `{"cloudId":"cloud/other"}`, `{"cloudId":"first","cloudId":"second"}`, `{"cloudId":"fixture"}{}`, `{"cloudId":"fixture","private":"` + strings.Repeat("x", maxCloudIDResponseBytes) + `"}`} {
		t.Run("invalid metadata", func(t *testing.T) {
			t.Parallel()
			client, err := New(Config{BaseURL: "https://example.invalid", Email: "person@example.invalid", APIToken: "synthetic-discovery-token", HTTPClient: &http.Client{Transport: redirectTransport(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			if id, err := client.DiscoverCloudID(t.Context()); id != "" || !errors.Is(err, ErrCloudIDResponseInvalid) {
				t.Fatal("unusable public metadata became a route", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client, err := New(Config{BaseURL: "https://example.invalid", Email: "person@example.invalid", APIToken: "synthetic-discovery-token", HTTPClient: &http.Client{Transport: redirectTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("canceled discovery reached transport")
		return nil, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.DiscoverCloudID(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation was discarded", err)
	}
}
