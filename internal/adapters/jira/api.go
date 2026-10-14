package jira

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/matcra587/jira-cli/internal/core"
)

// ErrAPIResponseTooLarge marks an explicitly incomplete raw response receipt.
// A mutation may already have completed; this is never permission to replay it.
var ErrAPIResponseTooLarge = errors.New("api response exceeds capture bound")

// ErrAPIResponseIncomplete distinguishes a failed response capture from bad
// caller input. The captured bytes remain available alongside the cause.
var ErrAPIResponseIncomplete = errors.New("api response capture incomplete")

// APIRequestValidationError identifies an invalid APIRequest option by its Go
// field name. Its message never echoes the caller's value.
type APIRequestValidationError struct {
	Field  string
	Reason string
}

func (e *APIRequestValidationError) Error() string { return e.Reason }

// ValidateAPIRequest is the pure raw-request boundary used before command I/O
// and again at the transport. Domain requests retain their own segment guards.
func ValidateAPIRequest(in core.APIRequest) error {
	switch in.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPost,
		http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return errors.New("api method must be GET, HEAD, OPTIONS, POST, PUT, PATCH or DELETE")
	}
	if !strings.HasPrefix(in.Endpoint, "/rest/api/") && !strings.HasPrefix(in.Endpoint, "/rest/agile/") {
		return fmt.Errorf("%w: endpoint must start with /rest/api/ or /rest/agile/; absolute URLs are refused", ErrUnsafePath)
	}
	if err := validatePathSegments(apiSegments(in.Endpoint)); err != nil {
		return err
	}
	if in.ContentType != "" {
		if strings.ContainsAny(in.ContentType, "\r\n") {
			return &APIRequestValidationError{Field: "ContentType", Reason: "content type cannot contain line breaks"}
		}
		if _, _, err := mime.ParseMediaType(in.ContentType); err != nil {
			return &APIRequestValidationError{Field: "ContentType", Reason: "content type must be a valid media type"}
		}
	}
	if in.IfMatch != "" && !validIfMatch(in.IfMatch) {
		return &APIRequestValidationError{Field: "IfMatch", Reason: "if-match must be * or a comma-separated list of quoted entity tags"}
	}
	if in.IfUnmodifiedSince != "" {
		date, err := http.ParseTime(in.IfUnmodifiedSince)
		if err != nil || date.UTC().Format(http.TimeFormat) != in.IfUnmodifiedSince {
			return &APIRequestValidationError{Field: "IfUnmodifiedSince", Reason: "if-unmodified-since must be an HTTP date in GMT (Mon, 02 Jan 2006 15:04:05 GMT)"}
		}
	}
	if in.MaxResponseBytes < 0 || in.MaxResponseBytes == math.MaxInt64 {
		return &APIRequestValidationError{Field: "MaxResponseBytes", Reason: "response byte cap must be nonnegative and less than the maximum signed 64-bit integer"}
	}
	return nil
}

// RFC 9110 sections 8.8.3 and 13.1.1 define entity tags. Commas inside the
// quoted opaque value are data, and backslashes do not escape quotes. Reject
// outer whitespace because net/http would trim it before transmission.
func validIfMatch(value string) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	if value == "*" {
		return true
	}
	for {
		value = strings.TrimLeft(value, " \t")
		value = strings.TrimPrefix(value, "W/")
		if len(value) < 2 || value[0] != '"' {
			return false
		}
		value = value[1:]
		end := strings.IndexByte(value, '"')
		if end < 0 {
			return false
		}
		for i := range end {
			if value[i] < 0x21 || value[i] == 0x7f {
				return false
			}
		}
		value = strings.TrimLeft(value[end+1:], " \t")
		if value == "" {
			return true
		}
		if value[0] != ',' {
			return false
		}
		value = value[1:]
	}
}

func apiSegments(endpoint string) []string {
	return strings.Split(strings.TrimPrefix(endpoint, "/"), "/")
}

// APIRequestIsRead uses the same method/path classification as the built
// transport request. Call ValidateAPIRequest first; this does not validate.
func APIRequestIsRead(in core.APIRequest) bool { return isReadPath(in.Method, in.Endpoint) }

// DoAPI implements core.JiraClient without adding a second transport stack.
// Raw bodies share request construction with domain JSON bodies but retain
// their original bytes and content type rather than being marshaled again.
func (c *Client) DoAPI(ctx context.Context, in core.APIRequest) (*core.APIResponse, error) {
	return c.doAPI(ctx, in, nil)
}

// doAPI lets bound domain reads validate the final successful reply before
// capture. Raw API requests continue to support credential-free media redirects.
func (c *Client) doAPI(ctx context.Context, in core.APIRequest, admit func(*http.Response) error) (*core.APIResponse, error) {
	if err := ValidateAPIRequest(in); err != nil {
		return nil, err
	}
	var reader io.Reader
	if in.Body != nil {
		reader = bytes.NewReader(in.Body)
	}
	req, err := c.newBodyRequest(ctx, in.Method, apiSegments(in.Endpoint), in.Query, reader, in.ContentType)
	if err != nil {
		return nil, err
	}
	if in.IfMatch != "" {
		req.Header.Set("If-Match", in.IfMatch)
	}
	if in.IfUnmodifiedSince != "" {
		req.Header.Set("If-Unmodified-Since", in.IfUnmodifiedSince)
	}
	resp, info, requestErr := c.doWithRetry(req)
	if resp == nil {
		return nil, requestErr
	}
	defer resp.Body.Close() //nolint:errcheck // body-read failures are reported separately
	out := &core.APIResponse{StatusCode: resp.StatusCode, Headers: apiResponseHeaders(resp.Header), Retry: info}
	if requestErr != nil {
		// net/http closes a refused redirect's body. Retain its status/headers,
		// mark the missing body and return the original permanent refusal.
		out.Truncated = true
		return out, requestErr
	}
	if admit != nil && resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		if err := admit(resp); err != nil {
			return nil, err
		}
	}
	var bodyReader io.Reader = resp.Body
	if in.MaxResponseBytes > 0 {
		bodyReader = io.LimitReader(resp.Body, in.MaxResponseBytes+1)
	}
	body, readErr := io.ReadAll(bodyReader)
	if readErr != nil {
		readErr = fmt.Errorf("%w: %w", ErrAPIResponseIncomplete, readErr)
	}
	var limitErr error
	if in.MaxResponseBytes > 0 && int64(len(body)) > in.MaxResponseBytes {
		body = body[:in.MaxResponseBytes]
		limitErr = fmt.Errorf("%w: %d bytes", ErrAPIResponseTooLarge, in.MaxResponseBytes)
	}
	out.Body = body
	out.Truncated = readErr != nil || limitErr != nil
	var statusErr error
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		copyResponse := *resp
		copyResponse.Body = io.NopCloser(bytes.NewReader(body))
		statusErr = c.apiError(&copyResponse, info)
	}
	return out, errors.Join(statusErr, readErr, limitErr)
}

// apiResponseHeaders is an allowlist: arbitrary server headers may contain
// credentials. Content, cache, retry, correlation and rate-limit facts remain
// inspectable; authorization and cookie state never enter an output envelope.
func apiResponseHeaders(headers http.Header) map[string][]string {
	out := map[string][]string{}
	for key, values := range headers {
		name := http.CanonicalHeaderKey(key)
		switch name {
		case "Content-Type", "Content-Length", "Content-Disposition", "Retry-After",
			"Etag", "Last-Modified", "Cache-Control", "X-Arequestid", "X-Asen":
			out[name] = slices.Clone(values)
		default:
			lower := strings.ToLower(name)
			if strings.HasPrefix(lower, "x-ratelimit-") || strings.HasPrefix(lower, "ratelimit-") {
				out[name] = slices.Clone(values)
			}
		}
	}
	return out
}

// newBodyRequest is the shared authenticated request constructor for buffered
// domain JSON, raw bytes and multipart streams. Safety runs before credentials
// are attached, and http.NewRequest supplies GetBody for bytes-backed reads.
func (c *Client) newBodyRequest(ctx context.Context, method string, segments []string, query url.Values, body io.Reader, contentType string) (*http.Request, error) {
	if err := validatePathSegments(segments); err != nil {
		return nil, err
	}
	u := c.base.JoinPath(segments...)
	if !withinBase(c.base, u) {
		return nil, fmt.Errorf("%w: path resolves outside configured API scope", ErrUnsafePath)
	}
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if c.readOnly && !c.isRead(req) {
		return nil, fmt.Errorf("%w: %s %s", ErrReadOnly, method, c.relativePath(req))
	}
	req.SetBasicAuth(c.email, c.token)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req, nil
}
