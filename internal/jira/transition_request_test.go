package jira

import (
	"encoding/json"
	"sync"
	"testing"
)

func TestTransitionNativeRequestPreservesValuesAndSourceOwnership(t *testing.T) {
	t.Parallel()
	const source = `{"fields":{},"update":{"assignee":[{"set":{"accountId":"fixture-original"}}],"customfield_10001":[{"set":9007199254740993}]},"historyMetadata":{"extraData":{"raw":"<>&\u2028"}},"properties":[{"key":"fixture","value":null}],"futureCamelField":false}`
	var payload IssueWriteRequest
	if err := json.Unmarshal([]byte(source), &payload); err != nil {
		t.Fatal(err)
	}
	before, err := payload.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for _, id := range []string{"31", "41", "51"} {
		workers.Go(func() {
			req := TransitionRequest{Transition: TransitionRef{ID: id}, Payload: &payload, Fields: &TransitionFields{Assignee: &AccountIDRef{AccountID: "fixture-composed"}}}
			raw, err := req.MarshalJSON()
			if err != nil {
				t.Error(err)
				return
			}
			var got IssueWriteRequest
			if json.Unmarshal(raw, &got) != nil || string(got.Fields["assignee"]) != `{"accountId":"fixture-composed"}` || string(got.Update["customfield_10001"]) != `[{"set":9007199254740993}]` || string(got.Extra["transition"]) != `{"id":"`+id+`"}` || string(got.Extra["historyMetadata"]) != `{"extraData":{"raw":"<>&\u2028"}}` || string(got.Extra["futureCamelField"]) != "false" || string(got.Extra["properties"]) != `[{"key":"fixture","value":null}]` {
				t.Error("native transition composition changed raw input or per-key identity")
			}
			if _, collides := got.Update["assignee"]; collides {
				t.Error("typed transition override retained a conflicting copied operation")
			}
		})
	}
	workers.Wait()
	after, err := payload.MarshalJSON()
	if err != nil || string(after) != string(before) {
		t.Fatal("per-key transition composition mutated the shared native source")
	}
}

func TestTransitionNativeRequestKeepsExplicitEmptyContainers(t *testing.T) {
	t.Parallel()
	var payload IssueWriteRequest
	if err := json.Unmarshal([]byte(`{"fields":{},"update":{}}`), &payload); err != nil {
		t.Fatal(err)
	}
	raw, err := (TransitionRequest{Transition: TransitionRef{ID: "31"}, Payload: &payload}).MarshalJSON()
	if err != nil || string(raw) != `{"fields":{},"transition":{"id":"31"},"update":{}}` {
		t.Fatal("transition request dropped explicit empty native containers")
	}
}
