//go:build live

package live

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

func TestLiveCommentMutationOwnershipGrammar(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "body.json")
	data := []byte(`{"version":1,"type":"doc","content":[]}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	r := &liveRun{verified: true, commentRole: "current-role", manifest: liveManifest{Issues: []ownedIssue{
		{Key: "KEY-1", ID: "1", Comments: []ownedComment{{ID: "10042"}, {ID: "10043", Deleted: true}}},
		{Key: "KEY-2", ID: "2", Comments: []ownedComment{{ID: "10044"}}},
		{Key: "KEY-3", ID: "3", Deleted: true, Comments: []ownedComment{{ID: "10045"}}},
	}}, commentInputs: map[string]nativeCommentInput{path: {key: "KEY-1", id: "10042", digest: sha256.Sum256(data)}}}
	for _, args := range [][]string{
		{"issue", "comment", "edit", "KEY-1", "10042", "--json-input", path},
		{"issue", "comment", "edit", "KEY-1", "10042", "--json-input=" + path, "--notify=false", "--visibility-role=current-role", "--dry-run"},
		{"issue", "comment", "edit", "KEY-1", "10042", "--json-input", path, "--clear-visibility"},
		{"issue", "comment", "delete", "KEY-1", "10042", "--force"},
		{"issue", "comment", "delete", "KEY-1", "10042", "--dry-run"},
		{"issue", "comment", "delete", "KEY-1", "10042"},
	} {
		if err := r.checkMutation([]string{"KEY-1"}, args); err != nil {
			t.Fatalf("owned authored comment workflow refused: %v", err)
		}
	}
	for name, args := range map[string][]string{
		"foreign-issue":       {"issue", "comment", "delete", "KEY-4", "10042", "--force"},
		"inactive-issue":      {"issue", "comment", "delete", "KEY-3", "10045", "--force"},
		"foreign-comment":     {"issue", "comment", "delete", "KEY-1", "90000", "--force"},
		"wrong-parent":        {"issue", "comment", "delete", "KEY-1", "10044", "--force"},
		"inactive-comment":    {"issue", "comment", "delete", "KEY-1", "10043", "--force"},
		"different-target":    {"issue", "comment", "delete", "KEY-2", "10044", "--force"},
		"extra-target":        {"issue", "comment", "delete", "KEY-1", "10042", "KEY-2", "--force"},
		"different-body":      {"issue", "comment", "edit", "KEY-1", "10042", "--json-input=other.json"},
		"different-binding":   {"issue", "comment", "edit", "KEY-2", "10044", "--json-input", path},
		"missing-body":        {"issue", "comment", "edit", "KEY-1", "10042"},
		"missing-value":       {"issue", "comment", "edit", "KEY-1", "10042", "--json-input"},
		"repeated-body":       {"issue", "comment", "edit", "KEY-1", "10042", "--json-input", path, "--json-input", path},
		"repeated-option":     {"issue", "comment", "delete", "KEY-1", "10042", "--force", "--force"},
		"false-preview":       {"issue", "comment", "delete", "KEY-1", "10042", "--dry-run=false"},
		"selector":            {"issue", "comment", "delete", "KEY-1", "10042", "--jql=project=KEY"},
		"foreign-role":        {"issue", "comment", "edit", "KEY-1", "10042", "--json-input", path, "--visibility-role=other-role"},
		"visibility-conflict": {"issue", "comment", "edit", "KEY-1", "10042", "--json-input", path, "--visibility-role=current-role", "--clear-visibility"},
		"force-edit":          {"issue", "comment", "edit", "KEY-1", "10042", "--json-input", path, "--force"},
		"notify-delete":       {"issue", "comment", "delete", "KEY-1", "10042", "--notify=false"},
		"native-delete":       {"issue", "comment", "delete", "KEY-1", "10042", "--json-input", path},
	} {
		t.Run(name, func(t *testing.T) {
			keys := []string{"KEY-1"}
			if name == "foreign-issue" || name == "inactive-issue" || name == "different-binding" {
				keys = []string{args[3]}
			}
			if err := r.checkMutation(keys, args); err == nil {
				t.Fatal("comment ownership guard admitted an unauthored workflow")
			}
		})
	}
	args := []string{"issue", "comment", "edit", "KEY-1", "10042", "--json-input", path}
	if err := os.WriteFile(path, []byte(`{"changed":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.checkMutation([]string{"KEY-1"}, args); err == nil {
		t.Fatal("comment ownership guard admitted changed registered bytes")
	}
	r.verified = false
	if err := r.checkMutation([]string{"KEY-1"}, []string{"issue", "comment", "delete", "KEY-1", "10042", "--force"}); err == nil {
		t.Fatal("comment ownership guard admitted unverified credentials")
	}
}
