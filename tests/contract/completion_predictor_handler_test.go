package contract

import (
	"strings"
	"testing"

	clib "github.com/gechr/clib/cli/cobra"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli/completion"
	"github.com/matcra587/jira-cli/internal/cli/root"
	"github.com/matcra587/jira-cli/internal/cli/runtime"
)

// TestDeclaredPredictorsAreHandled fails when any command declares a completion
// predictor that CompletionHandler does not implement. Such a predictor
// compiles and ships, but tab-completion silently returns nothing — the exact
// gap that left `--fields` (predictor=cachefield) dead. It walks the real
// command tree and checks both flag predictors (`Complete: "predictor=X"`) and
// positional predictors (`dynamic-args='X'`) against completion.HandledPredictors,
// which is sourced from the dispatch map itself, so a handler that loses an
// entry is caught too.
func TestDeclaredPredictorsAreHandled(t *testing.T) {
	rt, err := runtime.New()
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}
	rootCmd := root.New(rt)

	declared := make(map[string]string) // predictor -> where it was declared
	walkCommandTree(rootCmd, declared)
	if len(declared) == 0 {
		t.Fatal("no predictors extracted from the command tree; the walk is broken")
	}

	handled := make(map[string]struct{}, len(completion.HandledPredictors))
	for _, p := range completion.HandledPredictors {
		handled[p] = struct{}{}
	}

	for name, where := range declared {
		if _, ok := handled[name]; !ok {
			t.Errorf("predictor %q (declared at %s) is not implemented by CompletionHandler; "+
				"its tab-completion returns nothing — add it to completion.completionEmitters",
				name, where)
		}
	}
}

func walkCommandTree(cmd *cobra.Command, into map[string]string) {
	for _, meta := range clib.FlagMeta(cmd) {
		if name, ok := predictorFromDirective(meta.Complete); ok {
			into[name] = cmd.CommandPath() + " --" + meta.Name
		}
	}
	for _, name := range dynamicArgsPredictors(cmd.Annotations["clib"]) {
		into[name] = cmd.CommandPath() + " <positional>"
	}
	for _, sub := range cmd.Commands() {
		walkCommandTree(sub, into)
	}
}

// predictorFromDirective returns the predictor a completion directive names, if
// any. The directive is a comma list of tokens (e.g. "predictor=cachefield,comma");
// only the predictor= token identifies a handler the CLI must implement.
func predictorFromDirective(directive string) (string, bool) {
	for part := range strings.SplitSeq(directive, ",") {
		if rest, ok := strings.CutPrefix(part, "predictor="); ok {
			return rest, true
		}
	}
	return "", false
}

// dynamicArgsPredictors extracts the predictor names from a command's clib
// annotation, e.g. "dynamic-args='configkey,configvalue'" -> [configkey configvalue].
func dynamicArgsPredictors(annotation string) []string {
	const key = "dynamic-args='"
	_, after, ok := strings.Cut(annotation, key)
	if !ok {
		return nil
	}
	rest := after
	before0, _, ok0 := strings.Cut(rest, "'")
	if !ok0 {
		return nil
	}
	return strings.Split(before0, ",")
}
