//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type parityEditFlagsAttempt struct {
	Lane         string          `json:"lane"`
	Key          string          `json:"key"`
	Source       string          `json:"source"`
	Payload      json.RawMessage `json:"payload"`
	Intent       bool            `json:"intent"`
	Acknowledged bool            `json:"acknowledged"`
	Bound        bool            `json:"bound"`
}

type parityEditFlagsScope struct {
	Project, ProjectID, Type, TypeID, RunID string
}

func parityEditFlagsCompleteScope(raw []byte, scope parityEditFlagsScope) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return false
	}
	for _, name := range []string{"summary", "description", "assignee", "project", "issuetype", "labels"} {
		if _, ok := fields[name]; !ok {
			return false
		}
	}
	var labels []string
	return json.Unmarshal(fields["labels"], &labels) == nil && slices.Equal(labels, []string{scope.RunID}) && parityCreateFlagsIdentity(fields["project"], map[string]string{"id": scope.ProjectID, "key": scope.Project}) && parityCreateFlagsIdentity(fields["issuetype"], map[string]string{"id": scope.TypeID, "name": scope.Type})
}

func parityEditFlagsNative(raw, before []byte, issue liveIssue, summary, account string, description json.RawMessage, scope parityEditFlagsScope) bool {
	identity := map[string]string{"id": issue.ID, "key": issue.Key, "self": issue.Self}
	if !parityCreateFlagsIdentity(raw, identity) || !parityCreateFlagsIdentity(before, identity) {
		return false
	}
	var afterRoot, beforeRoot map[string]json.RawMessage
	if json.Unmarshal(raw, &afterRoot) != nil || json.Unmarshal(before, &beforeRoot) != nil || !parityEditFlagsCompleteScope(afterRoot["fields"], scope) || !parityEditFlagsCompleteScope(beforeRoot["fields"], scope) {
		return false
	}
	var fields map[string]json.RawMessage
	var observed string
	if json.Unmarshal(afterRoot["fields"], &fields) != nil || fields == nil || json.Unmarshal(fields["summary"], &observed) != nil || observed != summary {
		return false
	}
	if account == "" {
		if strings.TrimSpace(string(fields["assignee"])) != "null" {
			return false
		}
	} else if !parityCreateFlagsIdentity(fields["assignee"], map[string]string{"accountId": account}) {
		return false
	}
	return nativeMetadataMatches(fields["description"], description, nil) && parityUnchangedFields(afterRoot["fields"], beforeRoot["fields"], "summary", "description", "assignee")
}

func parityEditFlagsReceipt(raw []byte, lane, key string, fields map[string]any) bool {
	identity := map[string]any{"key": key}
	var data map[string]any
	switch lane {
	case "before":
		data = map[string]any{"issue": identity, "result": map[string]any{}, "dry_run": false, "fields": fields, "validated_remotely": true}
	case "after":
		data = map[string]any{"dry_run": false, "notify": true, "payload": map[string]any{"fields": fields}, "description_route": "markdown", "results": []any{map[string]any{"issue": identity, "ok": true, "data": map[string]any{}}}}
	default:
		return false
	}
	expected, err := json.Marshal(data)
	return err == nil && parityAuthoringJSON(raw, expected)
}

func TestLiveParityEditConvenienceFields(t *testing.T) {
	p := newParityRun(t)
	r := p.owner
	p.evidence.Preimages = make(map[string]json.RawMessage)
	for _, lane := range p.evidence.Lanes {
		if !t.Run(lane.Name, func(t *testing.T) {
			key := p.create(t, lane)
			issue := p.native(t, key)
			scope := parityEditFlagsScope{Project: r.manifest.Project, ProjectID: p.evidence.ProjectID, Type: r.issueType, TypeID: p.evidence.IssueTypeID, RunID: r.manifest.RunID}
			previousSummary := p.evidence.Creations[len(p.evidence.Creations)-1].Summary
			previousAccount := ""
			previousDescription := json.RawMessage(parityDescription)
			for _, source := range []string{"inline", "file"} {
				if !t.Run(source, func(t *testing.T) {
					before := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+issue.ID, "--query=fields=summary,description,assignee,labels,project,issuetype")

					summary := fmt.Sprintf("%s edited %s %s café <&>", r.manifest.RunID, lane.Name, source)
					text, markdown := "Inline café", "**Inline café**"
					account := r.watcherAccountID
					args := []string{"issue", "edit", key, "--summary", summary}
					fields := map[string]any{"summary": summary}
					if source == "inline" {
						args = append(args, "--assignee", account, "--markdown", markdown)
						fields["assignee"] = map[string]any{"accountId": account}
					} else {
						text, markdown, account = "File café", "**File café**", ""
						path := filepath.Join(r.dir, "edit spaces 古い '$;&.md")
						if err := os.WriteFile(path, []byte(markdown), 0o600); err != nil {
							t.Fatal(err)
						}
						clear := "--unassign"
						if lane.Name == "before" {
							clear = "--assignee=none"
						}
						args = append(args, clear, "--markdown-file", path)
						fields["assignee"] = nil
					}
					// These independently authored documents differ, so an ignored
					// file edit cannot pass using the preceding inline description.
					description := json.RawMessage(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"` + text + `","marks":[{"type":"strong"}]}]}]}`)
					fields["description"] = description
					if !parityEditFlagsNative(before.Response.JSON, before.Response.JSON, issue, previousSummary, previousAccount, previousDescription, scope) || previousSummary == summary || previousAccount == account || nativeMetadataMatches(previousDescription, description, nil) {
						t.Fatal("edit requires complete bound preimage and different summary, assignee and description before entry")
					}
					payload, err := json.Marshal(map[string]any{"fields": fields})
					if err != nil {
						t.Fatal(err)
					}
					p.evidence.Preimages[lane.Name+"."+source] = before.Response.JSON
					index := len(p.evidence.EditFlags)
					p.evidence.EditFlags = append(p.evidence.EditFlags, parityEditFlagsAttempt{Lane: lane.Name, Key: key, Source: source, Payload: payload, Intent: true})
					p.save(t)
					frame, raw, exit := p.checkedMutation(t, lane, key, args...)
					p.evidence.EditFlags[index].Acknowledged = exit == 0 && frame.OK && parityWriteTarget(frame, lane.Name, key)
					p.save(t)
					if !p.evidence.EditFlags[index].Acknowledged {
						t.Fatal("edit acknowledgement unresolved; owned cleanup retained; never replay")
					}
					after := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+issue.ID, "--query=fields=summary,description,assignee,labels,project,issuetype")
					if !parityEditFlagsNative(after.Response.JSON, before.Response.JSON, issue, summary, account, description, scope) || !parityEditFlagsReceipt(parityOriginalFrameData(raw), lane.Name, key, fields) {
						t.Fatal("edit differs from independent native changes, unchanged fields or complete lane receipt")
					}
					if source == "file" {
						bytes, err := os.ReadFile(filepath.Join(r.dir, "edit spaces 古い '$;&.md"))
						if err != nil || string(bytes) != markdown {
							t.Fatal("edit changed its authored Markdown file")
						}
					}
					previousSummary, previousAccount, previousDescription = summary, account, description
					p.evidence.EditFlags[index].Bound = true
					p.save(t)
					p.check(t, lane, "I013.edit.flags.live."+source)
				}) {
					t.Fatal("stop later edits after failure; never replay")
				}
			}
		}) {
			t.Fatal("stop later lanes after failure; owned cleanup retained")
		}
	}
}

func TestParityEditFlagsNativeRequiresExactChanges(t *testing.T) {
	issue := liveIssue{ID: "100", Key: "KEY-1", Self: "https://jira.invalid/issue/100"}
	scope := parityEditFlagsScope{Project: "KEY", ProjectID: "10", Type: "Task", TypeID: "20", RunID: "owned"}
	before := []byte(`{"id":"100","key":"KEY-1","self":"https://jira.invalid/issue/100","fields":{"summary":"old","assignee":null,"project":{"id":"10","key":"KEY"},"issuetype":{"id":"20","name":"Task"},"labels":["owned"],"description":null}}`)
	control := `{"id":"100","key":"KEY-1","self":"https://jira.invalid/issue/100","fields":{"summary":"new","assignee":{"accountId":"account"},"project":{"id":"10","key":"KEY"},"issuetype":{"id":"20","name":"Task"},"labels":["owned"],"description":` + parityAuthoringDocument + `}}`
	if !parityEditFlagsNative([]byte(control), before, issue, "new", "account", json.RawMessage(parityAuthoringDocument), scope) {
		t.Fatal("independent complete native edit refused")
	}
	for name, raw := range map[string]string{
		"wrong-fields-case": strings.Replace(control, `"fields":`, `"Fields":`, 1),
		"wrong-summary":     strings.Replace(control, `"new"`, `"old"`, 1),
		"wrong-assignee":    strings.Replace(control, `"accountId"`, `"AccountID"`, 1),
		"collateral-label":  strings.Replace(control, `"owned"`, `"other"`, 1),
		"wrong-description": strings.Replace(control, `Owned café`, `different`, 1),
		"duplicate-summary": strings.Replace(control, `"summary":"new"`, `"summary":"old","summary":"new"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if parityEditFlagsNative([]byte(raw), before, issue, "new", "account", json.RawMessage(parityAuthoringDocument), scope) {
				t.Fatal("wrong native edit, collateral change or ambiguous response accepted")
			}
		})
	}
	cleared := strings.Replace(control, `{"accountId":"account"}`, `null`, 1)
	if !parityEditFlagsNative([]byte(cleared), before, issue, "new", "", json.RawMessage(parityAuthoringDocument), scope) {
		t.Fatal("explicit native unassignment refused")
	}
	missing := strings.Replace(cleared, `"assignee":null,`, ``, 1)
	if parityEditFlagsNative([]byte(missing), before, issue, "new", "", json.RawMessage(parityAuthoringDocument), scope) {
		t.Fatal("absent assignee mistaken for explicit unassignment")
	}
	for _, field := range []string{"project", "issuetype", "labels"} {
		t.Run("jointly-missing-"+field, func(t *testing.T) {
			drop := func(raw []byte) []byte {
				var root map[string]json.RawMessage
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(raw, &root); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(root["fields"], &fields); err != nil {
					t.Fatal(err)
				}
				delete(fields, field)
				root["fields"], _ = json.Marshal(fields)
				out, err := json.Marshal(root)
				if err != nil {
					t.Fatal(err)
				}
				return out
			}
			if parityEditFlagsNative(drop([]byte(control)), drop(before), issue, "new", "account", json.RawMessage(parityAuthoringDocument), scope) {
				t.Fatal("jointly missing selected scope facts accepted")
			}
		})
	}
}

func TestParityEditFlagsReceiptRequiresCompleteFacts(t *testing.T) {
	fields := map[string]any{"summary": "new", "assignee": nil, "description": json.RawMessage(parityAuthoringDocument)}
	control := `{"dry_run":false,"notify":true,"payload":{"fields":{"summary":"new","assignee":null,"description":` + parityAuthoringDocument + `}},"description_route":"markdown","results":[{"issue":{"key":"KEY-1"},"ok":true,"data":{}}]}`
	if !parityEditFlagsReceipt([]byte(control), "after", "KEY-1", fields) {
		t.Fatal("independent complete acknowledgement refused")
	}
	for name, raw := range map[string]string{
		"foreign-key":        strings.Replace(control, `"KEY-1"`, `"KEY-2"`, 1),
		"false-preview":      strings.Replace(control, `"dry_run":false`, `"dry_run":true`, 1),
		"lost-unassignment":  strings.Replace(control, `"assignee":null,`, ``, 1),
		"false-notification": strings.Replace(control, `"notify":true`, `"notify":false`, 1),
		"missing-data":       strings.Replace(control, `,"data":{}`, ``, 1),
		"wrong-description":  strings.Replace(control, `Owned café`, `different`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if parityEditFlagsReceipt([]byte(raw), "after", "KEY-1", fields) {
				t.Fatal("wrong or incomplete acknowledgement accepted")
			}
		})
	}
}
