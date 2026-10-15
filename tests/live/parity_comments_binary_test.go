//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
)

func TestParityActualBinaryComments(t *testing.T) {
	const edited = `{"type":"doc","version":1,"content":[{"type":"panel","attrs":{"panelType":"info"},"content":[{"type":"paragraph","content":[{"type":"text","text":"Edited <&> café"}]}]}]}`
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		for _, bare := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/bare=%t", lane.name, bare), func(t *testing.T) {
				if lane.binary == "" {
					t.Skip("requires selected actual binary")
				}
				if err := parityFingerprint(lane.binary, lane.digest); err != nil {
					t.Fatal(err)
				}
				var mu sync.Mutex
				var requests []string
				var stored json.RawMessage
				var visibility json.RawMessage
				expectedBody := json.RawMessage(parityDescription)
				expectedMode := "replace"
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					requests = append(requests, r.Method+" "+r.URL.Path)
					w.Header().Set("Content-Type", "application/json")
					if user, token, ok := r.BasicAuth(); !ok || user != "fixture@example.invalid" || token != "synthetic-fixture-token" {
						http.Error(w, "wrong synthetic binding", http.StatusUnauthorized)
						return
					}
					data, err := io.ReadAll(io.LimitReader(r.Body, 8193))
					if err != nil || len(data) > 8192 {
						t.Error("comment writer sent an invalid bounded body")
					}
					if r.Method == http.MethodDelete && r.URL.Path == "/rest/api/3/issue/PAIR-1/comment/10001" {
						if stored == nil || len(data) != 0 || r.URL.RawQuery != "" {
							t.Error("comment deletion changed its target or request")
						}
						stored = nil
						w.WriteHeader(http.StatusNoContent)
						return
					}
					var request map[string]json.RawMessage
					if json.Unmarshal(data, &request) != nil || !nativeMetadataMatches(request["body"], expectedBody, nil) {
						t.Error("comment writer changed the independently authored rich body")
					}
					for key := range request {
						if key != "body" && key != "visibility" {
							t.Error("comment writer sent an unauthored member")
						}
					}
					vis, present := request["visibility"]
					switch expectedMode {
					case "keep":
						if present {
							t.Error("omitted visibility was replaced or cleared")
						}
					case "replace":
						if !present || !nativeMetadataMatches(vis, json.RawMessage(`{"type":"role","value":"Developers"}`), nil) {
							t.Error("comment restriction lost its exact role")
						}
						visibility = slices.Clone(vis)
					case "clear":
						if !present || string(vis) != "null" {
							t.Error("comment restriction clear was not explicit native null")
						}
						visibility = nil
					}
					if r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/issue/PAIR-1/comment" {
						if stored != nil || r.URL.RawQuery != "" {
							t.Error("comment creation was replayed or changed query")
						}
						w.WriteHeader(http.StatusCreated)
					} else if r.Method != http.MethodPut || r.URL.Path != "/rest/api/3/issue/PAIR-1/comment/10001" || stored == nil {
						t.Error("comment writer entered an unexpected route or missing resource")
					}
					if r.Method == http.MethodPut && r.URL.RawQuery != "" {
						t.Error("comment edit changed its declared notification query")
					}
					stored = slices.Clone(request["body"])
					comment := map[string]any{"id": "10001", "body": stored, "author": map[string]string{"accountId": "owned", "displayName": "Fixture"}, "created": "2026-01-01T00:00:00.000+0000", "updated": "2026-01-02T00:00:00.000+0000"}
					if visibility != nil {
						comment["visibility"] = visibility
					}
					if err := json.NewEncoder(w).Encode(comment); err != nil {
						t.Error(err)
					}
				}))
				t.Cleanup(server.Close)
				home := t.TempDir()
				path := filepath.Join(home, "body.json")
				if err := os.WriteFile(path, []byte(parityDescription), 0o600); err != nil {
					t.Fatal(err)
				}
				add := []string{"issue", "comment", "add", "PAIR-1", "--json-input", path, "--visibility-role", "Developers"}
				if bare && lane.name == "before" {
					add = append([]string{"issue", "comment", "PAIR-1"}, add[4:]...)
				}
				invoke := func(extra, args []string) (liveFrame, []byte, int) {
					frame, stdout, exit := parityFixtureRunAt(t, lane.name, lane.binary, server.URL, home, extra, args...)
					if err := parityValidateFrame(stdout, lane.name, parityExpectedOperation(lane.name, args, exit), exit); err != nil {
						t.Fatalf("actual comment envelope failed: %v %s", err, stdout)
					}
					return frame, stdout, exit
				}
				checkNoRequests := func(start int) {
					t.Helper()
					mu.Lock()
					defer mu.Unlock()
					if len(requests) != start {
						t.Fatal("comment preview or read-only refusal contacted the native fixture")
					}
				}
				frame, stdout, exit := invoke(nil, append(slices.Clone(add), "--dry-run"))
				preview := parityCommentAddReply(stdout, lane.name, true)
				if exit != 0 || !frame.OK || !preview.Valid || preview.Key != "PAIR-1" || !nativeMetadataMatches(preview.Body, json.RawMessage(parityDescription), nil) || (lane.name == "after" && !parityCommentVisibility(preview.Visibility, "replace", "Developers")) {
					t.Fatalf("actual comment add preview failed: %s", stdout)
				}
				blocked, blockedOutput, blockedExit := invoke([]string{"JIRA_READ_ONLY=true"}, add)
				if blockedExit != 3 || blocked.OK || len(blocked.Errors) != 1 || blocked.Errors[0].Code != parityCommentReadOnlyCode(lane.name) || !parityCommentRefusal(blockedOutput, lane.name, "add", "PAIR-1", []byte(parityDescription), "Developers") {
					t.Fatal("actual comment add bypassed read-only refusal")
				}
				checkNoRequests(0)
				frame, stdout, exit = invoke(nil, add)
				created := parityCommentAddReply(stdout, lane.name, false)
				if exit != 0 || !frame.OK || !created.Valid || created.Key != "PAIR-1" || created.ID != "10001" || !nativeMetadataMatches(created.Body, json.RawMessage(parityDescription), nil) || !nativeMetadataMatches(created.StoredBody, json.RawMessage(parityDescription), nil) || (lane.name == "after" && !parityCommentVisibility(created.Visibility, "replace", "Developers")) {
					t.Fatalf("actual comment add lost its native receipt: %s", stdout)
				}
				if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
					t.Fatal(err)
				}
				for _, mode := range []string{"keep", "replace", "clear"} {
					args := []string{"issue", "comment", "edit", "PAIR-1", "10001", "--json-input", path}
					switch mode {
					case "replace":
						args = append(args, "--visibility-role", "Developers")
					case "clear":
						args = append(args, "--clear-visibility")
					}
					mu.Lock()
					count := len(requests)
					expectedBody, expectedMode = json.RawMessage(edited), mode
					mu.Unlock()
					frame, stdout, exit = invoke(nil, append(slices.Clone(args), "--dry-run"))
					if exit != 0 || !frame.OK || !parityCommentEditReceipt(stdout, lane.name, "PAIR-1", "10001", []byte(edited), mode, "Developers", true) {
						t.Fatalf("actual comment edit preview failed: %s", stdout)
					}
					blocked, blockedOutput, blockedExit = invoke([]string{"JIRA_READ_ONLY=true"}, args)
					if blockedExit != 3 || blocked.OK || len(blocked.Errors) != 1 || blocked.Errors[0].Code != parityCommentReadOnlyCode(lane.name) || !parityCommentRefusal(blockedOutput, lane.name, "edit", "PAIR-1", nil, "") {
						t.Fatal("actual comment edit bypassed read-only refusal")
					}
					checkNoRequests(count)
					frame, stdout, exit = invoke(nil, args)
					if exit != 0 || !frame.OK || !parityCommentEditReceipt(stdout, lane.name, "PAIR-1", "10001", []byte(edited), mode, "Developers", false) {
						t.Fatalf("actual comment edit lost its native receipt: %s", stdout)
					}
				}
				del := []string{"issue", "comment", "delete", "PAIR-1", "10001", "--force"}
				frame, stdout, exit = invoke(nil, append(slices.Clone(del), "--dry-run"))
				if exit != 0 || !frame.OK || !parityCommentDeleteReceipt(stdout, lane.name, "PAIR-1", "10001", true) {
					t.Fatalf("actual comment delete preview failed: %s", stdout)
				}
				blocked, blockedOutput, blockedExit = invoke([]string{"JIRA_READ_ONLY=true"}, del)
				if blockedExit != 3 || blocked.OK || len(blocked.Errors) != 1 || blocked.Errors[0].Code != parityCommentReadOnlyCode(lane.name) || !parityCommentRefusal(blockedOutput, lane.name, "delete", "PAIR-1", nil, "") {
					t.Fatal("actual comment delete bypassed read-only refusal")
				}
				checkNoRequests(4)
				frame, stdout, exit = invoke(nil, del)
				if exit != 0 || !frame.OK || !parityCommentDeleteReceipt(stdout, lane.name, "PAIR-1", "10001", false) {
					t.Fatalf("actual comment deletion lost its native receipt: %s", stdout)
				}
				mu.Lock()
				defer mu.Unlock()
				want := []string{"POST /rest/api/3/issue/PAIR-1/comment", "PUT /rest/api/3/issue/PAIR-1/comment/10001", "PUT /rest/api/3/issue/PAIR-1/comment/10001", "PUT /rest/api/3/issue/PAIR-1/comment/10001", "DELETE /rest/api/3/issue/PAIR-1/comment/10001"}
				if !slices.Equal(requests, want) || stored != nil || visibility != nil {
					t.Fatalf("comment writer changed request sequence or retained state: %v", requests)
				}
			})
		}
	}
}

func TestParityActualBinaryCommentPages(t *testing.T) {
	comments := []map[string]any{
		{"id": "10001", "body": json.RawMessage(parityDescription), "created": "2026-01-01T00:00:00.000+0000", "updated": "2026-01-02T00:00:00.000+0000", "author": map[string]string{"accountId": "owned", "displayName": "Fixture"}},
		{"id": "10002", "body": json.RawMessage(parityDescription), "created": "2026-01-03T00:00:00.000+0000", "updated": "2026-01-04T00:00:00.000+0000", "author": map[string]string{"accountId": "owned", "displayName": "Fixture"}},
	}
	native, err := json.Marshal(map[string]any{"startAt": 0, "maxResults": 2, "total": 2, "comments": comments})
	if err != nil {
		t.Fatal(err)
	}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		for _, all := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/all=%t", lane.name, all), func(t *testing.T) {
				if lane.binary == "" {
					t.Skip("requires selected actual binary")
				}
				if err := parityFingerprint(lane.binary, lane.digest); err != nil {
					t.Fatal(err)
				}
				var mu sync.Mutex
				var starts []int
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					start := 0
					var err error
					if value := r.URL.Query().Get("startAt"); value != "" {
						start, err = strconv.Atoi(value)
					}
					starts = append(starts, start)
					order := "created"
					if lane.name == "after" {
						order = "+created"
					}
					limit := "1"
					if lane.name == "after" && all {
						limit = "100"
					}
					if err != nil || start < 0 || start >= 2 || r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issue/PAIR-1/comment" || r.URL.Query().Get("maxResults") != limit || r.URL.Query().Get("orderBy") != order {
						t.Error("actual comment reader changed its declared native page route or ordering")
						http.Error(w, "unexpected page request", http.StatusBadRequest)
						return
					}
					if user, token, ok := r.BasicAuth(); !ok || user != "fixture@example.invalid" || token != "synthetic-fixture-token" {
						t.Error("comment reader lost synthetic binding")
					}
					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(map[string]any{"startAt": start, "maxResults": 1, "total": 2, "comments": comments[start : start+1]}); err != nil {
						t.Error(err)
					}
				}))
				t.Cleanup(server.Close)
				args := []string{"issue", "comment", "list", "PAIR-1"}
				if !all || lane.name == "before" {
					args = append(args, "--limit=1")
				}
				if all {
					args = append(args, "--all")
				}
				if lane.name == "after" {
					args = append(args, "--order=oldest")
				}
				frame, stdout, exit := parityFixtureRun(t, lane.name, lane.binary, server.URL, nil, args...)
				if exit != 0 || !frame.OK || parityValidateFrame(stdout, lane.name, "issue.comment.list", exit) != nil || !parityPopulatedComments(stdout, native, lane.name, "PAIR-1", all, 1) {
					t.Fatalf("actual comment page lost native projection or lane pagination: %s", stdout)
				}
				want := []int{0}
				if all {
					want = []int{0, 1}
				}
				mu.Lock()
				defer mu.Unlock()
				if !slices.Equal(starts, want) {
					t.Fatalf("actual comment reader changed its page sequence: %v", starts)
				}
			})
		}
	}
}
