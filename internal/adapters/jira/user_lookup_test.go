package jira

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestExactUserLookupPreservesQueryNativeMembersAndRetryFacts(t *testing.T) {
	t.Parallel()
	const id = "literal &?+é@account"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/user" || len(r.URL.Query()) != 1 || r.URL.Query().Get("accountId") != id {
			t.Error("exact account query changed")
		}
		_, _, bound := r.BasicAuth()
		if !bound {
			t.Error("user lookup lost authentication")
		}
		if calls.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"accountId":"literal &?+é@account","active":true,"emailAddress":null,"future":{"large":9007199254740993,"values":[null,[],false,0]}}`)
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.invalid", APIToken: "fixture", ReadOnly: true, Retry: core.RetryPolicy{MaxAttempts: 2, MaxWait: time.Second, BaseDelay: time.Nanosecond, MaxDelay: time.Nanosecond}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := client.GetUser(t.Context(), id)
	if err != nil || out.User == nil || out.User.AccountID.Value != id || !out.User.EmailAddress.Null || string(out.User.Extra["future"]) != `{"large":9007199254740993,"values":[null,[],false,0]}` || out.Retry.Attempts != 2 || calls.Load() != 2 {
		t.Fatal("lookup lost native reply or safe-read retries", err, calls.Load(), out.Retry)
	}
}

func TestExactUserLookupRejectsInvalidStatusesAndProtocolBeforePublication(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"duplicate ID", `{"accountId":"other","accountId":"literal","active":true}`, 200, core.ErrUserLookupResponseInvalid},
		{"duplicate active", `{"accountId":"literal","active":false,"active":true}`, 200, core.ErrUserLookupResponseInvalid},
		{"duplicate deleted", `{"accountId":"literal","active":true,"deleted":true,"deleted":false}`, 200, core.ErrUserLookupResponseInvalid},
		{"case ID", `{"ACCOUNTID":"literal","active":true}`, 200, core.ErrUserLookupResponseInvalid},
		{"case active", `{"accountId":"literal","Active":true}`, 200, core.ErrUserLookupResponseInvalid},
		{"case deleted", `{"accountId":"literal","active":true,"Deleted":true}`, 200, core.ErrUserLookupResponseInvalid},
		{"escaped duplicate", `{"accountId":"other","account\u0049d":"literal","active":true}`, 200, core.ErrUserLookupResponseInvalid},
		{"null", "null", 200, core.ErrUserLookupResponseInvalid},
		{"array", "[]", 200, core.ErrUserLookupResponseInvalid},
		{"wrong ID", `{"accountId":"other","active":true}`, 200, core.ErrUserLookupResponseInvalid},
		{"missing ID", `{"active":true}`, 200, core.ErrUserLookupResponseInvalid},
		{"missing active", `{"accountId":"literal"}`, 200, core.ErrUserLookupResponseInvalid},
		{"null active", `{"accountId":"literal","active":null}`, 200, core.ErrUserLookupResponseInvalid},
		{"malformed deleted", `{"accountId":"literal","active":true,"deleted":"true"}`, 200, core.ErrUserLookupResponseInvalid},
		{"inactive", `{"accountId":"literal","active":false}`, 200, core.ErrUserNotFound},
		{"deleted", `{"accountId":"literal","active":true,"deleted":true}`, 200, core.ErrUserNotFound},
		{"trailing data", `{"accountId":"literal","active":true} {}`, 200, core.ErrUserLookupResponseInvalid},
		{"created", `{"accountId":"literal","active":true}`, 201, core.ErrUserLookupResponseInvalid},
		{"no content", "", 204, core.ErrUserLookupResponseInvalid},
		{"oversized", `{"accountId":"literal","active":true,"padding":"` + strings.Repeat("x", core.DefaultMaxAPIResponseBytes) + `"}`, 200, ErrAPIResponseTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.invalid", APIToken: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			out, err := client.GetUser(t.Context(), "literal")
			if !errors.Is(err, tc.want) || out.User != nil {
				t.Fatal("invalid exact-account reply published", err)
			}
		})
	}
}

func TestExactUserLookupHTTPFailuresAndCanceledEntryRetainClassification(t *testing.T) {
	t.Parallel()
	for _, status := range []int{401, 403, 404, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); _, _ = io.WriteString(w, `{}`) }))
			defer server.Close()
			client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.invalid", APIToken: "fixture", Retry: core.RetryPolicy{MaxAttempts: 1}})
			if err != nil {
				t.Fatal(err)
			}
			out, err := client.GetUser(t.Context(), "literal")
			api, ok := errors.AsType[*APIError](err)
			if !ok || api.StatusCode != status || out.User != nil {
				t.Fatal("lookup lost native HTTP failure", err)
			}
		})
	}
	calls := 0
	client := newTestClient(t, "https://example.invalid", &http.Client{Transport: redirectTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected canceled entry")
	})})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out, err := client.GetUser(ctx, "literal")
	if !errors.Is(err, context.Canceled) || out.User != nil || calls != 0 {
		t.Fatal("canceled lookup entered transport", err, calls)
	}
}

func TestExactUserLookupFinalRepliesRequireBoundAPIScopeAndCredentials(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"same scope", "foreign host", "outside prefix", "missing credentials", "changed credentials"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			client := newTestClient(t, "https://example.invalid/ex/jira/cloud-a", &http.Client{Transport: redirectTransport(func(req *http.Request) (*http.Response, error) {
				final := req.Clone(req.Context())
				url := *req.URL
				final.URL = &url
				switch mode {
				case "foreign host":
					final.URL.Host = "foreign.invalid"
				case "outside prefix":
					final.URL.Path = "/reply"
				case "missing credentials":
					final.Header.Del("Authorization")
				case "changed credentials":
					final.SetBasicAuth("different@example.invalid", "fixture-other")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"accountId":"literal","active":true}`)), Request: final}, nil
			})})
			out, err := client.GetUser(t.Context(), "literal")
			if mode == "same scope" {
				if err != nil || out.User == nil {
					t.Fatal("bound exact-account reply refused", err)
				}
			} else if !errors.Is(err, core.ErrUserLookupResponseInvalid) || out.User != nil {
				t.Fatal("unbound reply established account identity", err)
			}
		})
	}
}
