//go:build live

package live

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestLiveSubtaskCreatePayloadBindsOwnedParentAndMetadata(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"valid", "foreign-parent", "deleted-parent", "unknown-type", "unverified", "foreign-summary"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := &liveRun{verified: true, subtaskTypes: map[string]bool{"10000": true}, manifest: liveManifest{RunID: "owned-run", Project: "KEY", Issues: []ownedIssue{{Key: "KEY-1", ID: "1"}, {Key: "KEY-2", ID: "2", Deleted: true}}}}
			parent, typ, summary := "KEY-1", "10000", "owned-run child"
			switch name {
			case "foreign-parent":
				parent = "KEY-3"
			case "deleted-parent":
				parent = "KEY-2"
			case "unknown-type":
				typ = "10001"
			case "unverified":
				r.verified = false
			case "foreign-summary":
				summary = "another-run child"
			}
			data, err := r.subtaskPayload(parent, typ, summary)
			if name == "valid" {
				if err != nil || len(data) == 0 {
					t.Fatal("owned subtask payload refused")
				}
				var got, want map[string]any
				const expected = `{"fields":{"project":{"key":"KEY"},"issuetype":{"id":"10000"},"parent":{"key":"KEY-1"},"summary":"owned-run child","labels":["owned-run"]}}`
				if json.Unmarshal(data, &got) != nil || json.Unmarshal([]byte(expected), &want) != nil || !reflect.DeepEqual(got, want) {
					t.Fatal("owned subtask payload changed its exact authored project, type, parent, summary or label inventory")
				}
			} else if err == nil || data != nil {
				t.Fatal("subtask payload accepted unowned parent, unverified metadata or unauthored summary")
			}
		})
	}
}

func TestLiveIssueDeleteMutationOwnershipGrammar(t *testing.T) {
	t.Parallel()
	r := &liveRun{verified: true, manifest: liveManifest{Issues: []ownedIssue{
		{Key: "KEY-1", ID: "1"}, {Key: "KEY-2", ID: "2"}, {Key: "KEY-3", ID: "3", Deleted: true},
	}}}
	for name, tc := range map[string]struct{ keys, args []string }{
		"unregistered-cascade":       {keys: []string{"KEY-1"}, args: []string{"issue", "delete", "KEY-1", "--force", "--delete-subtasks"}},
		"unregistered-alias-cascade": {keys: []string{"KEY-1"}, args: []string{"issue", "delete", "KEY-1", "--force", "--cascade"}},
		"extra-declared-key":         {keys: []string{"KEY-1", "KEY-2"}, args: []string{"issue", "delete", "KEY-1", "--force"}},
		"undeclared-target":          {keys: []string{"KEY-1"}, args: []string{"issue", "delete", "KEY-1", "KEY-2", "--force"}},
		"repeated-target":            {keys: []string{"KEY-1", "KEY-1"}, args: []string{"issue", "delete", "KEY-1", "KEY-1", "--force"}},
		"unknown-option":             {keys: []string{"KEY-1"}, args: []string{"issue", "delete", "KEY-1", "--unexpected"}},
		"repeated-option":            {keys: []string{"KEY-1"}, args: []string{"issue", "delete", "KEY-1", "--force", "--force"}},
		"false-preview":              {keys: []string{"KEY-1"}, args: []string{"issue", "delete", "KEY-1", "--dry-run=false"}},
		"selector":                   {keys: []string{"KEY-1"}, args: []string{"issue", "delete", "KEY-1", "--jql=project=KEY"}},
		"foreign-key":                {keys: []string{"KEY-4"}, args: []string{"issue", "delete", "KEY-4", "--force"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := r.checkMutation(tc.keys, tc.args); err == nil {
				t.Fatal("issue deletion accepted an undeclared target or unregistered cascade")
			}
		})
	}
	for _, key := range []string{"KEY-1", "KEY-3"} {
		if err := r.checkMutation([]string{key}, []string{"issue", "delete", key, "--force"}); err != nil {
			t.Fatalf("explicit run-owned target refused: %v", err)
		}
	}
	r.manifest.Issues[1].Parent = "KEY-1"
	r.deleteChildren = map[string][]string{"KEY-1": {"KEY-2"}}
	if err := r.checkMutation([]string{"KEY-1"}, []string{"issue", "delete", "KEY-1", "--force", "--cascade"}); err != nil {
		t.Fatalf("observed owned cascade refused: %v", err)
	}
	r.manifest.Issues[1].Parent = "KEY-3"
	if err := r.checkMutation([]string{"KEY-1"}, []string{"issue", "delete", "KEY-1", "--force", "--cascade"}); err == nil {
		t.Fatal("cascade accepted a child from a different parent")
	}
	r.manifest.Issues[1].Parent = "KEY-1"
	r.manifest.Issues[1].Deleted = true
	if err := r.checkMutation([]string{"KEY-1"}, []string{"issue", "delete", "KEY-1", "--force", "--cascade"}); err == nil {
		t.Fatal("cascade accepted an inactive child")
	}
	r.verified = false
	if err := r.checkMutation([]string{"KEY-1"}, []string{"issue", "delete", "KEY-1", "--force"}); err == nil {
		t.Fatal("issue deletion accepted unverified credentials")
	}
}
