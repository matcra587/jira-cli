package jira

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestRateLimitObservationRetainsDiscardedRetry(t *testing.T) {
	t.Parallel()
	server, hits := countingServer(t, []func(http.ResponseWriter){
		func(w http.ResponseWriter) {
			w.Header().Set("X-RateLimit-NearLimit", " TRUE ")
			w.WriteHeader(http.StatusServiceUnavailable)
		},
		respondIssue("KEY-1"),
	})
	client, _ := newRetryTestClient(t, server.URL, retryPolicy())
	ctx := core.WithRateLimitObservation(t.Context())
	if _, err := client.GetIssue(ctx, "KEY-1", nil); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 || !core.TakeRateLimitNear(ctx) || core.TakeRateLimitNear(ctx) {
		t.Fatalf("retry lost or duplicated its observation; requests=%d", hits.Load())
	}
}

func TestRateLimitObservationRejectsUnboundReply(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
	}{
		{name: "another account", mutate: func(req *http.Request) { req.SetBasicAuth("other@example.test", "api-token") }},
		{name: "another token", mutate: func(req *http.Request) { req.SetBasicAuth("user@example.com", "other-token") }},
		{name: "missing credentials", mutate: func(req *http.Request) { req.Header.Del("Authorization") }},
		{name: "another API prefix", mutate: func(req *http.Request) { req.URL.Path = "/media/content" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			transport := redirectTransport(func(req *http.Request) (*http.Response, error) {
				bound := req.Clone(req.Context())
				tc.mutate(bound)
				return &http.Response{
					StatusCode: http.StatusOK, Request: bound,
					Header: http.Header{"X-Ratelimit-Nearlimit": {"true"}},
					Body:   io.NopCloser(strings.NewReader(`{"key":"KEY-1"}`)),
				}, nil
			})
			client := newTestClient(t, "https://example.invalid/api", &http.Client{Transport: transport})
			ctx := core.WithRateLimitObservation(t.Context())
			if _, err := client.GetIssue(ctx, "KEY-1", nil); err != nil {
				t.Fatal(err)
			}
			if core.TakeRateLimitNear(ctx) {
				t.Fatal("unbound response established Jira rate state")
			}
		})
	}
}

func TestRateLimitObservationIgnoresMediaRedirect(t *testing.T) {
	t.Parallel()
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "" {
			t.Error("credential reached media server")
		}
		w.Header().Set("X-RateLimit-NearLimit", "true")
		_, _ = io.WriteString(w, `{"key":"KEY-1"}`)
	}))
	t.Cleanup(media.Close)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, media.URL+"/content", http.StatusFound)
	}))
	t.Cleanup(api.Close)
	client := newTestClient(t, api.URL, nil)
	ctx := core.WithRateLimitObservation(t.Context())
	if _, err := client.GetIssue(ctx, "KEY-1", nil); err != nil {
		t.Fatal(err)
	}
	if core.TakeRateLimitNear(ctx) {
		t.Fatal("media response established Jira rate state")
	}
}

func TestRateLimitObservationDoesNotLeakAcrossCalls(t *testing.T) {
	t.Parallel()
	server, hits := countingServer(t, []func(http.ResponseWriter){
		func(w http.ResponseWriter) {
			w.Header().Set("X-RateLimit-NearLimit", "true")
			respondIssue("KEY-1")(w)
		},
		respondIssue("KEY-1"),
	})
	client, _ := newRetryTestClient(t, server.URL, core.RetryPolicy{})
	first := core.WithRateLimitObservation(t.Context())
	second := core.WithRateLimitObservation(t.Context())
	for _, ctx := range []context.Context{first, second} {
		if _, err := client.GetIssue(ctx, "KEY-1", nil); err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 2 || core.TakeRateLimitNear(second) || !core.TakeRateLimitNear(first) {
		t.Fatal("reusing a client shared invocation state")
	}
}
