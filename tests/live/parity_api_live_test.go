//go:build live

package live

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"unicode/utf8"

	"github.com/matcra587/jira-cli/internal/envelope"
)

// This current-only cohort binds native HTTP receipts to one independently
// owned live Jira issue. It does not invent a legacy API command or a live PATCH route.
func TestLiveParityAPI(t *testing.T) {
	p := newParityRun(t)
	lane := p.evidence.Lanes[1]
	key := p.create(t, lane)
	native := p.native(t, key)
	issuePath := "/rest/api/3/issue/" + key
	observe := func(method, endpoint string, body []byte, status int, args ...string) envelope.APIMutationOutput {
		t.Helper()
		frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, args...)
		out := parityLiveAPIReceipt(t, stdout, method, endpoint, body)
		if exit != 0 || !frame.OK || !out.Attempted || out.DryRun || out.Response == nil || out.Response.StatusCode != status {
			t.Fatal("owned API observation lost its successful complete HTTP receipt")
		}
		return out
	}
	read := observe("GET", issuePath, nil, 200, "api", "get", issuePath)
	var issue liveIssue
	if json.Unmarshal(read.Response.JSON, &issue) != nil || issue.ID != native.ID || issue.Key != key {
		t.Fatal("raw issue GET lost the independently established owned native identity")
	}
	p.check(t, lane, "C083.live.owned_issue")

	for _, method := range []string{"head", "options"} {
		frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, "api", method, issuePath)
		upper, id := "HEAD", "C084"
		if method == "options" {
			upper, id = "OPTIONS", "C085"
		}
		out := parityLiveAPIReceipt(t, stdout, upper, issuePath, nil)
		if !out.Attempted || out.Response == nil {
			t.Fatal("native method observation has no HTTP receipt")
		}
		status := out.Response.StatusCode
		switch status {
		case 200, 204:
			if exit != 0 || !frame.OK {
				t.Fatal("successful native method has a failed envelope")
			}
		case 405:
			if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "jira_request_failed" {
				t.Fatal("native method refusal lost its exact HTTP classification")
			}
		default:
			t.Fatalf("unexpected owned-route %s status %d; retain receipt without support credit", upper, status)
		}
		if method == "head" && out.Response.BodyBase64 != "" {
			t.Fatal("native HEAD invented response bytes")
		}
		p.check(t, lane, id+".live.native_status_"+strconv.Itoa(status))
	}

	numeric, err := strconv.ParseUint(native.ID, 10, 64)
	if err != nil || numeric == 0 {
		t.Fatal("owned search requires the independent numeric issue ID")
	}
	search, err := json.Marshal(map[string]any{"jql": "key = " + key, "fields": []string{"summary"}, "maxResults": 1, "reconcileIssues": []uint64{numeric}})
	if err != nil {
		t.Fatal(err)
	}
	searchPath := filepath.Join(p.owner.dir, "parity-api-search.json")
	if err := os.WriteFile(searchPath, search, 0o600); err != nil {
		t.Fatal(err)
	}
	found := observe("POST", "/rest/api/3/search/jql", search, 200, "api", "post", "/rest/api/3/search/jql", "--input", searchPath)
	var page struct {
		IsLast *bool       `json:"isLast"`
		Issues []liveIssue `json:"issues"`
	}
	if json.Unmarshal(found.Response.JSON, &page) != nil || page.IsLast == nil || !*page.IsLast || len(page.Issues) != 1 || page.Issues[0].ID != native.ID || page.Issues[0].Key != key {
		t.Fatal("read-only search POST lost the exact reconciled owned issue")
	}
	p.check(t, lane, "C087.live.read_only_search")

	initial := []byte(parityDescription)
	endpoint, input := p.owner.rawCommentInput(t, key, "", initial)
	payload, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"api", "post", endpoint, "--input", input, "--force"}
	frame, stdout, exit := p.invokeOwnedAPI(t, lane, key, nil, append(args, "--dry-run")...)
	preview := parityLiveAPIReceipt(t, stdout, "POST", endpoint, payload)
	if exit != 0 || !frame.OK || !preview.DryRun || preview.Attempted || preview.Response != nil {
		t.Fatal("owned raw comment preview entered HTTP")
	}
	p.requireCommentInventory(t, key, []string{}, map[string][]byte{})
	p.check(t, lane, "C087.live.comment_preview")
	frame, stdout, exit = p.invokeOwnedAPI(t, lane, key, []string{"JIRA_READ_ONLY=true"}, args...)
	refused := parityLiveAPIReceipt(t, stdout, "POST", endpoint, payload)
	if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "read_only_blocked" || refused.DryRun || !refused.Attempted || refused.Response != nil {
		t.Fatal("owned raw comment bypassed read-only refusal")
	}
	p.requireCommentInventory(t, key, []string{}, map[string][]byte{})
	p.check(t, lane, "C087.live.comment_read_only")

	attempt := len(p.evidence.Comments)
	p.evidence.Comments = append(p.evidence.Comments, parityCommentAttempt{Lane: lane.Name, Key: key, Digest: fmt.Sprintf("%x", sha256.Sum256(payload))})
	p.save(t)
	frame, stdout, exit = p.invokeOwnedAPI(t, lane, key, nil, args...)
	// Retain the returned identity before receipt assertions. An unknown write is
	// never retried; the independently owned parent remains available for cleanup.
	var returned struct {
		Data struct {
			Response *struct {
				JSON struct {
					ID string `json:"id"`
				} `json:"json"`
			} `json:"response"`
		} `json:"data"`
	}
	if json.Unmarshal(stdout, &returned) == nil && returned.Data.Response != nil {
		p.evidence.Comments[attempt].ID = returned.Data.Response.JSON.ID
	}
	p.save(t)
	id := p.evidence.Comments[attempt].ID
	numeric, err = strconv.ParseUint(id, 10, 64)
	if err != nil || numeric == 0 {
		t.Fatal("raw comment write has no retained native identity; never replay")
	}
	p.requireCommentInventory(t, key, []string{id}, map[string][]byte{id: initial})
	p.owner.registerComment(t, key, id)
	p.evidence.Comments[attempt].Bound = true
	p.save(t)
	posted := parityLiveAPIReceipt(t, stdout, "POST", endpoint, payload)
	requireRawSuccess(t, posted, frame, exit, 201)
	p.check(t, lane, "C087.live.comment_create")

	replacement := bytes.ReplaceAll(initial, []byte("Paired "), []byte("Updated "))
	endpoint, input = p.owner.rawCommentInput(t, key, id, replacement)
	payload, err = os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	args = []string{"api", "put", endpoint, "--input", input, "--force"}
	frame, stdout, exit = p.invokeOwnedAPI(t, lane, key, nil, args...)
	updated := parityLiveAPIReceipt(t, stdout, "PUT", endpoint, payload)
	requireRawSuccess(t, updated, frame, exit, 200)
	p.requireCommentInventory(t, key, []string{id}, map[string][]byte{id: replacement})
	p.check(t, lane, "C088.live.comment_update")

	args = []string{"api", "delete", endpoint, "--force"}
	frame, stdout, exit = p.invokeOwnedAPI(t, lane, key, nil, args...)
	deleted := parityLiveAPIReceipt(t, stdout, "DELETE", endpoint, nil)
	requireRawSuccess(t, deleted, frame, exit, 204)
	if deleted.Response.BodyBase64 != "" || len(deleted.Response.JSON) != 0 {
		t.Fatal("raw comment deletion invented native bytes")
	}
	frame, stdout, exit = p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, "api", "get", endpoint)
	missing := parityLiveAPIReceipt(t, stdout, "GET", endpoint, nil)
	if exit != 2 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "jira_not_found" || !missing.Attempted || missing.Response == nil || missing.Response.StatusCode != 404 {
		t.Fatal("raw deletion lacks independent native absence confirmation")
	}
	p.owner.ownedLiveComment(p.owner.ownedLiveIssue(key), id).Deleted = true
	p.owner.save(t)
	p.requireCommentInventory(t, key, []string{}, map[string][]byte{})
	if !nativeMetadataMatches(p.native(t, key).Fields, native.Fields, nil) {
		t.Fatal("raw comment lifecycle changed unrelated owned issue fields")
	}
	p.check(t, lane, "C082.live.comment_delete")
}

// Re-admit the registered bytes and exact owned route immediately before every
// process, including previews and refusals. Earlier admission cannot be reused.
func (p *parityRun) invokeOwnedAPI(t *testing.T, lane parityLane, key string, extra []string, args ...string) (liveFrame, []byte, int) {
	t.Helper()
	if lane.Name != "after" {
		t.Fatal("raw API mutations have no legacy lane")
	}
	if err := p.owner.checkMutation([]string{key}, args); err != nil {
		t.Fatal(err)
	}
	return p.invoke(t, lane, extra, args...)
}

func parityLiveAPIReceipt(t *testing.T, stdout []byte, method, endpoint string, body []byte) envelope.APIMutationOutput {
	t.Helper()
	var frame struct {
		Data envelope.APIMutationOutput `json:"data"`
	}
	if json.Unmarshal(stdout, &frame) != nil {
		t.Fatal("live API receipt unavailable")
	}
	out := frame.Data
	request := out.Request
	if request.Method != method || request.Endpoint != endpoint || len(request.Query) != 0 || request.ContentType != "application/json" || request.MaxResponseBytes != 16777216 || request.IfMatch != "" || request.IfUnmodifiedSince != "" {
		t.Fatal("live raw API request facts differ")
	}
	if body == nil {
		if request.BodyBase64 != nil {
			t.Fatal("live API invented an absent input body")
		}
	} else if request.BodyBase64 == nil || *request.BodyBase64 != base64.StdEncoding.EncodeToString(body) {
		t.Fatal("live API changed exact authored input bytes")
	}
	if out.Response != nil {
		response := out.Response
		captured, err := base64.StdEncoding.DecodeString(response.BodyBase64)
		if err != nil || base64.StdEncoding.EncodeToString(captured) != response.BodyBase64 || response.Truncated || response.Attempts != 1 || response.WaitedMS != 0 {
			t.Fatal("live API receipt is incomplete, replayed or noncanonical")
		}
		if utf8.Valid(captured) && (response.BodyText == nil || *response.BodyText != string(captured)) {
			t.Fatal("live API text view changed native bytes")
		}
		if json.Valid(captured) {
			if !parityAuthoringJSON(response.JSON, captured) {
				t.Fatal("live API JSON view changed native values")
			}
		} else if len(response.JSON) != 0 {
			t.Fatal("live API invented a JSON response view")
		}
	}
	return out
}
