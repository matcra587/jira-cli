package jira

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/core"
)

// countingServer returns an httptest server whose handler answers from
// responses in order (sticking on the last one) and the hit counter, so a
// test can assert exactly how many times Jira was called — the whole point
// of the safe-retry contract.
func countingServer(t *testing.T, responses []func(w http.ResponseWriter)) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := hits.Add(1)
		idx := min(int(n)-1, len(responses)-1)
		responses[idx](w)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func respondStatus(status int, retryAfter string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(status)
	}
}

func respondIssue(key string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":"` + key + `"}`))
	}
}

// newRetryTestClient wires deterministic seams: sleep records delays
// without waiting, jitter is pinned at 0.5.
func newRetryTestClient(t *testing.T, baseURL string, policy core.RetryPolicy) (*Client, *[]time.Duration) {
	t.Helper()
	c, err := New(Config{BaseURL: baseURL, Email: "user@example.com", APIToken: "api-token", Retry: policy})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	slept := &[]time.Duration{}
	c.sleep = func(_ context.Context, d time.Duration) error {
		*slept = append(*slept, d)
		return nil
	}
	c.jitter = func() float64 { return 0.5 }
	return c, slept
}

func retryPolicy() core.RetryPolicy {
	return core.RetryPolicy{
		MaxAttempts: 4,
		BaseDelay:   1 * time.Second,
		MaxDelay:    30 * time.Second,
		MaxWait:     60 * time.Second,
	}
}

func TestClient_doWithRetry(t *testing.T) {
	t.Parallel()

	t.Run("get 429 retries with backoff then succeeds", func(t *testing.T) {
		t.Parallel()
		srv, hits := countingServer(t, []func(http.ResponseWriter){
			respondStatus(http.StatusTooManyRequests, "1"),
			respondStatus(http.StatusTooManyRequests, "1"),
			respondIssue("ROCK-1"),
		})
		c, slept := newRetryTestClient(t, srv.URL, retryPolicy())

		got, err := c.GetIssue(t.Context(), "ROCK-1", nil)
		if err != nil {
			t.Fatalf("GetIssue: %v", err)
		}
		if k := got.Issue.Key.Value; k != "ROCK-1" {
			t.Errorf("issue key = %q, want ROCK-1", k)
		}
		if hits.Load() != 3 {
			t.Errorf("server hits = %d, want 3", hits.Load())
		}
		if got.Retry.Attempts != 3 {
			t.Errorf("attempts = %d, want 3", got.Retry.Attempts)
		}
		// Retry-After 1s is the floor; pinned jitter adds 0.5s on top.
		want := 1500 * time.Millisecond
		if len(*slept) != 2 || (*slept)[0] != want || (*slept)[1] != want {
			t.Errorf("slept = %v, want [%v %v]", *slept, want, want)
		}
		if got.Retry.Waited != 2*want {
			t.Errorf("waited = %v, want %v", got.Retry.Waited, 2*want)
		}
	})

	t.Run("get 500 retries then succeeds", func(t *testing.T) {
		t.Parallel()
		srv, hits := countingServer(t, []func(http.ResponseWriter){
			respondStatus(http.StatusInternalServerError, ""),
			respondIssue("ROCK-2"),
		})
		c, _ := newRetryTestClient(t, srv.URL, retryPolicy())

		got, err := c.GetIssue(t.Context(), "ROCK-2", nil)
		if err != nil {
			t.Fatalf("GetIssue: %v", err)
		}
		if hits.Load() != 2 || got.Retry.Attempts != 2 {
			t.Errorf("hits = %d, attempts = %d, want 2 and 2", hits.Load(), got.Retry.Attempts)
		}
	})

	t.Run("get gives up at the attempt cap", func(t *testing.T) {
		t.Parallel()
		srv, hits := countingServer(t, []func(http.ResponseWriter){
			respondStatus(http.StatusTooManyRequests, ""),
		})
		policy := retryPolicy()
		policy.MaxAttempts = 3
		c, slept := newRetryTestClient(t, srv.URL, policy)

		_, err := c.GetIssue(t.Context(), "ROCK-3", nil)
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("error = %v, want *APIError", err)
		}
		if apiErr.StatusCode != http.StatusTooManyRequests {
			t.Errorf("status = %d, want 429", apiErr.StatusCode)
		}
		if hits.Load() != 3 || apiErr.Retry.Attempts != 3 {
			t.Errorf("hits = %d, attempts = %d, want 3 and 3", hits.Load(), apiErr.Retry.Attempts)
		}
		// No Retry-After, so full jitter (pinned 0.5) over the exponential
		// window: 0.5*1s then 0.5*2s.
		if len(*slept) != 2 || (*slept)[0] != 500*time.Millisecond || (*slept)[1] != 1*time.Second {
			t.Errorf("slept = %v, want [500ms 1s]", *slept)
		}
	})

	t.Run("zero budget disables retry", func(t *testing.T) {
		t.Parallel()
		srv, hits := countingServer(t, []func(http.ResponseWriter){
			respondStatus(http.StatusTooManyRequests, "1"),
		})
		c, slept := newRetryTestClient(t, srv.URL, core.RetryPolicy{})

		_, err := c.GetIssue(t.Context(), "ROCK-4", nil)
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("error = %v, want *APIError", err)
		}
		if hits.Load() != 1 || apiErr.Retry.Attempts != 1 || len(*slept) != 0 {
			t.Errorf("hits = %d, attempts = %d, slept = %v; want single attempt, no sleep",
				hits.Load(), apiErr.Retry.Attempts, *slept)
		}
	})

	t.Run("retry-after beyond the budget gives up without sleeping", func(t *testing.T) {
		t.Parallel()
		srv, hits := countingServer(t, []func(http.ResponseWriter){
			respondStatus(http.StatusTooManyRequests, "3600"),
		})
		policy := retryPolicy()
		policy.MaxWait = 5 * time.Second
		c, slept := newRetryTestClient(t, srv.URL, policy)

		_, err := c.GetIssue(t.Context(), "ROCK-5", nil)
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("error = %v, want *APIError", err)
		}
		if hits.Load() != 1 || len(*slept) != 0 {
			t.Errorf("hits = %d, slept = %v; want 1 hit and no sleep", hits.Load(), *slept)
		}
		if apiErr.RetryAfterSeconds != 3600 {
			t.Errorf("retry-after = %d, want 3600", apiErr.RetryAfterSeconds)
		}
	})

	t.Run("post does not auto-replay on 429", func(t *testing.T) {
		t.Parallel()
		srv, hits := countingServer(t, []func(http.ResponseWriter){
			respondStatus(http.StatusTooManyRequests, "1"),
		})
		c, slept := newRetryTestClient(t, srv.URL, retryPolicy())

		req, err := c.newRequest(t.Context(), http.MethodPost,
			[]string{"rest", "api", "3", "issue"}, nil, map[string]string{"summary": "x"})
		if err != nil {
			t.Fatalf("newRequest: %v", err)
		}
		resp, info, err := c.doWithRetry(req)
		if err != nil {
			t.Fatalf("doWithRetry: %v", err)
		}
		defer resp.Body.Close() //nolint:errcheck // test cleanup
		if hits.Load() != 1 || info.Attempts != 1 || len(*slept) != 0 {
			t.Errorf("hits = %d, attempts = %d, slept = %v; a write must never auto-replay",
				hits.Load(), info.Attempts, *slept)
		}
	})

	t.Run("put does not auto-replay on 429", func(t *testing.T) {
		t.Parallel()
		srv, hits := countingServer(t, []func(http.ResponseWriter){
			respondStatus(http.StatusTooManyRequests, "1"),
		})
		c, _ := newRetryTestClient(t, srv.URL, retryPolicy())

		req, err := c.newRequest(t.Context(), http.MethodPut,
			[]string{"rest", "api", "3", "issue", "ROCK-7"}, nil, map[string]string{"summary": "x"})
		if err != nil {
			t.Fatalf("newRequest: %v", err)
		}
		resp, info, err := c.doWithRetry(req)
		if err != nil {
			t.Fatalf("doWithRetry: %v", err)
		}
		defer resp.Body.Close() //nolint:errcheck // test cleanup
		if hits.Load() != 1 || info.Attempts != 1 {
			t.Errorf("hits = %d, attempts = %d; a write must never auto-replay", hits.Load(), info.Attempts)
		}
	})

	t.Run("post search jql is a whitelisted read and retries", func(t *testing.T) {
		t.Parallel()
		srv, hits := countingServer(t, []func(http.ResponseWriter){
			respondStatus(http.StatusTooManyRequests, "1"),
			respondIssue("ROCK-8"),
		})
		c, _ := newRetryTestClient(t, srv.URL, retryPolicy())

		req, err := c.newRequest(t.Context(), http.MethodPost,
			[]string{"rest", "api", "3", "search", "jql"}, nil, map[string]string{"jql": "order by created"})
		if err != nil {
			t.Fatalf("newRequest: %v", err)
		}
		resp, info, err := c.doWithRetry(req)
		if err != nil {
			t.Fatalf("doWithRetry: %v", err)
		}
		defer resp.Body.Close() //nolint:errcheck // test cleanup
		if hits.Load() != 2 || info.Attempts != 2 {
			t.Errorf("hits = %d, attempts = %d, want 2 and 2", hits.Load(), info.Attempts)
		}
	})

	t.Run("context cancellation during backoff stops the loop", func(t *testing.T) {
		t.Parallel()
		srv, hits := countingServer(t, []func(http.ResponseWriter){
			respondStatus(http.StatusTooManyRequests, "1"),
		})
		c, _ := newRetryTestClient(t, srv.URL, retryPolicy())
		c.sleep = func(context.Context, time.Duration) error { return context.Canceled }

		_, err := c.GetIssue(t.Context(), "ROCK-9", nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if hits.Load() != 1 {
			t.Errorf("hits = %d, want 1", hits.Load())
		}
	})
}

func TestRetryAfterDelay(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		raw  string
		now  time.Time
		want time.Duration
	}{
		{name: "empty", raw: "", now: base, want: 0},
		{name: "delta seconds", raw: "7", now: base, want: 7 * time.Second},
		{name: "negative delta", raw: "-3", now: base, want: 0},
		{name: "junk", raw: "soon", now: base, want: 0},
		{name: "http date ahead", raw: base.Add(2 * time.Second).Format(http.TimeFormat), now: base, want: 2 * time.Second},
		// The date format carries whole seconds, so a sub-second now is
		// what exercises the round-up: 2.5s remaining must wait 3s, never 2.
		{name: "http date remainder rounds up", raw: base.Add(3 * time.Second).Format(http.TimeFormat), now: base.Add(500 * time.Millisecond), want: 3 * time.Second},
		{name: "http date in the past", raw: base.Add(-time.Minute).Format(http.TimeFormat), now: base, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := retryAfterDelay(tt.raw, tt.now); got != tt.want {
				t.Errorf("retryAfterDelay(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

// TestRedirectNeverReplaysWrites pins the finding that stdlib redirect
// following — not the retry loop — could replay a write body: NewRequest
// sets GetBody for buffered bodies and http.Client re-sends it on 307/308.
// The client must refuse redirects for unsafe methods and follow them for
// safe reads.
func TestRedirectNeverReplaysWrites(t *testing.T) {
	t.Parallel()

	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	client, err := New(Config{BaseURL: redirector.URL, Email: "user@example.com", APIToken: "api-token"})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("post is refused", func(t *testing.T) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, redirector.URL+"/rest/api/3/issue", strings.NewReader(`{"w":1}`))
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.http.Do(req) //nolint:bodyclose // error path, no body
		if err == nil {
			t.Fatal("write redirect followed; want refusal")
		}
		if got := targetHits.Load(); got != 0 {
			t.Fatalf("write body replayed to redirect target %d times", got)
		}
	})

	t.Run("get follows", func(t *testing.T) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, redirector.URL+"/rest/api/3/issue/K-1", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.http.Do(req)
		if err != nil {
			t.Fatalf("safe redirect refused: %v", err)
		}
		defer resp.Body.Close() //nolint:errcheck // test read path
		if targetHits.Load() != 1 {
			t.Fatal("safe redirect not followed to target")
		}
	})
}

// TestRetryAfterOverflowNeverDisablesBackoff pins the clamp: an absurd
// Retry-After used to overflow into a negative delay, which the loop read
// as "no wait" — four back-to-back attempts hammering a throttled server,
// with a negative RetryInfo.Waited. Clamped to maxRetryAfter, the value
// exceeds any sane budget, so the loop gives up after the first attempt
// instead of hammering.
func TestRetryAfterOverflowNeverDisablesBackoff(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"9223372036", "9223372036854775807", "Fri, 31 Dec 9999 23:59:59 GMT"} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if d := retryAfterDelay(raw, time.Now()); d != maxRetryAfter {
				t.Fatalf("retryAfterDelay(%q) = %v, want clamp to %v", raw, d, maxRetryAfter)
			}
		})
	}

	srv, hits := countingServer(t, []func(http.ResponseWriter){
		respondStatus(http.StatusTooManyRequests, "9223372036"),
	})
	c, slept := newRetryTestClient(t, srv.URL, core.RetryPolicy{
		MaxAttempts: 4, BaseDelay: time.Millisecond, MaxDelay: time.Second, MaxWait: 30 * time.Second,
	})

	_, err := c.GetIssue(t.Context(), "K-1", nil)
	if err == nil {
		t.Fatal("want the rate-limit error after giving up")
	}
	if hits.Load() != 1 {
		t.Fatalf("server hit %d times; clamped Retry-After exceeds the budget, so give up after 1", hits.Load())
	}
	if len(*slept) != 0 {
		t.Fatalf("slept %v; a beyond-budget Retry-After must not sleep at all", *slept)
	}
}

// TestRelativePathStripsProxyPrefix pins the proxy-prefix branch of
// relativePath: a Cloud tenant fronted by a reverse proxy that mounts the
// API under a path prefix must still have its whitelisted search/jql POST
// classified as safe to retry — the comparison is against the path with
// the site's own base prefix stripped, not the raw URL path.
func TestRelativePathStripsProxyPrefix(t *testing.T) {
	t.Parallel()
	c, err := New(Config{BaseURL: "https://example.atlassian.net/proxy/prefix/", Email: "user@example.com", APIToken: "api-token"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req, err := c.newRequest(t.Context(), http.MethodPost,
		[]string{"rest", "api", "3", "search", "jql"}, nil, map[string]string{"jql": "order by created"})
	if err != nil {
		t.Fatalf("newRequest: %v", err)
	}
	if got := c.relativePath(req); got != searchJQLPath {
		t.Errorf("relativePath = %q, want %q (proxy prefix stripped)", got, searchJQLPath)
	}
	if !c.safeToRetry(req) {
		t.Error("search/jql behind a proxy prefix must still classify as safe to retry")
	}
}

// TestApproximateCountPathIsSafeToRetry is TestRelativePathStripsProxyPrefix's
// sibling for CountIssues' own POST-for-a-read: the approximate-count
// endpoint must be classified as safe to retry (and, by the same
// isRead answer, allowed under ReadOnly) the same way search/jql is —
// forgetting to widen isRead's whitelist alongside adding the new
// method is exactly the regression this pins.
func TestApproximateCountPathIsSafeToRetry(t *testing.T) {
	t.Parallel()
	c, err := New(Config{BaseURL: "https://example.atlassian.net", Email: "user@example.com", APIToken: "api-token"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req, err := c.newRequest(t.Context(), http.MethodPost,
		[]string{"rest", "api", "3", "search", "approximate-count"}, nil, map[string]string{"jql": "order by created"})
	if err != nil {
		t.Fatalf("newRequest: %v", err)
	}
	if !c.safeToRetry(req) {
		t.Error("search/approximate-count must classify as safe to retry, same as search/jql")
	}
}

// TestBackoffWindow pins backoff's window arithmetic directly (no HTTP
// round trip needed): the normal exponential growth, the cap at MaxDelay
// once growth would exceed it, and the shift-overflow catch the "<= 0"
// guard in backoff exists for — BaseDelay<<attempt wraps negative once the
// shift count is large enough, and the comment in retry.go says the catch
// exists precisely to still land on a sane clamp rather than a negative
// (i.e. no-wait) delay.
func TestBackoffWindow(t *testing.T) {
	t.Parallel()
	c := &Client{
		retry:  core.RetryPolicy{BaseDelay: time.Second, MaxDelay: 30 * time.Second},
		jitter: func() float64 { return 1.0 }, // full window, no shrinkage, for simple arithmetic
	}
	tests := []struct {
		name    string
		attempt int
		want    time.Duration
	}{
		{name: "within window", attempt: 2, want: 4 * time.Second},                        // 1s<<2
		{name: "clamped at max delay", attempt: 10, want: 30 * time.Second},               // 1s<<10 = 1024s, capped
		{name: "shift overflow clamps to max delay", attempt: 34, want: 30 * time.Second}, // 1s<<34 wraps negative
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := c.backoff(tt.attempt, 0); got != tt.want {
				t.Errorf("backoff(%d, 0) = %v, want %v", tt.attempt, got, tt.want)
			}
		})
	}
}

// TestRetriedBodyArrivesIntact pins the rewind: req.Body is consumed by
// attempt 1 and stdlib only auto-rewinds on redirects, so the loop must
// refresh via GetBody or attempt 2 would send an empty body — the exact
// failure go-retryablehttp exists to prevent.
func TestRetriedBodyArrivesIntact(t *testing.T) {
	t.Parallel()

	const want = `{"jql":"project = DEMO"}`
	var bodies []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		n := len(bodies)
		mu.Unlock()
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	c, _ := newRetryTestClient(t, srv.URL, retryPolicy())
	req, err := c.newRequest(t.Context(), http.MethodPost, []string{"rest", "api", "3", "search", "jql"}, nil, json.RawMessage(want))
	if err != nil {
		t.Fatal(err)
	}
	resp, _, err := c.doWithRetry(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if len(bodies) != 2 {
		t.Fatalf("attempts = %d, want 2", len(bodies))
	}
	if bodies[0] != want || bodies[1] != want {
		t.Fatalf("retried body corrupted:\nattempt1 = %q\nattempt2 = %q\nwant     = %q", bodies[0], bodies[1], want)
	}
}

// TestUnreachableHostReturnsAnErrorNotANilPair pins doWithRetry's
// contract: it never returns a nil response AND a nil error.
//
// Every caller checks the error and then reads the response, so a nil
// pair is a panic waiting for the first person whose site is
// unreachable — a wrong base_url, a VPN that is down, an offline
// laptop. All ordinary conditions, and the transport used to crash on
// them once the retry budget ran out with nothing but transport errors.
func TestUnreachableHostReturnsAnErrorNotANilPair(t *testing.T) {
	t.Parallel()
	// A port nothing listens on: every attempt fails in transport, so
	// the loop gives up holding no response at all.
	client, err := New(Config{
		BaseURL:  "http://127.0.0.1:1",
		Email:    "person@example.com",
		APIToken: "a-token",
		Retry:    core.RetryPolicy{MaxAttempts: 2, MaxWait: 20 * time.Millisecond},
		// A short dial timeout: the default would make this test wait on
		// the operating system's full connect timeout, taxing every run.
		HTTPClient: &http.Client{Timeout: 100 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	req, err := client.newRequest(t.Context(), http.MethodGet, []string{"rest", "api", "3", "myself"}, nil, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, _, doErr := client.doWithRetry(req)
	if resp == nil && doErr == nil {
		t.Fatal("doWithRetry returned no response and no error; every caller dereferences the response after checking the error")
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
}

// TestCurrentUserSurvivesAnUnreachableHost is the same guarantee seen
// from the command layer, which is where it actually bit.
func TestCurrentUserSurvivesAnUnreachableHost(t *testing.T) {
	t.Parallel()
	client, err := New(Config{
		BaseURL:  "http://127.0.0.1:1",
		Email:    "person@example.com",
		APIToken: "a-token",
		Retry:    core.RetryPolicy{MaxAttempts: 2, MaxWait: 20 * time.Millisecond},
		// A short dial timeout: the default would make this test wait on
		// the operating system's full connect timeout, taxing every run.
		HTTPClient: &http.Client{Timeout: 100 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	if _, err := client.CurrentUser(t.Context()); err == nil {
		t.Fatal("an unreachable host reported success")
	}
}

// TestUnreachableHostRetriesWithoutPanicking covers the OTHER nil-resp
// path: the one where attempts remain and the backoff fits the budget,
// so the loop logs and sleeps rather than giving up.
//
// The give-up path was fixed first and its tests pin a tiny MaxWait,
// which forces the budget branch and never reaches the retry log — so
// the contract read as closed while half of it was still open. This runs
// with the production policy, which is what a real user gets.
func TestUnreachableHostRetriesWithoutPanicking(t *testing.T) {
	t.Parallel()
	client, err := New(Config{
		BaseURL:  "http://127.0.0.1:1",
		Email:    "person@example.com",
		APIToken: "a-token",
		// The real default, not a test-shrunk one: the panic lived in the
		// branch a generous budget reaches.
		Retry:      core.DefaultRetryPolicy(),
		HTTPClient: &http.Client{Timeout: 50 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, err := client.CurrentUser(ctx); err == nil {
		t.Fatal("an unreachable host reported success")
	}
}
