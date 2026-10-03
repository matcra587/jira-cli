//go:build live

package live

import "testing"

func TestLiveMutationRejectsUnownedTargets(t *testing.T) {
	r := &liveRun{verified: true, manifest: liveManifest{Issues: []ownedIssue{{Key: "KEY-123", ID: "10001"}}}}
	cases := []struct {
		name string
		keys []string
		args []string
	}{
		{name: "unowned-primary", keys: []string{"KEY-124"}, args: []string{"issue", "edit", "KEY-124"}},
		{name: "unowned-link-end", keys: []string{"KEY-123"}, args: []string{"issue", "link", "create", "KEY-123", "--to", "KEY-124"}},
		{name: "unowned-link-end-equals", keys: []string{"KEY-123"}, args: []string{"issue", "link", "create", "KEY-123", "--to=KEY-124"}},
		{name: "selector", keys: []string{"KEY-123"}, args: []string{"issue", "edit", "--jql", "project = KEY"}},
		{name: "selector-equals", keys: []string{"KEY-123"}, args: []string{"issue", "edit", "--filter=10000"}},
		{name: "payload", keys: []string{"KEY-123"}, args: []string{"issue", "clone", "KEY-123", "--json-input", "payload.json"}},
		{name: "payload-alias", keys: []string{"KEY-123"}, args: []string{"issue", "clone", "KEY-123", "--from-json=payload.json"}},
		{name: "unregistered-comment-body", keys: []string{"KEY-123"}, args: []string{"issue", "comment", "add", "KEY-123", "--json-input", "body.json"}},
		{name: "raw-api", keys: []string{"KEY-123"}, args: []string{"api", "DELETE", "/issue/KEY-124"}},
		{name: "global-id-delete", keys: []string{"KEY-123"}, args: []string{"issue", "link", "delete", "10001"}},
		{name: "unowned-actual-primary", keys: []string{"KEY-123"}, args: []string{"issue", "edit", "10002"}},
		{name: "empty-targets", args: []string{"issue", "delete", "KEY-123"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := r.checkMutation(tc.keys, tc.args); err == nil {
				t.Fatal("unsafe live mutation was accepted")
			}
		})
	}
	if err := r.checkMutation([]string{"KEY-123"}, []string{"issue", "edit", "KEY-123", "--summary", "owned fixture"}); err != nil {
		t.Fatalf("owned explicit target refused: %v", err)
	}
	r.adfBodies = map[string]bool{"owned-body.json": true}
	for _, route := range []string{"--json-input", "--from-json", "--body-adf"} {
		if err := r.checkMutation([]string{"KEY-123"}, []string{"issue", "comment", "add", "KEY-123", route + "=owned-body.json"}); err != nil {
			t.Fatalf("registered native comment body refused: %v", err)
		}
		if err := r.checkMutation([]string{"KEY-123"}, []string{"issue", "edit", "KEY-123", route + "=owned-body.json"}); err == nil {
			t.Fatal("registered body opened an issue-payload mutation route")
		}
	}
	r.verified = false
	if err := r.checkMutation([]string{"KEY-123"}, []string{"issue", "edit", "KEY-123"}); err == nil {
		t.Fatal("unverified live mutation accepted")
	}
}
