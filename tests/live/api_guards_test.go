//go:build live

package live

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLiveRawMutationRejectsUnownedResourcesAndInputs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "comment.json")
	body := []byte(`{"body":{"type":"doc","version":1,"content":[]}}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	endpoint := "/rest/api/3/issue/KEY-123/comment"
	r := &liveRun{
		verified: true,
		manifest: liveManifest{Issues: []ownedIssue{
			{Key: "KEY-123", ID: "10001", Comments: []ownedComment{{ID: "20001"}}},
			{Key: "KEY-124", ID: "10002"},
			{Key: "KEY-125", ID: "10003", Deleted: true},
		}},
		rawInputs: map[string]rawInput{path: {method: "POST", endpoint: endpoint, digest: sha256.Sum256(body)}},
	}
	post := []string{"api", "post", endpoint, "--input", path, "--force"}
	for _, args := range [][]string{post, slices.Concat(post, []string{"--dry-run"})} {
		if err := r.checkMutation([]string{"KEY-123"}, args); err != nil {
			t.Fatalf("registered exact owned raw write refused: %v", err)
		}
	}
	cases := []struct {
		name string
		keys []string
		args []string
	}{
		{"unowned-issue", []string{"KEY-126"}, []string{"api", "delete", "/rest/api/3/issue/KEY-126/comment/20001", "--force"}},
		{"wrong-declared-issue", []string{"KEY-124"}, post},
		{"extra-declared-unowned-issue", []string{"KEY-123", "KEY-126"}, post},
		{"deleted-issue", []string{"KEY-125"}, []string{"api", "post", "/rest/api/3/issue/KEY-125/comment", "--input", path, "--force"}},
		{"unowned-comment", []string{"KEY-123"}, []string{"api", "delete", endpoint + "/20002", "--force"}},
		{"comment-on-wrong-issue", []string{"KEY-124"}, []string{"api", "delete", "/rest/api/3/issue/KEY-124/comment/20001", "--force"}},
		{"global-comment-id", []string{"KEY-123"}, []string{"api", "delete", "/rest/api/3/comment/20001", "--force"}},
		{"issue-payload", []string{"KEY-123"}, []string{"api", "put", "/rest/api/3/issue/KEY-123", "--input", path, "--force"}},
		{"issue-delete", []string{"KEY-123"}, []string{"api", "delete", "/rest/api/3/issue/KEY-123", "--force"}},
		{"unregistered-input", []string{"KEY-123"}, []string{"api", "post", endpoint, "--input", path + "-other", "--force"}},
		{"stdin-input", []string{"KEY-123"}, []string{"api", "post", endpoint, "--input", "-", "--force"}},
		{"wrong-method-input", []string{"KEY-123"}, []string{"api", "put", endpoint + "/20001", "--input", path, "--force"}},
		{"second-input", []string{"KEY-123"}, slices.Concat(post, []string{"--input", path})},
		{"query", []string{"KEY-123"}, slices.Concat(post, []string{"--query", "expand=all"})},
		{"content-type", []string{"KEY-123"}, slices.Concat(post, []string{"--content-type=text/plain"})},
		{"force-missing", []string{"KEY-123"}, post[:len(post)-1]},
		{"force-disabled", []string{"KEY-123"}, slices.Concat(post[:len(post)-1], []string{"--force=false"})},
		{"encoded-path", []string{"KEY-123"}, []string{"api", "delete", strings.ReplaceAll(endpoint, "KEY-123", "%4bEY-123") + "/20001", "--force"}},
		{"embedded-query", []string{"KEY-123"}, []string{"api", "delete", endpoint + "/20001?cascade=true", "--force"}},
		{"absolute-url", []string{"KEY-123"}, []string{"api", "delete", "https://example.test" + endpoint + "/20001", "--force"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := r.checkMutation(tc.keys, tc.args); err == nil {
				t.Fatal("unsafe raw live mutation was accepted")
			}
		})
	}
	comment := endpoint + "/20001"
	r.rawInputs[path] = rawInput{method: "PUT", endpoint: comment, digest: sha256.Sum256(body)}
	put := []string{"api", "PUT", comment, "--input", path, "--force"}
	if err := r.checkMutation([]string{"KEY-123"}, put); err != nil {
		t.Fatalf("owned comment update refused: %v", err)
	}
	deleteArgs := []string{"api", "DELETE", comment, "--force"}
	if err := r.checkMutation([]string{"KEY-123"}, deleteArgs); err != nil {
		t.Fatalf("owned comment deletion refused: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"fields":{"summary":"changed after registration"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Run("changed-input", func(t *testing.T) {
		if err := r.checkMutation([]string{"KEY-123"}, put); err == nil {
			t.Fatal("changed raw input bypassed the authored-byte guard")
		}
	})
	r.manifest.Issues[0].Comments[0].Deleted = true
	t.Run("deleted-comment", func(t *testing.T) {
		if err := r.checkMutation([]string{"KEY-123"}, deleteArgs); err == nil {
			t.Fatal("already deleted raw comment was accepted")
		}
	})
	r.manifest.Issues[0].Comments[0].Deleted = false
	r.verified = false
	t.Run("unverified", func(t *testing.T) {
		if err := r.checkMutation([]string{"KEY-123"}, deleteArgs); err == nil {
			t.Fatal("unverified raw live write was accepted")
		}
	})
}

func TestLiveConditionalRawMutationBindsOwnedDescriptionAndHeader(t *testing.T) {
	r := &liveRun{verified: true, dir: t.TempDir(), manifest: liveManifest{Issues: []ownedIssue{{Key: "KEY-123", ID: "10001"}}}}
	document := []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"probe"}]}]}`)
	endpoint, path := r.rawDescriptionInput(t, "KEY-123", document)
	r.bindRawPrecondition(t, path, "--if-match", `"stale"`)
	args := []string{"api", "put", endpoint, "--input", path, "--if-match", `"stale"`, "--force"}
	if err := r.checkMutation([]string{"KEY-123"}, args); err != nil {
		t.Fatalf("registered owned description and header refused: %v", err)
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"missing-condition", []string{"api", "put", endpoint, "--input", path, "--force"}},
		{"changed-value", []string{"api", "put", endpoint, "--input", path, "--if-match", `"other"`, "--force"}},
		{"changed-flag", []string{"api", "put", endpoint, "--input", path, "--if-unmodified-since", "Thu, 01 Jan 1970 00:00:00 GMT", "--force"}},
		{"arbitrary-header", []string{"api", "put", endpoint, "--input", path, "--header", "X-Extra=value", "--force"}},
		{"other-route", []string{"api", "put", "/rest/api/3/issue/KEY-124", "--input", path, "--if-match", `"stale"`, "--force"}},
		{"additional-condition", slices.Concat(args, []string{"--if-match", `"other"`})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := r.checkMutation([]string{"KEY-123"}, tc.args); err == nil {
				t.Fatal("unregistered conditional live write was accepted")
			}
		})
	}
	registered := r.rawInputs[path]
	registered.issueDescription = false
	r.rawInputs[path] = registered
	if err := r.checkMutation([]string{"KEY-123"}, args); err == nil {
		t.Fatal("comment input became an arbitrary issue write")
	}
	registered.issueDescription = true
	r.rawInputs[path] = registered
	if err := os.WriteFile(path, []byte(`{"fields":{"summary":"changed after registration"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.checkMutation([]string{"KEY-123"}, args); err == nil {
		t.Fatal("conditional input bypassed the authored-byte guard")
	}
}
