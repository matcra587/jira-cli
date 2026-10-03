package jira

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gechr/clog"
	xhttp "github.com/gechr/x/http"

	"github.com/matcra587/jira-cli/internal/core"
)

// This file preserves the 0.15 scar: retry ONLY safe requests. Jira has no
// general idempotency-key contract, so auto-replaying a create, comment,
// move, or attachment can duplicate the write. Method safety is ours to
// classify; status classification is x/http's (408, 429, any 5xx).

// searchJQLPath is a Jira read that rides POST — the endpoint takes
// large JQL bodies — and is therefore safe to replay despite its method.
const searchJQLPath = "/rest/api/3/search/jql"

// searchApproximateCountPath is the second Jira read that rides POST,
// for the identical reason searchJQLPath does: a JQL body can outgrow a
// GET's query string. CountIssues (client.go) is the only caller.
const searchApproximateCountPath = "/rest/api/3/search/approximate-count"

// parseJQLPath validates queries without changing Jira state. Only this exact
// POST path joins the shared read-only and retry classification.
const parseJQLPath = "/rest/api/3/jql/parse"

// safeToRetry reports whether a request may be auto-resent. Only a read
// may: replaying a write can duplicate it. It is the retry loop's name
// for isRead's question rather than a second classification — see isRead
// for why the two must not drift apart.
func (c *Client) safeToRetry(req *http.Request) bool {
	return c.isRead(req)
}

// isRead reports whether a request only READS: GET/HEAD/OPTIONS plus the
// explicitly listed read POSTs. Everything else counts as a write, PUT and
// DELETE included — HTTP calls those idempotent, but Jira's side effects
// (notifications, webhooks, history entries) are not worth betting on.
//
// Two callers ask this question for different reasons — "may I replay
// it?" and "may I send it at all in read-only mode?" — and today the
// answer is the same. It is ONE function rather than two so the answers
// cannot drift: a second copy is how a request becomes retryable but not
// read, or the reverse.
func (c *Client) isRead(req *http.Request) bool {
	return isReadPath(req.Method, c.relativePath(req))
}

// isReadPath is shared by raw-request policy and the built-request gate.
func isReadPath(method, path string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	case http.MethodPost:
		switch path {
		case searchJQLPath, searchApproximateCountPath, parseJQLPath:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

// relativePath strips the site's base path (usually empty on Cloud, but a
// proxy prefix exists in the wild) so endpoint comparisons hold either way.
func (c *Client) relativePath(req *http.Request) string {
	p := req.URL.EscapedPath()
	base := strings.TrimSuffix(c.base.EscapedPath(), "/")
	if base != "" {
		p = strings.TrimPrefix(p, base)
	}
	return p
}

// replayableBody reports whether the body can be rewound for a resend. A
// bodyless request (every GET) trivially can; newRequest's bytes-backed
// bodies carry GetBody. A streaming body without GetBody cannot — and
// those are writes, which never auto-retry anyway.
func replayableBody(req *http.Request) bool {
	return req.Body == nil || req.GetBody != nil
}

// doWithRetry runs one request through the safe-retry loop and reports
// what it did. Unsafe requests, a zero budget, and non-replayable bodies
// fall through to a single attempt — the loop only exists for requests
// that cannot duplicate a write. On a retryable status it backs off with
// Retry-After as the floor, gives up at the attempt cap or once a further
// wait would blow the budget, and returns the final response for the
// caller to type — success and final failure flow up the same way.
func (c *Client) doWithRetry(req *http.Request) (*http.Response, core.RetryInfo, error) {
	info := core.RetryInfo{Attempts: 1}
	if c.retry.MaxWait <= 0 || !c.safeToRetry(req) || !replayableBody(req) {
		resp, err := c.http.Do(req)
		return resp, info, err
	}

	// lastErr carries the most recent transport failure across attempts.
	var lastErr error

	deadline := c.clock.Now().Add(c.retry.MaxWait)
	if d, ok := req.Context().Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	for attempt := 0; ; attempt++ {
		info.Attempts = attempt + 1
		// Each attempt rides a clone with a freshly rewound body so the
		// caller's request is never mutated; a bodyless request needs no
		// clone at all.
		attemptReq := req
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, info, err
			}
			attemptReq = req.Clone(req.Context())
			attemptReq.Body = body
		}

		resp, err := c.http.Do(attemptReq)
		switch {
		case err != nil:
			// A redirect-policy refusal is permanent for this request. It is
			// not a transient connection failure and must not trigger replay.
			if errors.Is(err, ErrUnsafeRedirect) || errors.Is(err, ErrInsecureScheme) {
				return resp, info, err
			}
			// Remember it: a give-up below may have no response to
			// return, and the caller must still learn WHY rather than
			// receive a nil pair it will dereference.
			lastErr = err

			// A transport error on a request we already classified as safe
			// IS the transient class retry exists for — a reset connection,
			// a DNS blip, a refused port during a deploy. Cancellation is
			// the exception: the caller asked to stop, so honor it.
			if ctxErr := req.Context().Err(); ctxErr != nil {
				return resp, info, err
			}
			if attempt+1 >= c.retry.MaxAttempts {
				return resp, info, err
			}
		case !xhttp.IsRetryableStatus(resp.StatusCode):
			return resp, info, err
		}
		if attempt+1 >= c.retry.MaxAttempts {
			c.logGiveUp(req, resp, info, "attempt cap reached")
			// A nil response means every attempt failed in transport, so
			// there is no status for the caller to type — the error is
			// the only thing that describes what happened, and returning
			// a nil pair would hand the caller something to crash on.
			return resp, info, errIfNoResponse(resp, lastErr) // the caller types the final failure
		}

		var retryAfter time.Duration
		if resp != nil {
			retryAfter = retryAfterDelay(resp.Header.Get("Retry-After"), c.clock.Now())
		}
		delay := c.backoff(attempt, retryAfter)
		if !c.clock.Now().Add(delay).Before(deadline) {
			c.logGiveUp(req, resp, info, "backoff would exceed the retry budget")
			return resp, info, errIfNoResponse(resp, lastErr)
		}

		// Say why before sleeping: backoff is hidden wall-clock, so the
		// verbose path must explain the stall. Path only, never the query
		// — JQL and its keys stay out of logs.
		//
		// resp is nil whenever this attempt failed in TRANSPORT rather
		// than returning a status, and Int() evaluates at the call site,
		// so a disabled debug level does not save it.
		retrying := clog.Ctx(req.Context()).Debug().
			Str("method", req.Method).
			Str("path", req.URL.EscapedPath()).
			Int("attempt", info.Attempts).
			Duration("delay", delay)
		if resp != nil {
			retrying = retrying.Int("status", resp.StatusCode)
		}
		retrying.Msg("jira transient failure; retrying after backoff")

		if resp != nil {
			drainAndClose(resp.Body)
		}
		if err := c.sleep(req.Context(), delay); err != nil {
			return nil, info, err // context canceled mid-backoff
		}
		info.Waited += delay
	}
}

// errIfNoResponse keeps doWithRetry's contract: it never returns a nil
// response AND a nil error. Every caller does `if err != nil { return }`
// and then reads the response, which is only safe if one of the two is
// always present.
func errIfNoResponse(resp *http.Response, lastErr error) error {
	if resp != nil {
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return errors.New("jira request failed with no response")
}

// logGiveUp records why the loop stopped retrying — the attempt cap or
// the wall-clock budget — so a stalled command stays explainable under
// the verbose flags.
//
// A nil resp is normal here and must not crash: the loop also gives up
// on a TRANSPORT error — a refused port, a DNS failure, an unreachable
// host — where there is no response and therefore no status. Losing a
// log field is the right cost; a panic on "the site is unreachable"
// turns an ordinary misconfiguration into a stack trace.
func (c *Client) logGiveUp(req *http.Request, resp *http.Response, info core.RetryInfo, why string) {
	event := clog.Ctx(req.Context()).Debug().
		Str("method", req.Method).
		Str("path", req.URL.EscapedPath()).
		Int("attempt", info.Attempts).
		Str("cause", why)
	if resp != nil {
		event = event.Int("status", resp.StatusCode)
	}
	event.Msg("jira transient failure; gave up retrying")
}

// backoff returns the next delay. Retry-After, when present, is the floor
// plus a small jitter — a fan-out whose calls all got the same Retry-After
// must not wake in lockstep. Otherwise full jitter over a capped
// exponential window spreads concurrent retries.
func (c *Client) backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter + time.Duration(c.jitter()*float64(time.Second))
	}
	window := c.retry.BaseDelay << attempt
	if window > c.retry.MaxDelay || window <= 0 { // <= 0 catches shift overflow
		window = c.retry.MaxDelay
	}
	return time.Duration(c.jitter() * float64(window))
}

// maxRetryAfter caps what we believe from a Retry-After header. Anything
// larger is a broken or hostile proxy, and an unclamped value overflows
// the Duration arithmetic into a NEGATIVE delay — which the loop reads as
// "no wait", disabling backoff exactly when the server begged for it. A
// day is beyond any budget, so the clamp never changes a sane outcome.
const maxRetryAfter = 24 * time.Hour

// retryAfterDelay parses a Retry-After value: RFC 9110 allows delta-seconds
// or an HTTP-date and both appear in the wild. A past date (server clock
// skew), a negative delta, or junk yields 0 — retry now — so a malformed
// header never blocks a request; an absurdly large value clamps to
// maxRetryAfter for the same reason. Date remainders round up so we never
// wake before the server's deadline. Deliberately local: x/http (v0.5.11)
// classifies statuses and parses Link headers only.
func retryAfterDelay(raw string, now time.Time) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if secs, err := strconv.Atoi(raw); err == nil {
		if secs < 0 {
			return 0
		}
		if secs > int(maxRetryAfter/time.Second) {
			return maxRetryAfter
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(raw); err == nil {
		// Sub saturates at the Duration extremes, so a far-future date
		// arrives here huge-positive and clamps rather than overflowing.
		d := t.Sub(now)
		if d <= 0 {
			return 0
		}
		if d >= maxRetryAfter {
			return maxRetryAfter
		}
		if r := d % time.Second; r != 0 {
			d += time.Second - r
		}
		return d
	}
	return 0
}

// maxDrainBytes bounds the throwaway read of a discarded retryable
// response. A keep-alive connection only returns to the idle pool once its
// body reaches EOF, so we drain to reuse it; the cap stops a pathological
// body from stalling the retry — an over-cap body simply forgoes reuse.
const maxDrainBytes = 64 << 10

// drainAndClose drains a discarded response body (within the cap) and
// closes it, so the underlying connection can be reused for the resend.
func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxDrainBytes)) //nolint:errcheck // best-effort drain only affects connection reuse
	_ = body.Close()                                                //nolint:errcheck // the retry proceeds on a new connection if close fails
}

// realSleep waits for d or the context, whichever ends first.
func realSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// defaultJitter spreads backoff; it needs uniformity, not secrecy.
func defaultJitter() float64 { return rand.Float64() } //nolint:gosec // jitter, not crypto
