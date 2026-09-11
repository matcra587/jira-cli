package unit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

func TestEpicServiceListMembershipAndStatusCounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"issues":[{"key":"EPIC-1","fields":{"summary":"Epic","status":{"name":"To Do"}}}]}`))
	}))
	defer srv.Close()
	service := jira.NewEpicService(jira.NewClient(jira.WithBaseURL(srv.URL + "/")))
	epics, _, err := service.List(context.Background(), nil)
	if err != nil || len(epics) != 1 {
		t.Fatalf("List() = %+v err=%v", epics, err)
	}
	counts := jira.StatusCounts([]*jira.Issue{{Fields: &jira.IssueFields{Status: &jira.Status{Name: new("Done")}}}})
	if counts["Done"] != 1 {
		t.Fatalf("counts = %+v", counts)
	}
}
