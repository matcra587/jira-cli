package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGeneratedFileIsFresh mirrors the ADF generator's fence: regenerating
// from the vendored spec must reproduce the committed wire_gen.go byte for
// byte, so generator-logic drift and hand edits both fail here instead of
// shipping silently. (The spec drift test catches property-set drift, but
// not a changed field type or template edit — that is this test's job.)
func TestGeneratedFileIsFresh(t *testing.T) {
	jiraDir := filepath.Join("..", "..", "internal", "jira")
	out := filepath.Join(t.TempDir(), "wire_gen.go")

	if err := run(filepath.Join(jiraDir, "spec", "platform.json"), out); err != nil {
		t.Fatalf("regenerate: %v", err)
	}

	fresh, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read regenerated output: %v", err)
	}
	committed, err := os.ReadFile(filepath.Join(jiraDir, "wire_gen.go"))
	if err != nil {
		t.Fatalf("read committed wire_gen.go: %v", err)
	}
	if string(fresh) != string(committed) {
		t.Error("wire_gen.go is stale or hand-edited — run `go generate ./internal/jira`")
	}
}

func TestFieldName(t *testing.T) {
	tests := []struct {
		prop, want string
	}{
		{"summary", "Summary"},
		{"issuetype", "Issuetype"},
		{"accountId", "AccountID"},
		{"avatarUrls", "AvatarURLs"},
		{"self", "Self"},
		{"16x16", "Field16x16"},
		{"statusCategory", "StatusCategory"},
	}
	for _, tt := range tests {
		t.Run(tt.prop, func(t *testing.T) {
			if got := fieldName(tt.prop); got != tt.want {
				t.Errorf("fieldName(%q) = %q, want %q", tt.prop, got, tt.want)
			}
		})
	}
}

func TestRefName(t *testing.T) {
	node := &schemaNode{Ref: "#/components/schemas/StatusDetails"}
	if got := refName(node); got != "StatusDetails" {
		t.Errorf("refName = %q", got)
	}
}
