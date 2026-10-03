//go:build live

package live

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

func TestLiveLinkMutationOwnershipGrammar(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "payload.json")
	data := []byte(`{"comment":{"body":{"type":"doc","version":1,"content":[]}}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	r := &liveRun{verified: true, manifest: liveManifest{Issues: []ownedIssue{{Key: "KEY-1", ID: "1"}, {Key: "KEY-2", ID: "2"}, {Key: "KEY-3", ID: "3", Deleted: true}}, Links: []ownedLink{{ID: "10042", Source: "KEY-1", Target: "KEY-2"}, {ID: "10043", Source: "KEY-1", Target: "KEY-2", Deleted: true}, {ID: "10044", Source: "KEY-1", Target: "KEY-3"}}}, linkTypes: map[string]storedLinkType{"blocks": {ID: "1", Name: "Blocks", Outward: "blocks", Inward: "is blocked by"}}, linkInputs: map[string]nativeLinkInput{path: {sources: []string{"KEY-1"}, target: "KEY-2", phrase: "blocks", digest: sha256.Sum256(data)}}}
	for _, args := range [][]string{
		{"issue", "link", "create", "KEY-1", "--to", "KEY-2", "--type", "blocks"},
		{"issue", "link", "create", "KEY-1", "--to=KEY-2", "--type=blocks", "--json-input=" + path, "--parallelism=2", "--refresh", "--dry-run", "--validate-remote"},
		{"issue", "link", "delete", "10042"},
		{"issue", "link", "delete", "10042", "--dry-run"},
	} {
		if err := r.checkMutation([]string{"KEY-1", "KEY-2"}, args); err != nil {
			t.Fatalf("owned authored link workflow refused: %v", err)
		}
	}
	for name, test := range map[string]struct{ keys, args []string }{
		"foreign-source":         {[]string{"KEY-4", "KEY-2"}, []string{"issue", "link", "create", "KEY-4", "--to=KEY-2", "--type=blocks"}},
		"inactive-source":        {[]string{"KEY-3", "KEY-2"}, []string{"issue", "link", "create", "KEY-3", "--to=KEY-2", "--type=blocks"}},
		"foreign-target":         {[]string{"KEY-1", "KEY-4"}, []string{"issue", "link", "create", "KEY-1", "--to=KEY-4", "--type=blocks"}},
		"inactive-target":        {[]string{"KEY-1", "KEY-3"}, []string{"issue", "link", "create", "KEY-1", "--to=KEY-3", "--type=blocks"}},
		"undeclared-source":      {[]string{"KEY-2", "KEY-1"}, []string{"issue", "link", "create", "KEY-1", "--to=KEY-2", "--type=blocks"}},
		"duplicate-source":       {[]string{"KEY-1", "KEY-1", "KEY-2"}, []string{"issue", "link", "create", "KEY-1", "KEY-1", "--to=KEY-2", "--type=blocks"}},
		"self-link":              {[]string{"KEY-1", "KEY-1"}, []string{"issue", "link", "create", "KEY-1", "--to=KEY-1", "--type=blocks"}},
		"foreign-type":           {[]string{"KEY-1", "KEY-2"}, []string{"issue", "link", "create", "KEY-1", "--to=KEY-2", "--type=foreign"}},
		"repeated-target":        {[]string{"KEY-1", "KEY-2"}, []string{"issue", "link", "create", "KEY-1", "--to=KEY-2", "--to=KEY-2", "--type=blocks"}},
		"repeated-preview":       {[]string{"KEY-1", "KEY-2"}, []string{"issue", "link", "create", "KEY-1", "--to=KEY-2", "--type=blocks", "--dry-run", "--dry-run"}},
		"changed-boolean":        {[]string{"KEY-1", "KEY-2"}, []string{"issue", "link", "create", "KEY-1", "--to=KEY-2", "--type=blocks", "--dry-run=false"}},
		"selector":               {[]string{"KEY-1", "KEY-2"}, []string{"issue", "link", "create", "KEY-1", "--to=KEY-2", "--type=blocks", "--jql=project=KEY"}},
		"missing-value":          {[]string{"KEY-1", "KEY-2"}, []string{"issue", "link", "create", "KEY-1", "--to=KEY-2", "--type"}},
		"unknown-option":         {[]string{"KEY-1", "KEY-2"}, []string{"issue", "link", "create", "KEY-1", "--to=KEY-2", "--type=blocks", "--force"}},
		"foreign-payload":        {[]string{"KEY-1", "KEY-2"}, []string{"issue", "link", "create", "KEY-1", "--to=KEY-2", "--type=blocks", "--json-input=other.json"}},
		"different-binding":      {[]string{"KEY-2", "KEY-1"}, []string{"issue", "link", "create", "KEY-2", "--to=KEY-1", "--type=blocks", "--json-input", path}},
		"unregistered-resource":  {[]string{"KEY-1", "KEY-2"}, []string{"issue", "link", "delete", "90000"}},
		"retired-resource":       {[]string{"KEY-1", "KEY-2"}, []string{"issue", "link", "delete", "10043"}},
		"wrong-resource-ends":    {[]string{"KEY-2", "KEY-1"}, []string{"issue", "link", "delete", "10042"}},
		"inactive-resource-end":  {[]string{"KEY-1", "KEY-3"}, []string{"issue", "link", "delete", "10044"}},
		"extra-resource":         {[]string{"KEY-1", "KEY-2"}, []string{"issue", "link", "delete", "10042", "10043"}},
		"delete-force":           {[]string{"KEY-1", "KEY-2"}, []string{"issue", "link", "delete", "10042", "--force"}},
		"delete-payload":         {[]string{"KEY-1", "KEY-2"}, []string{"issue", "link", "delete", "10042", "--json-input", path}},
		"delete-changed-preview": {[]string{"KEY-1", "KEY-2"}, []string{"issue", "link", "delete", "10042", "--dry-run=false"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := r.checkMutation(test.keys, test.args); err == nil {
				t.Fatal("link ownership guard admitted an unauthored workflow")
			}
		})
	}
	if err := os.WriteFile(path, []byte(`{"changed":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.checkMutation([]string{"KEY-1", "KEY-2"}, []string{"issue", "link", "create", "KEY-1", "--to=KEY-2", "--type=blocks", "--json-input", path}); err == nil {
		t.Fatal("link ownership guard admitted changed registered bytes")
	}
	r.verified = false
	if err := r.checkMutation([]string{"KEY-1", "KEY-2"}, []string{"issue", "link", "delete", "10042"}); err == nil {
		t.Fatal("link ownership guard admitted unverified credentials")
	}
}
