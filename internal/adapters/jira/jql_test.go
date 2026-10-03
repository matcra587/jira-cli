package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestParseJQLRequestReadOnlyAndDiagnostics(t *testing.T) {
	t.Parallel()
	queries := []string{"project = KEY", "summary ~ \"<&>é\"", "project = KEY"}
	for _, mode := range []core.JQLValidationMode{core.JQLValidationStrict, core.JQLValidationWarn, core.JQLValidationNone} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				body, err := io.ReadAll(r.Body)
				var request struct {
					Queries []string `json:"queries"`
				}
				if err != nil || json.Unmarshal(body, &request) != nil || !reflect.DeepEqual(request.Queries, queries) || r.Method != http.MethodPost || r.URL.Path != "/proxy/rest/api/3/jql/parse" || len(r.URL.Query()) != 1 || r.URL.Query().Get("validation") != string(mode) || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" || r.Header.Get("Authorization") == "" || !bytes.Contains(body, []byte("<&>é")) {
					t.Error("parser changed input order, duplicates, strings, mode, proxy route or shared headers")
				}
				_, _ = io.WriteString(w, `{"queries":[{"query":"normalized","errors":[]},{"query":"invalid","errors":["syntax error"],"warnings":["warning"],"structure":null},{"query":"project = KEY","warnings":[]}]}`)
			}))
			defer server.Close()
			c, err := New(Config{BaseURL: server.URL + "/proxy", Email: "user@example.com", APIToken: "fixture", ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			out, err := c.ParseJQL(t.Context(), queries, mode)
			if err != nil || out.Parsed == nil || len(out.Parsed.Queries.Value) != 3 || out.Parsed.Queries.Value[1].Errors.Value[0] != "syntax error" || hits.Load() != 1 || out.Retry.Attempts != 1 {
				t.Fatalf("query diagnostics became transport failures: %+v %v calls=%d", out, err, hits.Load())
			}
		})
	}
}

func TestJQLReadsKeepHTTPFailuresAndRejectTrailingData(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"parse", "reference"} {
		for _, status := range []int{200, 201, 204, 400, 401, 403, 429, 503} {
			for _, trailing := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/trailing=%t", operation, status, trailing), func(t *testing.T) {
					t.Parallel()
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(status)
						body := `{"queries":[{"query":"one"}]}`
						if operation == "reference" {
							body = `{}`
						}
						if trailing {
							body += " {}"
						}
						_, _ = io.WriteString(w, body)
					}))
					defer server.Close()
					c, err := New(Config{BaseURL: server.URL, Email: "user@example.com", APIToken: "fixture", ReadOnly: true})
					if err != nil {
						t.Fatal(err)
					}
					if operation == "parse" {
						_, err = c.ParseJQL(t.Context(), []string{"one"}, core.JQLValidationStrict)
					} else {
						_, err = c.GetJQLReference(t.Context())
					}
					if status == 200 {
						if trailing != errors.Is(err, ErrJQLResponseInvalid) || (!trailing && err != nil) {
							t.Fatalf("trailing/success classification: %v", err)
						}
					} else {
						api, ok := errors.AsType[*APIError](err)
						if !ok || api.StatusCode != status || errors.Is(err, ErrJQLResponseInvalid) {
							t.Fatalf("lost HTTP failure: %v", err)
						}
					}
				})
			}
		}
	}
}

func TestJQLReadsValidateBeforeHTTPAndHonorCancellation(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); _, _ = io.WriteString(w, `{}`) }))
	defer server.Close()
	c, err := New(Config{BaseURL: server.URL, Email: "user@example.com", APIToken: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		queries []string
		mode    core.JQLValidationMode
	}{{nil, core.JQLValidationStrict}, {[]string{""}, core.JQLValidationStrict}, {[]string{"one"}, "STRICT"}} {
		if _, err := c.ParseJQL(t.Context(), test.queries, test.mode); err == nil {
			t.Fatal("invalid parser request accepted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.ParseJQL(ctx, []string{"one"}, core.JQLValidationStrict); !errors.Is(err, context.Canceled) {
		t.Fatalf("parse lost cancellation: %v", err)
	}
	if _, err := c.GetJQLReference(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("reference lost cancellation: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("invalid/canceled request reached HTTP: %d", hits.Load())
	}
}

func TestJQLReadsRejectInvalidWireResponses(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"parse", "reference"} {
		for _, body := range []string{``, `null`, `[]`, `{"queries":[]}`, `{"queries":[{}],"visibleFieldNames":[null]}`, `{`} {
			t.Run(operation+"/"+body, func(t *testing.T) {
				t.Parallel()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
				defer server.Close()
				c, err := New(Config{BaseURL: server.URL, Email: "user@example.com", APIToken: "fixture"})
				if err != nil {
					t.Fatal(err)
				}
				if operation == "parse" {
					out, err := c.ParseJQL(t.Context(), []string{"one"}, core.JQLValidationStrict)
					if !errors.Is(err, ErrJQLResponseInvalid) || out.Parsed != nil {
						t.Fatalf("invalid parser response accepted: %+v %v", out, err)
					}
				} else {
					out, err := c.GetJQLReference(t.Context())
					// queries is an unmodeled root extension on reference.
					if body == `{"queries":[]}` {
						if err != nil || out.Reference == nil || string(out.Reference.Extra["queries"]) != `[]` {
							t.Fatalf("reference extension rejected: %+v %v", out, err)
						}
					} else if !errors.Is(err, ErrJQLResponseInvalid) || out.Reference != nil {
						t.Fatalf("invalid reference accepted: %+v %v", out, err)
					}
				}
			})
		}
	}
}

func TestJQLReadRetryReplaysIdenticalBody(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"parse", "reference"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			var first []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				attempt := hits.Add(1)
				if attempt == 1 {
					first = body
				} else if !bytes.Equal(first, body) {
					t.Error("safe retry changed parser body")
				}
				if attempt == 1 {
					w.WriteHeader(429)
					return
				}
				if attempt == 2 {
					w.WriteHeader(503)
					return
				}
				if operation == "parse" {
					_, _ = io.WriteString(w, `{"queries":[{"query":"one"}]}`)
				} else {
					if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/jql/autocompletedata" || len(r.URL.Query()) != 0 || r.ContentLength != 0 {
						t.Error("reference request acquired a body or wrong route")
					}
					_, _ = io.WriteString(w, `{"visibleFieldNames":[],"visibleFunctionNames":null,"jqlReservedWords":[]}`)
				}
			}))
			defer server.Close()
			c, slept := newRetryTestClient(t, server.URL, retryPolicy())
			c.readOnly = true
			var retry core.RetryInfo
			var err error
			if operation == "parse" {
				var out core.JQLParseRead
				out, err = c.ParseJQL(t.Context(), []string{"one"}, core.JQLValidationStrict)
				retry = out.Retry
			} else {
				var out core.JQLReferenceRead
				out, err = c.GetJQLReference(t.Context())
				retry = out.Retry
			}
			if err != nil || hits.Load() != 3 || retry.Attempts != 3 || retry.Waited <= 0 || len(*slept) != 2 {
				t.Fatalf("read retry failed: %v hits=%d retry=%+v sleeps=%v", err, hits.Load(), retry, *slept)
			}
		})
	}
}

func TestJQLReadTimeoutWhileCheckingTrailingData(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"parse", "reference"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := `{"queries":[{"query":"one"}]}`
				if operation == "reference" {
					body = `{}`
				}
				_, _ = io.WriteString(w, body)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer server.Close()
			c, err := New(Config{BaseURL: server.URL, Email: "user@example.com", APIToken: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()
			if operation == "parse" {
				_, err = c.ParseJQL(ctx, []string{"one"}, core.JQLValidationStrict)
			} else {
				_, err = c.GetJQLReference(ctx)
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("trailing read lost timeout identity: %v", err)
			}
		})
	}
}

func TestJQLReadClassificationRequiresExactPathAndMethod(t *testing.T) {
	t.Parallel()
	c, err := New(Config{BaseURL: "https://example.atlassian.net/proxy", Email: "user@example.com", APIToken: "fixture", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{parseJQLPath, parseJQLPath + "/", parseJQLPath + "/write", "/rest/api/3/jql/Parse", "/rest/api/3/jql/%70arse", "/rest/api/3/jql/autocompletedata", "/rest/api/3/bulk/issues/move", "/rest/api/3/issue"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			want := method == http.MethodPost && path == parseJQLPath
			request, err := http.NewRequestWithContext(t.Context(), method, "https://example.atlassian.net/proxy"+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			if c.safeToRetry(request) != want || c.isRead(request) != want || APIRequestIsRead(core.APIRequest{Method: method, Endpoint: path}) != want {
				t.Fatalf("classification widened or drifted for %s %s", method, path)
			}
			segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
			_, err = c.newRequest(t.Context(), method, segments, nil, nil)
			if want && err != nil {
				t.Fatalf("known read refused: %v", err)
			}
			if !want && err == nil {
				t.Fatalf("unlisted write passed read-only gate: %s %s", method, path)
			}
		}
	}
}
