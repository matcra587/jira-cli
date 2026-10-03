package jira

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

// notifyUsers is a QUERY parameter on both endpoints that take it, which
// makes it the one request input invisible in the published payload —
// `--dry-run` shows the body, and the body never mentions it. So the
// only place its wiring can be checked is the URL a real request
// carries, which is what these tests read.

// TestEditIssueSendsNotifyUsersOnlyWhenSuppressing pins both halves of
// the rule. Sending notifyUsers=true would add a parameter to every edit
// request in the CLI while changing nothing about any of them, since
// true is Jira's own default — the wire equivalent of a flag that does
// nothing. A request carrying it is a request that means it.
func TestEditIssueSendsNotifyUsersOnlyWhenSuppressing(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		notify bool
		want   string // "" means the parameter must be absent
	}{
		{name: "default sends nothing", notify: true, want: ""},
		{name: "suppressed sends false", notify: false, want: "false"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got string
			var present bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.Query().Get("notifyUsers")
				present = r.URL.Query().Has("notifyUsers")
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(srv.Close)

			c := newTestClient(t, srv.URL, srv.Client())
			if _, err := c.EditIssue(t.Context(), "ROCK-1", jira.IssueWriteRequest{}, tt.notify); err != nil {
				t.Fatalf("EditIssue: %v", err)
			}
			if tt.want == "" {
				if present {
					t.Errorf("notifyUsers=%q was sent for the default; a parameter that restates Jira's own default changes nothing and should not be on the wire", got)
				}
				return
			}
			if got != tt.want {
				t.Errorf("notifyUsers = %q, want %q — without it the flag parses and the emails still go out", got, tt.want)
			}
		})
	}
}

// TestUpdateCommentSendsNotifyUsersOnlyWhenSuppressing is the same guard
// on the comment endpoint, which takes the identical parameter.
func TestUpdateCommentSendsNotifyUsersOnlyWhenSuppressing(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		notify  bool
		present bool
	}{
		{name: "default sends nothing", notify: true, present: false},
		{name: "suppressed sends false", notify: false, present: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got string
			var present bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.Query().Get("notifyUsers")
				present = r.URL.Query().Has("notifyUsers")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"10042"}`))
			}))
			t.Cleanup(srv.Close)

			c := newTestClient(t, srv.URL, srv.Client())
			if _, err := c.UpdateComment(t.Context(), "ROCK-1", "10042", jira.UpdateCommentRequest{}, tt.notify); err != nil {
				t.Fatalf("UpdateComment: %v", err)
			}
			if present != tt.present {
				t.Errorf("notifyUsers present = %v (value %q), want present = %v", present, got, tt.present)
			}
			if tt.present && got != "false" {
				t.Errorf("notifyUsers = %q, want %q", got, "false")
			}
		})
	}
}
