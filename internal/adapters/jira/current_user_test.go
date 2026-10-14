package jira

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

func TestCurrentUserPreservesNativeIdentityAndPrivacy(t *testing.T) {
	t.Parallel()
	for _, privacy := range []string{"", `,"emailAddress":null,"displayName":null,"active":false`, `,"emailAddress":"","displayName":"","active":true`} {
		t.Run(privacy, func(t *testing.T) {
			t.Parallel()
			body := `{"accountId":"opaque:é<&>\u2028", "future":{"large":9007199254740993,"values":[null,[],false,0]}` + privacy + `}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				email, candidate, ok := r.BasicAuth()
				if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/myself" || r.URL.RawQuery != "" || !ok || email != "person@example.com" || candidate != "fixture" {
					t.Error("current-user lookup changed its route or credential binding")
				}
				_, _ = io.WriteString(w, " \t"+body+" \n")
			}))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Email: "person@example.com", APIToken: "fixture", ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			out, err := client.CurrentUser(t.Context())
			if err != nil || out.User == nil || out.Retry.Attempts != 1 {
				t.Fatalf("native identity refused: %+v %v", out, err)
			}
			var expected wire.UserDetails
			if err := expected.UnmarshalJSON([]byte(body)); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(*out.User, expected) || string(out.User.Extra["future"]) != `{"large":9007199254740993,"values":[null,[],false,0]}` {
				t.Fatal("account lookup normalized opaque identity, collapsed privacy values or lost native extensions")
			}
		})
	}
}

func TestCurrentUserRejectsAmbiguousNativeIdentity(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"accountId":null,"accountId":"fixture"}`,
		`{"accountId":"other","accountId":"fixture"}`,
		`{"accountId":"other","account\u0049d":"fixture"}`,
		`{"accountId":"fixture","ACCOUNTID":"other"}`,
		`{"accountId":"fixture","active":false,"active":true}`,
		`{"accountId":"fixture","Active":true}`,
		`{"accountId":"fixture","emailAddress":null,"emailAddress":"other@example.invalid"}`,
		`{"accountId":"fixture","EmailAddress":"other@example.invalid"}`,
		`{"accountId":"fixture","future":null,"future":42}`,
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				email, token, ok := r.BasicAuth()
				if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/myself" || r.URL.RawQuery != "" || !ok || email != "person@example.com" || token != "fixture" {
					t.Error("identity request changed route or credential binding")
				}
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Email: "person@example.com", APIToken: "fixture", Retry: core.RetryPolicy{MaxAttempts: 3}})
			if err != nil {
				t.Fatal(err)
			}
			out, err := client.CurrentUser(t.Context())
			if !errors.Is(err, ErrCurrentUserResponseInvalid) || out.User != nil || calls.Load() != 1 {
				t.Fatalf("ambiguous native identity published or replayed: %+v %v calls=%d", out, err, calls.Load())
			}
		})
	}
}

func TestCurrentUserRetainsInterruptedBodyCauses(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, io.ErrUnexpectedEOF} {
		for _, prefix := range []string{"", `{"accountId":`, `{"accountId":"fixture"}`} {
			t.Run(cause.Error()+"/"+prefix, func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				client, err := New(Config{
					BaseURL: "https://example.invalid", Email: "person@example.com", APIToken: "fixture",
					HTTPClient: &http.Client{Transport: redirectTransport(func(r *http.Request) (*http.Response, error) {
						calls.Add(1)
						return &http.Response{
							StatusCode: http.StatusOK, Header: make(http.Header), Request: r,
							Body: io.NopCloser(io.MultiReader(strings.NewReader(prefix), transitionErrorReader{err: cause})),
						}, nil
					})},
					Retry: core.RetryPolicy{MaxAttempts: 3},
				})
				if err != nil {
					t.Fatal(err)
				}
				out, err := client.CurrentUser(t.Context())
				if !errors.Is(err, cause) || !errors.Is(err, ErrCurrentUserResponseInvalid) || out.User != nil || calls.Load() != 1 {
					t.Fatalf("interrupted identity became success, lost its cause or replayed: %+v %v calls=%d", out, err, calls.Load())
				}
			})
		}
	}
}

func TestCurrentUserHTTPFailuresRetainAuthenticationTaxonomy(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"errorMessages":["fixture refusal"]}`)
			}))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Email: "person@example.com", APIToken: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			out, err := client.CurrentUser(t.Context())
			api, ok := errors.AsType[*APIError](err)
			if !ok || api.StatusCode != status || errors.Is(err, ErrCurrentUserResponseInvalid) || out.User != nil {
				t.Fatalf("HTTP failure lost classification: %+v %v", out, err)
			}
		})
	}
}

func TestCurrentUserRetriesSafeReadsAndRetainsReceipt(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"accountId":"fixture"}`)
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{
		BaseURL: server.URL, Email: "person@example.com", APIToken: "fixture", ReadOnly: true,
		Retry: core.RetryPolicy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, MaxWait: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := client.CurrentUser(t.Context())
	if err != nil || out.User == nil || out.User.AccountID.Value != "fixture" || out.Retry.Attempts != 2 || out.Retry.Waited <= 0 || calls.Load() != 2 {
		t.Fatalf("safe identity retry lost account or receipt: %+v %v calls=%d", out, err, calls.Load())
	}
}
