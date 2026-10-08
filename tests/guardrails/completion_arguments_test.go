package guardrails

import (
	"slices"
	"testing"

	"github.com/gechr/clib/complete"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

func TestCompletionCommandDeclaresShellPredictor(t *testing.T) {
	gen := guardProgram(t).Generator()
	for _, sub := range gen.Subs {
		if sub.Name == "completion" {
			if len(sub.DynamicArgs) != 1 || sub.DynamicArgs[0] != "shell" {
				t.Fatalf("completion positional predictors=%v, want [shell]", sub.DynamicArgs)
			}
			return
		}
	}
	t.Fatal("completion command disappeared")
}

func TestCompletionWalkPreservesAuthoredPositionalSlots(t *testing.T) {
	program := guardProgram(t)
	gen := program.Generator()
	var walk func(*cobra.Command, []complete.SubSpec)
	walk = func(cmd *cobra.Command, subs []complete.SubSpec) {
		for _, child := range cmd.Commands() {
			var matched *complete.SubSpec
			for i := range subs {
				if subs[i].Name == child.Name() {
					matched = &subs[i]
					break
				}
			}
			if matched == nil {
				continue
			}
			metas, ok := command.MetaFor(child)
			if ok {
				for _, meta := range metas {
					if meta.Args == nil {
						continue
					}
					has := false
					for _, arg := range meta.Args.Positions {
						has = has || arg.Predictor != ""
					}
					if !has {
						continue
					}
					want := make([]string, len(meta.Args.Positions))
					for i, arg := range meta.Args.Positions {
						want[i] = arg.Predictor
						if want[i] == "" {
							want[i] = command.NoCompletionPredictor
						}
					}
					if !slices.Equal(matched.DynamicArgs, want) {
						t.Errorf("%s positional predictors=%v want=%v", child.CommandPath(), matched.DynamicArgs, want)
					}
				}
			}
			walk(child, matched.Subs)
		}
	}
	walk(program.Root, gen.Subs)
}
