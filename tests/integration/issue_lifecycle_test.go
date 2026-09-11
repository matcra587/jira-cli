package integration

// End-to-end integration test driving one issue through every
// lifecycle service exposed by the issue-lifecycle work.
//
// This test exercises the pkg/jira services directly (not the cobra CLI)
// so it stays fast and deterministic; the CLI surface is covered by the
// contract suite. The service-level walkthrough confirms:
//   - attachment add → list → download → delete
//   - comment add → list → edit → delete
//   - watcher list → add → remove
//   - issue link list → delete; link types fetch
//
// The fixture server handles every endpoint inline so a future
// regression in any of the four services flips this single test red
// rather than hiding behind a passing happy-path elsewhere.

import (
	"context"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestIssueLifecycleEndToEnd(t *testing.T) {
	mux := http.NewServeMux()

	// ----- Issue GET (attachment + issuelinks expansions) -----------
	mux.HandleFunc("/rest/api/3/issue/PROJ-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// link.List restricts to the issuelinks field; attachment.List
		// fetches the full issue. Branch on the `fields` query so each
		// service sees the shape it expects.
		if strings.Contains(r.URL.RawQuery, "issuelinks") {
			_, _ = w.Write([]byte(`{"key":"PROJ-1","fields":{"issuelinks":[
				{"id":"L1","type":{"name":"Blocks","inward":"is blocked by","outward":"blocks"},
				 "outwardIssue":{"key":"PROJ-2","fields":{"summary":"target","status":{"name":"To Do"}}}}
			]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"key":"PROJ-1","fields":{"attachment":[
			{"id":"100","filename":"trace.log","mimeType":"text/plain","size":5,
			 "author":{"accountId":"a","displayName":"A"},
			 "created":"2026-04-01T00:00:00.000+0000",
			 "content":"http://x/100","self":"http://x/100"}
		]}}`))
	})
	mux.HandleFunc("/rest/api/3/issue/PROJ-1/attachments", func(w http.ResponseWriter, r *http.Request) {
		// Verify multipart upload contract.
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
			t.Errorf("attachment add Content-Type = %q; want multipart/*", r.Header.Get("Content-Type"))
		}
		_ = params
		if r.Header.Get("X-Atlassian-Token") != "no-check" {
			t.Errorf("attachment add missing X-Atlassian-Token: no-check")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"101","filename":"upload.bin","mimeType":"application/octet-stream","size":3,
			"author":{"accountId":"a","displayName":"A"},"created":"2026-05-01T00:00:00.000+0000",
			"content":"http://x/101","self":"http://x/101"}]`))
	})
	mux.HandleFunc("/rest/api/3/attachment/100", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected /attachment/100 method %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/rest/api/3/attachment/content/100", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected /attachment/content/100 method %s", r.Method)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Disposition", `attachment; filename="trace.log"`)
		_, _ = w.Write([]byte("hello"))
	})

	// ----- Comment endpoints ----------------------------------------
	commentBody := func(id, text string) string {
		return `{"id":"` + id + `","body":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"` + text + `"}]}]},
			"author":{"accountId":"a","displayName":"A"},
			"created":"2026-05-01T00:00:00.000+0000","updated":"2026-05-01T00:00:00.000+0000"}`
	}
	mux.HandleFunc("/rest/api/3/issue/PROJ-1/comment", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			_, _ = w.Write([]byte(commentBody("500", "hi")))
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"comments":[` + commentBody("500", "hi") + `],"startAt":0,"maxResults":50,"total":1,"isLast":true}`))
		default:
			t.Errorf("unexpected /comment method %s", r.Method)
		}
	})
	mux.HandleFunc("/rest/api/3/issue/PROJ-1/comment/500", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPut:
			_, _ = w.Write([]byte(commentBody("500", "edited")))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected /comment/500 method %s", r.Method)
		}
	})

	// ----- Watcher endpoints ----------------------------------------
	mux.HandleFunc("/rest/api/3/issue/PROJ-1/watchers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"isWatching":true,"watchCount":1,"watchers":[
					{"accountId":"712020:test-user","displayName":"Test","active":true}
			]}`))
		case http.MethodPost, http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected /watchers method %s", r.Method)
		}
	})

	// ----- Link endpoints -------------------------------------------
	// (issuelinks expansion handled by the /issue/PROJ-1 handler above)
	mux.HandleFunc("/rest/api/3/issueLink/L1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected /issueLink/L1 method %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/rest/api/3/issueLinkType", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issueLinkTypes":[
			{"id":"10000","name":"Blocks","inward":"is blocked by","outward":"blocks"}
		]}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := jira.NewClient(jira.WithBaseURL(srv.URL + "/"))
	ctx := context.Background()

	// ----- attachment lifecycle ----------------------------------
	att := jira.NewAttachmentService(client)
	listed, _, err := att.List(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("attachment.List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("attachment.List len = %d; want 1", len(listed))
	}

	uploaded, _, err := att.Add(ctx, "PROJ-1", []jira.FileSource{
		{Name: "upload.bin", Reader: strings.NewReader("abc")},
	})
	if err != nil {
		t.Fatalf("attachment.Add: %v", err)
	}
	if len(uploaded) != 1 || uploaded[0].ID == nil || *uploaded[0].ID != "101" {
		t.Fatalf("attachment.Add returned %+v", uploaded)
	}

	body, _, err := att.Download(ctx, "100")
	if err != nil {
		t.Fatalf("attachment.Download: %v", err)
	}
	bytes, _ := io.ReadAll(body)
	_ = body.Close()
	if string(bytes) != "hello" {
		t.Fatalf("attachment.Download body = %q; want hello", string(bytes))
	}

	if _, err := att.Delete(ctx, "100"); err != nil {
		t.Fatalf("attachment.Delete: %v", err)
	}

	// ----- comment lifecycle -------------------------------------
	cs := jira.NewCommentService(client)
	doc, _, err := adf.FromMarkdownLossy("hi")
	if err != nil {
		t.Fatalf("FromMarkdownLossy: %v", err)
	}
	added, _, err := cs.Add(ctx, "PROJ-1", &jira.CommentBody{ADF: &doc})
	if err != nil {
		t.Fatalf("comment.Add: %v", err)
	}
	if added.ID == nil || *added.ID != "500" {
		t.Fatalf("comment.Add returned id %v", added.ID)
	}
	page, _, err := cs.List(ctx, "PROJ-1", &jira.ListCommentsOptions{MaxResults: 50})
	if err != nil {
		t.Fatalf("comment.List: %v", err)
	}
	if len(page) != 1 {
		t.Fatalf("comment.List len = %d; want 1", len(page))
	}
	editedDoc, _, err := adf.FromMarkdownLossy("edited")
	if err != nil {
		t.Fatalf("FromMarkdownLossy: %v", err)
	}
	if _, _, err := cs.Edit(ctx, "PROJ-1", "500", &jira.CommentBody{ADF: &editedDoc}, jira.VisibilityChange{Mode: jira.VisibilityKeep}); err != nil {
		t.Fatalf("comment.Edit: %v", err)
	}
	if _, err := cs.Delete(ctx, "PROJ-1", "500"); err != nil {
		t.Fatalf("comment.Delete: %v", err)
	}

	// ----- watcher lifecycle -------------------------------------
	ws := jira.NewWatcherService(client)
	w, _, err := ws.List(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("watchers.List: %v", err)
	}
	if w == nil || w.WatchCount != 1 {
		t.Fatalf("watchers.List = %+v; want WatchCount 1", w)
	}
	if _, err := ws.Add(ctx, "PROJ-1", "712020:test-user"); err != nil {
		t.Fatalf("watchers.Add: %v", err)
	}
	if _, err := ws.Remove(ctx, "PROJ-1", "712020:test-user"); err != nil {
		t.Fatalf("watchers.Remove: %v", err)
	}

	// ----- link lifecycle ----------------------------------------
	ls := jira.NewIssueLinkService(client)
	links, _, err := ls.List(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("link.List: %v", err)
	}
	if len(links) != 1 || links[0].ID != "L1" {
		t.Fatalf("link.List = %+v", links)
	}
	if _, err := ls.Delete(ctx, "L1"); err != nil {
		t.Fatalf("link.Delete: %v", err)
	}
	lts := jira.NewIssueLinkTypeService(client)
	types, _, err := lts.List(ctx)
	if err != nil {
		t.Fatalf("linktype.List: %v", err)
	}
	if len(types) != 1 || types[0].Name != "Blocks" {
		t.Fatalf("linktype.List = %+v", types)
	}
}
