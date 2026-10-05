package guardrails

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	clibcobra "github.com/gechr/clib/cli/cobra"
	"github.com/gechr/clib/complete"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli/root"
)

// A flag can declare `Complete: "predictor=X"` (or a positional can
// declare `dynamic-args='X'`) with no handler behind it: the generated
// script calls back `jira --@complete=X`, the handler finds no kind, and
// TAB silently offers nothing — no error anywhere. The join below closes
// that gap mechanically: everything the completion generator publishes
// must be answerable by the dispatch map in internal/cli/root.

// TestDeclaredPredictorsAreHandled joins the generator the shells are
// actually built from — not a re-parse of the source — against the
// handler's kind set. It fails by name: the kind, and every command path
// and flag that declares it.
func TestDeclaredPredictorsAreHandled(t *testing.T) {
	gen := guardProgram(t).Generator()
	handled := make(map[string]bool)
	for _, kind := range root.CompletionKinds() {
		handled[kind] = true
	}
	declared := declaredPredictors(gen)
	for _, kind := range slices.Sorted(maps.Keys(declared)) {
		if handled[kind] {
			continue
		}
		t.Errorf("predictor %q has no entry in root's completionKinds — TAB is silently dead at: %s",
			kind, strings.Join(declared[kind], ", "))
	}
}

func TestIssueKeyHistoryCompletesRankTargetsAndAnchors(t *testing.T) {
	sites := declaredPredictors(guardProgram(t).Generator())["issuekey"]
	for _, site := range []string{"jira issue rank <arg 1>", "jira issue rank --before", "jira issue rank --after"} {
		if !slices.Contains(sites, site) {
			t.Errorf("recent issue history is not offered at %s", site)
		}
	}
}

// TestPredictorWalkSeesBothDeclarationChannels is the fixture that keeps
// the guard above honest while the real tree declares zero predictors: a
// clib bump that moved either declaration channel would otherwise leave
// the join vacuously green. The fixture goes through clib's own
// extraction (FlagMeta + Subcommands, the same calls conductor's
// Generator makes), so it also pins that `predictor=` and `dynamic-args`
// still parse the way the guard assumes.
func TestPredictorWalkSeesBothDeclarationChannels(t *testing.T) {
	child := &cobra.Command{
		Use:         "child",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"clib": "dynamic-args='spirit'"},
	}
	child.Flags().String("haunt", "", "fixture flag")
	clibcobra.Extend(child.Flags().Lookup("haunt"), clibcobra.FlagExtra{
		Complete: "predictor=ghost",
	})
	fixture := &cobra.Command{Use: "fixture"}
	fixture.AddCommand(child)

	gen := complete.NewGenerator("fixture").FromFlags(clibcobra.FlagMeta(fixture))
	gen.Subs = clibcobra.Subcommands(fixture)
	declared := declaredPredictors(gen)

	tests := []struct {
		name     string
		kind     string
		wantSite string
	}{
		{name: "flag predictor", kind: "ghost", wantSite: "fixture child --haunt"},
		{name: "positional dynamic-args", kind: "spirit", wantSite: "fixture child <arg 1>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sites := declared[tt.kind]
			if len(sites) == 0 {
				t.Fatalf("walk did not extract predictor %q — the main guard is scanning for nothing", tt.kind)
			}
			if !slices.Contains(sites, tt.wantSite) {
				t.Errorf("predictor %q attributed to %v, want %q", tt.kind, sites, tt.wantSite)
			}
		})
	}
}

// declaredPredictors flattens every dynamic completion the generator
// publishes — flag predictors (Spec.Dynamic) and positional dynamic-args
// — into kind → declaration sites. Walking the generator rather than the
// cobra tree means the guard covers exactly what the shell scripts will
// call back with; a predictor on a surface the generator drops (hidden
// command, grouper's flags) is by the same token never called.
func declaredPredictors(gen *complete.Generator) map[string][]string {
	sites := make(map[string][]string)
	add := func(kind, site string) {
		sites[kind] = append(sites[kind], site)
	}
	collectSpecs := func(path string, specs []complete.Spec) {
		for _, spec := range specs {
			if spec.Dynamic == "" {
				continue
			}
			flag := "--" + spec.LongFlag
			if spec.LongFlag == "" {
				flag = "-" + spec.ShortFlag
			}
			add(spec.Dynamic, path+" "+flag)
		}
	}
	collectArgs := func(path string, kinds []string) {
		for i, kind := range kinds {
			if kind == "" {
				continue
			}
			add(kind, fmt.Sprintf("%s <arg %d>", path, i+1))
		}
	}
	var walk func(path string, subs []complete.SubSpec)
	walk = func(path string, subs []complete.SubSpec) {
		for _, sub := range subs {
			subPath := path + " " + sub.Name
			collectSpecs(subPath, sub.Specs)
			collectArgs(subPath, sub.DynamicArgs)
			walk(subPath, sub.Subs)
		}
	}
	collectSpecs(gen.AppName, gen.Specs)
	collectArgs(gen.AppName, gen.DynamicArgs)
	walk(gen.AppName, gen.Subs)
	return sites
}
