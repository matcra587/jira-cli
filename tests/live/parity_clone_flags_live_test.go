//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Cleanup ownership excludes behavioral fields: a broken assignee/description
// override must still leave a uniquely run-bound clone eligible for deletion.
func parityCloneFlagsOwned(raw []byte, identity liveIssue, want parityCreateFlagsWant, runID string) (liveIssue, bool) {
	if runID == "" || identity.ID == "" || identity.Key == "" || identity.Self == "" || want.Summary == "" || want.Project == "" || want.ProjectID == "" || want.Type == "" || want.TypeID == "" || !parityCreateFlagsIdentity(raw, map[string]string{"id": identity.ID, "key": identity.Key, "self": identity.Self}) {
		return liveIssue{}, false
	}
	var root, fields map[string]json.RawMessage
	var summary string
	var labels []string
	valid := json.Unmarshal(raw, &root) == nil && json.Unmarshal(root["fields"], &fields) == nil && fields != nil &&
		json.Unmarshal(fields["summary"], &summary) == nil && summary == want.Summary &&
		json.Unmarshal(fields["labels"], &labels) == nil && slices.Contains(labels, runID) &&
		parityCreateFlagsIdentity(fields["project"], map[string]string{"id": want.ProjectID, "key": want.Project}) &&
		parityCreateFlagsIdentity(fields["issuetype"], map[string]string{"id": want.TypeID, "name": want.Type})
	if !valid {
		return liveIssue{}, false
	}
	// Retain the exact validated identity and lowercase native field member.
	// A second case-insensitive struct decode could replace them with aliases.
	identity.Fields = slices.Clone(root["fields"])
	return identity, true
}

func TestParityCloneFlagsOwnershipPrecedesBehavior(t *testing.T) {
	identity := liveIssue{ID: "901", Key: "KEY-90", Self: "https://jira.invalid/rest/api/3/issue/901"}
	want := parityCreateFlagsWant{Summary: "owned-run clone", Project: "KEY", ProjectID: "10", Type: "Task", TypeID: "20", AccountID: "account", Labels: []string{"owned-run", "extra"}, Description: json.RawMessage(parityDescription)}
	const prefix = `{"id":"901","key":"KEY-90","self":"https://jira.invalid/rest/api/3/issue/901","fields":{"summary":"owned-run clone","project":{"id":"10","key":"KEY"},"issuetype":{"id":"20","name":"Task"},"labels":["owned-run","extra"],"assignee":{"accountId":"account"},"description":`
	valid := prefix + parityDescription + `}}`
	if _, owned := parityCloneFlagsOwned([]byte(valid), identity, want, "owned-run"); !owned || !parityCreateFlagsReadback([]byte(valid), identity, want) {
		t.Fatal("independent native ownership and behavior control was refused")
	}
	for _, tc := range []struct{ name, original, replacement string }{
		{"ignored-assignee", `"assignee":{"accountId":"account"}`, `"assignee":null`},
		{"damaged-description", parityDescription, `null`},
		{"missing-extra-label", `"labels":["owned-run","extra"]`, `"labels":["owned-run"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(valid, tc.original, tc.replacement, 1)
			_, owned := parityCloneFlagsOwned([]byte(changed), identity, want, "owned-run")
			if changed == valid || !owned || parityCreateFlagsReadback([]byte(changed), identity, want) {
				t.Fatal("a behavioral regression lost known ownership or earned field acceptance")
			}
		})
	}
	for _, tc := range []struct{ name, original, replacement string }{
		{"wrong-id", `"id":"901"`, `"id":"902"`},
		{"wrong-key", `"key":"KEY-90"`, `"key":"KEY-91"`},
		{"wrong-self", `issue/901`, `issue/902`},
		{"wrong-project", `"id":"10"`, `"id":"11"`},
		{"wrong-type", `"id":"20"`, `"id":"21"`},
		{"wrong-summary", `"summary":"owned-run clone"`, `"summary":"another run"`},
		{"missing-run-label", `"labels":["owned-run","extra"]`, `"labels":["extra"]`},
		{"wrong-case-fields", `"fields":`, `"Fields":`},
		{"duplicate-id", `"id":"901"`, `"id":"902","id":"901"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(valid, tc.original, tc.replacement, 1)
			if _, owned := parityCloneFlagsOwned([]byte(changed), identity, want, "owned-run"); changed == valid || owned {
				t.Fatal("unbound or ambiguous native identity earned cleanup ownership")
			}
		})
	}
	for _, tc := range []struct{ name, original, replacement string }{
		{"mixed-case-key", `"key":"KEY-90"`, `"key":"KEY-90","Key":"KEY-777"`},
		{"mixed-case-id", `"id":"901"`, `"id":"901","ID":"777"`},
		{"mixed-case-self", `"self":"https://jira.invalid/rest/api/3/issue/901"`, `"self":"https://jira.invalid/rest/api/3/issue/901","Self":"https://jira.invalid/rest/api/3/issue/777"`},
		{"mixed-case-fields", parityDescription + `}}`, parityDescription + `},"Fields":{"summary":"foreign"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(valid, tc.original, tc.replacement, 1)
			clone, owned := parityCloneFlagsOwned([]byte(changed), identity, want, "owned-run")
			if changed == valid || !owned || clone.ID != identity.ID || clone.Key != identity.Key || clone.Self != identity.Self || !parityCreateFlagsNative(clone, want) {
				t.Fatal("an unknown case alias replaced the exact validated cleanup identity or native fields")
			}
		})
	}
}

// Compare the legacy native override route with current convenience flags.
// Both create a new owned clone; neither may change its original source.
func TestLiveParityCloneConvenienceFields(t *testing.T) {
	p := newParityRun(t)
	r := p.owner
	inventory := cloneProjectInventory(t, p)
	p.evidence.Preimages = make(map[string]json.RawMessage)
	for _, lane := range p.evidence.Lanes {
		if !t.Run(lane.Name, func(t *testing.T) {
			sourceIndex := len(p.evidence.Creations)
			key := p.create(t, lane)
			source := p.native(t, key)
			inventory[key] = source.ID
			waitCloneInventory(t, p, inventory)
			fieldsQuery := "--query=fields=summary,description,assignee,labels,project,issuetype"
			before := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+source.ID, fieldsQuery)
			scope := parityEditFlagsScope{Project: r.manifest.Project, ProjectID: p.evidence.ProjectID, Type: r.issueType, TypeID: p.evidence.IssueTypeID, RunID: r.manifest.RunID}
			if !parityEditFlagsNative(before.Response.JSON, before.Response.JSON, source, p.evidence.Creations[sourceIndex].Summary, "", json.RawMessage(parityDescription), scope) {
				t.Fatal("clone flags require a complete owned unassigned native source")
			}
			p.evidence.Preimages[lane.Name] = slices.Clone(before.Response.JSON)
			p.save(t)
			summary := fmt.Sprintf("%s clone flags %s v2", r.manifest.RunID, lane.Name)
			labels := []string{r.manifest.RunID, r.manifest.RunID + "-clone-flags-" + lane.Name}
			fields := map[string]any{"summary": summary, "labels": labels, "assignee": map[string]any{"accountId": r.watcherAccountID}}
			payload, err := json.Marshal(map[string]any{"fields": fields})
			if err != nil {
				t.Fatal(err)
			}
			recipe := parityCloneRecipe{source: source.Key, sourceID: source.ID, path: filepath.Join(r.dir, "clone-flags-"+lane.Name+".json"), payload: slices.Clone(payload)}
			if err := os.WriteFile(recipe.path, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"issue", "clone", source.Key, "--json-input", recipe.path, "--force"}
			if lane.Name == "after" {
				args = []string{"issue", "clone", source.Key, "--summary", fmt.Sprintf("%s clone flags %s v1", r.manifest.RunID, lane.Name), "--replace=v1=v2", "--label", labels[0], "--label", labels[1], "--assignee", r.watcherAccountID}
			}
			if err := r.checkCloneRecipe(recipe); err != nil {
				t.Fatal(err)
			}
			r.manifest.Attempts = append(r.manifest.Attempts, summary)
			r.save(t)
			attempt := len(p.evidence.Creations)
			p.evidence.Creations = append(p.evidence.Creations, parityCreation{Lane: lane.Name, Summary: summary, Intent: true})
			p.save(t)
			// Re-admit the exact frozen input and immutable source immediately
			// before entry. This observation is not a server precondition.
			fresh := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+source.ID, fieldsQuery)
			if !nativeMetadataMatches(fresh.Response.JSON, before.Response.JSON, nil) || r.checkCloneRecipe(recipe) != nil || r.checkCloneSource(recipe, source) != nil {
				t.Fatal("clone source or frozen candidate changed before entry")
			}
			frame, stdout, exit := p.invoke(t, lane, nil, args...)
			identity := parityCloneIdentity(stdout, lane.Name)
			p.evidence.Creations[attempt].Identity = identity
			p.save(t)
			id, idErr := strconv.ParseInt(identity.ID, 10, 64)
			if idErr != nil || id <= 0 || !liveKey.MatchString(identity.Key) || identity.Self == "" {
				t.Fatal("clone returned no complete native identity; retain intent and never replay")
			}
			if _, exists := inventory[identity.Key]; exists {
				t.Fatal("clone returned a previously existing identity; retain unresolved receipt")
			}
			readback := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+identity.ID, fieldsQuery)
			want := parityCreateFlagsWant{Summary: summary, Project: r.manifest.Project, ProjectID: p.evidence.ProjectID, Type: r.issueType, TypeID: p.evidence.IssueTypeID, AccountID: r.watcherAccountID, Labels: labels, Description: json.RawMessage(parityDescription)}
			clone, owned := parityCloneFlagsOwned(readback.Response.JSON, identity, want, r.manifest.RunID)
			if !owned {
				t.Fatal("clone cannot be bound to native identity and unique owned scope")
			}
			r.register(t, clone)
			inventory[clone.Key] = clone.ID
			p.evidence.Creations[attempt].Identity, p.evidence.Creations[attempt].Bound = clone, true
			p.save(t)
			if !parityCreateFlagsReadback(readback.Response.JSON, identity, want) {
				t.Fatal("owned clone lost requested native fields; cleanup registration retained")
			}
			if exit != 0 || !frame.OK {
				t.Fatal("bound clone retained a failed command outcome; never replay")
			}
			parityAuthoringCleanData(t, stdout, lane.Name, "issue.clone")
			if !parityCloneAcknowledged(parityOriginalFrameData(stdout), lane.Name, source.Key, clone, fields) {
				t.Fatal("bound clone lost its complete acknowledgement or authored override receipt")
			}
			after := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+source.ID, fieldsQuery)
			if !nativeMetadataMatches(after.Response.JSON, before.Response.JSON, nil) {
				t.Fatal("clone changed its source's independently observed fields")
			}
			if observed, err := os.ReadFile(recipe.path); err != nil || !slices.Equal(observed, payload) {
				t.Fatal("clone changed its frozen authored overrides")
			}
			waitCloneInventory(t, p, inventory)
			p.check(t, lane, "I005.clone.convenience.native")
		}) {
			break
		}
	}
}
