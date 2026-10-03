package jira

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestDoAPIKeepsScopeQueryAndBodyBytes(t *testing.T) {
	t.Parallel()
	const base = "https://example.invalid/ex/jira/cloud-a"
	for _, tc := range []struct {
		name, method, path, contentType string
		body                            []byte
	}{
		{name: "platform GET", method: http.MethodGet, path: "/rest/api/3/myself"},
		{name: "platform v2 JSON", method: http.MethodPost, path: "/rest/api/2/issue", contentType: "application/json", body: []byte(" {\"fields\": {\"summary\": \"<&>  \"}, \"large\": 9007199254740993} \n")},
		{name: "Agile PUT", method: http.MethodPut, path: "/rest/agile/1.0/issue/rank", contentType: "application/json", body: []byte(`{"issues":["ABC-1"],"rankBeforeIssue":"ABC-2"}`)},
		{name: "opaque PATCH", method: http.MethodPatch, path: "/rest/api/3/custom", contentType: "application/octet-stream", body: []byte{0, 255, '\n'}},
		{name: "raw segment escaping", method: http.MethodGet, path: "/rest/api/3/group/a b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			transport := redirectTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != tc.method || req.URL.Host != "example.invalid" || req.URL.Path != "/ex/jira/cloud-a"+tc.path {
					t.Error("raw request changed method, authority or cloud prefix")
				}
				if req.URL.Query().Get("query") != "literal+comma,space &" || len(req.URL.Query()["field"]) != 2 {
					t.Error("raw query lost literal punctuation or repeated values")
				}
				if req.Header.Get("Authorization") == "" || req.Header.Get("Accept") != "application/json" || req.Header.Get("Content-Type") != tc.contentType || req.Header.Get(xsrfHeader) != "" {
					t.Error("raw request changed shared headers or added an attachment-only header")
				}
				var body []byte
				if req.Body != nil {
					var err error
					body, err = io.ReadAll(req.Body)
					if err != nil {
						t.Error(err)
					}
				}
				if !bytes.Equal(body, tc.body) {
					t.Error("raw body was marshaled, escaped or otherwise changed")
				}
				return &http.Response{StatusCode: http.StatusCreated, Header: http.Header{"Content-Type": {"application/octet-stream"}, "Etag": {"fixture-etag"}, "Set-Cookie": {"fixture_session=private"}, "Authorization": {"private"}, "X-Auth-Token": {"private"}}, Body: io.NopCloser(bytes.NewReader([]byte{0, 255, 'x'})), Request: req}, nil
			})
			c := newTestClient(t, base, &http.Client{Transport: transport})
			out, err := c.DoAPI(t.Context(), core.APIRequest{Method: tc.method, Endpoint: tc.path, Query: url.Values{"query": {"literal+comma,space &"}, "field": {"summary", "description"}}, Body: tc.body, ContentType: tc.contentType, MaxResponseBytes: 1024})
			if err != nil || out == nil {
				t.Fatalf("DoAPI: %v", err)
			}
			if out.StatusCode != http.StatusCreated || !bytes.Equal(out.Body, []byte{0, 255, 'x'}) || out.Truncated || out.Retry.Attempts != 1 {
				t.Fatal("raw response lost status, exact bytes or transport facts")
			}
			if len(out.Headers) != 2 || out.Headers["Etag"][0] != "fixture-etag" {
				t.Fatal("response metadata included credentials or lost its relevant headers")
			}
		})
	}
}

func TestDoAPISendsExactPreconditionsWithSharedAuthentication(t *testing.T) {
	t.Parallel()
	const match = `"tag,with,commas", W/"weak", "other"`
	const date = "Thu, 01 Jan 1970 00:00:00 GMT"
	var hits atomic.Int32
	c := newTestClient(t, "https://example.invalid", &http.Client{Transport: redirectTransport(func(req *http.Request) (*http.Response, error) {
		hits.Add(1)
		if req.Header.Get("If-Match") != match || req.Header.Get("If-Unmodified-Since") != date || req.Header.Get("Authorization") == "" {
			t.Error("preconditions were dropped or changed shared authentication")
		}
		return &http.Response{StatusCode: http.StatusPreconditionFailed, Header: http.Header{}, Body: http.NoBody, Request: req}, nil
	})})
	c.retry = core.DefaultRetryPolicy()
	out, err := c.DoAPI(t.Context(), core.APIRequest{Method: http.MethodPut, Endpoint: "/rest/api/3/issue/ABC-1", IfMatch: match, IfUnmodifiedSince: date})
	if err == nil || out == nil || out.StatusCode != http.StatusPreconditionFailed || out.Retry.Attempts != 1 || hits.Load() != 1 {
		t.Fatalf("precondition refusal was lost or replayed: %v, %+v, %d", err, out, hits.Load())
	}
}

func TestAPIRequestAcceptsQuotedEntityTagsWithoutCSVParsing(t *testing.T) {
	t.Parallel()
	for _, value := range []string{`*`, `""`, `W/"weak"`, `"comma,in,opaque", "next"`, `"back\slash"`, `"é"`, "\"first\" ,\t\"next\""} {
		if err := ValidateAPIRequest(core.APIRequest{Method: http.MethodGet, Endpoint: "/rest/api/3/myself", IfMatch: value}); err != nil {
			t.Errorf("valid quoted entity tag rejected: %q: %v", value, err)
		}
	}
}

func TestDoAPIPreconditionsAreIndependentlyOptional(t *testing.T) {
	t.Parallel()
	const match = `"snapshot"`
	const date = "Thu, 01 Jan 1970 00:00:00 GMT"
	for _, tc := range []struct{ name, match, date string }{
		{name: "absent"},
		{name: "match only", match: match},
		{name: "date only", date: date},
		{name: "both", match: match, date: date},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			c := newTestClient(t, "https://example.invalid", &http.Client{Transport: redirectTransport(func(req *http.Request) (*http.Response, error) {
				hits.Add(1)
				for name, want := range map[string]string{"If-Match": tc.match, "If-Unmodified-Since": tc.date} {
					values := req.Header.Values(name)
					if want == "" {
						if len(values) != 0 {
							t.Errorf("unset %s was sent: %q", name, values)
						}
					} else if len(values) != 1 || values[0] != want {
						t.Errorf("%s changed: %q, want %q", name, values, want)
					}
				}
				return &http.Response{StatusCode: http.StatusNoContent, Header: http.Header{}, Body: http.NoBody, Request: req}, nil
			})})
			out, err := c.DoAPI(t.Context(), core.APIRequest{Method: http.MethodPut, Endpoint: "/rest/api/3/issue/ABC-1", IfMatch: tc.match, IfUnmodifiedSince: tc.date})
			if err != nil || out == nil || out.StatusCode != http.StatusNoContent || hits.Load() != 1 {
				t.Fatalf("optional precondition request failed: %v, %+v, %d", err, out, hits.Load())
			}
		})
	}
}

func TestDoAPIRejectsInvalidPreconditionsBeforeNetwork(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		match string
		date  string
	}{
		{name: "unquoted", match: "unquoted"},
		{name: "mixed wildcard", match: `*, "tag"`},
		{name: "unterminated", match: `"unterminated`},
		{name: "opaque space", match: `"space inside"`},
		{name: "outer space", match: ` "tag"`},
		{name: "outer tab", match: "\"tag\"\t"},
		{name: "trailing comma", match: `"tag",`},
		{name: "doubled comma", match: `"tag",, "next"`},
		{name: "lowercase weak marker", match: `w/"tag"`},
		{name: "escaped quote", match: `"tag\"extra"`},
		{name: "tag line break", match: "\"tag\"\r\nX-Injected: value"},
		{name: "null byte", match: "\"tag\x00\""},
		{name: "delete byte", match: "\"tag\x7f\""},
		{name: "invalid UTF8", match: "\"tag\xff\""},
		{name: "ISO date", date: "2026-10-02T00:00:00Z"},
		{name: "non GMT date", date: "Thu, 01 Jan 1970 00:00:00 EST"},
		{name: "wrong weekday", date: "Fri, 01 Jan 1970 00:00:00 GMT"},
		{name: "date line break", date: "Thu, 01 Jan 1970 00:00:00 GMT\r\nX-Injected: value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			c := newTestClient(t, "https://example.invalid", &http.Client{Transport: redirectTransport(func(req *http.Request) (*http.Response, error) {
				hits.Add(1)
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: http.NoBody, Request: req}, nil
			})})
			out, err := c.DoAPI(t.Context(), core.APIRequest{Method: http.MethodPut, Endpoint: "/rest/api/3/issue/ABC-1", IfMatch: tc.match, IfUnmodifiedSince: tc.date})
			if err == nil || out != nil || hits.Load() != 0 {
				t.Fatalf("invalid precondition reached HTTP: %q, %q, %v, %d", tc.match, tc.date, err, hits.Load())
			}
		})
	}
}

func TestDoAPIRefusesInvalidRequestsBeforeNetwork(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		request core.APIRequest
	}{
		{name: "absolute URL", request: core.APIRequest{Method: http.MethodGet, Endpoint: "https://other.invalid/rest/api/3/myself"}},
		{name: "network path", request: core.APIRequest{Method: http.MethodGet, Endpoint: "//other.invalid/rest/api/3/myself"}},
		{name: "outside REST namespace", request: core.APIRequest{Method: http.MethodGet, Endpoint: "/browse/ABC-1"}},
		{name: "embedded query", request: core.APIRequest{Method: http.MethodGet, Endpoint: "/rest/api/3/myself?x=1"}},
		{name: "fragment", request: core.APIRequest{Method: http.MethodGet, Endpoint: "/rest/api/3/myself#x"}},
		{name: "dot traversal", request: core.APIRequest{Method: http.MethodGet, Endpoint: "/rest/api/3/../../agile/board"}},
		{name: "encoded traversal", request: core.APIRequest{Method: http.MethodGet, Endpoint: "/rest/api/3/%2e%2e/myself"}},
		{name: "double encoded separator", request: core.APIRequest{Method: http.MethodGet, Endpoint: "/rest/api/3/a%252fb"}},
		{name: "empty segment", request: core.APIRequest{Method: http.MethodGet, Endpoint: "/rest/api/3//myself"}},
		{name: "invalid UTF8", request: core.APIRequest{Method: http.MethodGet, Endpoint: "/rest/api/3/\xff"}},
		{name: "control character", request: core.APIRequest{Method: http.MethodGet, Endpoint: "/rest/api/3/a\x00b"}},
		{name: "unsupported method", request: core.APIRequest{Method: http.MethodTrace, Endpoint: "/rest/api/3/myself"}},
		{name: "negative bound", request: core.APIRequest{Method: http.MethodGet, Endpoint: "/rest/api/3/myself", MaxResponseBytes: -1}},
		{name: "overflowing bound", request: core.APIRequest{Method: http.MethodGet, Endpoint: "/rest/api/3/myself", MaxResponseBytes: math.MaxInt64}},
		{name: "trailing empty segment", request: core.APIRequest{Method: http.MethodGet, Endpoint: "/rest/api/3/myself/"}},
		{name: "invalid content type", request: core.APIRequest{Method: http.MethodPost, Endpoint: "/rest/api/3/issue", ContentType: "application/"}},
		{name: "header injection", request: core.APIRequest{Method: http.MethodPost, Endpoint: "/rest/api/3/issue", ContentType: "application/json\r\nX-Extra: value"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			hits := 0
			c := newTestClient(t, "https://example.invalid/proxy", &http.Client{Transport: redirectTransport(func(req *http.Request) (*http.Response, error) {
				hits++
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: http.NoBody, Request: req}, nil
			})})
			out, err := c.DoAPI(t.Context(), tc.request)
			if err == nil || hits != 0 || out != nil {
				t.Fatalf("invalid raw request: refused=%t requests=%d receipt=%t", err != nil, hits, out != nil)
			}
		})
	}
}

func TestDoAPIReadOnlyAndSafeReplayShareClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		method, path string
		read         bool
	}{
		{method: http.MethodGet, path: "/rest/api/3/myself", read: true},
		{method: http.MethodHead, path: "/rest/api/3/myself", read: true},
		{method: http.MethodOptions, path: "/rest/agile/1.0/board", read: true},
		{method: http.MethodPost, path: "/rest/api/3/search/jql", read: true},
		{method: http.MethodPost, path: "/rest/api/3/search/approximate-count", read: true},
		{method: http.MethodPost, path: "/rest/api/3/issue"},
		{method: http.MethodPut, path: "/rest/api/3/issue/ABC-1"},
		{method: http.MethodPatch, path: "/rest/api/3/issue/ABC-1"},
		{method: http.MethodDelete, path: "/rest/api/3/issue/ABC-1"},
		{method: http.MethodPost, path: "/rest/agile/1.0/board"},
	} {
		for _, readOnly := range []bool{false, true} {
			t.Run(tc.method+tc.path+fmtReadOnly(readOnly), func(t *testing.T) {
				t.Parallel()
				body := []byte(`{"probe":"<&>"}`)
				hits := 0
				c := newTestClient(t, "https://example.invalid/ex/jira/cloud-a", &http.Client{Transport: redirectTransport(func(req *http.Request) (*http.Response, error) {
					hits++
					if req.Body != nil {
						got, err := io.ReadAll(req.Body)
						if err != nil || !bytes.Equal(got, body) {
							t.Error("safe replay changed the request body")
						}
					}
					status := http.StatusServiceUnavailable
					if hits > 1 {
						status = http.StatusOK
					}
					return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
				})})
				c.readOnly = readOnly
				c.retry = core.DefaultRetryPolicy()
				c.sleep = func(context.Context, time.Duration) error { return nil }
				in := core.APIRequest{Method: tc.method, Endpoint: tc.path, Body: body, ContentType: "application/json", MaxResponseBytes: 1024}
				if APIRequestIsRead(in) != tc.read {
					t.Fatal("command policy disagrees with independent method/endpoint classification")
				}
				out, err := c.DoAPI(t.Context(), in)
				switch {
				case readOnly && !tc.read:
					if !errors.Is(err, ErrReadOnly) || hits != 0 || out != nil {
						t.Fatal("read-only raw write reached the transport")
					}
				case tc.read:
					if err != nil || hits != 2 || out == nil || out.Retry.Attempts != 2 {
						t.Fatalf("safe raw read failed to retry: requests=%d err=%v", hits, err)
					}
				default:
					var apiErr *APIError
					if !errors.As(err, &apiErr) || hits != 1 || out == nil || out.Retry.Attempts != 1 || out.StatusCode != http.StatusServiceUnavailable {
						t.Fatal("raw write was replayed or lost its failure receipt")
					}
				}
			})
		}
	}
}

func fmtReadOnly(readOnly bool) string {
	if readOnly {
		return "/read-only"
	}
	return "/writable"
}

func TestDoAPIHTTPErrorRetainsExactResponse(t *testing.T) {
	t.Parallel()
	body := []byte(` {"errorMessages":["Missing"],"errors":{"summary":"required"},"unknown":9007199254740993} `)
	c := newTestClient(t, "https://example.invalid", &http.Client{Transport: redirectTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Retry-After": {"3"}, "X-Ratelimit-Remaining": {"9"}}, Body: io.NopCloser(bytes.NewReader(body)), Request: req}, nil
	})})
	out, err := c.DoAPI(t.Context(), core.APIRequest{Method: http.MethodPost, Endpoint: "/rest/api/3/issue", MaxResponseBytes: 1024})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || out == nil || out.StatusCode != http.StatusBadRequest || !bytes.Equal(out.Body, body) || out.Truncated {
		t.Fatal("HTTP failure discarded or changed the raw response")
	}
	if apiErr.Messages[0] != "Missing" || apiErr.FieldErrors["summary"] != "required" || apiErr.RetryAfterSeconds != 3 || out.Headers["Retry-After"][0] != "3" {
		t.Fatal("HTTP failure lost shared taxonomy or transport facts")
	}
}

func TestDoAPIResponseBoundIsExplicit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		cap       int64
		want      string
		truncated bool
	}{
		{name: "below body", cap: 4, want: "hell", truncated: true},
		{name: "exact size", cap: 5, want: "hello"},
		{name: "above body", cap: 6, want: "hello"},
		{name: "unbounded", want: "hello"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newTestClient(t, "https://example.invalid", &http.Client{Transport: redirectTransport(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("hello")), Request: req}, nil
			})})
			out, err := c.DoAPI(t.Context(), core.APIRequest{Method: http.MethodGet, Endpoint: "/rest/api/3/myself", MaxResponseBytes: tc.cap})
			if out == nil || string(out.Body) != tc.want || out.Truncated != tc.truncated || errors.Is(err, ErrAPIResponseTooLarge) != tc.truncated {
				t.Fatalf("response bound: body retained=%t truncated=%t limit error=%t", out != nil, out != nil && out.Truncated, errors.Is(err, ErrAPIResponseTooLarge))
			}
			if !tc.truncated && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDoAPICanceledContextMakesNoRequest(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var hits atomic.Int32
	// A real transport honors the context before sending; use a loopback
	// server rather than a RoundTripper that ignores context cancellation.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); w.WriteHeader(http.StatusOK) }))
	t.Cleanup(server.Close)
	c := newTestClient(t, server.URL, nil)
	out, err := c.DoAPI(ctx, core.APIRequest{Method: http.MethodGet, Endpoint: "/rest/api/3/myself"})
	if !errors.Is(err, context.Canceled) || out != nil || hits.Load() != 0 {
		t.Fatal("canceled raw request reached the site or fabricated an HTTP receipt")
	}
}

func TestDoAPIPartialBodyKeepsReceiptWithoutReplay(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.Header().Set("Content-Length", "100")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				if _, err := io.WriteString(w, `{"partial":`); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(server.Close)
			c := newTestClient(t, server.URL, nil)
			out, err := c.DoAPI(t.Context(), core.APIRequest{Method: http.MethodPost, Endpoint: "/rest/api/3/issue", MaxResponseBytes: 1024})
			if !errors.Is(err, io.ErrUnexpectedEOF) || hits.Load() != 1 || out == nil || out.StatusCode != status || out.Retry.Attempts != 1 || !out.Truncated || string(out.Body) != `{"partial":` {
				t.Fatalf("incomplete body lost its receipt or caused replay: requests=%d receipt=%t err=%v", hits.Load(), out != nil, err)
			}
			var apiErr *APIError
			if errors.As(err, &apiErr) != (status == http.StatusBadRequest) {
				t.Fatal("body-read failure discarded the original HTTP classification")
			}
		})
	}
}

func TestDoAPIRedirectedWriteRetainsRefusalReceipt(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		w.Header().Set("Location", "/rest/api/3/myself")
		w.Header().Set("Set-Cookie", "fixture_session=private")
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	c := newTestClient(t, server.URL, nil)
	out, err := c.DoAPI(t.Context(), core.APIRequest{Method: http.MethodPost, Endpoint: "/rest/api/3/issue", Body: []byte(`{"fields":{}}`), ContentType: "application/json", MaxResponseBytes: 1024})
	if !errors.Is(err, ErrUnsafeRedirect) || hits.Load() != 1 || out == nil || out.StatusCode != http.StatusTemporaryRedirect || out.Retry.Attempts != 1 || !out.Truncated || len(out.Body) != 0 {
		t.Fatalf("redirected write lost refusal facts or was replayed: requests=%d receipt=%t err=%v", hits.Load(), out != nil, err)
	}
	if _, leaked := out.Headers["Set-Cookie"]; leaked || len(out.Headers["Retry-After"]) != 1 || out.Headers["Retry-After"][0] != "3" {
		t.Fatal("refused redirect leaked cookie state or lost response metadata")
	}
}

func TestDoAPIClosesCapturedResponse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status int
		cap    int64
	}{
		{name: "success", status: http.StatusOK, cap: 1024},
		{name: "HTTP failure", status: http.StatusBadRequest, cap: 1024},
		{name: "capture limit", status: http.StatusOK, cap: 1},
		{name: "HTTP failure and capture limit", status: http.StatusBadRequest, cap: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := &apiTrackedBody{Reader: strings.NewReader(`{"errorMessages":["Missing"]}`)}
			c := newTestClient(t, "https://example.invalid", &http.Client{Transport: redirectTransport(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: http.Header{}, Body: body, Request: req}, nil
			})})
			out, err := c.DoAPI(t.Context(), core.APIRequest{Method: http.MethodPost, Endpoint: "/rest/api/3/issue", MaxResponseBytes: tc.cap})
			if out == nil || !body.closed || out.Truncated != (tc.cap == 1) || errors.Is(err, ErrAPIResponseTooLarge) != (tc.cap == 1) {
				t.Fatal("capture failed to close the response or lost its explicit limit marker")
			}
			var apiErr *APIError
			if errors.As(err, &apiErr) != (tc.status == http.StatusBadRequest) {
				t.Fatal("capture limit discarded the original HTTP classification")
			}
		})
	}
}

type apiTrackedBody struct {
	io.Reader
	closed bool
}

func (body *apiTrackedBody) Close() error {
	body.closed = true
	return nil
}
