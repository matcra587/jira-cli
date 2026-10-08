package command

import (
	"slices"

	"github.com/gechr/clib/complete"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const completionForwardAnnotation = "jira.completion-forward"

func markCompletionForward(flag *pflag.Flag) {
	if flag.Annotations == nil {
		flag.Annotations = make(map[string][]string)
	}
	flag.Annotations[completionForwardAnnotation] = []string{"true"}
}

func completionForward(flag *pflag.Flag) bool {
	return flag != nil && slices.Contains(flag.Annotations[completionForwardAnnotation], "true")
}

// ForwardCompletion projects declarations onto clib's native flag forwarding.
// Pinned clib 0.7.17 supports Spec.Forward in all six shell generators, but its
// Cobra FlagExtra omits that field. Conductor's WithGenerator supplies this
// missing projection after the actual command tree has been extracted. Hidden
// aliases remain hidden while forwarding the same context as their canonical.
func ForwardCompletion(root *cobra.Command, gen *complete.Generator) {
	project := func(cmd *cobra.Command, specs []complete.Spec) []complete.Spec {
		seen := make(map[string]bool, len(specs))
		for i := range specs {
			flag := cmd.Flags().Lookup(specs[i].LongFlag)
			if flag == nil {
				flag = cmd.PersistentFlags().Lookup(specs[i].LongFlag)
			}
			specs[i].Forward = specs[i].Forward || completionForward(flag)
			seen[specs[i].LongFlag] = true
		}
		cmd.Flags().VisitAll(func(flag *pflag.Flag) {
			if flag.Hidden && completionForward(flag) && !seen[flag.Name] {
				specs = append(specs, complete.Spec{LongFlag: flag.Name, HasArg: true, Hidden: true, Forward: true})
			}
		})
		return specs
	}
	var walk func(*cobra.Command, []complete.SubSpec)
	walk = func(cmd *cobra.Command, subs []complete.SubSpec) {
		for i := range subs {
			for _, child := range cmd.Commands() {
				if child.Name() == subs[i].Name || slices.Contains(child.Aliases, subs[i].Name) {
					subs[i].Specs = project(child, subs[i].Specs)
					walk(child, subs[i].Subs)
					break
				}
			}
		}
	}
	gen.Specs = project(root, gen.Specs)
	walk(root, gen.Subs)
}
