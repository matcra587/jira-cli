package jira

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestJiraScopeReportsActualPublicRouteWithoutCredentials(t *testing.T) {
	t.Parallel()
	for _, cloudID := range []string{"", "cloud-123"} {
		t.Run(cloudID, func(t *testing.T) {
			t.Parallel()
			const site = "https://jira.example.test/team/"
			const token = "synthetic-not-published"
			client, err := New(Config{BaseURL: site, CloudID: cloudID, Email: "reader@example.test", APIToken: token})
			if err != nil {
				t.Fatal(err)
			}
			api := site
			if cloudID != "" {
				api = "https://api.atlassian.com/ex/jira/cloud-123"
			}
			want := core.JiraScope{Site: site, Account: "reader@example.test", APIBaseURL: api}
			if got := client.JiraScope(); got != want {
				t.Fatal("scope reports configuration rather than the actual transport route", got, want)
			}
			encoded, err := json.Marshal(client.JiraScope())
			if err != nil || strings.Contains(string(encoded), token) {
				t.Fatal("public routing scope exposed credentials", err)
			}
			copy := client.JiraScope()
			copy.APIBaseURL = "https://other.example.test/"
			if client.JiraScope() != want {
				t.Fatal("returned metadata changed the transport")
			}
		})
	}
}

func TestDescriptionReadRefusesLostAuthenticatedBinding(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"missing", "wrong account", "wrong token", "foreign route"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			calls := 0
			client, err := New(Config{BaseURL: "https://jira.example.test", Email: "reader@example.test", APIToken: "fixture", Retry: core.RetryPolicy{MaxAttempts: 3}, HTTPClient: &http.Client{Transport: redirectTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				replyRequest := request.Clone(request.Context())
				switch mode {
				case "missing":
					replyRequest.Header.Del("Authorization")
				case "wrong account":
					replyRequest.SetBasicAuth("other@example.test", "fixture")
				case "wrong token":
					replyRequest.SetBasicAuth("reader@example.test", "other-fixture")
				case "foreign route":
					replyRequest.URL = &url.URL{Scheme: "https", Host: "other.example.test", Path: "/rest/api/3/issue/KEY-1"}
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: replyRequest, Body: io.NopCloser(strings.NewReader(`{"id":"123456","key":"KEY-1","fields":{"description":null}}`))}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			read, err := client.GetIssue(t.Context(), "KEY-1", []string{"description", "updated"})
			if !errors.Is(err, core.ErrIssueDescriptionInvalid) || read.Issue != nil || calls != 1 {
				t.Fatal("unbound reply became native editing input or replayed", err, calls)
			}
		})
	}
}

func TestDescriptionReadRetainsInterruptedBodyCauses(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, io.ErrUnexpectedEOF} {
		t.Run(cause.Error(), func(t *testing.T) {
			t.Parallel()
			client, err := New(Config{BaseURL: "https://jira.example.test", Email: "reader@example.test", APIToken: "fixture", HTTPClient: &http.Client{Transport: redirectTransport(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: request, Body: io.NopCloser(io.MultiReader(strings.NewReader(`{"id":`), transitionErrorReader{err: cause}))}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			read, err := client.GetIssue(t.Context(), "KEY-1", []string{"description", "updated"})
			if !errors.Is(err, core.ErrIssueDescriptionInvalid) || !errors.Is(err, cause) || read.Issue != nil {
				t.Fatal("partial response became a snapshot or lost its body cause", err)
			}
		})
	}
}
