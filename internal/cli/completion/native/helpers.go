package native

import (
	"slices"
)

func persistentSpecs(specs []Spec) []Spec {
	var result []Spec
	for _, spec := range specs {
		if spec.Persistent {
			result = append(result, spec)
		}
	}
	return result
}

func combineVisibleSpecs(specSets ...[]Spec) []Spec {
	total := 0
	for _, specs := range specSets {
		total += len(specs)
	}

	combined := make([]Spec, 0, total)
	for _, specs := range specSets {
		combined = append(combined, specs...)
	}
	return SortVisibleSpecs(combined)
}

func appendSpecs(specs ...[]Spec) []Spec {
	total := 0
	for _, group := range specs {
		total += len(group)
	}
	result := make([]Spec, 0, total)
	for _, group := range specs {
		result = append(result, group...)
	}
	return result
}

// forwardSpec describes a flag whose value should be forwarded to dynamic
// completion handlers as --LongFlag=<value> in the positional args.
type forwardSpec struct {
	LongFlag  string
	ShortFlag string
}

// forwardableSpecs returns the Forward-marked specs that have a value (HasArg).
func forwardableSpecs(specs []Spec) []forwardSpec {
	var result []forwardSpec
	for _, spec := range specs {
		if spec.Forward && spec.HasArg && spec.LongFlag != "" {
			result = append(result, forwardSpec{
				LongFlag:  spec.LongFlag,
				ShortFlag: spec.ShortFlag,
			})
		}
	}
	return result
}

// allForwardableSpecs returns the forwardable specs across the entire generator
// tree, deduplicated by long flag. Forwardable context flags are usually
// persistent/global, so a single command-line scan can collect them regardless
// of which subcommand the cursor sits in.
//
// A short flag that maps to more than one long flag across scopes (e.g. two
// sibling subcommands both using -p, one for --profile and one for --project)
// is ambiguous: a single global helper can't tell which subcommand's flag the
// short refers to, so forwarding it could misattribute the value. In that case
// the short alias is dropped from every entry; the long forms still forward
// unambiguously.
func allForwardableSpecs(g *Generator) []forwardSpec {
	seen := map[string]bool{}
	shortLongs := map[string]map[string]bool{}
	var result []forwardSpec
	var walk func(specs []Spec, subs []SubSpec)
	walk = func(specs []Spec, subs []SubSpec) {
		for _, fs := range forwardableSpecs(specs) {
			if fs.ShortFlag != "" {
				if shortLongs[fs.ShortFlag] == nil {
					shortLongs[fs.ShortFlag] = map[string]bool{}
				}
				shortLongs[fs.ShortFlag][fs.LongFlag] = true
			}
			if seen[fs.LongFlag] {
				continue
			}
			seen[fs.LongFlag] = true
			result = append(result, fs)
		}
		for _, sub := range subs {
			walk(sub.Specs, sub.Subs)
		}
	}
	walk(g.Specs, g.Subs)

	for i := range result {
		if s := result[i].ShortFlag; s != "" && len(shortLongs[s]) > 1 {
			result[i].ShortFlag = ""
		}
	}
	return result
}

// anySpecInTree reports whether pred matches any flag spec, or subPred any
// subcommand, anywhere in the (specs, subs) tree. Either predicate may be nil.
func anySpecInTree(
	specs []Spec, subs []SubSpec,
	pred func(Spec) bool, subPred func(SubSpec) bool,
) bool {
	if pred != nil && slices.ContainsFunc(specs, pred) {
		return true
	}
	for _, sub := range subs {
		if subPred != nil && subPred(sub) {
			return true
		}
		if anySpecInTree(sub.Specs, sub.Subs, pred, subPred) {
			return true
		}
	}
	return false
}

// hasValueFlag reports whether any flag in the tree takes a value, i.e.
// whether the script needs a prev-token value-completion switch.
func hasValueFlag(specs []Spec, subs []SubSpec) bool {
	return anySpecInTree(specs, subs, func(s Spec) bool { return s.HasArg }, nil)
}

// hasDynamicFlagValue reports whether any flag in the tree completes its value
// dynamically (spec.Dynamic). Used to decide whether a forwarded-flags helper
// is worth emitting.
func hasDynamicFlagValue(specs []Spec, subs []SubSpec) bool {
	return anySpecInTree(specs, subs, func(s Spec) bool { return s.Dynamic != "" }, nil)
}

// hasDynamicArgs reports whether the tree has any positional dynamic-args
// completion that forwards context to the handler.
func hasDynamicArgs(g *Generator) bool {
	return len(g.DynamicArgs) > 0 || anySpecInTree(nil, g.Subs, nil,
		func(sub SubSpec) bool { return len(sub.DynamicArgs) > 0 })
}

// forwardingActive reports whether g should emit a shared forwarded-flags
// helper: it needs forwardable specs and at least one dynamic completion
// (flag value or positional) that would consume them.
func forwardingActive(g *Generator) bool {
	if len(allForwardableSpecs(g)) == 0 {
		return false
	}
	return hasDynamicFlagValue(g.Specs, g.Subs) || hasDynamicArgs(g)
}

func argValuePatterns(specs []Spec) ([]string, []string) {
	var exact []string
	var equals []string
	for _, spec := range specs {
		if !spec.HasArg {
			continue
		}
		if spec.LongFlag != "" {
			exact = append(exact, "--"+spec.LongFlag)
			equals = append(equals, "--"+spec.LongFlag+"=*")
		}
		if spec.ShortFlag != "" {
			exact = append(exact, "-"+spec.ShortFlag)
			equals = append(equals, "-"+spec.ShortFlag+"=*")
		}
	}
	return exact, equals
}
