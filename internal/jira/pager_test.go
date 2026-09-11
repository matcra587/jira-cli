package jira

import (
	"context"
	"testing"
)

type fakePageLister struct {
	gotTokens []string
	pages     [][]*Issue
	resps     []*Response
	call      int
}

func (f *fakePageLister) List(_ context.Context, opts *IssueListOptions) ([]*Issue, *Response, error) {
	f.gotTokens = append(f.gotTokens, opts.NextPageToken)
	i := f.call
	f.call++
	return f.pages[i], f.resps[i], nil
}

// TestListIssuesPageThreadsCursor pins the cursor contract: the zero cursor
// requests the first page, the returned cursor carries the server token, and a
// last page yields a done cursor.
func TestListIssuesPageThreadsCursor(t *testing.T) {
	svc := &fakePageLister{
		pages: [][]*Issue{
			{{Key: new("A-1")}},
			{{Key: new("A-2")}},
		},
		resps: []*Response{
			{NextPageToken: "tok-2", IsLast: false},
			{IsLast: true},
		},
	}
	opts := &IssueListOptions{JQL: "project = A"}

	first, cur, err := ListIssuesPage(context.Background(), svc, opts, PageCursor{})
	if err != nil || len(first) != 1 {
		t.Fatalf("first page = %v issues, err %v", len(first), err)
	}
	if !cur.More() {
		t.Fatal("cursor after a non-final page should report More")
	}

	second, cur, err := ListIssuesPage(context.Background(), svc, opts, cur)
	if err != nil || len(second) != 1 {
		t.Fatalf("second page = %v issues, err %v", len(second), err)
	}
	if cur.More() {
		t.Error("cursor after the final page should be done")
	}
	if svc.gotTokens[0] != "" || svc.gotTokens[1] != "tok-2" {
		t.Errorf("tokens sent = %q, want [\"\" tok-2]", svc.gotTokens)
	}
	if opts.NextPageToken != "" {
		t.Error("caller's options must not be mutated")
	}
}
