//go:build live

package live

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLiveAttachmentMutationOwnershipGrammar(t *testing.T) {
	t.Parallel()
	r := &liveRun{verified: true, dir: t.TempDir(), manifest: liveManifest{Issues: []ownedIssue{
		{Key: "KEY-1", ID: "1", Attachments: []ownedAttachment{{ID: "200", Filename: "authored.bin"}}},
		{Key: "KEY-2", ID: "2", Attachments: []ownedAttachment{{ID: "201", Filename: "other.bin"}}},
		{Key: "KEY-3", ID: "3", Deleted: true},
	}}}
	path := r.attachmentInput(t, "authored.bin", []byte("registered bytes"))
	for _, args := range [][]string{
		{"issue", "attachment", "add", "KEY-1", path},
		{"issue", "attachment", "add", "KEY-1", path, "--dry-run"},
		{"issue", "attachment", "delete", "200", "--force"},
		{"issue", "attachment", "delete", "200", "--dry-run"},
	} {
		if err := r.checkMutation([]string{"KEY-1"}, args); err != nil {
			t.Fatalf("registered owned attachment workflow refused: %v", err)
		}
	}
	for name, args := range map[string][]string{
		"different-declared-key": {"issue", "attachment", "add", "KEY-2", path},
		"foreign-key":            {"issue", "attachment", "add", "KEY-4", path},
		"deleted-key":            {"issue", "attachment", "add", "KEY-3", path},
		"extra-owned-key":        {"issue", "attachment", "add", "KEY-1", "KEY-2", path},
		"selector":               {"issue", "attachment", "add", "KEY-1", path, "--jql=project=KEY"},
		"file-flag-switch":       {"issue", "attachment", "add", "KEY-1", path, "--file", path},
		"unknown-upload-file":    {"issue", "attachment", "add", "KEY-1", "not-registered.bin"},
		"repeated-file":          {"issue", "attachment", "add", "KEY-1", path, path},
		"missing-file":           {"issue", "attachment", "add", "KEY-1"},
		"foreign-resource":       {"issue", "attachment", "delete", "999", "--force"},
		"other-owned-resource":   {"issue", "attachment", "delete", "201", "--force"},
		"repeat-option":          {"issue", "attachment", "delete", "200", "--force", "--force"},
		"extra-resource":         {"issue", "attachment", "delete", "200", "201", "--force"},
		"opaque-help-flag":       {"issue", "attachment", "delete", "--help", "--force"},
		"unknown-operation":      {"issue", "attachment", "update", "200", "--force"},
	} {
		t.Run(name, func(t *testing.T) {
			declared := []string{"KEY-1"}
			if name == "foreign-key" || name == "deleted-key" {
				declared = []string{args[3]}
			}
			if err := r.checkMutation(declared, args); err == nil {
				t.Fatal("attachment ownership guard accepted an unauthored workflow")
			}
		})
	}
	args := []string{"issue", "attachment", "add", "KEY-1", path}
	if err := r.checkMutation([]string{"KEY-1", "KEY-2"}, args); err == nil {
		t.Fatal("upload accepted multiple declared targets")
	}
	if err := os.WriteFile(path, []byte("changed bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.checkMutation([]string{"KEY-1"}, args); err == nil {
		t.Fatal("upload accepted modified bytes")
	}
	r.manifest.Issues[0].Attachments[0].Deleted = true
	if err := r.checkMutation([]string{"KEY-1"}, []string{"issue", "attachment", "delete", "200", "--force"}); err == nil {
		t.Fatal("attachment deletion accepted an inactive resource")
	}
	r.verified = false
	if err := os.WriteFile(path, []byte("registered bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.checkMutation([]string{"KEY-1"}, args); err == nil {
		t.Fatal("attachment upload accepted unverified credentials")
	}
	t.Run("input-symlink", func(t *testing.T) {
		r.verified = true
		target := filepath.Join(t.TempDir(), "same-bytes.bin")
		if err := os.WriteFile(target, []byte("registered bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if err := r.checkMutation([]string{"KEY-1"}, args); err == nil {
			t.Fatal("attachment upload followed a substituted link with matching bytes")
		}
	})
}
