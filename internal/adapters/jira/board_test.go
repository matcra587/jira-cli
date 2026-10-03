package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/core"
)

const (
	boardPageFixture   = `{"isLast":false,"startAt":17,"maxResults":3,"values":[{"id":42,"name":"Sprint","type":null,"location":{"projectKey":"KEY"},"future":{"big":9007199254740993,"states":[null,[],false,0]}}],"future":{"text":"<tag>"}}`
	projectPageFixture = `{"isLast":false,"startAt":17,"maxResults":3,"values":[{"id":"10042","key":"KEY","name":null,"avatarUrls":{},"future":{"big":9007199254740993,"states":[null,[],false,0]}}]}`
)

func TestBoardPagesRequestAndFidelity(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != http.MethodGet || q.Get("startAt") != "17" || q.Get("maxResults") != "3" {
			t.Errorf("wrong paging: %s %s", r.Method, r.URL)
		}
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			if q.Get("name") != "Sprint &?+é" || len(q) != 3 {
				t.Errorf("name query changed: %s", r.URL)
			}
			_, _ = fmt.Fprint(w, boardPageFixture)
		case "/rest/agile/1.0/board/42/project":
			if len(q) != 2 {
				t.Errorf("project query changed: %s", r.URL)
			}
			_, _ = fmt.Fprint(w, projectPageFixture)
		default:
			t.Errorf("wrong endpoint: %s", r.URL)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c, err := New(Config{BaseURL: server.URL, Email: "user@example.com", APIToken: "fixture", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	boards, err := c.GetBoards(t.Context(), "Sprint &?+é", 17, 3)
	if err != nil || boards.Page == nil || len(boards.Page.Values.Value) != 1 || boards.Page.IsLast.Value || boards.Page.Total.Set {
		t.Fatalf("board page changed or absent total fabricated: %+v %v", boards, err)
	}
	board := boards.Page.Values.Value[0]
	if board.ID.Value != 42 || !board.Type.Null || string(board.Extra["future"]) != `{"big":9007199254740993,"states":[null,[],false,0]}` || string(boards.Page.Extra["future"]) != `{"text":"<tag>"}` {
		t.Fatal("board identity, presence or extension lost")
	}
	projects, err := c.GetBoardProjects(t.Context(), 42, 17, 3)
	if err != nil || projects.Page == nil || len(projects.Page.Values.Value) != 1 || projects.Page.Total.Set {
		t.Fatalf("project page failed: %+v %v", projects, err)
	}
	project := projects.Page.Values.Value[0]
	if project.ID.Value != "10042" || project.Key.Value != "KEY" || !project.Name.Null || string(project.Extra["future"]) != `{"big":9007199254740993,"states":[null,[],false,0]}` {
		t.Fatal("project identity, presence or extension lost")
	}
}

func TestBoardPagesRejectMalformedProtocol(t *testing.T) {
	t.Parallel()
	for _, domain := range []string{"board", "project"} {
		t.Run(domain, func(t *testing.T) {
			t.Parallel()
			validRow := `{"id":42,"name":"Sprint"}`
			badRows := []string{`null`, `{}`, `{"id":0,"name":"Sprint"}`, `{"id":null,"name":"Sprint"}`, `{"id":42}`, `{"id":42,"name":null}`, `{"id":42,"name":" "}`, `{"id":"42","name":"Sprint"}`}
			if domain == "project" {
				validRow = `{"id":"42","key":"KEY"}`
				badRows = []string{`null`, `{}`, `{"id":42,"key":"KEY"}`, `{"id":null,"key":"KEY"}`, `{"id":" ","key":"KEY"}`, `{"id":"42"}`, `{"id":"42","key":null}`, `{"id":"42","key":" "}`}
			}
			base := `{"isLast":true,"startAt":0,"maxResults":1,"values":[` + validRow + `]}`
			bodies := map[string]string{"null": `null`, "empty-object": `{}`, "wrong-root": `[]`, "trailing-object": base + ` {}`, "trailing-garbage": base + ` garbage`, "broken-json": `{`, "repeat-id": strings.Replace(base, validRow, validRow+`,`+validRow, 1), "empty-nonfinal": `{"isLast":false,"startAt":0,"maxResults":1,"values":[]}`}
			for _, member := range []string{"isLast", "startAt", "maxResults", "values"} {
				var members map[string]json.RawMessage
				if err := json.Unmarshal([]byte(base), &members); err != nil {
					t.Fatal(err)
				}
				members[member] = json.RawMessage(`null`)
				encoded, err := json.Marshal(members)
				if err != nil {
					t.Fatal(err)
				}
				bodies[member+"-null"] = string(encoded)
				delete(members, member)
				encoded, err = json.Marshal(members)
				if err != nil {
					t.Fatal(err)
				}
				bodies[member+"-absent"] = string(encoded)
			}
			bodies["wrong-offset"] = strings.Replace(base, `"startAt":0`, `"startAt":1`, 1)
			bodies["zero-page-size"] = strings.Replace(base, `"maxResults":1`, `"maxResults":0`, 1)
			bodies["negative-total"] = strings.Replace(base, `"values":`, `"total":-1,"values":`, 1)
			bodies["total-before-rows"] = strings.Replace(base, `"values":`, `"total":0,"values":`, 1)
			for i, row := range badRows {
				bodies[fmt.Sprintf("row-%d", i)] = strings.Replace(base, validRow, row, 1)
			}
			for name, body := range bodies {
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body) }))
					defer server.Close()
					c, err := New(Config{BaseURL: server.URL, Email: "user@example.com", APIToken: "fixture"})
					if err != nil {
						t.Fatal(err)
					}
					if domain == "board" {
						out, err := c.GetBoards(t.Context(), "", 0, 1)
						if out.Page != nil || !errors.Is(err, ErrBoardResponseInvalid) {
							t.Fatalf("malformed board page accepted: %+v %v", out, err)
						}
						return
					}
					out, err := c.GetBoardProjects(t.Context(), 42, 0, 1)
					if out.Page != nil || !errors.Is(err, ErrBoardResponseInvalid) {
						t.Fatalf("malformed project page accepted: %+v %v", out, err)
					}
				})
			}
		})
	}
}

func TestBoardPagesOptionalTotalAndEmptySuccess(t *testing.T) {
	t.Parallel()
	for _, suffix := range []string{"", `,"total":null`, `,"total":0`} {
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, `{"isLast":true,"startAt":0,"maxResults":50,"values":[]`+suffix+`}`)
			}))
			defer server.Close()
			c, err := New(Config{BaseURL: server.URL, Email: "user@example.com", APIToken: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			boards, err := c.GetBoards(t.Context(), "", 0, 50)
			if err != nil || boards.Page == nil || boards.Page.Values.Value == nil || len(boards.Page.Values.Value) != 0 || boards.Page.Total.Set != (suffix != "") || boards.Page.Total.Null != (suffix == `,"total":null`) {
				t.Fatalf("empty board page collapsed: %+v %v", boards, err)
			}
			projects, err := c.GetBoardProjects(t.Context(), 42, 0, 50)
			if err != nil || projects.Page == nil || projects.Page.Values.Value == nil || len(projects.Page.Values.Value) != 0 || projects.Page.Total.Set != (suffix != "") || projects.Page.Total.Null != (suffix == `,"total":null`) {
				t.Fatalf("empty project page collapsed: %+v %v", projects, err)
			}
		})
	}
}

func TestBoardPagesRejectDuplicateIdentities(t *testing.T) {
	t.Parallel()
	for _, projects := range []bool{false, true} {
		t.Run(fmt.Sprint(projects), func(t *testing.T) {
			t.Parallel()
			row := `{"id":42,"name":"Sprint"}`
			if projects {
				row = `{"id":"42","key":"KEY"}`
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, `{"isLast":true,"startAt":0,"maxResults":50,"values":[`+row+`,`+row+`]}`)
			}))
			defer server.Close()
			c, err := New(Config{BaseURL: server.URL, Email: "user@example.com", APIToken: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			if projects {
				_, err = c.GetBoardProjects(t.Context(), 42, 0, 50)
			} else {
				_, err = c.GetBoards(t.Context(), "", 0, 50)
			}
			if !errors.Is(err, ErrBoardResponseInvalid) {
				t.Fatalf("duplicate identity accepted: %v", err)
			}
		})
	}
}

func TestBoardPagesRejectOversizedDistinctRows(t *testing.T) {
	t.Parallel()
	for _, projects := range []bool{false, true} {
		for _, bounds := range []struct {
			name                  string
			advertised, requested int
		}{{"request", 2, 1}, {"server", 1, 3}} {
			t.Run(fmt.Sprint(projects)+"-"+bounds.name, func(t *testing.T) {
				t.Parallel()
				rows := `{"id":42,"name":"Sprint"},{"id":43,"name":"Other"}`
				if projects {
					rows = `{"id":"42","key":"KEY"},{"id":"43","key":"OTHER"}`
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = fmt.Fprintf(w, `{"isLast":true,"startAt":0,"maxResults":%d,"values":[%s]}`, bounds.advertised, rows)
				}))
				defer server.Close()
				c, err := New(Config{BaseURL: server.URL, Email: "user@example.com", APIToken: "fixture"})
				if err != nil {
					t.Fatal(err)
				}
				if projects {
					_, err = c.GetBoardProjects(t.Context(), 42, 0, bounds.requested)
				} else {
					_, err = c.GetBoards(t.Context(), "", 0, bounds.requested)
				}
				if !errors.Is(err, ErrBoardResponseInvalid) {
					t.Fatalf("oversized page accepted: %v", err)
				}
			})
		}
	}
}

func TestBoardPagesHTTPFailuresAndSafeRetry(t *testing.T) {
	t.Parallel()
	for _, status := range []int{401, 403, 404, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
			defer server.Close()
			c, err := New(Config{BaseURL: server.URL, Email: "user@example.com", APIToken: "fixture", Retry: core.RetryPolicy{MaxAttempts: 1}})
			if err != nil {
				t.Fatal(err)
			}
			_, boardErr := c.GetBoards(t.Context(), "", 0, 50)
			_, projectErr := c.GetBoardProjects(t.Context(), 42, 0, 50)
			for _, err := range []error{boardErr, projectErr} {
				api, ok := errors.AsType[*APIError](err)
				if !ok || api.StatusCode != status || errors.Is(err, ErrBoardResponseInvalid) {
					t.Fatalf("HTTP failure misclassified: %v", err)
				}
			}
		})
	}
	for _, projects := range []bool{false, true} {
		t.Run(fmt.Sprint(projects), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(503)
					return
				}
				_, _ = fmt.Fprint(w, `{"isLast":true,"startAt":0,"maxResults":50,"values":[]}`)
			}))
			defer server.Close()
			c, err := New(Config{BaseURL: server.URL, Email: "user@example.com", APIToken: "fixture", ReadOnly: true, Retry: core.RetryPolicy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, MaxWait: time.Second}})
			if err != nil {
				t.Fatal(err)
			}
			var retry core.RetryInfo
			if projects {
				out, readErr := c.GetBoardProjects(t.Context(), 42, 0, 50)
				err = readErr
				retry = out.Retry
			} else {
				out, readErr := c.GetBoards(t.Context(), "", 0, 50)
				err = readErr
				retry = out.Retry
			}
			if err != nil || calls.Load() != 2 || retry.Attempts != 2 {
				t.Fatalf("safe retry failed: calls=%d retry=%+v err=%v", calls.Load(), retry, err)
			}
		})
	}
}

func TestBoardPagesCancellationAndInputBounds(t *testing.T) {
	t.Parallel()
	c, err := New(Config{BaseURL: "https://example.atlassian.net", Email: "user@example.com", APIToken: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	boards, err := c.GetBoards(ctx, "", 0, 50)
	if !errors.Is(err, context.Canceled) || boards.Page != nil {
		t.Fatalf("board cancellation lost: %v", err)
	}
	projects, err := c.GetBoardProjects(ctx, 42, 0, 50)
	if !errors.Is(err, context.Canceled) || projects.Page != nil {
		t.Fatalf("project cancellation lost: %v", err)
	}
	for _, input := range []struct {
		id          int64
		start, size int
	}{{0, 0, 50}, {-1, 0, 50}, {42, -1, 50}, {42, 0, 0}, {42, 0, -1}} {
		out, err := c.GetBoardProjects(t.Context(), input.id, input.start, input.size)
		if err == nil || out.Page != nil || errors.Is(err, ErrBoardResponseInvalid) {
			t.Fatalf("invalid caller bounds reached transport or counted as server failure: %v", err)
		}
	}
}
