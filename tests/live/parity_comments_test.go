//go:build live

package live

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type parityCommentAttempt struct {
	Lane   string `json:"lane"`
	Key    string `json:"key"`
	Digest string `json:"body_sha256"`
	ID     string `json:"returned_id"`
	Bound  bool   `json:"bound"`
}

func (r *liveRun) parityCommentInput(t *testing.T, key, id string, body []byte) string {
	t.Helper()
	if id != "" {
		return r.commentInput(t, key, id, body)
	}
	if !r.verified || r.ownedLiveIssue(key) == nil {
		t.Fatal("comment add input requires an active owned issue")
	}
	path := r.nativeBody(t, body)
	if r.commentInputs == nil {
		r.commentInputs = make(map[string]nativeCommentInput)
	}
	r.commentInputs[path] = nativeCommentInput{key: key, digest: sha256.Sum256(body)}
	return path
}

func parityCommentArgs(lane, key string, args []string) ([]string, error) {
	canonical := slices.Clone(args)
	if lane == "before" && len(args) >= 3 && args[0] == "issue" && args[1] == "comment" && args[2] == key {
		canonical = append([]string{"issue", "comment", "add"}, args[2:]...)
	}
	if len(canonical) < 4 || canonical[0] != "issue" || canonical[1] != "comment" || canonical[3] != key || (canonical[2] != "add" && canonical[2] != "edit" && canonical[2] != "delete") {
		return nil, fmt.Errorf("comment recipe requires its exact declared owned target")
	}
	return canonical, nil
}

func (p *parityRun) validateCommentMutation(lane parityLane, key string, args []string) error {
	if !slices.Contains(p.evidence.Lanes, lane) {
		return fmt.Errorf("comment recipe selected an unadmitted lane")
	}
	if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
		return err
	}
	canonical, err := parityCommentArgs(lane.Name, key, args)
	if err != nil || p.owner.ownedLiveIssue(key) == nil {
		return fmt.Errorf("comment recipe selected an unsupported or inactive target")
	}
	if canonical[2] != "add" {
		return p.owner.checkCommentMutation([]string{key}, canonical)
	}
	if len(canonical) < 6 || canonical[4] != "--json-input" {
		return fmt.Errorf("comment add requires one exact registered native input")
	}
	input, ok := p.owner.commentInputs[canonical[5]]
	if !ok || input.key != key || input.id != "" {
		return fmt.Errorf("comment add input is not registered for this target")
	}
	data, readErr := os.ReadFile(canonical[5])
	if readErr != nil || sha256.Sum256(data) != input.digest {
		return fmt.Errorf("comment add input differs from its bound target or bytes")
	}
	seen := map[string]bool{}
	for i := 6; i < len(canonical); i++ {
		flag := canonical[i]
		if seen[flag] {
			return fmt.Errorf("comment add repeats an option")
		}
		seen[flag] = true
		switch flag {
		case "--dry-run":
		case "--visibility-role":
			i++
			if i == len(canonical) || p.owner.commentRole == "" || canonical[i] != p.owner.commentRole {
				return fmt.Errorf("comment add requires its verified current-account role")
			}
		default:
			return fmt.Errorf("comment add contains an unauthored option or extra target")
		}
	}
	return p.owner.checkMutation([]string{key}, canonical)
}

type parityCommentReply struct {
	Key        string
	ID         string
	Body       json.RawMessage
	DryRun     bool
	Valid      bool
	Comment    json.RawMessage
	StoredBody json.RawMessage
	Visibility json.RawMessage
}

func parityCommentAddReply(stdout []byte, lane string, preview bool) parityCommentReply {
	var frame struct {
		Data struct {
			Issue      liveIssue       `json:"issue"`
			DryRun     *bool           `json:"dry_run"`
			Body       json.RawMessage `json:"body"`
			BodyRoute  string          `json:"body_route"`
			Comment    json.RawMessage `json:"comment"`
			Visibility json.RawMessage `json:"visibility"`
			Results    []struct {
				Issue liveIssue       `json:"issue"`
				OK    bool            `json:"ok"`
				Data  json.RawMessage `json:"data"`
			} `json:"results"`
		} `json:"data"`
	}
	if json.Unmarshal(stdout, &frame) != nil {
		return parityCommentReply{}
	}
	out := parityCommentReply{Key: frame.Data.Issue.Key, Body: frame.Data.Body, Comment: frame.Data.Comment, Visibility: frame.Data.Visibility}
	valid := frame.Data.DryRun != nil && *frame.Data.DryRun == preview
	if frame.Data.DryRun != nil {
		out.DryRun = *frame.Data.DryRun
	}
	if lane == "after" {
		valid = valid && frame.Data.BodyRoute == "adf" && len(frame.Data.Results) == 1 && frame.Data.Results[0].OK
		if len(frame.Data.Results) == 1 {
			row := frame.Data.Results[0]
			out.Key = row.Issue.Key
			var data map[string]json.RawMessage
			if json.Unmarshal(row.Data, &data) != nil || data == nil || (preview && len(data) != 0) {
				valid = false
			}
			out.Comment = data["comment"]
		}
	}
	var comment struct {
		ID   string          `json:"id"`
		Body json.RawMessage `json:"body"`
	}
	if len(out.Comment) > 0 && string(out.Comment) != "null" {
		if json.Unmarshal(out.Comment, &comment) != nil {
			valid = false
		}
	}
	out.ID = comment.ID
	out.StoredBody = comment.Body
	if lane == "before" {
		out.Body = comment.Body
	}
	out.Valid = valid && out.Key != "" && len(out.Body) != 0 && string(out.Body) != "null" && ((preview && out.ID == "" && (lane == "before" || len(out.Comment) == 0)) || (!preview && out.ID != "" && len(out.StoredBody) != 0 && string(out.StoredBody) != "null"))
	return out
}

func parityCommentVisibility(raw json.RawMessage, mode, role string) bool {
	if mode != "replace" {
		return len(raw) == 0
	}
	if role == "" {
		return false
	}
	want, err := json.Marshal(map[string]string{"type": "role", "value": role})
	return err == nil && nativeMetadataMatches(raw, want, nil)
}

func parityCommentRefusal(stdout []byte, lane, verb, key string, body []byte, role string) bool {
	var frame struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(stdout, &frame) != nil {
		return false
	}
	if lane == "before" || verb != "add" {
		return bytes.Equal(bytes.TrimSpace(frame.Data), []byte("null"))
	}
	var data struct {
		DryRun     *bool           `json:"dry_run"`
		Body       json.RawMessage `json:"body"`
		BodyRoute  string          `json:"body_route"`
		Visibility json.RawMessage `json:"visibility"`
		Results    []struct {
			Issue liveIssue       `json:"issue"`
			OK    *bool           `json:"ok"`
			Data  json.RawMessage `json:"data"`
			Error *struct {
				Code string `json:"code"`
			} `json:"error"`
		} `json:"results"`
	}
	mode := "keep"
	if role != "" {
		mode = "replace"
	}
	if json.Unmarshal(frame.Data, &data) != nil || data.DryRun == nil || *data.DryRun || data.BodyRoute != "adf" || !nativeMetadataMatches(data.Body, body, nil) || !parityCommentVisibility(data.Visibility, mode, role) || len(data.Results) != 1 {
		return false
	}
	row := data.Results[0]
	return row.Issue.Key == key && row.OK != nil && !*row.OK && (len(row.Data) == 0 || bytes.Equal(bytes.TrimSpace(row.Data), []byte("null"))) && row.Error != nil && row.Error.Code == "read_only_blocked"
}

func (p *parityRun) commentInvocation(t *testing.T, lane parityLane, key string, extra []string, args ...string) (liveFrame, []byte, int, error) {
	t.Helper()
	if err := p.validateCommentMutation(lane, key, args); err != nil {
		return liveFrame{}, nil, 0, err
	}
	frame, stdout, exit := p.invoke(t, lane, extra, args...)
	return frame, stdout, exit, nil
}

func (p *parityRun) invokeComment(t *testing.T, lane parityLane, key string, extra []string, args ...string) (liveFrame, []byte, int) {
	t.Helper()
	frame, stdout, exit, err := p.commentInvocation(t, lane, key, extra, args...)
	if err != nil {
		t.Fatal(err)
	}
	return frame, stdout, exit
}

func (p *parityRun) commentAdd(t *testing.T, lane parityLane, key, path string, bare bool) string {
	t.Helper()
	args := []string{"issue", "comment", "add", key, "--json-input", path}
	if bare && lane.Name == "before" {
		args = append([]string{"issue", "comment", key}, args[4:]...)
	}
	if err := p.validateCommentMutation(lane, key, args); err != nil {
		t.Fatal(err)
	}
	input := p.owner.commentInputs[path]
	index := len(p.evidence.Comments)
	p.evidence.Comments = append(p.evidence.Comments, parityCommentAttempt{Lane: lane.Name, Key: key, Digest: fmt.Sprintf("%x", input.digest)})
	p.save(t)
	frame, stdout, exit := p.invokeComment(t, lane, key, nil, args...)
	reply := parityCommentAddReply(stdout, lane.Name, false)
	p.evidence.Comments[index].ID = reply.ID
	p.save(t)
	if reply.ID != "" && reply.Key == key {
		p.owner.registerComment(t, key, reply.ID)
		p.evidence.Comments[index].Bound = true
		p.save(t)
	}
	body, err := os.ReadFile(path)
	if exit != 0 || !frame.OK || !reply.Valid || reply.Key != key || !p.evidence.Comments[index].Bound || err != nil || !nativeMetadataMatches(reply.Body, body, nil) || !nativeMetadataMatches(reply.StoredBody, body, nil) || (lane.Name == "after" && !parityCommentVisibility(reply.Visibility, "keep", "")) {
		t.Fatal("comment add lacks its expected native receipt; retain intent and never replay")
	}
	return reply.ID
}

func parityCommentEditReceipt(stdout []byte, lane, key, id string, body []byte, mode, role string, preview bool) bool {
	var frame struct {
		Data struct {
			Issue             liveIssue       `json:"issue"`
			ID                string          `json:"comment_id"`
			DryRun            *bool           `json:"dry_run"`
			Body              json.RawMessage `json:"body"`
			Summary           json.RawMessage `json:"body_adf_summary"`
			BodyRoute         string          `json:"body_route"`
			VisibilityChange  string          `json:"visibility_change"`
			VisibilityCleared *bool           `json:"visibility_cleared"`
			Visibility        json.RawMessage `json:"visibility"`
			Notify            *bool           `json:"notify"`
			Comment           *struct {
				ID   string          `json:"id"`
				Body json.RawMessage `json:"body"`
			} `json:"comment"`
		} `json:"data"`
	}
	if json.Unmarshal(stdout, &frame) != nil || frame.Data.Issue.Key != key || frame.Data.ID != id || frame.Data.DryRun == nil || *frame.Data.DryRun != preview {
		return false
	}
	if lane == "before" {
		if frame.Data.VisibilityChange != mode || !nativeMetadataMatches(frame.Data.Summary, body, nil) {
			return false
		}
	} else if frame.Data.BodyRoute != "adf" || frame.Data.VisibilityCleared == nil || *frame.Data.VisibilityCleared != (mode == "clear") || !nativeMetadataMatches(frame.Data.Body, body, nil) || !parityCommentVisibility(frame.Data.Visibility, mode, role) || frame.Data.Notify == nil || !*frame.Data.Notify {
		return false
	}
	if preview {
		return frame.Data.Comment == nil
	}
	return frame.Data.Comment != nil && frame.Data.Comment.ID == id && nativeMetadataMatches(frame.Data.Comment.Body, body, nil)
}

func parityCommentDeleteReceipt(stdout []byte, lane, key, id string, preview bool) bool {
	var frame struct {
		Data struct {
			Issue   liveIssue `json:"issue"`
			ID      string    `json:"comment_id"`
			DryRun  *bool     `json:"dry_run"`
			Deleted *bool     `json:"deleted"`
		} `json:"data"`
	}
	if json.Unmarshal(stdout, &frame) != nil || frame.Data.Issue.Key != key || frame.Data.ID != id || frame.Data.DryRun == nil || *frame.Data.DryRun != preview {
		return false
	}
	if lane == "before" && !preview {
		return frame.Data.Deleted != nil && *frame.Data.Deleted
	}
	return frame.Data.Deleted == nil || !*frame.Data.Deleted
}

func parityCommentBody(marker string) []byte {
	text, _ := json.Marshal(marker + " <&> café")
	return []byte(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":` + string(text) + `},{"type":"text","text":" bold","marks":[{"type":"strong"}]}]}]}`)
}

func TestLiveParityComments(t *testing.T) {
	p := newParityRun(t)
	p.owner.commentRole = currentCommentRole(t, p.owner)
	var initialIssue json.RawMessage
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			key := p.create(t, lane)
			issueBefore := p.native(t, key)
			if initialIssue == nil {
				initialIssue = slices.Clone(issueBefore.Fields)
			} else if !parityUnchangedFields(issueBefore.Fields, initialIssue, "summary") {
				t.Fatal("paired comment issue preimages differ")
			}
			var ids []string
			bodies := map[string][]byte{}
			p.requireCommentInventory(t, key, ids, bodies)
			for _, recipe := range []struct {
				name string
				bare bool
			}{{"I006", true}, {"I007", false}} {
				body := parityCommentBody(p.owner.manifest.RunID + " " + recipe.name)
				path := p.owner.parityCommentInput(t, key, "", body)
				args := []string{"issue", "comment", "add", key, "--json-input", path}
				if recipe.bare && lane.Name == "before" {
					args = append([]string{"issue", "comment", key}, args[4:]...)
				}
				for _, preview := range []bool{true, false} {
					call := slices.Clone(args)
					var extra []string
					if preview {
						call = append(call, "--dry-run")
					} else {
						extra = []string{"JIRA_READ_ONLY=true"}
					}
					if err := p.validateCommentMutation(lane, key, call); err != nil {
						t.Fatal(err)
					}
					frame, stdout, exit := p.invokeComment(t, lane, key, extra, call...)
					if preview {
						reply := parityCommentAddReply(stdout, lane.Name, true)
						if exit != 0 || !frame.OK || !reply.Valid || reply.Key != key || !nativeMetadataMatches(reply.Body, body, nil) || (lane.Name == "after" && !parityCommentVisibility(reply.Visibility, "keep", "")) {
							t.Fatal("comment preview lost its native body or invented a receipt")
						}
					} else if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != parityCommentReadOnlyCode(lane.Name) || !parityCommentRefusal(stdout, lane.Name, "add", key, body, "") {
						t.Fatal("comment add bypassed its read-only refusal")
					}
					p.requireCommentInventory(t, key, ids, bodies)
					name := "preview"
					if !preview {
						name = "read_only"
					}
					p.check(t, lane, recipe.name+"."+name)
				}
				id := p.commentAdd(t, lane, key, path, recipe.bare)
				ids = append(ids, id)
				bodies[id] = body
				requireNativeComment(t, p.owner, key, id, body, "")
				p.requireCommentInventory(t, key, ids, bodies)
				p.check(t, lane, recipe.name+".native")
			}
			roleBefore := ""
			for _, step := range []struct{ name, mode string }{{"restrict", "replace"}, {"preserve", "keep"}, {"clear", "clear"}} {
				body := parityCommentBody(p.owner.manifest.RunID + " " + step.name)
				path := p.owner.parityCommentInput(t, key, ids[0], body)
				args := []string{"issue", "comment", "edit", key, ids[0], "--json-input", path}
				switch step.mode {
				case "replace":
					args = append(args, "--visibility-role", p.owner.commentRole)
				case "clear":
					args = append(args, "--clear-visibility")
				}
				if err := p.validateCommentMutation(lane, key, args); err != nil {
					t.Fatal(err)
				}
				for _, preview := range []bool{true, false} {
					call := slices.Clone(args)
					var extra []string
					if preview {
						call = append(call, "--dry-run")
					} else {
						extra = []string{"JIRA_READ_ONLY=true"}
					}
					if err := p.validateCommentMutation(lane, key, call); err != nil {
						t.Fatal(err)
					}
					frame, stdout, exit := p.invokeComment(t, lane, key, extra, call...)
					name := "read_only"
					if preview {
						name = "preview"
						if exit != 0 || !frame.OK || !parityCommentEditReceipt(stdout, lane.Name, key, ids[0], body, step.mode, p.owner.commentRole, true) {
							t.Fatal("comment edit preview lost its candidate or invented a native receipt")
						}
					} else if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != parityCommentReadOnlyCode(lane.Name) || !parityCommentRefusal(stdout, lane.Name, "edit", key, nil, "") {
						t.Fatal("comment edit bypassed its read-only refusal")
					}
					requireNativeComment(t, p.owner, key, ids[0], bodies[ids[0]], roleBefore)
					requireNativeComment(t, p.owner, key, ids[1], bodies[ids[1]], "")
					p.check(t, lane, "I009."+step.name+"."+name)
				}
				frame, stdout, exit := p.invokeComment(t, lane, key, nil, args...)
				if exit != 0 || !frame.OK || !parityCommentEditReceipt(stdout, lane.Name, key, ids[0], body, step.mode, p.owner.commentRole, false) {
					t.Fatal("comment edit lacks its expected stored receipt; never replay")
				}
				bodies[ids[0]] = body
				role := p.owner.commentRole
				if step.mode == "clear" {
					role = ""
				}
				roleBefore = role
				requireNativeComment(t, p.owner, key, ids[0], body, role)
				requireNativeComment(t, p.owner, key, ids[1], bodies[ids[1]], "")
				p.check(t, lane, "I009."+step.name+".native")
			}
			for _, all := range []bool{false, true} {
				p.commentList(t, lane, key, ids, bodies, all)
				name := "I010.populated.page"
				if all {
					name = "I010.populated.walk"
				}
				p.check(t, lane, name)
			}
			remaining := slices.Clone(ids)
			for index, id := range ids {
				args := []string{"issue", "comment", "delete", key, id, "--force"}
				if err := p.validateCommentMutation(lane, key, args); err != nil {
					t.Fatal(err)
				}
				if index == 0 {
					for _, preview := range []bool{true, false} {
						call := slices.Clone(args)
						var extra []string
						if preview {
							call = append(call, "--dry-run")
						} else {
							extra = []string{"JIRA_READ_ONLY=true"}
						}
						if err := p.validateCommentMutation(lane, key, call); err != nil {
							t.Fatal(err)
						}
						frame, stdout, exit := p.invokeComment(t, lane, key, extra, call...)
						name := "read_only"
						if preview {
							name = "preview"
							if exit != 0 || !frame.OK || !parityCommentDeleteReceipt(stdout, lane.Name, key, id, true) {
								t.Fatal("comment delete preview lost its context or invented deletion")
							}
						} else if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != parityCommentReadOnlyCode(lane.Name) || !parityCommentRefusal(stdout, lane.Name, "delete", key, nil, "") {
							t.Fatal("comment delete bypassed read-only refusal")
						}
						p.requireCommentInventory(t, key, remaining, bodies)
						p.check(t, lane, "I008."+name)
					}
				}
				frame, stdout, exit := p.invokeComment(t, lane, key, nil, args...)
				missing, _, missingExit := p.owner.invoke(t, t.Context(), nil, "api", "get", "/rest/api/3/issue/"+key+"/comment/"+id)
				if missingExit != 2 || missing.OK || len(missing.Errors) != 1 || missing.Errors[0].Code != "jira_not_found" {
					t.Fatal("comment deletion lacks an independent native not-found observation")
				}
				p.owner.ownedLiveComment(p.owner.ownedLiveIssue(key), id).Deleted = true
				p.owner.save(t)
				if exit != 0 || !frame.OK || !parityCommentDeleteReceipt(stdout, lane.Name, key, id, false) {
					t.Fatal("comment deletion lacks its expected acknowledgement; never replay")
				}
				remaining = remaining[1:]
				p.requireCommentInventory(t, key, remaining, bodies)
			}
			p.requireCommentInventory(t, key, nil, map[string][]byte{})
			after := p.native(t, key)
			if !nativeMetadataMatches(after.Fields, issueBefore.Fields, nil) {
				t.Fatal("comment workflows changed unrelated issue fields")
			}
			p.check(t, lane, "I008.delete.native")
		})
	}
}

func parityCommentReadOnlyCode(lane string) string {
	if lane == "before" {
		return "read_only"
	}
	return "read_only_blocked"
}

func (p *parityRun) requireCommentInventory(t *testing.T, key string, ids []string, bodies map[string][]byte) json.RawMessage {
	t.Helper()
	native := rawRead(t, p.owner, "api", "get", "/rest/api/3/issue/"+key+"/comment", "--query=orderBy=+created")
	var page struct {
		Comments *[]struct {
			ID   string          `json:"id"`
			Body json.RawMessage `json:"body"`
		} `json:"comments"`
		StartAt *int `json:"startAt"`
		Total   *int `json:"total"`
	}
	if json.Unmarshal(native.Response.JSON, &page) != nil || page.Comments == nil || page.StartAt == nil || *page.StartAt != 0 || page.Total == nil || *page.Total != len(ids) || len(*page.Comments) != len(ids) {
		t.Fatal("native comment inventory differs from the run's exact resource set")
	}
	for i, comment := range *page.Comments {
		if comment.ID != ids[i] || !nativeMetadataMatches(comment.Body, bodies[comment.ID], nil) {
			t.Fatal("native comment inventory changed order, identity or complete rich body")
		}
	}
	return native.Response.JSON
}

func (p *parityRun) commentList(t *testing.T, lane parityLane, key string, ids []string, bodies map[string][]byte, all bool) {
	t.Helper()
	native := p.requireCommentInventory(t, key, ids, bodies)
	args := []string{"issue", "comment", "list", key}
	if !all || lane.Name == "before" {
		args = append(args, "--limit=1")
	}
	expectedMax := 1
	if all {
		args = append(args, "--all")
	}
	if lane.Name == "after" {
		args = append(args, "--order=oldest")
		if all {
			read := rawRead(t, p.owner, "api", "get", "/rest/api/3/issue/"+key+"/comment", "--query=maxResults=100", "--query=orderBy=+created")
			var page struct {
				MaxResults *int `json:"maxResults"`
			}
			if json.Unmarshal(read.Response.JSON, &page) != nil || page.MaxResults == nil || *page.MaxResults < 1 || *page.MaxResults > 100 {
				t.Fatal("native walk-page size is unavailable")
			}
			expectedMax = *page.MaxResults
		}
	}
	frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, args...)
	if exit != 0 || !frame.OK || !parityPopulatedComments(stdout, native, lane.Name, key, all, expectedMax) {
		t.Fatal("domain comment page differs from its native identity, authorship, rich body or declared pagination")
	}
}

func parityPopulatedComments(stdout, native []byte, lane, key string, all bool, expectedMax int) bool {
	var expected struct {
		Comments []struct {
			ID      string          `json:"id"`
			Body    json.RawMessage `json:"body"`
			Created string          `json:"created"`
			Updated string          `json:"updated"`
			Author  struct {
				ID   string `json:"accountId"`
				Name string `json:"displayName"`
			} `json:"author"`
		} `json:"comments"`
	}
	var frame struct {
		Meta struct {
			Pagination *struct {
				StartAt    *int  `json:"startAt"`
				MaxResults *int  `json:"maxResults"`
				Total      *int  `json:"total"`
				IsLast     *bool `json:"isLast"`
			} `json:"pagination"`
		} `json:"meta"`
		Data struct {
			Issue    liveIssue `json:"issue"`
			Comments *[]struct {
				ID      string          `json:"id"`
				Body    json.RawMessage `json:"body"`
				Created string          `json:"created"`
				Updated string          `json:"updated"`
				Author  struct {
					ID   string `json:"account_id"`
					Name string `json:"display_name"`
				} `json:"author"`
			} `json:"comments"`
			Pagination *struct {
				StartAt    *int `json:"start_at"`
				MaxResults *int `json:"max_results"`
				Total      *int `json:"total"`
				Returned   *int `json:"returned"`
			} `json:"pagination"`
			Truncated *bool `json:"truncated"`
		} `json:"data"`
	}
	if json.Unmarshal(stdout, &frame) != nil || json.Unmarshal(native, &expected) != nil || len(expected.Comments) != 2 || frame.Data.Issue.Key != key || frame.Data.Comments == nil {
		return false
	}
	count, start := 1, 0
	if all {
		count, start = 2, 1
	}
	if len(*frame.Data.Comments) != count {
		return false
	}
	if lane == "before" {
		page := frame.Meta.Pagination
		if page == nil || page.StartAt == nil || *page.StartAt != start || page.MaxResults == nil || *page.MaxResults != 1 || page.Total == nil || *page.Total != 2 || page.IsLast == nil || *page.IsLast != all {
			return false
		}
	} else {
		page := frame.Data.Pagination
		if page == nil || page.StartAt == nil || *page.StartAt != 0 || page.MaxResults == nil || *page.MaxResults != expectedMax || page.Total == nil || *page.Total != 2 || page.Returned == nil || *page.Returned != count || frame.Data.Truncated == nil || *frame.Data.Truncated {
			return false
		}
	}
	for i, comment := range *frame.Data.Comments {
		want := expected.Comments[i]
		if want.ID == "" || want.Created == "" || want.Updated == "" || want.Author.ID == "" || comment.ID != want.ID || comment.Created != want.Created || comment.Updated != want.Updated || comment.Author.ID != want.Author.ID || comment.Author.Name != want.Author.Name || !nativeMetadataMatches(comment.Body, want.Body, nil) {
			return false
		}
	}
	return true
}

func TestParityCommentArgs(t *testing.T) {
	for _, lane := range []string{"before", "after"} {
		args := []string{"issue", "comment", "add", "PAIR-1", "--json-input", "body.json"}
		if lane == "before" {
			args = append([]string{"issue", "comment", "PAIR-1"}, args[4:]...)
		}
		canonical, err := parityCommentArgs(lane, "PAIR-1", args)
		if err != nil || strings.Join(canonical[:4], " ") != "issue comment add PAIR-1" {
			t.Fatal("declared comment alias translation failed")
		}
		if _, err := parityCommentArgs(lane, "PAIR-2", args); err == nil {
			t.Fatal("comment alias translation admitted a different target")
		}
	}
}

func TestParityCommentOwnership(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "selected-binary")
	if err := os.WriteFile(binary, []byte("fingerprint fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	lane := parityLane{Name: "before", Binary: binary, Digest: fmt.Sprintf("%x", sha256.Sum256([]byte("fingerprint fixture")))}
	r := &liveRun{verified: true, dir: dir, commentRole: "Developers", manifest: liveManifest{Issues: []ownedIssue{{Key: "KEY-1", ID: "1", Comments: []ownedComment{{ID: "10001"}}}, {Key: "KEY-2", ID: "2"}, {Key: "KEY-3", ID: "3", Deleted: true}}}}
	p := &parityRun{owner: r, evidence: parityEvidence{Lanes: []parityLane{lane}}}
	path := r.parityCommentInput(t, "KEY-1", "", []byte(parityDescription))
	valid := []string{"issue", "comment", "KEY-1", "--json-input", path}
	if err := p.validateCommentMutation(lane, "KEY-1", valid); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"different-owned-target": {"issue", "comment", "KEY-2", "--json-input", path},
		"deleted-target":         {"issue", "comment", "KEY-3", "--json-input", path},
		"extra-target":           append(slices.Clone(valid), "KEY-2"),
		"selector":               append(slices.Clone(valid), "--jql", "project = KEY"),
		"unknown-role":           append(slices.Clone(valid), "--visibility-role", "Other role"),
		"repeated-preview":       append(slices.Clone(valid), "--dry-run", "--dry-run"),
		"repeated-input":         append(slices.Clone(valid), "--json-input", path),
		"unregistered-input":     {"issue", "comment", "KEY-1", "--json-input", filepath.Join(dir, "unregistered")},
		"add-input-as-edit":      {"issue", "comment", "edit", "KEY-1", "10001", "--json-input", path},
	} {
		t.Run(name, func(t *testing.T) {
			key := "KEY-1"
			if name == "different-owned-target" || name == "deleted-target" {
				key = args[2]
			}
			if err := p.validateCommentMutation(lane, key, args); err == nil {
				t.Fatal("comment recipe admitted an unauthored target, option or body route")
			}
		})
	}
	if err := os.WriteFile(path, []byte(`{"type":"doc","version":1,"content":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.validateCommentMutation(lane, "KEY-1", valid); err == nil {
		t.Fatal("comment recipe admitted changed body bytes")
	}
	if _, _, _, err := p.commentInvocation(t, lane, "KEY-1", nil, valid...); err == nil || len(p.evidence.Invocations) != 0 {
		t.Fatal("comment invocation entered a process after its admitted input changed")
	}
}

func TestParityCommentCandidateMetadata(t *testing.T) {
	encode := func(data map[string]any) []byte {
		t.Helper()
		value, err := json.Marshal(map[string]any{"data": data})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	edit := map[string]any{"issue": map[string]string{"key": "PAIR-1"}, "comment_id": "10001", "dry_run": true, "body": json.RawMessage(parityDescription), "body_route": "adf", "visibility_cleared": false, "notify": true, "visibility": map[string]string{"type": "role", "value": "Developers"}}
	if !parityCommentEditReceipt(encode(edit), "after", "PAIR-1", "10001", []byte(parityDescription), "replace", "Developers", true) {
		t.Fatal("complete restricted edit candidate refused")
	}
	for name, change := range map[string]func(map[string]any){
		"missing-visibility": func(data map[string]any) { delete(data, "visibility") },
		"null-visibility":    func(data map[string]any) { data["visibility"] = nil },
		"foreign-role": func(data map[string]any) {
			data["visibility"] = map[string]string{"type": "role", "value": "Other role"}
		},
		"wrong-visibility-type": func(data map[string]any) {
			data["visibility"] = map[string]string{"type": "group", "value": "Developers"}
		},
		"missing-notify": func(data map[string]any) { delete(data, "notify") },
		"false-notify":   func(data map[string]any) { data["notify"] = false },
	} {
		t.Run("edit/"+name, func(t *testing.T) {
			copy := make(map[string]any, len(edit))
			for key, value := range edit {
				copy[key] = value
			}
			change(copy)
			if parityCommentEditReceipt(encode(copy), "after", "PAIR-1", "10001", []byte(parityDescription), "replace", "Developers", true) {
				t.Fatal("changed candidate visibility or notification metadata admitted")
			}
		})
	}
	for _, mode := range []string{"keep", "clear"} {
		t.Run("edit/unexpected-visibility/"+mode, func(t *testing.T) {
			copy := make(map[string]any, len(edit))
			for key, value := range edit {
				copy[key] = value
			}
			copy["visibility_cleared"] = mode == "clear"
			if parityCommentEditReceipt(encode(copy), "after", "PAIR-1", "10001", []byte(parityDescription), mode, "Developers", true) {
				t.Fatal("unauthored visibility admitted")
			}
		})
	}
	for _, name := range []string{"valid", "missing-data", "null-data", "unexpected-comment", "missing-visibility", "foreign-role"} {
		t.Run("add/"+name, func(t *testing.T) {
			row := map[string]any{"issue": map[string]string{"key": "PAIR-1"}, "ok": true, "data": map[string]any{}}
			data := map[string]any{"dry_run": true, "body": json.RawMessage(parityDescription), "body_route": "adf", "visibility": map[string]string{"type": "role", "value": "Developers"}, "results": []any{row}}
			switch name {
			case "missing-data":
				delete(row, "data")
			case "null-data":
				row["data"] = nil
			case "unexpected-comment":
				row["data"] = map[string]any{"comment": nil}
			case "missing-visibility":
				delete(data, "visibility")
			case "foreign-role":
				data["visibility"] = map[string]string{"type": "role", "value": "Other role"}
			}
			reply := parityCommentAddReply(encode(data), "after", true)
			accepted := reply.Valid && parityCommentVisibility(reply.Visibility, "replace", "Developers")
			if accepted != (name == "valid") {
				t.Fatal("restricted add candidate admitted malformed metadata or refused the complete object")
			}
		})
	}
}

func TestParityCommentReplyPresence(t *testing.T) {
	for _, lane := range []string{"before", "after"} {
		for _, sparse := range []string{`{}`, `{"data":null}`, `{"data":{"dry_run":false,"comment":{"id":"10001"}}}`, `{"data":{"dry_run":true,"body_route":"adf","results":[]}}`} {
			if parityCommentAddReply([]byte(sparse), lane, true).Valid || parityCommentAddReply([]byte(sparse), lane, false).Valid {
				t.Fatal("sparse comment receipt admitted")
			}
		}
	}
}
