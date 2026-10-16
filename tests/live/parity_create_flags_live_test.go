//go:build live

package live

import (
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type parityCreateFlagsWant struct {
	Summary, Project, ProjectID, Type, TypeID string
	AccountID                                 string
	Labels                                    []string
	Description                               json.RawMessage
}

func parityCreateFlagsIdentity(raw []byte, expected map[string]string) bool {
	if !jsontext.Value(raw).IsValid() {
		return false
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil || members == nil {
		return false
	}
	for name, want := range expected {
		var got string
		if json.Unmarshal(members[name], &got) != nil || got != want {
			return false
		}
	}
	return true
}

func parityCreateFlagsNative(issue liveIssue, want parityCreateFlagsWant) bool {
	if !jsontext.Value(issue.Fields).IsValid() {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(issue.Fields, &fields) != nil || fields == nil {
		return false
	}
	var summary string
	var labels []string
	if json.Unmarshal(fields["summary"], &summary) != nil || json.Unmarshal(fields["labels"], &labels) != nil {
		return false
	}
	// Jira may reorder native labels. Exact submitted order is guarded by the
	// synthetic writer; this readback requires complete membership instead.
	expected := slices.Clone(want.Labels)
	slices.Sort(labels)
	slices.Sort(expected)
	description := want.Description
	if description == nil {
		description = json.RawMessage(parityAuthoringDocument)
	}
	return summary == want.Summary && parityCreateFlagsIdentity(fields["project"], map[string]string{"id": want.ProjectID, "key": want.Project}) && parityCreateFlagsIdentity(fields["issuetype"], map[string]string{"id": want.TypeID, "name": want.Type}) && parityCreateFlagsIdentity(fields["assignee"], map[string]string{"accountId": want.AccountID}) && slices.Equal(labels, expected) && nativeMetadataMatches(fields["description"], description, nil)
}

func parityCreateFlagsReadback(raw []byte, issue liveIssue, want parityCreateFlagsWant) bool {
	if !parityCreateFlagsIdentity(raw, map[string]string{"id": issue.ID, "key": issue.Key, "self": issue.Self}) {
		return false
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil {
		return false
	}
	return parityCreateFlagsNative(liveIssue{Fields: members["fields"]}, want)
}

func parityCreateFlagsReceipt(raw []byte, lane string, issue liveIssue, want parityCreateFlagsWant) bool {
	identity := map[string]any{"id": issue.ID, "key": issue.Key, "self": issue.Self}
	var data map[string]any
	switch lane {
	case "before":
		data = map[string]any{"issue": identity, "dry_run": false, "validated_remotely": true, "preview": map[string]any{"project_key": want.Project, "issue_type": want.Type, "summary": want.Summary, "labels": want.Labels, "assignee_account_id": want.AccountID, "description_adf": json.RawMessage(parityAuthoringDocument)}}
	case "after":
		data = map[string]any{"issue": identity, "dry_run": false, "attempted": true, "applied": true, "receipt": identity, "description_route": "markdown", "payload": map[string]any{"fields": map[string]any{"project": map[string]any{"key": want.Project}, "issuetype": map[string]any{"name": want.Type}, "summary": want.Summary, "labels": want.Labels, "assignee": map[string]any{"accountId": want.AccountID}, "description": json.RawMessage(parityAuthoringDocument)}}}
	default:
		return false
	}
	expected, err := json.Marshal(data)
	return err == nil && parityAuthoringJSON(raw, expected)
}

func TestLiveParityCreateConvenienceFields(t *testing.T) {
	p := newParityRun(t)
	r := p.owner
	for _, lane := range p.evidence.Lanes {
		for _, source := range []string{"inline", "file"} {
			if !t.Run(lane.Name+"/"+source, func(t *testing.T) {
				summary := fmt.Sprintf("%s flags %s %s café <&>", r.manifest.RunID, lane.Name, source)
				labels := []string{r.manifest.RunID, r.manifest.RunID + "-flags"}
				args := []string{"issue", "create", "--project", r.manifest.Project, "--type", r.issueType, "--summary", summary, "--label=" + labels[0] + "," + labels[1], "--assignee", r.watcherAccountID}
				path := filepath.Join(r.dir, "description spaces 古い '$;&.md")
				if source == "file" {
					if err := os.WriteFile(path, []byte("**Owned café**"), 0o600); err != nil {
						t.Fatal(err)
					}
					args = append(args, "--markdown-file", path)
				} else {
					args = append(args, "--markdown=**Owned café**")
				}
				// A fresh fixed recipe, not arbitrary create argv. Persist intent
				// before entry and never replay an unresolved acknowledgement.
				r.manifest.Attempts = append(r.manifest.Attempts, summary)
				r.save(t)
				index := len(p.evidence.Creations)
				p.evidence.Creations = append(p.evidence.Creations, parityCreation{Lane: lane.Name, Summary: summary, Intent: true})
				p.save(t)
				frame, raw, exit := p.invoke(t, lane, nil, args...)
				issue := p.bindCreated(t, lane, index, frame, exit)
				want := parityCreateFlagsWant{Summary: summary, Project: r.manifest.Project, ProjectID: p.evidence.ProjectID, Type: r.issueType, TypeID: p.evidence.IssueTypeID, Labels: labels, AccountID: r.watcherAccountID}
				read := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+issue.ID, "--query=fields=summary,description,labels,project,issuetype,assignee")
				if !parityCreateFlagsReadback(read.Response.JSON, issue, want) {
					t.Fatal("convenience fields differ from exact native identity, membership or description; owned cleanup retained")
				}
				if !parityCreateFlagsReceipt(parityOriginalFrameData(raw), lane.Name, issue, want) {
					t.Fatal("convenience create lost its complete candidate or acknowledgement; owned cleanup retained")
				}
				if source == "file" {
					bytes, err := os.ReadFile(path)
					if err != nil || string(bytes) != "**Owned café**" {
						t.Fatal("create changed its authored Markdown file")
					}
				}
				p.check(t, lane, "I011.create.flags.live."+source)
			}) {
				t.Fatal("stop dependent create cases after failure; never replay")
			}
		}
	}
}

func TestParityCreateFlagsNativeRequiresCompleteFields(t *testing.T) {
	want := parityCreateFlagsWant{Summary: "Owned", Project: "KEY", ProjectID: "10", Type: "Task", TypeID: "20", AccountID: "account", Labels: []string{"first", "second"}}
	fields := `{"summary":"Owned","project":{"id":"10","key":"KEY"},"issuetype":{"id":"20","name":"Task"},"assignee":{"accountId":"account"},"labels":["second","first"],"description":` + parityAuthoringDocument + `}`
	if !parityCreateFlagsNative(liveIssue{Fields: json.RawMessage(fields)}, want) {
		t.Fatal("independent complete native control refused")
	}
	for _, field := range []string{"summary", "project", "issuetype", "assignee", "labels", "description"} {
		t.Run(field, func(t *testing.T) {
			var changed map[string]json.RawMessage
			if err := json.Unmarshal([]byte(fields), &changed); err != nil {
				t.Fatal(err)
			}
			changed[field] = json.RawMessage(`null`)
			raw, err := json.Marshal(changed)
			if err != nil || parityCreateFlagsNative(liveIssue{Fields: raw}, want) {
				t.Fatal("missing native field accepted")
			}
		})
	}
	for name, changed := range map[string]string{
		"wrong-case-field":     strings.Replace(fields, `"assignee":`, `"Assignee":`, 1),
		"wrong-case-identity":  strings.Replace(fields, `"accountId":`, `"AccountID":`, 1),
		"duplicate-summary":    strings.Replace(fields, `"summary":"Owned"`, `"summary":"other","summary":"Owned"`, 1),
		"duplicate-identity":   strings.Replace(fields, `"accountId":"account"`, `"accountId":"other","accountId":"account"`, 1),
		"different-membership": strings.Replace(fields, `"second","first"`, `"second","third"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if parityCreateFlagsNative(liveIssue{Fields: json.RawMessage(changed)}, want) {
				t.Fatal("changed or ambiguous native facts accepted")
			}
		})
	}
}

func TestParityCreateFlagsReceiptRequiresCompleteAcknowledgement(t *testing.T) {
	want := parityCreateFlagsWant{Summary: "Owned", Project: "KEY", Type: "Task", AccountID: "account", Labels: []string{"first", "second"}}
	issue := liveIssue{ID: "100", Key: "KEY-1", Self: "https://jira.invalid/rest/api/3/issue/100"}
	identity := `{"id":"100","key":"KEY-1","self":"https://jira.invalid/rest/api/3/issue/100"}`
	controls := map[string]string{
		"before": `{"issue":` + identity + `,"dry_run":false,"validated_remotely":true,"preview":{"project_key":"KEY","issue_type":"Task","summary":"Owned","labels":["first","second"],"assignee_account_id":"account","description_adf":` + parityAuthoringDocument + `}}`,
		"after":  `{"issue":` + identity + `,"dry_run":false,"attempted":true,"applied":true,"receipt":` + identity + `,"description_route":"markdown","payload":{"fields":{"project":{"key":"KEY"},"issuetype":{"name":"Task"},"summary":"Owned","labels":["first","second"],"assignee":{"accountId":"account"},"description":` + parityAuthoringDocument + `}}}`,
	}
	for lane, control := range controls {
		t.Run(lane, func(t *testing.T) {
			if !parityCreateFlagsReceipt([]byte(control), lane, issue, want) {
				t.Fatal("independent acknowledgement control refused")
			}
			var members map[string]json.RawMessage
			if err := json.Unmarshal([]byte(control), &members); err != nil {
				t.Fatal(err)
			}
			for name := range members {
				t.Run(name, func(t *testing.T) {
					changed := make(map[string]json.RawMessage)
					for key, value := range members {
						if key != name {
							changed[key] = value
						}
					}
					raw, err := json.Marshal(changed)
					if err != nil || parityCreateFlagsReceipt(raw, lane, issue, want) {
						t.Fatal("incomplete acknowledgement accepted")
					}
				})
			}
		})
	}
}

func TestParityCreateFlagsReadbackRequiresExactMembers(t *testing.T) {
	want := parityCreateFlagsWant{Summary: "Owned", Project: "KEY", ProjectID: "10", Type: "Task", TypeID: "20", AccountID: "account", Labels: []string{"label"}}
	issue := liveIssue{ID: "100", Key: "KEY-1", Self: "https://jira.invalid/rest/api/3/issue/100"}
	control := `{"id":"100","key":"KEY-1","self":"https://jira.invalid/rest/api/3/issue/100","fields":{"summary":"Owned","project":{"id":"10","key":"KEY"},"issuetype":{"id":"20","name":"Task"},"assignee":{"accountId":"account"},"labels":["label"],"description":` + parityAuthoringDocument + `}}`
	if !parityCreateFlagsReadback([]byte(control), issue, want) {
		t.Fatal("independent complete native response refused")
	}
	for name, changed := range map[string]string{
		"wrong-case-fields": strings.Replace(control, `"fields":`, `"Fields":`, 1),
		"wrong-case-id":     strings.Replace(control, `"id":"100"`, `"ID":"100"`, 1),
		"duplicate-id":      strings.Replace(control, `"id":"100"`, `"id":"other","id":"100"`, 1),
		"foreign-key":       strings.Replace(control, `"key":"KEY-1"`, `"key":"KEY-2"`, 1),
		"foreign-self":      strings.Replace(control, `issue/100`, `issue/200`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if parityCreateFlagsReadback([]byte(changed), issue, want) {
				t.Fatal("changed or ambiguous outer native fields accepted")
			}
		})
	}
}

func TestParityCreateFlagsReadbackUsesAuthoredDescription(t *testing.T) {
	want := parityCreateFlagsWant{Summary: "Owned", Project: "KEY", ProjectID: "10", Type: "Task", TypeID: "20", AccountID: "account", Labels: []string{"label"}, Description: json.RawMessage(parityDescription)}
	issue := liveIssue{ID: "100", Key: "KEY-1", Self: "https://jira.invalid/rest/api/3/issue/100"}
	const prefix = `{"id":"100","key":"KEY-1","self":"https://jira.invalid/rest/api/3/issue/100","fields":{"summary":"Owned","project":{"id":"10","key":"KEY"},"issuetype":{"id":"20","name":"Task"},"assignee":{"accountId":"account"},"labels":["label"],"description":`
	if !parityCreateFlagsReadback([]byte(prefix+parityDescription+`}}`), issue, want) {
		t.Fatal("independently authored clone description was refused")
	}
	for _, tc := range []struct{ name, value string }{
		{"default-description", parityAuthoringDocument},
		{"null-description", `null`},
		{"malformed-description", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if parityCreateFlagsReadback([]byte(prefix+tc.value+`}}`), issue, want) {
				t.Fatal("a different native description overrode the independently authored expectation")
			}
		})
	}
}
