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

const (
	metadataTypePage  = `{"startAt":3,"maxResults":2,"total":4,"issueTypes":[{"id":"10001","name":"Task","future":{"n":9007199254740993}}],"future":null}`
	metadataFieldPage = `{"startAt":3,"maxResults":2,"total":4,"fields":[{"fieldId":"summary","key":"summary","name":"Summary","operations":["set"],"schema":{"type":"string"},"required":true,"allowedValues":[],"defaultValue":null,"future":{"text":"\u003c&"}}],"future":[]}`
	metadataEditMap   = `{"fields":{"summary":{"key":"summary","name":"Summary","schema":{"type":"string"},"required":true,"operations":["set"],"future":{"n":9007199254740993}}},"future":null}`
)

func TestCreateProjectReadUsesScopedAuthenticatedReadOnlyTransport(t *testing.T) {
	t.Parallel()
	for _, selected := range []string{"ENG", "10402", "Équipe space"} {
		t.Run(selected, func(t *testing.T) {
			t.Parallel()
			const native = ` {"id":"10402","key":"ENG","future":{"n":9007199254740993},"optional":null} `
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				user, password, ok := r.BasicAuth()
				if !ok || user != "fixture@example.com" || password != "fixture" || r.Method != http.MethodGet || r.URL.Path != "/ex/jira/fixture-cloud/rest/api/3/project/"+selected || r.URL.RawQuery != "" {
					t.Errorf("project read changed scope, selector, method or authentication: %s %s", r.Method, r.URL.Path)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || len(body) != 0 {
					t.Error("project read unexpectedly sent a body")
				}
				_, _ = fmt.Fprint(w, native)
			}))
			defer server.Close()
			client, err := New(Config{BaseURL: server.URL + "/ex/jira/fixture-cloud", Email: "fixture@example.com", APIToken: "fixture", ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			read, err := client.GetCreateIssueProject(t.Context(), selected)
			if err != nil || read.ID != "10402" || read.Key != "ENG" || string(read.Native) != native || read.Retry.Attempts != 1 || calls.Load() != 1 {
				t.Fatalf("project read lost identity, native bytes or retry facts: %+v %v", read, err)
			}
		})
	}
}

func TestCreateProjectReadRefusesPathInjectionBeforeHTTP(t *testing.T) {
	t.Parallel()
	for _, selected := range []string{"", "..", "%2e%2e", "ENG/admin", "ENG?expand=secret", "ENG\n"} {
		t.Run(selected, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				_, _ = fmt.Fprint(w, `{"id":"10402","key":"ENG"}`)
			}))
			defer server.Close()
			client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.com", APIToken: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			read, err := client.GetCreateIssueProject(t.Context(), selected)
			if err == nil || calls.Load() != 0 || read.Native != nil || read.ID != "" {
				t.Fatalf("unsafe project selector reached HTTP or established identity: %+v %v calls=%d", read, err, calls.Load())
			}
		})
	}
}

func TestCreateProjectReadRequiresCompleteHTTP200(t *testing.T) {
	t.Parallel()
	for _, status := range []int{200, 201, 202, 204, 206, 400, 401, 403, 404, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = fmt.Fprint(w, `{"id":"10402","key":"ENG"}`)
			}))
			defer server.Close()
			client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.com", APIToken: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			read, err := client.GetCreateIssueProject(t.Context(), "ENG")
			if status == 200 {
				if err != nil || read.ID != "10402" {
					t.Fatalf("native200 refused: %+v %v", read, err)
				}
				return
			}
			if err == nil || read.ID != "" || read.Native != nil {
				t.Fatalf("unexpected status established an identity: %+v %v", read, err)
			}
			if status >= 400 {
				api, ok := errors.AsType[*APIError](err)
				if !ok || api.StatusCode != status {
					t.Fatalf("project HTTP failure lost its type: %v", err)
				}
			} else if !errors.Is(err, core.ErrIssueMetadataInvalid) {
				t.Fatalf("unexpected success lost protocol classification: %v", err)
			}
		})
	}
}

func TestCreateProjectReadRetriesOnlySafeRequests(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("project retry became a mutation")
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, `{"id":"10402","key":"ENG"}`)
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.com", APIToken: "fixture", Retry: core.DefaultRetryPolicy(), ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	client.sleep = func(context.Context, time.Duration) error { return nil }
	read, err := client.GetCreateIssueProject(t.Context(), "ENG")
	if err != nil || read.Retry.Attempts != 2 || calls.Load() != 2 {
		t.Fatalf("project read bypassed safe retry or lost attempts: %+v %v", read, err)
	}
}

func TestIssueMetadataReadsUseScopedAuthenticatedTransport(t *testing.T) {
	t.Parallel()
	for _, project := range []string{"ENG", "Équipe space"} {
		t.Run(project, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				user, password, auth := r.BasicAuth()
				if !auth || user != "fixture@example.com" || password != "fixture" || r.Method != http.MethodGet {
					t.Error("metadata bypassed shared authentication or used a mutation method")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || len(body) != 0 {
					t.Error("metadata request unexpectedly has a body")
				}
				prefix := "/ex/jira/fixture-cloud/rest/api/3/issue/"
				switch r.URL.Path {
				case prefix + "createmeta/" + project + "/issuetypes":
					if r.URL.Query().Get("startAt") != "3" || r.URL.Query().Get("maxResults") != "2" || len(r.URL.Query()) != 2 {
						t.Error("issue-type pagination changed")
					}
					_, _ = fmt.Fprint(w, metadataTypePage)
				case prefix + "createmeta/" + project + "/issuetypes/10001":
					if r.URL.Query().Get("startAt") != "3" || r.URL.Query().Get("maxResults") != "2" || len(r.URL.Query()) != 2 {
						t.Error("field pagination changed")
					}
					_, _ = fmt.Fprint(w, metadataFieldPage)
				case prefix + "ENG-123/editmeta":
					if r.URL.RawQuery != "" {
						t.Error("edit metadata enabled an override or invented pagination")
					}
					_, _ = fmt.Fprint(w, metadataEditMap)
				default:
					t.Errorf("metadata escaped configured scope: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client, err := New(Config{BaseURL: server.URL + "/ex/jira/fixture-cloud", Email: "fixture@example.com", APIToken: "fixture", ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			types, err := client.GetCreateIssueTypes(t.Context(), project, 3, 2)
			if err != nil || types.Page == nil || string(types.Page.Native) != metadataTypePage || types.Retry.Attempts != 1 {
				t.Fatalf("scoped issue types lost native data or transport facts: %+v %v", types, err)
			}
			fields, err := client.GetCreateIssueFields(t.Context(), project, "10001", 3, 2)
			if err != nil || fields.Page == nil || string(fields.Page.Native) != metadataFieldPage || fields.Retry.Attempts != 1 {
				t.Fatalf("scoped create fields lost native data or transport facts: %+v %v", fields, err)
			}
			edit, err := client.GetEditIssueFields(t.Context(), "ENG-123")
			if err != nil || edit.Fields == nil || string(edit.Native) != metadataEditMap || edit.Retry.Attempts != 1 {
				t.Fatalf("scoped edit fields lost native data or transport facts: %+v %v", edit, err)
			}
			if calls.Load() != 3 {
				t.Fatalf("metadata methods secretly walked, mutated or repeated requests: %d", calls.Load())
			}
		})
	}
}

func TestIssueMetadataRejectsInvalidInputBeforeHTTP(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		call func(context.Context, *Client) error
	}{
		{name: "negative-offset", call: func(ctx context.Context, c *Client) error {
			_, err := c.GetCreateIssueTypes(ctx, "ENG", -1, 50)
			return err
		}},
		{name: "zero-size", call: func(ctx context.Context, c *Client) error {
			_, err := c.GetCreateIssueTypes(ctx, "ENG", 0, 0)
			return err
		}},
		{name: "oversized-size", call: func(ctx context.Context, c *Client) error {
			_, err := c.GetCreateIssueFields(ctx, "ENG", "10001", 0, 201)
			return err
		}},
		{name: "project-traversal", call: func(ctx context.Context, c *Client) error {
			_, err := c.GetCreateIssueTypes(ctx, "..", 0, 50)
			return err
		}},
		{name: "type-separator", call: func(ctx context.Context, c *Client) error {
			_, err := c.GetCreateIssueFields(ctx, "ENG", "10001/admin", 0, 50)
			return err
		}},
		{name: "empty-project", call: func(ctx context.Context, c *Client) error {
			_, err := c.GetCreateIssueTypes(ctx, "", 0, 50)
			return err
		}},
		{name: "encoded-project", call: func(ctx context.Context, c *Client) error {
			_, err := c.GetCreateIssueFields(ctx, "%2e%2e", "10001", 0, 50)
			return err
		}},
		{name: "edit-query", call: func(ctx context.Context, c *Client) error {
			_, err := c.GetEditIssueFields(ctx, "ENG-123?overrideScreenSecurity=true")
			return err
		}},
		{name: "edit-control", call: func(ctx context.Context, c *Client) error {
			_, err := c.GetEditIssueFields(ctx, "ENG-123\n")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); _, _ = fmt.Fprint(w, metadataTypePage) }))
			defer server.Close()
			client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.com", APIToken: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.call(t.Context(), client); err == nil || calls.Load() != 0 {
				t.Fatalf("invalid metadata input reached HTTP: calls=%d err=%v", calls.Load(), err)
			}
		})
	}
}

func TestIssueMetadataRejectsUnexpectedSuccessAndTypedHTTPFailures(t *testing.T) {
	t.Parallel()
	for _, status := range []int{200, 201, 202, 204, 206, 400, 401, 403, 404, 429, 500} {
		t.Run(fmt.Sprintf("status-%d", status), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = fmt.Fprint(w, `{}`)
			}))
			defer server.Close()
			client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.com", APIToken: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			read, err := client.GetCreateIssueFields(t.Context(), "ENG", "10001", 0, 50)
			if read.Page != nil || err == nil || calls.Load() != 1 {
				t.Fatalf("invalid response became successful metadata: %+v %v", read, err)
			}
			if status >= 400 {
				var api *APIError
				if !errors.As(err, &api) || api.StatusCode != status {
					t.Fatalf("HTTP failure lost its type: %v", err)
				}
			} else if !errors.Is(err, core.ErrIssueMetadataInvalid) {
				t.Fatalf("unexpected success lost invalid-metadata classification: %v", err)
			}
		})
	}
}

func TestIssueMetadataPreservesCancellationAndTimeout(t *testing.T) {
	t.Parallel()
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint(deadline), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); _, _ = fmt.Fprint(w, metadataTypePage) }))
			defer server.Close()
			client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.com", APIToken: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			want := context.Canceled
			if deadline {
				cancel()
				ctx, cancel = context.WithDeadline(t.Context(), time.Time{})
				want = context.DeadlineExceeded
			}
			cancel()
			read, err := client.GetCreateIssueTypes(ctx, "ENG", 3, 2)
			if read.Page != nil || !errors.Is(err, want) || calls.Load() != 0 {
				t.Fatalf("cancellation lost or request dispatched: %+v %v calls=%d", read, err, calls.Load())
			}
		})
	}
}

func TestIssueMetadataSafeReadRetryReportsAttempts(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("metadata retry used a write method")
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, metadataEditMap)
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.com", APIToken: "fixture", Retry: core.DefaultRetryPolicy(), ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	client.sleep = func(context.Context, time.Duration) error { return nil }
	read, err := client.GetEditIssueFields(t.Context(), "ENG-123")
	if err != nil || read.Fields == nil || read.Retry.Attempts != 2 || calls.Load() != 2 {
		t.Fatalf("safe metadata retry lost: %+v %v calls=%d", read, err, calls.Load())
	}
}

func TestIssueMetadataCaptureBoundRejectsIncompleteDocument(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"fields":{},"padding":"`)
		_, _ = io.CopyN(w, strings.NewReader(strings.Repeat("x", int(core.DefaultMaxAPIResponseBytes)+1)), core.DefaultMaxAPIResponseBytes+1)
		_, _ = fmt.Fprint(w, `"}`)
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.com", APIToken: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	read, err := client.GetEditIssueFields(t.Context(), "ENG-123")
	if read.Fields != nil || len(read.Native) != 0 || !errors.Is(err, ErrAPIResponseTooLarge) {
		t.Fatalf("truncated metadata published as complete: fields=%d native_bytes=%d err=%v", len(read.Fields), len(read.Native), err)
	}
}

func TestIssueMetadataRejectsUnboundRedirects(t *testing.T) {
	t.Parallel()
	for _, scope := range []string{"foreign-origin", "outside-base", "inside-base"} {
		for _, resource := range []string{"project", "types", "fields", "edit"} {
			t.Run(scope+"/"+resource, func(t *testing.T) {
				t.Parallel()
				var replies atomic.Int32
				reply := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					replies.Add(1)
					user, token, bound := r.BasicAuth()
					if scope == "inside-base" {
						if !bound || user != "fixture@example.com" || token != "fixture" {
							t.Error("scoped redirect lost credentials")
						}
					} else if bound {
						t.Error("unscoped redirect leaked credentials")
					}
					switch resource {
					case "project":
						_, _ = io.WriteString(w, `{"id":"10402","key":"ENG"}`)
					case "types":
						_, _ = io.WriteString(w, strings.ReplaceAll(metadataTypePage, `"startAt":3`, `"startAt":0`))
					case "fields":
						_, _ = io.WriteString(w, strings.ReplaceAll(metadataFieldPage, `"startAt":3`, `"startAt":0`))
					case "edit":
						_, _ = io.WriteString(w, metadataEditMap)
					}
				})
				foreign := httptest.NewServer(reply)
				defer foreign.Close()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/reply") {
						reply.ServeHTTP(w, r)
						return
					}
					target := foreign.URL + "/reply"
					if scope == "outside-base" {
						target = "/reply"
					}
					if scope == "inside-base" {
						target = "/ex/jira/fixture-cloud/reply"
					}
					http.Redirect(w, r, target, http.StatusFound)
				}))
				defer server.Close()
				client, err := New(Config{BaseURL: server.URL + "/ex/jira/fixture-cloud", Email: "fixture@example.com", APIToken: "fixture"})
				if err != nil {
					t.Fatal(err)
				}
				switch resource {
				case "project":
					_, err = client.GetCreateIssueProject(t.Context(), "ENG")
				case "types":
					_, err = client.GetCreateIssueTypes(t.Context(), "ENG", 0, 2)
				case "fields":
					_, err = client.GetCreateIssueFields(t.Context(), "ENG", "10001", 0, 2)
				case "edit":
					_, err = client.GetEditIssueFields(t.Context(), "ENG-1")
				}
				if replies.Load() != 1 {
					t.Fatalf("redirect guard did not observe a reply: %d", replies.Load())
				}
				if scope == "inside-base" {
					if err != nil {
						t.Fatalf("authenticated scoped redirect refused: %v", err)
					}
					return
				}
				if !errors.Is(err, core.ErrIssueMetadataInvalid) {
					t.Fatalf("unbound metadata established a context: %v", err)
				}
			})
		}
	}
}
