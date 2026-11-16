package api_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/api"
)

func TestFailureRetainsHTTPReceiptWithoutReplayingWrites(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status int
		body   string
		limit  int64
		code   string
	}{
		{
			name: "rate limited", status: 429,
			body: `{"errorMessages":["synthetic-private-remote-diagnostic"]}`, code: "rate_limited",
		},
		{
			name: "capture bound after success", status: 201,
			body: `{"id":"10001","more":"bytes"}`, limit: 10, code: "response_too_large",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(tc.status)
				if _, err := io.WriteString(w, tc.body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			client := explicitClient(t, server, func(opts *api.ClientOptions) {
				opts.Retry = nil // Even the default safe-read policy must not replay a write.
				opts.MaxResponseBytes = tc.limit
			})
			response, err := client.Do(t.Context(), api.Request{
				Method: "POST", Endpoint: "/rest/api/3/issue",
				Body: []byte(`{"fields":{}}`), ContentType: "application/json",
			})
			failure, ok := errors.AsType[*api.Error](err)
			if !ok {
				t.Fatal("typed request failure lost", err)
			}
			if response == nil || response.StatusCode != tc.status {
				t.Fatal("HTTP receipt lost", response, err)
			}
			if failure.Code != tc.code || failure.StatusCode != tc.status {
				t.Fatal("failure classification changed", failure)
			}
			if calls.Load() != 1 {
				t.Fatal("write replayed", calls.Load())
			}
			if tc.limit != 0 {
				if !response.Truncated || !errors.Is(err, api.ErrResponseTooLarge) {
					t.Fatal("incomplete capture classification lost", response, err)
				}
				if string(response.Body) != tc.body[:tc.limit] {
					t.Fatal("partial acknowledged body lost", response, err)
				}
			}
			if tc.status == 429 && failure.RetryAfter != 2*time.Second {
				t.Fatal("rate-limit retry advice lost", failure)
			}
			printed := fmt.Sprintf(
				"%v %+v %#v",
				err,
				err,
				err,
			)
			if strings.Contains(printed, "synthetic-private") {
				t.Fatal("remote diagnostics escaped safe error formatting")
			}
		})
	}
}

func TestRedirectCannotForwardCredentials(t *testing.T) {
	t.Parallel()
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		forwarded.Add(1)
		w.WriteHeader(204)
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(
			w,
			r,
			target.URL+"/rest/api/3/myself",
			http.StatusTemporaryRedirect,
		)
	}))
	defer origin.Close()
	client := explicitClient(t, origin, nil)
	_, err := client.Do(t.Context(), api.Request{Method: "GET", Endpoint: "/rest/api/3/myself"})
	if err == nil || forwarded.Load() != 0 {
		t.Fatal("redirect escaped configured site", err)
	}
}
