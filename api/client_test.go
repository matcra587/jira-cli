package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/api"
)

const syntheticToken = "synthetic-public-api-credential"

type blockedStore struct{}

func (blockedStore) Get(context.Context, string) (string, error) {
	panic("unexpected credential read")
}

func (blockedStore) Set(context.Context, string, string) error {
	panic("unexpected credential write")
}

func (blockedStore) Delete(context.Context, string) error {
	panic("unexpected credential deletion")
}

func explicitClient(t *testing.T, server *httptest.Server, configure func(*api.ClientOptions)) *api.Client {
	t.Helper()
	opts := api.ClientOptions{Credentials: &api.Credentials{
		BaseURL: server.URL, Email: "reader@example.invalid", APIToken: syntheticToken,
	}, HTTPClient: server.Client(), Retry: &api.RetryPolicy{}}
	if configure != nil {
		configure(&opts)
	}
	client, err := api.NewClientWithContext(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestExplicitClientBypassesSavedAuthAndPreservesNativeBytes(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	query := url.Values{"field": {"one", "two"}, "literal": {"a+b & c"}}
	body := []byte(`{"fields":{"customfield_1":9007199254740993,"description":null},"properties":[]}`)
	reply := []byte(`{"id":"10001","unknown":9007199254740993,"explicit":null,"zero":0}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		email, value, ok := r.BasicAuth()
		got, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		expectedIdentity := email == "reader@example.invalid" && value == syntheticToken
		if !ok || !expectedIdentity {
			t.Error("native request changed authentication")
		}
		if r.Method != "POST" || r.URL.Path != "/rest/api/3/issue" {
			t.Error("native request changed method or path")
		}
		if !reflect.DeepEqual(r.URL.Query(), query) || string(got) != string(body) {
			t.Error("native request changed literal query or body")
		}
		if r.Header.Get("If-Match") != `"version-1"` {
			t.Error("native request lost precondition")
		}
		w.Header().Set("Set-Cookie", "synthetic-private-cookie")
		w.Header().Set("Authorization", "synthetic-private-response-auth")
		w.WriteHeader(http.StatusCreated)
		if _, err := w.Write(reply); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client := explicitClient(t, server, func(opts *api.ClientOptions) {
		opts.ConfigPath = filepath.Join(t.TempDir(), "must-not-be-opened.toml")
		opts.Profile = "must-not-be-selected"
		opts.CredentialStore = blockedStore{}
	})
	if calls.Load() != 0 {
		t.Fatal("construction contacted Jira")
	}
	response, err := client.Do(t.Context(), api.Request{
		Method: "POST", Endpoint: "/rest/api/3/issue", Query: query,
		Body: body, ContentType: "application/json", IfMatch: `"version-1"`,
	})
	if err != nil || response == nil {
		t.Fatalf("native reply lost: response=%+v error=%v", response, err)
	}
	if response.StatusCode != 201 || calls.Load() != 1 {
		t.Fatal("native reply status or request count changed", response.StatusCode, calls.Load())
	}
	if string(response.Body) != string(reply) || response.Truncated {
		t.Fatal("native reply body changed", response)
	}
	for name := range response.Headers {
		if strings.EqualFold(name, "Set-Cookie") || strings.EqualFold(name, "Authorization") {
			t.Fatal("private response header escaped")
		}
	}
	for _, printable := range []any{
		client,
		*client,
		&api.Credentials{APIToken: syntheticToken},
		api.ClientOptions{Credentials: &api.Credentials{APIToken: syntheticToken}},
	} {
		raw, err := json.Marshal(printable)
		if err != nil {
			t.Fatal(err)
		}
		printed := fmt.Sprintf(
			"%v %+v %#v",
			printable,
			printable,
			printable,
		)
		if strings.Contains(string(raw)+printed, syntheticToken) {
			t.Fatal("client/option formatting exposed credentials")
		}
	}
}

func TestClientCopiesHTTPPolicyAndRefusesWritesAndUnsafeRoutes(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(204)
	}))
	defer server.Close()
	original := server.Client()
	original.Timeout = 3 * time.Second
	client := explicitClient(t, server, func(opts *api.ClientOptions) {
		opts.HTTPClient, opts.Timeout, opts.ReadOnly = original, 200*time.Millisecond, true
	})
	if original.Timeout != 3*time.Second || original.CheckRedirect != nil {
		t.Fatal("constructor mutated caller HTTP client")
	}
	for _, tc := range []struct {
		name    string
		request api.Request
		code    string
	}{
		{name: "write", request: api.Request{Method: "DELETE", Endpoint: "/rest/api/3/issue/10001"}, code: "read_only"},
		{
			name:    "absolute URL",
			request: api.Request{Method: "GET", Endpoint: server.URL + "/rest/api/3/myself"},
			code:    "invalid_request",
		},
		{name: "traversal", request: api.Request{Method: "GET", Endpoint: "/rest/api/3/../myself"}, code: "invalid_request"},
		{name: "method", request: api.Request{Method: "TRACE", Endpoint: "/rest/api/3/myself"}, code: "invalid_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, err := client.Do(t.Context(), tc.request)
			if response != nil || calls.Load() != 0 {
				t.Fatal("refused request entered HTTP", err)
			}
			failure, ok := errors.AsType[*api.Error](err)
			if !ok || failure.Code != tc.code {
				t.Fatal("refusal lost its classification", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := client.Do(ctx, api.Request{Method: "GET", Endpoint: "/rest/api/3/myself"})
	if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatal("canceled call entered HTTP", err)
	}
	_, err = api.NewClientWithContext(ctx, api.ClientOptions{CredentialStore: blockedStore{}})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("canceled construction lost cancellation", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestScopedCloudClientKeepsSiteIdentityAndGatewayRoutingSeparate(t *testing.T) {
	t.Parallel()
	const cloud = "11111111-1111-4111-8111-111111111111"
	var calls atomic.Int32
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.URL.Scheme != "https" || request.URL.Host != "api.atlassian.com" {
			t.Error("cloud request escaped the gateway origin", request.URL)
		}
		if request.URL.Path != "/ex/jira/"+cloud+"/rest/api/3/myself" {
			t.Error("cloud request escaped the selected tenant", request.URL)
		}
		return &http.Response{
			StatusCode: 200, Header: http.Header{}, Request: request,
			Body: io.NopCloser(strings.NewReader(`{"accountId":"synthetic-account"}`)),
		}, nil
	})}
	client, err := api.NewClientWithContext(t.Context(), api.ClientOptions{
		Credentials: &api.Credentials{
			BaseURL: "https://site.example.invalid", CloudID: cloud,
			Email: "reader@example.invalid", APIToken: syntheticToken,
		},
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.Identity().BaseURL != "https://site.example.invalid" || client.Identity().CloudID != cloud {
		t.Fatal("cloud construction changed the public site identity", err)
	}
	response, err := client.Do(t.Context(), api.Request{Method: "GET", Endpoint: "/rest/api/3/myself"})
	if err != nil || response == nil {
		t.Fatal("scoped request failed", err)
	}
	if calls.Load() != 1 {
		t.Fatal("scoped request count changed", calls.Load())
	}
	_, err = api.NewClientWithContext(t.Context(), api.ClientOptions{
		Credentials: &api.Credentials{}, CredentialStore: blockedStore{},
	})
	if err == nil {
		t.Fatal("incomplete explicit credentials fell back to saved authentication")
	}
}

func TestSavedProfileProducesFreshBatchClients(t *testing.T) {
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "JIRA_") {
			t.Setenv(name, "")
		}
	}
	observed := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, value, _ := r.BasicAuth()
		observed <- value
		w.WriteHeader(204)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := fmt.Sprintf(`format_version=1
default_profile='work'
[profiles.work]
base_url=%q
email='reader@example.invalid'
secret_backend='env'
api_token_env='JIRA_TOKEN_PUBLIC_CLIENT_FIXTURE'
read_only=true
`, server.URL)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JIRA_TOKEN_PUBLIC_CLIENT_FIXTURE", "synthetic-old-token")
	opts := api.ClientOptions{ConfigPath: path, HTTPClient: server.Client()}
	first, err := api.NewClientWithContext(t.Context(), opts)
	if err != nil || first.Identity().Profile != "work" {
		t.Fatal("saved login unavailable", err)
	}
	t.Setenv("JIRA_TOKEN_PUBLIC_CLIENT_FIXTURE", "synthetic-new-token")
	second, err := api.NewClientWithContext(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range []*api.Client{first, second} {
		if _, err := client.Do(t.Context(), api.Request{Method: "GET", Endpoint: "/rest/api/3/myself"}); err != nil {
			t.Fatal(err)
		}
		_, err := client.Do(t.Context(), api.Request{Method: "POST", Endpoint: "/rest/api/3/issue"})
		if !errors.Is(err, api.ErrReadOnly) {
			t.Fatal("library relaxed saved read-only policy", err)
		}
	}
	got := []string{<-observed, <-observed}
	if !reflect.DeepEqual(got, []string{"synthetic-old-token", "synthetic-new-token"}) {
		t.Fatal("credential snapshot changed between batches", got)
	}
}
