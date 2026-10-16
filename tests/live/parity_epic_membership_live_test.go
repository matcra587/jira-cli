//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
)

func (p *parityRun) epicNative(t *testing.T, key string) parityEpicState {
	t.Helper()
	owned := p.owner.ownedLiveIssue(key)
	if !p.owner.verified || owned == nil || owned.ID == "" {
		t.Fatal("epic observation requires a verified active owned issue")
	}
	response := rawRead(t, p.owner, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=parent,summary,description,labels,project,issuetype,status")
	state, err := parityEpicNative(response.Response.JSON, owned.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func (p *parityRun) epicObserve(t *testing.T, keys []string, before map[string]parityEpicState, parents map[string]string) map[string]parityEpicState {
	t.Helper()
	observed := make(map[string]parityEpicState, len(keys))
	for _, key := range keys {
		state := p.epicNative(t, key)
		want := before[key]
		actualFields, _ := json.Marshal(state.Fields)
		wantFields, _ := json.Marshal(want.Fields)
		if state.ID != want.ID || !parityAuthoringJSON(actualFields, wantFields) || state.ParentKey != parents[key] {
			t.Fatal("epic step changed native identity, parent or unrelated fields")
		}
		if parent := parents[key]; parent != "" {
			if state.ParentState != "value" || state.ParentID != p.owner.ownedLiveIssue(parent).ID {
				t.Fatal("epic relation lost the independently bound native parent ID")
			}
		} else if state.ParentState == "value" {
			t.Fatal("epic step retained an unexpected parent")
		}
		observed[key] = state
	}
	return observed
}

func TestLiveParityEpicMembership(t *testing.T) {
	p := newParityRun(t)
	epic := createLiveEpic(t, p.owner, "paired-epic-parent")
	children := []string{p.create(t, p.evidence.Lanes[0]), p.create(t, p.evidence.Lanes[1])}
	all := append(slices.Clone(children), epic)
	baseline := map[string]parityEpicState{}
	for _, key := range all {
		baseline[key] = p.epicNative(t, key)
		if baseline[key].ParentKey != "" {
			t.Fatal("membership fixtures must begin independently unparented")
		}
	}
	var typ struct {
		Name    string `json:"name"`
		Subtask *bool  `json:"subtask"`
		Level   *int   `json:"hierarchyLevel"`
	}
	if json.Unmarshal(baseline[epic].Fields["issuetype"], &typ) != nil || typ.Subtask == nil || *typ.Subtask || typ.Level == nil || *typ.Level != 1 {
		t.Fatal("native destination does not establish a non-subtask Epic hierarchy")
	}
	for _, key := range children {
		var childType struct {
			Subtask *bool `json:"subtask"`
		}
		if json.Unmarshal(baseline[key].Fields["issuetype"], &childType) != nil || childType.Subtask == nil || *childType.Subtask {
			t.Fatal("native child does not establish an ordinary non-subtask issue")
		}
	}
	for _, lane := range p.evidence.Lanes {
		for _, width := range []int{1, 2} {
			targets := children[:width]
			parents := map[string]string{}
			restored := p.epicObserve(t, all, baseline, parents)
			for _, key := range all {
				if !parityEpicRestoredParent(restored[key], baseline[key]) {
					t.Fatal("epic lane did not restore the exact native unparented preimage")
				}
			}
			for _, verb := range []string{"add", "remove"} {
				parent, id := "", "C051"
				if verb == "add" {
					parent, id = epic, "C048"
				}
				for _, step := range []string{"preview", "read_only", "native"} {
					t.Run(fmt.Sprintf("%s/%s/%d/%s", lane.Name, verb, width, step), func(t *testing.T) {
						args := append([]string{"epic", verb}, targets...)
						declared := slices.Clone(targets)
						if parent != "" {
							args = append(args, parent)
							declared = append(declared, parent)
						}
						args = append(args, "--parallelism=1")
						var extra []string
						if step == "preview" {
							args = append(args, "--dry-run")
						}
						if step == "read_only" {
							extra = []string{"JIRA_READ_ONLY=true"}
						}
						if err := p.owner.checkMutation(declared, args); err != nil {
							t.Fatal(err)
						}
						preimage := p.epicObserve(t, all, baseline, parents)
						first := len(p.evidence.Epics)
						if step == "native" {
							for _, key := range targets {
								p.evidence.Epics = append(p.evidence.Epics, parityEpicAttempt{Lane: lane.Name, Key: key, Parent: parent})
							}
							p.save(t)
						}
						if err := p.owner.checkMutation(declared, args); err != nil {
							t.Fatal(err)
						}
						frame, stdout, exit := p.invoke(t, lane, extra, args...)
						if step == "read_only" {
							if exit != 3 || frame.OK || !parityEpicRefusal(stdout, lane.Name, verb, targets, parent) {
								t.Fatal("epic write did not retain its lane-specific read-only refusal")
							}
						} else {
							if exit != 0 || !frame.OK || !parityEpicReceipt(parityOriginalFrameData(stdout), lane.Name, verb, targets, parent, step == "preview") {
								t.Fatal("epic step lost the exact ordered acknowledgement; never replay")
							}
							if step == "native" {
								for _, key := range targets {
									parents[key] = parent
								}
							}
						}
						postimage := p.epicObserve(t, all, baseline, parents)
						for _, key := range all {
							if (step != "native" || !slices.Contains(targets, key)) && postimage[key].ParentState != preimage[key].ParentState {
								t.Fatal("epic step changed untouched native parent presence")
							}
						}
						if step == "native" {
							for i := first; i < len(p.evidence.Epics); i++ {
								p.evidence.Epics[i].Bound = true
							}
							p.save(t)
						}
						p.check(t, lane, fmt.Sprintf("%s.epic.%s_%d.%s", id, verb, width, step))
					})
					if t.Failed() {
						return
					}
				}
			}
		}
	}
	final := p.epicObserve(t, all, baseline, map[string]string{})
	for _, key := range all {
		if !parityEpicRestoredParent(final[key], baseline[key]) {
			t.Fatal("final epic relation did not restore the exact native preimage")
		}
	}
}
