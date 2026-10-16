//go:build live

package live

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type parityFieldMoveRecipe struct {
	Keys                  []string
	TypeID, Summary, Path string
	Body                  []byte
}

func (p *parityRun) fieldMoveInput(t *testing.T, keys []string, typeID, summary string) parityFieldMoveRecipe {
	t.Helper()
	r := p.owner
	if !r.verified || !r.moveTypes[typeID] || len(keys) == 0 || summary == "" {
		t.Fatal("field recipe requires verified native type metadata and authored fields")
	}
	for _, key := range keys {
		if r.ownedLiveIssue(key) == nil {
			t.Fatal("field recipe requires active explicit owned keys")
		}
	}
	body, err := json.Marshal(map[string]any{"fields": map[string]any{"summary": summary, "issuetype": map[string]any{"id": typeID}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.dir, fmt.Sprintf("paired-field-move-%03d.json", len(r.moveInputs)+1))
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if r.moveInputs == nil {
		r.moveInputs = make(map[string]nativeMoveInput)
	}
	r.moveInputs[path] = nativeMoveInput{keys: slices.Clone(keys), digest: sha256.Sum256(body), fields: true}
	return parityFieldMoveRecipe{Keys: slices.Clone(keys), TypeID: typeID, Summary: summary, Path: path, Body: body}
}

func (p *parityRun) fieldMoveNative(t *testing.T, key string) parityEpicState {
	t.Helper()
	owned := p.owner.ownedLiveIssue(key)
	if !p.owner.verified || owned == nil || owned.ID == "" {
		t.Fatal("field read requires a verified owned immutable ID")
	}
	read := rawRead(t, p.owner, "api", "get", "/rest/api/3/issue/"+owned.ID, "--query=fields=parent,subtasks,summary,description,labels,project,issuetype,status")
	state, err := parityEpicNative(read.Response.JSON, owned.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	var subtasks []json.RawMessage
	if json.Unmarshal(state.Fields["subtasks"], &subtasks) != nil || subtasks == nil || len(subtasks) != 0 || state.ParentKey != "" {
		t.Fatal("field fixture must remain independently unparented without subtasks")
	}
	return state
}

func parityFieldMoveID(raw []byte) string {
	var object map[string]json.RawMessage
	var id string
	if json.Unmarshal(raw, &object) != nil || json.Unmarshal(object["id"], &id) != nil {
		return ""
	}
	return id
}

func (p *parityRun) fieldMoveObserve(t *testing.T, keys []string, baseline map[string]parityEpicState, types, summaries map[string]string) {
	t.Helper()
	for _, key := range keys {
		actual := p.fieldMoveNative(t, key)
		before := baseline[key]
		var summary string
		if json.Unmarshal(actual.Fields["summary"], &summary) != nil || summary != summaries[key] || parityFieldMoveID(actual.Fields["issuetype"]) != types[key] || parityFieldMoveID(actual.Fields["project"]) != p.owner.moveProjectID || !parityEpicRestoredParent(actual, before) {
			t.Fatal("field write changed the wrong native identity, fields, project or hierarchy")
		}
		got, want := maps.Clone(actual.Fields), maps.Clone(before.Fields)
		for _, field := range []string{"summary", "issuetype"} {
			delete(got, field)
			delete(want, field)
		}
		gotRaw, _ := json.Marshal(got)
		wantRaw, _ := json.Marshal(want)
		if !parityAuthoringJSON(gotRaw, wantRaw) {
			t.Fatal("field write changed an unrelated native field")
		}
	}
}

func (p *parityRun) fieldMoveStep(t *testing.T, lane parityLane, all []string, baseline map[string]parityEpicState, types, summaries map[string]string, recipe parityFieldMoveRecipe, step string) {
	t.Helper()
	args := []string{"issue", "move"}
	if lane.Name == "after" {
		args = append(args, "fields")
	}
	args = append(args, recipe.Keys...)
	args = append(args, "--json-input", recipe.Path, "--parallelism=1")
	if step != "confirmation" {
		args = append(args, "--force")
	}
	var extra []string
	switch step {
	case "preview":
		args = append(args, "--dry-run")
	case "read_only":
		extra = []string{"JIRA_READ_ONLY=true"}
	case "confirmation", "native":
	default:
		t.Fatal("field recipe requires an authored acceptance step")
	}
	if err := parityFieldMoveAdmission(p.owner, lane.Name, recipe.Keys, args); err != nil {
		t.Fatal(err)
	}
	p.fieldMoveObserve(t, all, baseline, types, summaries)
	first := len(p.evidence.FieldMoves)
	if step == "native" {
		for _, key := range recipe.Keys {
			p.evidence.FieldMoves = append(p.evidence.FieldMoves, parityFieldMoveAttempt{Lane: lane.Name, Key: key, Input: recipe.Path})
		}
		p.save(t)
	}
	if err := parityFieldMoveAdmission(p.owner, lane.Name, recipe.Keys, args); err != nil {
		t.Fatal(err)
	}
	retained, err := os.ReadFile(recipe.Path)
	if err != nil || !bytes.Equal(retained, recipe.Body) {
		t.Fatal("field recipe bytes changed before process entry")
	}
	frame, stdout, exit := p.invoke(t, lane, extra, args...)
	switch step {
	case "confirmation":
		if exit != 3 || frame.OK || !parityFieldMoveConfirmation(stdout, lane.Name, recipe.Keys) {
			t.Fatal("field move did not retain missing-confirmation refusal")
		}
	case "read_only":
		if exit != 3 || frame.OK || !parityFieldMoveReadOnly(stdout, lane.Name, recipe.Keys, recipe.Body) {
			t.Fatal("field move did not retain its exact read-only refusal")
		}
	default:
		if exit != 0 || !frame.OK || !parityFieldMoveData(parityOriginalFrameData(stdout), lane.Name, recipe.Keys, recipe.Body, step == "preview") {
			t.Fatal("field write acknowledgement failed; never replay")
		}
		if step == "native" {
			for _, key := range recipe.Keys {
				types[key], summaries[key] = recipe.TypeID, recipe.Summary
			}
		}
	}
	p.fieldMoveObserve(t, all, baseline, types, summaries)
	retained, err = os.ReadFile(recipe.Path)
	if err != nil || !bytes.Equal(retained, recipe.Body) {
		t.Fatal("field invocation changed its authored input")
	}
	if step == "native" {
		for i := first; i < len(p.evidence.FieldMoves); i++ {
			p.evidence.FieldMoves[i].Bound = true
		}
		p.save(t)
	}
}

func (p *parityRun) moveTypeMetadata(t *testing.T) (string, string) {
	t.Helper()
	r := p.owner
	read := rawRead(t, r, "api", "get", "/rest/api/3/project/"+r.manifest.Project)
	var project map[string]json.RawMessage
	var id string
	var types []map[string]json.RawMessage
	if !jsontext.Value(read.Response.JSON).IsValid() || json.Unmarshal(read.Response.JSON, &project) != nil || json.Unmarshal(project["id"], &id) != nil || id == "" || json.Unmarshal(project["issueTypes"], &types) != nil || types == nil {
		t.Fatal("field fixture lacks complete native project/type metadata")
	}
	initialType, destination := "", ""
	for _, typ := range types {
		var name, typeID string
		var subtask *bool
		if json.Unmarshal(typ["name"], &name) != nil || json.Unmarshal(typ["id"], &typeID) != nil || typeID == "" || json.Unmarshal(typ["subtask"], &subtask) != nil || subtask == nil || *subtask {
			continue
		}
		if name == r.issueType {
			if initialType != "" {
				t.Fatal("ambiguous native initial issue type")
			}
			initialType = typeID
		}
		if name == "Epic" {
			if destination != "" {
				t.Fatal("ambiguous native destination issue type")
			}
			destination = typeID
		}
	}
	if initialType == "" || destination == "" || initialType == destination {
		t.Fatal("field comparison requires two distinct available non-subtask types")
	}
	r.moveProjectID = id
	r.moveTypes = map[string]bool{initialType: true, destination: true}
	return initialType, destination
}

func TestLiveParityMoveFields(t *testing.T) {
	p := newParityRun(t)
	r := p.owner
	initialType, destination := p.moveTypeMetadata(t)
	keys := []string{p.create(t, p.evidence.Lanes[0]), p.create(t, p.evidence.Lanes[1])}
	baseline := make(map[string]parityEpicState)
	expectedTypes, summaries := map[string]string{}, map[string]string{}
	for i, key := range keys {
		baseline[key] = p.fieldMoveNative(t, key)
		expectedTypes[key] = initialType
		summaries[key] = p.evidence.Creations[i].Summary
	}
	p.fieldMoveObserve(t, keys, baseline, expectedTypes, summaries)
	for _, lane := range p.evidence.Lanes {
		for _, width := range []int{1, 2} {
			p.fieldMoveObserve(t, keys, baseline, expectedTypes, summaries)
			for i, key := range keys {
				if expectedTypes[key] != initialType || summaries[key] != p.evidence.Creations[i].Summary {
					t.Fatal("field lane did not restore its original independently authored preimage")
				}
			}
			recipe := p.fieldMoveInput(t, keys[:width], destination, r.manifest.RunID+fmt.Sprintf(" paired field move %d café <&>", width))
			for _, step := range []string{"preview", "confirmation", "read_only", "native"} {
				t.Run(fmt.Sprintf("%s/%d/%s", lane.Name, width, step), func(t *testing.T) {
					p.fieldMoveStep(t, lane, keys, baseline, expectedTypes, summaries, recipe, step)
					p.check(t, lane, fmt.Sprintf("I020.move.fields.%d.%s", width, step))
				})
				if t.Failed() {
					return
				}
			}
			for i, key := range recipe.Keys {
				restore := p.fieldMoveInput(t, []string{key}, initialType, p.evidence.Creations[slices.Index(keys, key)].Summary)
				p.fieldMoveStep(t, lane, keys, baseline, expectedTypes, summaries, restore, "native")
				p.check(t, lane, fmt.Sprintf("I020.move.fields.%d.restore_%d", width, i+1))
			}
		}
	}
	p.fieldMoveObserve(t, keys, baseline, expectedTypes, summaries)
	for i, key := range keys {
		if expectedTypes[key] != initialType || summaries[key] != p.evidence.Creations[i].Summary {
			t.Fatal("final field comparison lost original type/summary restoration")
		}
	}
}
