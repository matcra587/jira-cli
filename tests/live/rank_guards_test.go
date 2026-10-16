//go:build live

package live

import (
	"fmt"
	"slices"
	"testing"
)

func rankGuardRun() *liveRun {
	r := &liveRun{verified: true, manifest: liveManifest{Issues: []ownedIssue{
		{Key: "KEY-1", ID: "1"},
		{Key: "KEY-2", ID: "2"},
		{Key: "KEY-3", ID: "3", Deleted: true},
		{Key: "KEY-8", ID: "8"},
		{Key: "KEY-9", ID: "9"},
	}}}
	r.allowRank([]string{"KEY-1", "KEY-2"}, "before", "KEY-9")
	return r
}

func TestLiveRankMutationOwnershipGrammar(t *testing.T) {
	t.Parallel()
	keys := []string{"KEY-1", "KEY-2", "KEY-9"}
	for _, args := range [][]string{
		{"issue", "rank", "KEY-1", "KEY-2", "--before=KEY-9"},
		{"issue", "rank", "KEY-1", "KEY-2", "--before", "KEY-9", "--dry-run"},
	} {
		if err := rankGuardRun().checkMutation(keys, args); err != nil {
			t.Fatalf("owned registered ranking refused: %v", err)
		}
	}
	for name, test := range map[string]struct {
		keys, args []string
		register   bool
	}{
		"foreign-source":     {[]string{"KEY-4", "KEY-2", "KEY-9"}, rankArgs([]string{"KEY-4", "KEY-2"}, "before", "KEY-9"), true},
		"inactive-source":    {[]string{"KEY-3", "KEY-2", "KEY-9"}, rankArgs([]string{"KEY-3", "KEY-2"}, "before", "KEY-9"), true},
		"repeated-source":    {[]string{"KEY-1", "KEY-1", "KEY-9"}, rankArgs([]string{"KEY-1", "KEY-1"}, "before", "KEY-9"), true},
		"numeric-source":     {[]string{"1", "KEY-2", "KEY-9"}, rankArgs([]string{"1", "KEY-2"}, "before", "KEY-9"), true},
		"foreign-anchor":     {[]string{"KEY-1", "KEY-2", "KEY-4"}, rankArgs([]string{"KEY-1", "KEY-2"}, "before", "KEY-4"), true},
		"inactive-anchor":    {[]string{"KEY-1", "KEY-2", "KEY-3"}, rankArgs([]string{"KEY-1", "KEY-2"}, "before", "KEY-3"), true},
		"source-anchor":      {[]string{"KEY-1", "KEY-2", "KEY-1"}, rankArgs([]string{"KEY-1", "KEY-2"}, "before", "KEY-1"), true},
		"changed-order":      {[]string{"KEY-2", "KEY-1", "KEY-9"}, rankArgs([]string{"KEY-2", "KEY-1"}, "before", "KEY-9"), false},
		"changed-direction":  {keys, rankArgs([]string{"KEY-1", "KEY-2"}, "after", "KEY-9"), false},
		"changed-anchor":     {[]string{"KEY-1", "KEY-2", "KEY-8"}, rankArgs([]string{"KEY-1", "KEY-2"}, "before", "KEY-8"), false},
		"declaration-order":  {[]string{"KEY-2", "KEY-1", "KEY-9"}, rankArgs([]string{"KEY-1", "KEY-2"}, "before", "KEY-9"), false},
		"undeclared-anchor":  {[]string{"KEY-1", "KEY-2"}, rankArgs([]string{"KEY-1", "KEY-2"}, "before", "KEY-9"), false},
		"extra-declaration":  {[]string{"KEY-1", "KEY-2", "KEY-9", "KEY-9"}, rankArgs([]string{"KEY-1", "KEY-2"}, "before", "KEY-9"), false},
		"missing-source":     {[]string{"KEY-9"}, []string{"issue", "rank", "--before=KEY-9"}, false},
		"missing-anchor":     {keys, []string{"issue", "rank", "KEY-1", "KEY-2"}, false},
		"missing-value":      {keys, []string{"issue", "rank", "KEY-1", "KEY-2", "--before"}, false},
		"repeated-anchor":    {keys, []string{"issue", "rank", "KEY-1", "KEY-2", "--before=KEY-9", "--before=KEY-9"}, false},
		"both-directions":    {keys, []string{"issue", "rank", "KEY-1", "KEY-2", "--before=KEY-9", "--after=KEY-9"}, false},
		"repeated-preview":   {keys, []string{"issue", "rank", "KEY-1", "KEY-2", "--before=KEY-9", "--dry-run", "--dry-run"}, false},
		"changed-preview":    {keys, []string{"issue", "rank", "KEY-1", "KEY-2", "--before=KEY-9", "--dry-run=false"}, false},
		"force":              {keys, []string{"issue", "rank", "KEY-1", "KEY-2", "--before=KEY-9", "--force"}, false},
		"parallelism":        {keys, []string{"issue", "rank", "KEY-1", "KEY-2", "--before=KEY-9", "--parallelism=2"}, false},
		"selector":           {keys, []string{"issue", "rank", "KEY-1", "KEY-2", "--before=KEY-9", "--jql=project=KEY"}, false},
		"native-input":       {keys, []string{"issue", "rank", "KEY-1", "KEY-2", "--before=KEY-9", "--json-input=body.json"}, false},
		"positional-trailer": {keys, []string{"issue", "rank", "KEY-1", "KEY-2", "--before=KEY-9", "KEY-9"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			r := rankGuardRun()
			if test.register {
				// Register the invalid recipe too so the ownership check itself
				// must refuse it, rather than relying on a recipe mismatch.
				r.allowRank(test.keys[:len(test.keys)-1], "before", test.keys[len(test.keys)-1])
			}
			if err := r.checkMutation(test.keys, test.args); err == nil {
				t.Fatal("ranking guard admitted an unowned or unauthored move")
			}
		})
	}
	t.Run("registration-clones-order", func(t *testing.T) {
		r := rankGuardRun()
		r.rankInputs = nil
		sources := []string{"KEY-1", "KEY-2"}
		r.allowRank(sources, "before", "KEY-9")
		slices.Reverse(sources)
		if err := r.checkMutation(keys, rankArgs([]string{"KEY-1", "KEY-2"}, "before", "KEY-9")); err != nil {
			t.Fatal("caller mutation changed the registered ranking recipe")
		}
		if err := r.checkMutation([]string{"KEY-2", "KEY-1", "KEY-9"}, rankArgs(sources, "before", "KEY-9")); err == nil {
			t.Fatal("caller mutation replaced the registered ranking order")
		}
	})
	t.Run("unverified-direct-leaf", func(t *testing.T) {
		r := rankGuardRun()
		r.verified = false
		if err := r.checkRankMutation(keys, rankArgs([]string{"KEY-1", "KEY-2"}, "before", "KEY-9")); err == nil {
			t.Fatal("direct ranking guard admitted unverified credentials")
		}
	})
	t.Run("batch-ceiling", func(t *testing.T) {
		r := &liveRun{verified: true, manifest: liveManifest{Issues: []ownedIssue{{Key: "KEY-999", ID: "999"}}}}
		sources := make([]string, 102)
		for i := range sources {
			sources[i] = fmt.Sprintf("KEY-%d", i+1)
			r.manifest.Issues = append(r.manifest.Issues, ownedIssue{Key: sources[i], ID: fmt.Sprint(i + 1)})
		}
		for _, n := range []int{1, 50, 51, 100, 101, 102} {
			r.allowRank(sources[:n], "before", "KEY-999")
			declared := append(slices.Clone(sources[:n]), "KEY-999")
			err := r.checkMutation(declared, rankArgs(sources[:n], "before", "KEY-999"))
			if (n <= 101) != (err == nil) {
				t.Fatalf("owned batch ceiling changed: size=%d error=%v", n, err)
			}
		}
	})
	t.Run("batch-tail-ownership", func(t *testing.T) {
		for _, name := range []string{"foreign", "inactive", "duplicate"} {
			t.Run(name, func(t *testing.T) {
				r := &liveRun{verified: true, manifest: liveManifest{Issues: []ownedIssue{{Key: "KEY-999", ID: "999"}}}}
				sources := make([]string, 101)
				for i := range sources {
					sources[i] = fmt.Sprintf("KEY-%d", i+1)
					r.manifest.Issues = append(r.manifest.Issues, ownedIssue{Key: sources[i], ID: fmt.Sprint(i + 1)})
				}
				switch name {
				case "foreign":
					sources[50] = "KEY-888"
				case "inactive":
					r.manifest.Issues[51].Deleted = true
				case "duplicate":
					sources[50] = sources[0]
				}
				r.allowRank(sources, "before", "KEY-999")
				declared := append(slices.Clone(sources), "KEY-999")
				if err := r.checkRankMutation(declared, rankArgs(sources, "before", "KEY-999")); err == nil {
					t.Fatal("rank ownership validation stopped at the first native batch")
				}
			})
		}
	})
}
