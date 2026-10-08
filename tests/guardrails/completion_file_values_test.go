package guardrails

import (
	"slices"
	"testing"

	"github.com/gechr/clib/complete"
)

func TestInputFileFlagsDeclareNativeCompletionHints(t *testing.T) {
	fileFlags := []string{"config", "input", "json-input", "markdown-file", "comment-file", "from-file", "current"}
	checked := 0
	check := func(path string, specs []complete.Spec) {
		for _, spec := range specs {
			if spec.Hidden || !slices.Contains(fileFlags, spec.LongFlag) {
				continue
			}
			checked++
			if !spec.HasArg || spec.CommaList || spec.ValueHint != complete.HintFile {
				t.Errorf("%s --%s must complete a literal input file, got %#v", path, spec.LongFlag, spec)
			}
		}
	}
	gen := guardProgram(t).Generator()
	check(gen.AppName, gen.Specs)
	var walk func(string, []complete.SubSpec)
	walk = func(path string, subs []complete.SubSpec) {
		for _, sub := range subs {
			child := path + " " + sub.Name
			check(child, sub.Specs)
			walk(child, sub.Subs)
		}
	}
	walk(gen.AppName, gen.Subs)
	if checked == 0 {
		t.Fatal("the command inventory exposed no input file flags")
	}
}
