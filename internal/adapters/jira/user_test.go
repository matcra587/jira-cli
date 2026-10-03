package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

func TestSearchUsersRequestAndWireFidelity(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/user/search" || r.URL.Query().Get("query") != "sam &?+é@example.com" || r.URL.Query().Get("startAt") != "17" || r.URL.Query().Get("maxResults") != "3" || len(r.URL.Query()) != 3 {
			t.Errorf("wrong user search route/query: %s", r.URL)
		}
		_, _ = fmt.Fprint(w, `[{"accountId":"first","active":true,"emailAddress":null,"future":{"large":9007199254740993,"values":[null,[],false,0]}},{"accountId":"second","active":false},{"accountId":"third","active":true,"emailAddress":""}]`)
	}))
	defer server.Close()
	c, err := New(Config{BaseURL: server.URL, Email: "user@example.com", APIToken: "fixture", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.SearchUsers(t.Context(), "sam &?+é@example.com", 17, 3)
	if err != nil || len(out.Users) != 3 {
		t.Fatalf("search failed: %+v %v", out, err)
	}
	if !out.Users[0].EmailAddress.Null || out.Users[1].EmailAddress.Set || !out.Users[2].EmailAddress.Set || out.Users[2].EmailAddress.Null || out.Users[2].EmailAddress.Value != "" {
		t.Fatal("nullable email collapsed")
	}
	if string(out.Users[0].Extra["future"]) != `{"large":9007199254740993,"values":[null,[],false,0]}` {
		t.Fatal("future user members lost fidelity")
	}
	encoded, err := wire.MarshalNoEscape(out.Users[0])
	if err != nil || !json.Valid(encoded) {
		t.Fatalf("generated User cannot roundtrip: %s %v", encoded, err)
	}
}

func TestSearchUsersRejectsMalformedResponse(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`null`, `{}`, `[null]`, `[{"active":true}]`, `[{"accountId":null}]`, `[{"accountId":""}]`, `[{"accountId":3}]`, `[] {}`, `[] garbage`, `[`, `[{"accountId":"a"},{"accountId":"b"}]`} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body) }))
			defer server.Close()
			c, err := New(Config{BaseURL: server.URL, Email: "user@example.com", APIToken: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			out, err := c.SearchUsers(t.Context(), "sam", 0, 1)
			if !errors.Is(err, ErrUserSearchResponseInvalid) || out.Users != nil {
				t.Fatalf("invalid response accepted: %+v %v", out, err)
			}
		})
	}
}

func TestSearchUsersEmptyAndHTTPFailures(t *testing.T) {
	t.Parallel()
	for _, status := range []int{200, 401, 403, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status); _, _ = fmt.Fprint(w, `[]`) }))
			defer server.Close()
			c, err := New(Config{BaseURL: server.URL, Email: "user@example.com", APIToken: "fixture", Retry: core.RetryPolicy{MaxAttempts: 1}})
			if err != nil {
				t.Fatal(err)
			}
			out, err := c.SearchUsers(t.Context(), "sam", 0, 50)
			if status == 200 {
				if err != nil || out.Users == nil || len(out.Users) != 0 {
					t.Fatalf("empty success lost: %+v %v", out, err)
				}
				return
			}
			api, ok := errors.AsType[*APIError](err)
			if !ok || api.StatusCode != status {
				t.Fatalf("HTTP failure lost taxonomy: %v", err)
			}
		})
	}
}

func TestSearchUsersPropagatesCancellation(t *testing.T) {
	t.Parallel()
	c, err := New(Config{BaseURL: "https://example.atlassian.net", Email: "user@example.com", APIToken: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out core.UserSearchRead
	out, err = c.SearchUsers(ctx, "sam", 0, 50)
	if !errors.Is(err, context.Canceled) || out.Users != nil {
		t.Fatalf("cancellation lost: %+v %v", out, err)
	}
}

func TestSearchUsersRetriesSafeReads(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, `[]`)
	}))
	defer server.Close()
	c, err := New(Config{
		BaseURL: server.URL, Email: "user@example.com", APIToken: "fixture", ReadOnly: true,
		Retry: core.RetryPolicy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, MaxWait: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.SearchUsers(t.Context(), "sam", 0, 50)
	if err != nil || out.Users == nil || calls.Load() != 2 || out.Retry.Attempts != 2 {
		t.Fatalf("safe user read lost retry policy/facts: %+v %v calls=%d", out, err, calls.Load())
	}
}
