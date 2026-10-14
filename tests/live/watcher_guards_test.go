//go:build live

package live

import "testing"

func TestLiveWatcherMutationOwnershipGrammar(t *testing.T) {
	t.Parallel()
	r := &liveRun{verified: true, account: "self@example.test", watcherAccountID: "current-account", watcherQuery: "self@example.test", manifest: liveManifest{Issues: []ownedIssue{
		{Key: "KEY-1", ID: "1"},
		{Key: "KEY-2", ID: "2"},
		{Key: "KEY-3", ID: "3", Deleted: true},
	}}}
	for _, args := range [][]string{
		{"issue", "watchers", "add", "KEY-1", "--user=@me"},
		{"issue", "watchers", "remove", "KEY-1", "--user", "current-account", "--dry-run", "--validate-remote"},
		{"issue", "watchers", "add", "KEY-1", "--user=current-account", "--no-readback", "--parallelism=1"},
		{"issue", "watchers", "add", "KEY-1", "--user-query=self@example.test"},
		{"issue", "watchers", "remove", "KEY-1", "--user-query", "self@example.test", "--dry-run", "--validate-remote"},
	} {
		if err := r.checkMutation([]string{"KEY-1"}, args); err != nil {
			t.Fatalf("owned current-account watcher workflow refused: %v", err)
		}
	}
	if err := r.checkMutation([]string{"KEY-1", "KEY-2"}, []string{"issue", "watchers", "remove", "KEY-1", "KEY-2", "--user=@me", "--parallelism=2"}); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"unowned-target":     {"issue", "watchers", "add", "KEY-4", "--user=@me"},
		"inactive-target":    {"issue", "watchers", "add", "KEY-3", "--user=@me"},
		"different-target":   {"issue", "watchers", "add", "KEY-2", "--user=@me"},
		"extra-target":       {"issue", "watchers", "add", "KEY-1", "KEY-2", "--user=@me"},
		"duplicate-target":   {"issue", "watchers", "add", "KEY-1", "KEY-1", "--user=@me"},
		"foreign-account":    {"issue", "watchers", "add", "KEY-1", "--user=other-account"},
		"foreign-query":      {"issue", "watchers", "add", "KEY-1", "--user-query=other@example.test"},
		"query-sentinel":     {"issue", "watchers", "add", "KEY-1", "--user-query=@me"},
		"two-identifiers":    {"issue", "watchers", "add", "KEY-1", "--user=@me", "--user-query=self@example.test"},
		"missing-query":      {"issue", "watchers", "add", "KEY-1", "--user-query"},
		"repeated-query":     {"issue", "watchers", "add", "KEY-1", "--user-query=self@example.test", "--user-query=self@example.test"},
		"missing-account":    {"issue", "watchers", "add", "KEY-1"},
		"missing-user-value": {"issue", "watchers", "add", "KEY-1", "--user"},
		"repeated-user":      {"issue", "watchers", "add", "KEY-1", "--user=@me", "--user=current-account"},
		"repeated-option":    {"issue", "watchers", "remove", "KEY-1", "--user=@me", "--dry-run", "--dry-run"},
		"unauthored-option":  {"issue", "watchers", "remove", "KEY-1", "--user=@me", "--force"},
		"selector":           {"issue", "watchers", "add", "KEY-1", "--user=@me", "--jql=project=KEY"},
		"false-preview":      {"issue", "watchers", "remove", "KEY-1", "--user=@me", "--dry-run=false"},
		"different-workers":  {"issue", "watchers", "add", "KEY-1", "--user=@me", "--parallelism=64"},
		"read-operation":     {"issue", "watchers", "list", "KEY-1"},
	} {
		t.Run(name, func(t *testing.T) {
			declared := []string{"KEY-1"}
			if name == "unowned-target" || name == "inactive-target" {
				declared = []string{args[3]}
			}
			if name == "duplicate-target" {
				declared = []string{"KEY-1", "KEY-1"}
			}
			if err := r.checkMutation(declared, args); err == nil {
				t.Fatal("watcher ownership guard admitted an unauthored workflow")
			}
		})
	}
	args := []string{"issue", "watchers", "add", "KEY-1", "--user=@me"}
	r.watcherAccountID = ""
	if err := r.checkMutation([]string{"KEY-1"}, args); err == nil {
		t.Fatal("watcher mutation accepted an unbound current account")
	}
	r.watcherAccountID = "current-account"
	r.watcherQuery = ""
	if err := r.checkMutation([]string{"KEY-1"}, []string{"issue", "watchers", "add", "KEY-1", "--user-query=self@example.test"}); err == nil {
		t.Fatal("watcher mutation accepted an unbound query")
	}
	r.watcherQuery = "self@example.test"
	r.verified = false
	if err := r.checkMutation([]string{"KEY-1"}, args); err == nil {
		t.Fatal("watcher mutation accepted unverified credentials")
	}
}
