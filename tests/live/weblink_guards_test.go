//go:build live

package live

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLiveNativeWeblinkOwnershipGrammar(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "native.json")
	data := []byte(`{"object":{"url":"https://example.com","title":"Example"}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	r := &liveRun{verified: true, manifest: liveManifest{Issues: []ownedIssue{
		{Key: "KEY-1", ID: "1"}, {Key: "KEY-2", ID: "2"}, {Key: "KEY-3", ID: "3", Deleted: true},
	}}}
	keys := []string{"KEY-1", "KEY-2"}
	for _, binding := range [][]string{keys, {"KEY-4", "KEY-2"}, {"KEY-3", "KEY-2"}, {"KEY-1", "KEY-1"}} {
		r.weblinkInputs = append(r.weblinkInputs, ownedWeblinkInput{keys: slices.Clone(binding), path: path, digest: sha256.Sum256(data)})
	}
	args := nativeWeblinkArgs(keys, path)
	for _, suffix := range [][]string{nil, {"--dry-run"}} {
		if err := r.checkMutation(keys, append(slices.Clone(args), suffix...)); err != nil {
			t.Fatal(err)
		}
	}
	for name, test := range map[string]struct{ keys, args []string }{
		"foreign":            {[]string{"KEY-4", "KEY-2"}, nativeWeblinkArgs([]string{"KEY-4", "KEY-2"}, path)},
		"inactive":           {[]string{"KEY-3", "KEY-2"}, nativeWeblinkArgs([]string{"KEY-3", "KEY-2"}, path)},
		"repeated":           {[]string{"KEY-1", "KEY-1"}, nativeWeblinkArgs([]string{"KEY-1", "KEY-1"}, path)},
		"undeclared":         {[]string{"KEY-2", "KEY-1"}, args},
		"changed-binding":    {[]string{"KEY-1"}, nativeWeblinkArgs([]string{"KEY-1"}, path)},
		"different-file":     {keys, nativeWeblinkArgs(keys, path+".other")},
		"stdin":              {keys, nativeWeblinkArgs(keys, "-")},
		"repeated-input":     {keys, append(slices.Clone(args), "--json-input="+path)},
		"flag-merge":         {keys, append(slices.Clone(args), "--url=https://example.com")},
		"native-turned-flag": {keys, []string{"issue", "weblink", "add", "KEY-1", "KEY-2", "--url="}},
		"selector":           {keys, append(slices.Clone(args), "--jql=project=KEY")},
		"hidden-alias":       {keys, append(slices.Clone(args), "--from-json="+path)},
		"changed-preview":    {keys, append(slices.Clone(args), "--dry-run=false")},
		"changed-workers":    {keys, append(slices.Clone(args), "--parallelism=99")},
	} {
		t.Run(name, func(t *testing.T) {
			if err := r.checkMutation(test.keys, test.args); err == nil {
				t.Fatal("native remote-link owner admitted an unauthored mutation")
			}
		})
	}
	t.Run("changed-bytes", func(t *testing.T) {
		if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := r.checkMutation(keys, args); err == nil {
			t.Fatal("native remote-link owner admitted changed input")
		}
	})
	t.Run("unverified", func(t *testing.T) {
		r.verified = false
		if err := r.checkMutation(keys, args); err == nil {
			t.Fatal("native remote-link owner admitted unverified credentials")
		}
	})
}

func TestLiveWeblinkMutationOwnershipGrammar(t *testing.T) {
	t.Parallel()
	r := &liveRun{verified: true, manifest: liveManifest{Issues: []ownedIssue{
		{Key: "KEY-1", ID: "1", Weblinks: []ownedWeblink{{ID: "10042"}, {ID: "10043", Deleted: true}}},
		{Key: "KEY-2", ID: "2"},
		{Key: "KEY-3", ID: "3", Deleted: true, Weblinks: []ownedWeblink{{ID: "10044"}}},
	}}}
	recipe := weblinkRecipe{URL: "https://example.com/runbook", Title: "Runbook", GlobalID: "fixture-link"}
	keys := []string{"KEY-1", "KEY-2"}
	r.allowWeblink(keys, recipe)
	// Registration binds payloads; it never grants ownership. Give these
	// invalid targets matching payloads so the ownership checks must refuse.
	for _, declared := range [][]string{{"KEY-4", "KEY-2"}, {"KEY-3", "KEY-2"}, {"KEY-1", "KEY-1"}, {"KEY-2", "KEY-1"}} {
		r.allowWeblink(declared, recipe)
	}
	keys[0] = "KEY-4"
	for _, args := range [][]string{
		weblinkArgs([]string{"KEY-1", "KEY-2"}, recipe),
		{"issue", "weblink", "add", "KEY-1", "KEY-2", "--url", recipe.URL, "--title", recipe.Title, "--global-id", recipe.GlobalID, "--dry-run", "--parallelism=1"},
	} {
		if err := r.checkMutation([]string{"KEY-1", "KEY-2"}, args); err != nil {
			t.Fatalf("registered authored remote-link add refused: %v", err)
		}
	}
	for _, args := range [][]string{{"issue", "weblink", "delete", "KEY-1", "10042"}, {"issue", "weblink", "delete", "KEY-1", "10042", "--dry-run"}} {
		if err := r.checkMutation([]string{"KEY-1"}, args); err != nil {
			t.Fatalf("registered owned remote-link delete refused: %v", err)
		}
	}
	for name, test := range map[string]struct{ keys, args []string }{
		"foreign-source":           {[]string{"KEY-4", "KEY-2"}, weblinkArgs([]string{"KEY-4", "KEY-2"}, recipe)},
		"inactive-source":          {[]string{"KEY-3", "KEY-2"}, weblinkArgs([]string{"KEY-3", "KEY-2"}, recipe)},
		"undeclared-source":        {[]string{"KEY-2", "KEY-1"}, weblinkArgs([]string{"KEY-1", "KEY-2"}, recipe)},
		"repeated-source":          {[]string{"KEY-1", "KEY-1"}, weblinkArgs([]string{"KEY-1", "KEY-1"}, recipe)},
		"changed-source-binding":   {[]string{"KEY-1"}, weblinkArgs([]string{"KEY-1"}, recipe)},
		"changed-url":              {[]string{"KEY-1", "KEY-2"}, weblinkArgs([]string{"KEY-1", "KEY-2"}, weblinkRecipe{URL: "https://example.com/other", Title: recipe.Title, GlobalID: recipe.GlobalID})},
		"changed-global-id":        {[]string{"KEY-1", "KEY-2"}, weblinkArgs([]string{"KEY-1", "KEY-2"}, weblinkRecipe{URL: recipe.URL, Title: recipe.Title, GlobalID: "other"})},
		"missing-value":            {[]string{"KEY-1", "KEY-2"}, []string{"issue", "weblink", "add", "KEY-1", "KEY-2", "--url"}},
		"repeated-option":          {[]string{"KEY-1", "KEY-2"}, append(weblinkArgs([]string{"KEY-1", "KEY-2"}, recipe), "--url="+recipe.URL)},
		"changed-preview":          {[]string{"KEY-1", "KEY-2"}, append(weblinkArgs([]string{"KEY-1", "KEY-2"}, recipe), "--dry-run=false")},
		"foreign-payload":          {[]string{"KEY-1", "KEY-2"}, append(weblinkArgs([]string{"KEY-1", "KEY-2"}, recipe), "--json-input=other.json")},
		"selector":                 {[]string{"KEY-1", "KEY-2"}, append(weblinkArgs([]string{"KEY-1", "KEY-2"}, recipe), "--jql=project=KEY")},
		"changed-worker-bound":     {[]string{"KEY-1", "KEY-2"}, []string{"issue", "weblink", "add", "KEY-1", "KEY-2", "--url=" + recipe.URL, "--title=" + recipe.Title, "--global-id=" + recipe.GlobalID, "--parallelism=99"}},
		"foreign-resource":         {[]string{"KEY-1"}, []string{"issue", "weblink", "delete", "KEY-1", "90000"}},
		"different-parent":         {[]string{"KEY-2"}, []string{"issue", "weblink", "delete", "KEY-2", "10042"}},
		"different-declared-key":   {[]string{"KEY-2"}, []string{"issue", "weblink", "delete", "KEY-1", "10042"}},
		"retired-resource":         {[]string{"KEY-1"}, []string{"issue", "weblink", "delete", "KEY-1", "10043"}},
		"inactive-resource-parent": {[]string{"KEY-3"}, []string{"issue", "weblink", "delete", "KEY-3", "10044"}},
		"delete-extra-id":          {[]string{"KEY-1"}, []string{"issue", "weblink", "delete", "KEY-1", "10042", "10043"}},
		"delete-force":             {[]string{"KEY-1"}, []string{"issue", "weblink", "delete", "KEY-1", "10042", "--force"}},
		"delete-changed-preview":   {[]string{"KEY-1"}, []string{"issue", "weblink", "delete", "KEY-1", "10042", "--dry-run=false"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := r.checkMutation(test.keys, test.args); err == nil {
				t.Fatal("remote-link ownership admitted an unauthored or foreign mutation")
			}
		})
	}
	r.verified = false
	if err := r.checkWeblinkMutation([]string{"KEY-1"}, []string{"issue", "weblink", "delete", "KEY-1", "10042"}); err == nil {
		t.Fatal("remote-link resource owner admitted unverified credentials")
	}
	if err := r.checkMutation([]string{"KEY-1"}, []string{"issue", "weblink", "delete", "KEY-1", "10042"}); err == nil {
		t.Fatal("remote-link ownership admitted unverified credentials")
	}
}
