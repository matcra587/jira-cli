//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

type parityCloseTarget struct {
	ID, StatusID, StatusName string
	HasScreen                bool
}

// Select a real closing destination, not a transition merely named "Close".
// Metadata is independently decoded rather than projected through CLI models.
func parityClosingTarget(raw []byte, current string) (parityCloseTarget, error) {
	var page struct {
		Transitions *[]struct {
			ID          string `json:"id"`
			HasScreen   *bool  `json:"hasScreen"`
			IsAvailable *bool  `json:"isAvailable"`
			To          struct {
				ID, Name       string
				StatusCategory struct{ Key string } `json:"statusCategory"`
			} `json:"to"`
		} `json:"transitions"`
	}
	if current == "" || json.Unmarshal(raw, &page) != nil || page.Transitions == nil {
		return parityCloseTarget{}, fmt.Errorf("closing transition requires complete independent workflow metadata")
	}
	seen := map[string]bool{}
	var targets []parityCloseTarget
	for _, item := range *page.Transitions {
		if item.ID == "" || strings.IndexFunc(item.ID, func(c rune) bool { return c < '0' || c > '9' }) >= 0 || seen[item.ID] || item.HasScreen == nil || item.To.ID == "" || item.To.Name == "" || item.To.StatusCategory.Key == "" {
			return parityCloseTarget{}, fmt.Errorf("closing transition inventory is incomplete or ambiguous")
		}
		seen[item.ID] = true
		if (item.IsAvailable == nil || *item.IsAvailable) && item.To.StatusCategory.Key == "done" && item.To.ID != current {
			targets = append(targets, parityCloseTarget{item.ID, item.To.ID, item.To.Name, *item.HasScreen})
		}
	}
	if len(targets) != 1 {
		return parityCloseTarget{}, fmt.Errorf("closing acceptance needs exactly one eligible terminal destination; found %d", len(targets))
	}
	return targets[0], nil
}

func parityCloseArgs(lane, key, id, comment string, options []string) ([]string, error) {
	if (lane != "before" && lane != "after") || !liveKey.MatchString(key) || id == "" || strings.IndexFunc(id, func(c rune) bool { return c < '0' || c > '9' }) >= 0 || comment == "" {
		return nil, fmt.Errorf("close recipe requires its admitted lane, target, transition and comment")
	}
	seen := map[string]bool{}
	for _, option := range options {
		if seen[option] || (option != "--dry-run" && option != "--validate-remote" && (option != "--atomic-only" || lane != "after")) {
			return nil, fmt.Errorf("close recipe refuses additional inputs or selectors")
		}
		seen[option] = true
	}
	if seen["--validate-remote"] && !seen["--dry-run"] {
		return nil, fmt.Errorf("close metadata validation must be a preview")
	}
	flag := "--comment"
	if lane == "before" {
		flag = "--markdown"
	}
	return append([]string{"issue", "transition", key, "--transition", id, flag, comment}, options...), nil
}

func (p *parityRun) closeInvocation(t *testing.T, lane parityLane, key string, target parityCloseTarget, marker string, environment []string, options ...string) (liveFrame, []byte, int) {
	t.Helper()
	if !slices.Contains(p.evidence.Lanes, lane) || p.owner.ownedLiveIssue(key) == nil || marker != p.owner.manifest.RunID+" paired close "+lane.Name {
		t.Fatal("close invocation requires the active parent and exact run/lane comment")
	}
	args, err := parityCloseArgs(lane.Name, key, target.ID, marker, options)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := parityCloseArgs("after", key, target.ID, marker, options)
	if err != nil || p.owner.checkMutation([]string{key}, canonical) != nil {
		t.Fatal("close invocation failed shared ownership admission")
	}
	return p.invoke(t, lane, environment, args...)
}

func (p *parityRun) closeState(t *testing.T, key string) liveIssue {
	t.Helper()
	owned := p.owner.ownedLiveIssue(key)
	if owned == nil {
		t.Fatal("close observation requires an active owned issue")
	}
	read := rawRead(t, p.owner, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=summary,description,labels,project,issuetype,status")
	var issue liveIssue
	if json.Unmarshal(read.Response.JSON, &issue) != nil || issue.ID != owned.ID || issue.Key != key || parityStatusID(issue.Fields) == "" {
		t.Fatal("close observation lost its bound native identity or status")
	}
	return issue
}

func parityStatusID(fields json.RawMessage) string {
	var native struct{ Status struct{ ID string } }
	if json.Unmarshal(fields, &native) != nil {
		return ""
	}
	return native.Status.ID
}

func parityCloseBody(marker string) []byte {
	body, _ := json.Marshal(map[string]any{"type": "doc", "version": 1, "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]string{"type": "text", "text": marker}}}}})
	return body
}

func parityCloseReceipt(stdout []byte, lane, key string, target parityCloseTarget, body []byte, preview, validated bool, route string) bool {
	var frame struct {
		Data struct {
			Issue        liveIssue       `json:"issue"`
			DryRun       *bool           `json:"dry_run"`
			Comment      json.RawMessage `json:"comment"`
			Payload      json.RawMessage `json:"payload"`
			AtomicOnly   *bool           `json:"atomic_only"`
			CommentRoute string          `json:"comment_route"`
			Transition   string          `json:"transition"`
			Results      []struct {
				Issue liveIssue              `json:"issue"`
				OK    *bool                  `json:"ok"`
				Error *struct{ Code string } `json:"error"`
				Data  struct {
					Payload          json.RawMessage `json:"payload"`
					Attempted        *bool           `json:"attempted"`
					Applied          *bool           `json:"applied"`
					CommentPlan      string          `json:"comment_plan"`
					CommentPosted    string          `json:"comment_posted"`
					CommentID        string          `json:"comment_id"`
					CommentErrorCode string          `json:"comment_error_code"`
					CommentErrorHint string          `json:"comment_error_hint"`
					CommentError     string          `json:"comment_error"`
					Transition       *struct {
						ID        string `json:"id"`
						Status    string `json:"status"`
						HasScreen *bool  `json:"has_screen"`
					} `json:"transition"`
				} `json:"data"`
			} `json:"results"`
		} `json:"data"`
		Warnings []struct{ Type, Message string } `json:"warnings"`
	}
	if json.Unmarshal(stdout, &frame) != nil || frame.Data.DryRun == nil || *frame.Data.DryRun != preview || frame.Data.Transition != target.ID || !nativeMetadataMatches(frame.Data.Comment, body, nil) {
		return false
	}
	if lane == "before" {
		return frame.Data.Issue.Key == key
	}
	atomicPayload := []byte(`{"transition":{"id":"` + target.ID + `"},"update":{"comment":[{"add":{"body":` + string(body) + `}}]}}`)
	if frame.Data.AtomicOnly == nil || *frame.Data.AtomicOnly || frame.Data.CommentRoute != "markdown" || !nativeMetadataMatches(frame.Data.Payload, atomicPayload, nil) {
		return false
	}
	if len(frame.Data.Results) != 1 {
		return false
	}
	row := frame.Data.Results[0]
	if row.Issue.Key != key || row.OK == nil || *row.OK != (route != "move_failed") || row.Data.Attempted == nil || row.Data.Applied == nil || *row.Data.Attempted != !preview || *row.Data.Applied != (!preview && route != "move_failed") {
		return false
	}
	if preview {
		if !validated {
			return row.Data.CommentPosted == "" && row.Data.CommentID == "" && row.Data.CommentPlan == "" && row.Data.Transition == nil && len(row.Data.Payload) == 0
		}
	}
	prepared := atomicPayload
	if !target.HasScreen {
		prepared = []byte(`{"transition":{"id":"` + target.ID + `"}}`)
	}
	if !nativeMetadataMatches(row.Data.Payload, prepared, nil) || row.Data.Transition == nil || row.Data.Transition.ID != target.ID || row.Data.Transition.Status != target.StatusName || row.Data.Transition.HasScreen == nil || *row.Data.Transition.HasScreen != target.HasScreen {
		return false
	}
	if preview {
		return row.Data.CommentPosted == "" && row.Data.CommentID == "" && row.Data.CommentPlan == route
	}
	if route == "move_failed" {
		return row.Error != nil && row.Error.Code == "jira_bad_request" && row.Data.CommentPosted == "" && row.Data.CommentID == "" && row.Data.CommentPlan == ""
	}
	if row.Error != nil {
		return false
	}
	if route == "failed" {
		return row.Data.CommentPosted == "failed" && row.Data.CommentID == "" && row.Data.CommentPlan == "" && row.Data.CommentErrorCode == "jira_forbidden" && !strings.Contains(row.Data.CommentError, "acknowledged the write") && strings.Contains(row.Data.CommentErrorHint, "Inspect") && strings.Contains(row.Data.CommentErrorHint, "Do not repeat") && len(frame.Warnings) == 1 && frame.Warnings[0].Type == "comment_failed" && strings.Contains(frame.Warnings[0].Message, key) && strings.Contains(frame.Warnings[0].Message, "do not repeat")
	}
	if route == "separate" && (len(frame.Warnings) != 1 || frame.Warnings[0].Type != "comment_not_atomic" || !strings.Contains(frame.Warnings[0].Message, key)) {
		return false
	}
	return row.Data.CommentPosted == route && row.Data.CommentPlan == "" && ((route == "separate" && row.Data.CommentID != "") || (route == "atomic" && row.Data.CommentID == ""))
}

func parityCloseLegacyRefusal(stdout []byte, id string) bool {
	var frame struct {
		Errors []struct{ Code, Message string }
	}
	return json.Unmarshal(stdout, &frame) == nil && len(frame.Errors) == 1 && frame.Errors[0].Code == "validation_failed" && strings.Contains(frame.Errors[0].Message, fmt.Sprintf("%q transition has no screen", id)) && strings.Contains(frame.Errors[0].Message, "silently discards") && strings.Contains(frame.Errors[0].Message, "run the transition bare")
}

func TestLiveParityCommentClose(t *testing.T) {
	p := newParityRun(t)
	var baseline json.RawMessage
	var baselineTarget parityCloseTarget
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			key := p.create(t, lane)
			before := p.closeState(t, key)
			if baseline == nil {
				baseline = slices.Clone(before.Fields)
			} else if !parityUnchangedFields(before.Fields, baseline, "summary") {
				t.Fatal("paired close fixtures do not have equivalent native preimages")
			}
			workflow := rawRead(t, p.owner, "api", "get", "/rest/api/3/issue/"+key+"/transitions", "--query=expand=transitions.fields")
			target, err := parityClosingTarget(workflow.Response.JSON, parityStatusID(before.Fields))
			if err != nil || target.HasScreen {
				t.Fatal("screenless comment/close comparison requires an independently discovered terminal transition")
			}
			if baselineTarget.ID == "" {
				baselineTarget = target
			} else if baselineTarget != target {
				t.Fatal("paired close fixtures selected different native transition destinations")
			}
			if p.evidence.Preimages == nil {
				p.evidence.Preimages = make(map[string]json.RawMessage)
			}
			p.evidence.Preimages["I022.close."+lane.Name] = before.Fields
			p.evidence.Preimages["I022.workflow."+lane.Name] = workflow.Response.JSON
			p.save(t)
			marker := p.owner.manifest.RunID + " paired close " + lane.Name
			body := parityCloseBody(marker)
			unchanged := func() {
				t.Helper()
				if !nativeMetadataMatches(p.closeState(t, key).Fields, before.Fields, nil) {
					t.Fatal("close preview/refusal changed native state")
				}
				p.requireCommentInventory(t, key, []string{}, map[string][]byte{})
			}
			unchanged()
			frame, stdout, exit := p.closeInvocation(t, lane, key, target, marker, nil, "--dry-run")
			if exit != 0 || !frame.OK || !parityCloseReceipt(stdout, lane.Name, key, target, body, true, false, "") {
				t.Fatal("local close preview lost its comment or claimed a delivery route")
			}
			unchanged()
			p.check(t, lane, "I022.comment_close.preview")
			frame, stdout, exit = p.closeInvocation(t, lane, key, target, marker, nil, "--dry-run", "--validate-remote")
			if lane.Name == "before" {
				if exit != 3 || frame.OK || !parityCloseLegacyRefusal(stdout, target.ID) {
					t.Fatal("legacy screenless comment preview no longer has its recorded refusal")
				}
			} else if exit != 0 || !frame.OK || !parityCloseReceipt(stdout, lane.Name, key, target, body, true, true, "separate") {
				t.Fatal("remote close preview did not report its separate-comment plan")
			}
			unchanged()
			p.check(t, lane, "I022.comment_close.validated_preview")
			frame, stdout, exit = p.closeInvocation(t, lane, key, target, marker, []string{"JIRA_READ_ONLY=true"})
			code := parityCommentReadOnlyCode(lane.Name)
			if lane.Name == "before" {
				code = "validation_failed"
			}
			if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != code {
				t.Fatal("comment/close bypassed read-only refusal")
			}
			if lane.Name == "before" && !parityCloseLegacyRefusal(stdout, target.ID) {
				t.Fatal("legacy read-only compound invocation failed for an unrelated reason")
			}
			unchanged()
			p.check(t, lane, "I022.comment_close.read_only")
			if lane.Name == "after" {
				frame, _, exit = p.closeInvocation(t, lane, key, target, marker, nil, "--atomic-only")
				if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "flag_not_applicable" {
					t.Fatal("atomic-only did not refuse the screenless compound operation")
				}
				unchanged()
				p.check(t, lane, "I022.comment_close.atomic_only")
			}
			attempt := len(p.evidence.Comments)
			p.evidence.Comments = append(p.evidence.Comments, parityCommentAttempt{Lane: lane.Name, Key: key, Digest: fmt.Sprintf("%x", sha256.Sum256(body))})
			p.save(t)
			frame, stdout, exit = p.closeInvocation(t, lane, key, target, marker, nil)
			if lane.Name == "before" {
				if exit != 3 || frame.OK || !parityCloseLegacyRefusal(stdout, target.ID) {
					t.Fatal("legacy compound close did not have its expected screenless refusal; never replay")
				}
				unchanged()
			} else {
				var reply struct {
					Data struct {
						Results []struct {
							Data struct {
								CommentID string `json:"comment_id"`
							}
						}
					}
				}
				if json.Unmarshal(stdout, &reply) == nil && len(reply.Data.Results) == 1 {
					p.evidence.Comments[attempt].ID = reply.Data.Results[0].Data.CommentID
					p.save(t)
				}
				if exit != 0 || !frame.OK || !parityCloseReceipt(stdout, lane.Name, key, target, body, false, false, "separate") {
					t.Fatal("compound close lacks its acknowledged status and comment receipts; never replay")
				}
				after := p.closeState(t, key)
				if parityStatusID(after.Fields) != target.StatusID || !parityUnchangedFields(after.Fields, before.Fields, "status") {
					t.Fatal("compound close did not store the terminal status or changed unrelated fields")
				}
				id := p.evidence.Comments[attempt].ID
				p.requireCommentInventory(t, key, []string{id}, map[string][]byte{id: body})
				p.owner.registerComment(t, key, id)
				p.evidence.Comments[attempt].Bound = true
				p.save(t)
			}
			p.check(t, lane, "I022.comment_close.native_improvement")
		})
	}
}

func TestParityClosingTarget(t *testing.T) {
	const workflow = `{"transitions":[{"id":"31","hasScreen":false,"to":{"id":"100","name":"Done","statusCategory":{"key":"done"}}}]}`
	target, err := parityClosingTarget([]byte(workflow), "1")
	if err != nil || target != (parityCloseTarget{"31", "100", "Done", false}) {
		t.Fatal("complete terminal workflow refused")
	}
	for name, raw := range map[string]string{
		"nonterminal":    strings.ReplaceAll(workflow, `"done"`, `"indeterminate"`),
		"missing_screen": strings.ReplaceAll(workflow, `"hasScreen":false,`, ""),
		"unavailable":    strings.ReplaceAll(workflow, `"id":"31"`, `"id":"31","isAvailable":false`),
		"duplicate":      strings.ReplaceAll(workflow, `}]}`, `},{"id":"31","hasScreen":false,"to":{"id":"100","name":"Done","statusCategory":{"key":"done"}}}]}`),
		"missing":        `{}`,
		"null":           `{"transitions":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parityClosingTarget([]byte(raw), "1"); err == nil {
				t.Fatal("unusable terminal inventory admitted")
			}
		})
	}
	if _, err := parityClosingTarget([]byte(workflow), "100"); err == nil {
		t.Fatal("already-terminal fixture mistaken for a closing state change")
	}
}

func TestParityCloseReceiptRequiresCompleteBoundOutcome(t *testing.T) {
	const body = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Authored"}]}]}`
	const candidate = `{"data":{"dry_run":false,"transition":"31","comment":` + body + `,"comment_route":"markdown","atomic_only":false,"payload":{"transition":{"id":"31"},"update":{"comment":[{"add":{"body":` + body + `}}]}},"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"attempted":true,"applied":true,"payload":{"transition":{"id":"31"}},"transition":{"id":"31","status":"Done","has_screen":false},"comment_posted":"separate","comment_id":"10001"}}]},"warnings":[{"type":"comment_not_atomic","message":"PAIR-1 used two requests"}]}`
	target := parityCloseTarget{"31", "100", "Done", false}
	if !parityCloseReceipt([]byte(candidate), "after", "PAIR-1", target, []byte(body), false, false, "separate") {
		t.Fatal("independent complete close receipt refused")
	}
	for name, replacement := range map[string][2]string{
		"foreign_target":           {`"key":"PAIR-1"`, `"key":"PAIR-2"`},
		"destination":              {`"status":"Done"`, `"status":"Open"`},
		"screen":                   {`"has_screen":false`, `"has_screen":true`},
		"atomic_policy":            {`"atomic_only":false`, `"atomic_only":true`},
		"input_route":              {`"comment_route":"markdown"`, `"comment_route":"adf"`},
		"missing_attempt":          {`"attempted":true,`, ``},
		"unacknowledged":           {`"applied":true`, `"applied":false`},
		"delivery_route":           {`"comment_posted":"separate"`, `"comment_posted":"atomic"`},
		"missing_comment_identity": {`"comment_id":"10001"`, `"comment_id":""`},
		"missing_warning":          {`[{"type":"comment_not_atomic","message":"PAIR-1 used two requests"}]`, `[]`},
		"prepared_transition":      {`"payload":{"transition":{"id":"31"}}`, `"payload":{"transition":{"id":"32"}}`},
		"composed_comment":         {`"text":"Authored"`, `"text":"Different"`},
	} {
		t.Run(name, func(t *testing.T) {
			changed := strings.Replace(candidate, replacement[0], replacement[1], 1)
			if changed == candidate || parityCloseReceipt([]byte(changed), "after", "PAIR-1", target, []byte(body), false, false, "separate") {
				t.Fatal("incomplete or changed close outcome admitted")
			}
		})
	}
}

func TestParityCloseArgsRefuseAdditionalMutationInputs(t *testing.T) {
	for _, lane := range []string{"before", "after"} {
		args, err := parityCloseArgs(lane, "PAIR-1", "31", "owned comment", nil)
		if err != nil || len(args) != 7 || args[2] != "PAIR-1" || args[4] != "31" || args[6] != "owned comment" {
			t.Fatal("fixed close recipe refused or changed its literal inputs")
		}
		for _, options := range [][]string{{"--jql=project=OTHER"}, {"--json-input=foreign.json"}, {"--transition=32"}, {"--validate-remote"}, {"--dry-run", "--dry-run"}, {"OTHER-1"}} {
			if _, err := parityCloseArgs(lane, "PAIR-1", "31", "owned comment", options); err == nil {
				t.Fatal("additional mutation input admitted")
			}
		}
	}
}
