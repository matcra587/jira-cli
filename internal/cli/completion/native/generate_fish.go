// Backported from github.com/gechr/clib v0.7.17; licensed under the adjacent LICENSE.
//
//nolint:dupword // fish script keywords (e.g. "end\nend") trigger false positives
package native

import (
	"fmt"
	"strings"
)

// GenerateFish generates a fish shell completion script.
func GenerateFish(g *Generator) (string, error) {
	if err := ValidateGenerator(g); err != nil {
		return "", err
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "complete -c %s -f\n", g.AppName)

	funcID := fishFuncName(g.AppName)
	tokenFn := fishTokensHelperName(g)
	if hasCurrentWordPredictor(g) {
		fmt.Fprintf(&sb, `
function __%s_current_word
    set -l raw (commandline -ct)
    if not set -q raw[1]; set raw ''; end
    if set -q argv[1]
        set -l completed (%s)
        for flag in $argv
            if string match -q -- "$flag=*" "$raw"
                # Fish treats this spelling as an inline option even after
                # a valued flag. Refuse ambiguous replacement; quote the
                # whole CSV field to retain a literal leading option name.
                if contains -- $completed[-1] $argv
                    return 1
                else
                    set raw (string sub -s (math (string length -- $flag) + 2) -- "$raw")
                    break
                end
            end
        end
    end
    set -l decoded (string unescape -- "$raw" 2>/dev/null)
    if test $status -ne 0; return 1; end
    if not set -q decoded[1]; set decoded ''; end
    printf '%%s\n' "--@word=$decoded"
end

function __%s_word_flag_values
    set -l prefix ''
    set -l major (string split . -- $version)[1]
    if test $major -lt 4
        set -l raw (commandline -ct)
        if not set -q raw[1]; set raw ''; end
        for flag in $argv
            if string match -q -- "$flag=*" "$raw"
                # Fish 3 replaces the whole token for quoted CSV choices.
                set prefix "$flag="
                break
            end
        end
    end
    while read -l candidate
        printf '%%s\n' "$prefix$candidate"
    end
end
`, funcID, tokenFn, funcID)
	}
	rootSpecs := SortVisibleSpecs(g.Specs)
	rootPersistent := SortVisibleSpecs(persistentSpecs(g.Specs))
	rootLocal := fishNonPersistentSpecs(g.Specs)

	resolved, funcLookup := fishBuildFuncPlan(g.AppName, g.Specs, g.Subs)
	fishWriteCommaFunctions(g, &sb, resolved)

	if tokenFn != "" {
		fishWriteTokensHelper(&sb, tokenFn)
	}

	if fwdFn := fishForwardHelperName(g); fwdFn != "" {
		fishWriteForwardedFlagsHelper(&sb, fwdFn, tokenFn, allForwardableSpecs(g))
	}

	hasSubs := len(g.Subs) > 0
	if hasSubs {
		needsCmd := fmt.Sprintf("__%s_needs_command", funcID)
		usingSub := fmt.Sprintf("__%s_using_subcommand", funcID)
		usingPrimarySub := fmt.Sprintf("__%s_using_primary_subcommand", funcID)

		fishWriteHelpers(g, &sb, funcID, tokenFn)

		fmt.Fprint(&sb, "\n")
		fishWriteSubEntries(g, &sb, g.Subs, needsCmd)

		if len(rootPersistent) > 0 || len(rootLocal) > 0 {
			fmt.Fprint(&sb, "\n")
			for _, spec := range rootPersistent {
				fishWriteSpec(g, &sb, spec, "", funcLookup, "")
			}
			for _, spec := range rootLocal {
				fishWriteSpec(g, &sb, spec, needsCmd, funcLookup, "")
			}
		}

		fishWriteSubTree(
			g,
			&sb,
			g.Subs,
			usingSub,
			usingPrimarySub,
			"",
			"",
			funcID,
			tokenFn,
			persistentSpecs(g.Specs),
			1,
			funcLookup,
		)
	}
	if !hasSubs {
		fmt.Fprint(&sb, "\n")
		for _, spec := range rootSpecs {
			fishWriteSpec(g, &sb, spec, "", funcLookup, "")
		}
		if len(g.DynamicArgs) > 0 {
			helperName := fmt.Sprintf("__%s_dynamic_args", funcID)
			fishWriteDynamicArgsHelper(g, &sb, helperName, tokenFn, g.Specs, g.DynamicArgs, 0)
			if g.HasMaxPositionalArgs {
				guardName := fmt.Sprintf("__%s_accepts_positional", funcID)
				fishWritePositionalCountHelper(
					&sb,
					guardName,
					tokenFn,
					g.Specs,
					0,
					g.MaxPositionalArgs,
				)
				fmt.Fprintf(
					&sb,
					`
complete -c %s -n '%s' -a "(%s)" -f
`,
					g.AppName,
					guardName,
					helperName,
				)
			} else {
				fmt.Fprintf(&sb, `
complete -c %s -a "(%s)" -f
`, g.AppName, helperName)
			}
		}
	}

	return sb.String(), nil
}

// fishLookupFuncName returns the function name for a flag in the given subcommand
// path, falling back to the default naming convention if not in the lookup.
func fishLookupFuncName(lookup map[string]string, subPath, flagName, appName string) string {
	if fn, ok := lookup[subPath+"/"+flagName]; ok {
		return fn
	}
	return fmt.Sprintf("__%s_complete_%s", fishFuncName(appName), fishFuncName(flagName))
}

func fishFuncName(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

func fishVarName(name string) string {
	return "_flag_" + strings.ReplaceAll(name, "-", "_")
}

func fishFuncPath(pathPrefix, name string) string {
	name = fishFuncName(name)
	if pathPrefix == "" {
		return name
	}
	return pathPrefix + "_" + name
}

func fishEscapeString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, `$`, `\$`)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return s
}

// fishForwardHelperName returns the name of the shared forwarded-flags helper
// for g, or "" when no flag-value dynamic completion would consume it.
func fishForwardHelperName(g *Generator) string {
	if !forwardingActive(g) {
		return ""
	}
	return fmt.Sprintf("__%s_forwarded_flags", fishFuncName(g.AppName))
}

func fishTokensHelperName(g *Generator) string {
	if len(g.Subs) == 0 && !forwardingActive(g) && len(g.DynamicArgs) == 0 && !hasCurrentWordPredictor(g) {
		return ""
	}
	return fmt.Sprintf("__%s_tokens", fishFuncName(g.AppName))
}

//nolint:gosec // This is a native tokenization helper, not a credential.
const fishTokensHelper = `
function %s
    set -l major (string split . -- $version)[1]
    set -l tokens
    if test $major -ge 4
        set tokens (commandline -xpc)
    else
        set tokens (commandline -opc)
    end
    if set -q tokens[1]
        printf '%%s\n' $tokens
    end
end
`

func fishWriteTokensHelper(sb *strings.Builder, helperName string) {
	fmt.Fprintf(sb, fishTokensHelper, helperName)
}

// fishDynamicCall returns the command that produces dynamic completions for a
// flag value, appending forwarded context flags when forwarding is in play.
func fishDynamicCall(g *Generator, spec Spec) string {
	predictor := spec.Dynamic
	if CurrentWordPredictor(predictor) {
		var flags []string
		if spec.LongFlag != "" {
			flags = append(flags, "--"+spec.LongFlag)
		}
		if spec.ShortFlag != "" {
			flags = append(flags, "-"+spec.ShortFlag)
		}
		call := fmt.Sprintf("%s --@shell=fish --%s=%s -- (__%s_current_word %s)", g.AppName, FlagComplete, predictor, fishFuncName(g.AppName), fishQuotedWords(flags))
		if fn := fishForwardHelperName(g); fn != "" {
			call += fmt.Sprintf(" (%s)", fn)
		}
		return call + fmt.Sprintf(" -- | __%s_word_flag_values %s", fishFuncName(g.AppName), fishQuotedWords(flags))
	}
	call := fmt.Sprintf("%s --%s=%s", g.AppName, FlagComplete, predictor)
	if fn := fishForwardHelperName(g); fn != "" {
		call += fmt.Sprintf(" -- (%s)", fn)
	}
	return call
}

// fishWriteForwardedFlagsHelper emits a helper that scans the command line and
// prints each forwardable context flag as --name=value, one per line.
func fishWriteForwardedFlagsHelper(
	sb *strings.Builder,
	helperName string,
	tokenHelper string,
	fwd []forwardSpec,
) {
	fmt.Fprintf(sb, "\nfunction %s\n", helperName)
	fmt.Fprintf(sb, "    set -l tokens (%s)\n", tokenHelper)
	fmt.Fprint(sb, "    set -e tokens[1]\n")
	fmt.Fprint(sb, "    set -l forwarded\n")
	fmt.Fprint(sb, "    set -l skip_next 0\n")
	fmt.Fprint(sb, "    set -l fwd_name\n")
	fmt.Fprint(sb, "    for t in $tokens\n")
	fmt.Fprint(sb, "        if test $skip_next -eq 1\n")
	fmt.Fprint(sb, "            if set -q fwd_name[1]\n")
	fmt.Fprint(sb, "                set -a forwarded \"--$fwd_name=$t\"\n")
	fmt.Fprint(sb, "                set -e fwd_name\n")
	fmt.Fprint(sb, "            end\n")
	fmt.Fprint(sb, "            set skip_next 0\n")
	fmt.Fprint(sb, "        else if test \"$t\" = --\n")
	fmt.Fprint(sb, "            break\n")
	fmt.Fprint(sb, "        else\n")
	fmt.Fprint(sb, "            switch $t\n")
	for _, f := range fwd {
		var patterns []string
		if f.LongFlag != "" {
			patterns = append(patterns, "--"+f.LongFlag)
		}
		if f.ShortFlag != "" {
			patterns = append(patterns, "-"+f.ShortFlag)
		}
		fmt.Fprintf(sb, "            case %s\n", strings.Join(patterns, " "))
		fmt.Fprint(sb, "                set skip_next 1\n")
		fmt.Fprintf(sb, "                set fwd_name %s\n", f.LongFlag)

		var eqPatterns []string
		if f.LongFlag != "" {
			eqPatterns = append(eqPatterns, "'--"+f.LongFlag+"=*'")
		}
		if f.ShortFlag != "" {
			eqPatterns = append(eqPatterns, "'-"+f.ShortFlag+"=*'")
		}
		fmt.Fprintf(sb, "            case %s\n", strings.Join(eqPatterns, " "))
		if f.ShortFlag != "" && f.LongFlag != "" {
			// "--" terminates string's option parsing; without it fish reads
			// the "--long=" replacement as an unknown option.
			fmt.Fprintf(
				sb,
				"                set -a forwarded (string replace -r -- '^-%s=' '--%s=' $t)\n",
				f.ShortFlag,
				f.LongFlag,
			)
		} else {
			fmt.Fprint(sb, "                set -a forwarded $t\n")
		}
	}
	fmt.Fprint(sb, "            end\n")
	fmt.Fprint(sb, "        end\n")
	fmt.Fprint(sb, "    end\n")
	fmt.Fprint(sb, "    if set -q forwarded[1]\n")
	fmt.Fprintf(sb, "        printf '%%s\\n' $forwarded\n")
	fmt.Fprint(sb, "    end\n")
	fmt.Fprint(sb, "end\n")
}

func fishWriteDynamicArgsHelper(
	g *Generator,
	sb *strings.Builder,
	helperName string,
	tokenHelper string,
	specs []Spec,
	dynamicArgs []string,
	cmdSkip int,
) {
	// Forwardable flags are classified as ordinary value flags so their values
	// are skipped rather than counted as positionals. Forwarded context is
	// collected separately below and passed alongside the real positionals.
	exact, equals := argValuePatterns(specs)
	fwdFn := fishForwardHelperName(g)
	fmt.Fprintf(sb, "\nfunction %s\n", helperName)
	fmt.Fprintf(sb, "    set -l tokens (%s)\n", tokenHelper)
	fmt.Fprint(sb, "    set -e tokens[1]\n")
	fmt.Fprint(sb, "    set -l positional\n")
	fmt.Fprint(sb, "    set -l skip_next 0\n")
	fmt.Fprint(sb, "    set -l dashdash 0\n")
	if cmdSkip > 0 {
		fmt.Fprintf(sb, "    set -l cmd_skip %d\n", cmdSkip)
	}
	fmt.Fprint(sb, "    for t in $tokens\n")
	fmt.Fprint(sb, "        if test $dashdash -eq 1\n")
	fmt.Fprint(sb, "            set -a positional $t\n")
	fmt.Fprint(sb, "        else if test $skip_next -eq 1\n")
	fmt.Fprint(sb, "            set skip_next 0\n")
	fmt.Fprint(sb, `        else if test "$t" = --
`)
	fmt.Fprint(sb, "            set dashdash 1\n")
	fmt.Fprint(sb, "        else\n")
	fishWriteTokenClassifier(sb, exact, equals, cmdSkip, "            ")
	fmt.Fprint(sb, "        end\n")
	fmt.Fprint(sb, "    end\n")
	fmt.Fprint(sb, "    set -l nargs (count $positional)\n")
	if fwdFn != "" {
		fmt.Fprintf(sb, "    set -l forwarded (%s)\n", fwdFn)
	}
	fmt.Fprint(sb, "    switch $nargs\n")
	for i, da := range dynamicArgs {
		fmt.Fprintf(sb, "        case %d\n", i)
		fmt.Fprintf(sb, "            %s\n", fishDynamicArgsCall(g, da, fwdFn, i == 0))
	}
	fmt.Fprint(sb, "        case '*'\n")
	fmt.Fprintf(
		sb,
		"            %s\n",
		fishDynamicArgsCall(g, dynamicArgs[len(dynamicArgs)-1], fwdFn, false),
	)
	fmt.Fprint(sb, "    end\nend\n")
}

// fishDynamicArgsCall builds the handler invocation for a positional dynamic
// completion slot. The first slot has no preceding positionals; forwarded
// context flags (when present) are passed ahead of any real positionals.
func fishDynamicArgsCall(g *Generator, da, fwdFn string, first bool) string {
	if CurrentWordPredictor(da) {
		call := fmt.Sprintf("%s --@shell=fish --%s=%s -- (__%s_current_word)", g.AppName, FlagComplete, da, fishFuncName(g.AppName))
		if fwdFn != "" {
			call += " $forwarded"
		}
		call += " --"
		if !first {
			call += " $positional"
		}
		return call
	}
	call := fmt.Sprintf("%s --%s=%s", g.AppName, FlagComplete, da)
	switch {
	case fwdFn != "" && first:
		return call + " -- $forwarded"
	case fwdFn != "":
		return call + " -- $forwarded $positional"
	case first:
		return call
	default:
		return call + " -- $positional"
	}
}

func fishWritePositionalCountHelper(
	sb *strings.Builder,
	helperName string,
	tokenHelper string,
	specs []Spec,
	cmdSkip int,
	maxPositionalArgs int,
) {
	exact, equals := argValuePatterns(specs)
	fmt.Fprintf(sb, "\nfunction %s\n", helperName)
	fmt.Fprintf(sb, "    set -l tokens (%s)\n", tokenHelper)
	fmt.Fprint(sb, "    set -e tokens[1]\n")
	fmt.Fprint(sb, "    set -l positional\n")
	fmt.Fprint(sb, "    set -l skip_next 0\n")
	fmt.Fprint(sb, "    set -l dashdash 0\n")
	if cmdSkip > 0 {
		fmt.Fprintf(sb, "    set -l cmd_skip %d\n", cmdSkip)
	}
	fmt.Fprint(sb, "    for t in $tokens\n")
	fmt.Fprint(sb, "        if test $dashdash -eq 1\n")
	fmt.Fprint(sb, "            set -a positional $t\n")
	fmt.Fprint(sb, "        else if test $skip_next -eq 1\n")
	fmt.Fprint(sb, "            set skip_next 0\n")
	fmt.Fprint(sb, `        else if test "$t" = --
`)
	fmt.Fprint(sb, "            set dashdash 1\n")
	fmt.Fprint(sb, "        else\n")
	fishWriteTokenClassifier(sb, exact, equals, cmdSkip, "            ")
	fmt.Fprint(sb, "        end\n")
	fmt.Fprint(sb, "    end\n")
	fmt.Fprintf(sb, "    test (count $positional) -lt %d\n", maxPositionalArgs)
	fmt.Fprint(sb, "end\n")
}

func fishWriteTokenClassifier(
	sb *strings.Builder,
	exact, equals []string,
	cmdSkip int,
	indent string,
) {
	fmt.Fprintf(sb, "%sswitch $t\n", indent)
	if len(exact) > 0 {
		fmt.Fprintf(sb, "%scase %s\n", indent, strings.Join(exact, " "))
		fmt.Fprintf(sb, "%s    set skip_next 1\n", indent)
	}
	if len(equals) > 0 {
		quoted := make([]string, len(equals))
		for i, pattern := range equals {
			quoted[i] = "'" + pattern + "'"
		}
		fmt.Fprintf(sb, "%scase %s\n", indent, strings.Join(quoted, " "))
		fmt.Fprintf(sb, "%s    true\n", indent)
	}
	fmt.Fprintf(sb, "%scase '-*'\n", indent)
	fmt.Fprintf(sb, "%s    true\n", indent)
	fmt.Fprintf(sb, "%scase '*'\n", indent)
	if cmdSkip > 0 {
		fmt.Fprintf(sb, "%s    if test $cmd_skip -gt 0\n", indent)
		fmt.Fprintf(sb, "%s        set cmd_skip (math $cmd_skip - 1)\n", indent)
		fmt.Fprintf(sb, "%s    else\n", indent)
		fmt.Fprintf(sb, "%s        set -a positional $t\n", indent)
		fmt.Fprintf(sb, "%s    end\n", indent)
	} else {
		fmt.Fprintf(sb, "%s    set -a positional $t\n", indent)
	}
	fmt.Fprintf(sb, "%send\n", indent)
}

// fishResolvedSpec pairs a Spec with the function name assigned to it.
type fishResolvedSpec struct {
	Spec     Spec
	FuncName string
}

// fishBuildFuncPlan walks the full spec tree and assigns a helper function name
// to every spec that needs one (CommaList or ValueDescs). When the same flag
// name appears with identical completions in multiple subcommands, they share
// one function. When the same flag name appears with different completions, each
// variant gets a path-scoped name (e.g. __app_complete_sub_flag) to avoid
// collision.
//
// It returns:
//   - the deduplicated list of (spec, funcName) to write functions for
//   - a lookup map "path/flagname" → funcName for use in fishWriteSpec
func fishBuildFuncPlan(
	appName string,
	specs []Spec,
	subs []SubSpec,
) ([]fishResolvedSpec, map[string]string) {
	type item struct {
		spec Spec
		path string
		fp   string
	}
	var all []item

	var gather func([]Spec, []SubSpec, string)
	gather = func(specs []Spec, subs []SubSpec, path string) {
		for _, spec := range SortVisibleSpecs(specs) {
			if !fishSpecNeedsFunc(spec) {
				continue
			}
			all = append(all, item{spec, path, fishSpecFingerprint(spec)})
		}
		for _, sub := range subs {
			subPath := fishFuncName(sub.Name)
			if path != "" {
				subPath = path + "_" + fishFuncName(sub.Name)
			}
			gather(sub.Specs, sub.Subs, subPath)
		}
	}
	gather(specs, subs, "")

	// Group by flag name; detect whether any two entries have different content.
	type group struct {
		fp       string
		conflict bool
		items    []item
	}
	groups := map[string]*group{}
	var order []string

	for _, it := range all {
		g, ok := groups[it.spec.LongFlag]
		if !ok {
			groups[it.spec.LongFlag] = &group{fp: it.fp, items: []item{it}}
			order = append(order, it.spec.LongFlag)
		} else {
			if g.fp != it.fp {
				g.conflict = true
			}
			g.items = append(g.items, it)
		}
	}

	appID := fishFuncName(appName)
	var resolved []fishResolvedSpec
	lookup := map[string]string{} // "path/flagname" → funcName

	for _, flagName := range order {
		g := groups[flagName]
		flagID := fishFuncName(flagName)
		seenFuncs := map[string]bool{}

		for _, it := range g.items {
			var funcName string
			if g.conflict && it.path != "" {
				funcName = fmt.Sprintf("__%s_complete_%s_%s", appID, it.path, flagID)
			} else {
				funcName = fmt.Sprintf("__%s_complete_%s", appID, flagID)
			}
			lookup[it.path+"/"+flagName] = funcName
			if !seenFuncs[funcName] {
				seenFuncs[funcName] = true
				resolved = append(resolved, fishResolvedSpec{it.spec, funcName})
			}
		}
	}
	return resolved, lookup
}

func fishSpecNeedsFunc(spec Spec) bool {
	if CurrentWordPredictor(spec.Dynamic) {
		return false
	}
	if spec.LongFlag == "" {
		return false
	}
	if spec.CommaList {
		return spec.Dynamic != "" || len(spec.Values) > 0 || len(spec.ValueDescs) > 0
	}
	return len(spec.ValueDescs) > 0
}

func fishSpecFingerprint(spec Spec) string {
	return fmt.Sprintf("dynamic=%s|values=%v|descs=%v", spec.Dynamic, spec.Values, spec.ValueDescs)
}

func fishWriteCommaFunctions(
	g *Generator,
	sb *strings.Builder,
	resolved []fishResolvedSpec,
) {
	for _, rs := range resolved {
		fmt.Fprint(sb, "\n")
		if rs.Spec.CommaList {
			fishWriteCommaFunction(g, sb, rs.Spec, rs.FuncName)
		} else {
			fishWriteValueDescsFunction(sb, rs.Spec, rs.FuncName)
		}
	}
}

func fishWriteHelpers(g *Generator, sb *strings.Builder, funcID, tokenHelper string) {
	optspecsFn := fmt.Sprintf("__%s_global_optspecs", funcID)
	needsFn := fmt.Sprintf("__%s_needs_command", funcID)
	usingFn := fmt.Sprintf("__%s_using_subcommand", funcID)
	usingPrimaryFn := fmt.Sprintf("__%s_using_primary_subcommand", funcID)

	fmt.Fprintf(sb, "\nfunction %s\n    string join \\n --", optspecsFn)
	for _, spec := range g.Specs {
		if spec.Hidden {
			continue
		}
		var optspec string
		switch {
		case spec.ShortFlag != "" && spec.LongFlag != "":
			optspec = spec.ShortFlag + "/" + spec.LongFlag
		case spec.LongFlag != "":
			optspec = spec.LongFlag
		case spec.ShortFlag != "":
			optspec = spec.ShortFlag
		default:
			continue
		}
		if spec.HasArg {
			optspec += "="
		}
		fmt.Fprintf(sb, " '%s'", optspec)
	}
	fmt.Fprint(sb, "\nend\n")

	fmt.Fprintf(sb, `
function %[1]s
    set -l cmd (%[5]s)
    set -e cmd[1]
    argparse -s --ignore-unknown (%[2]s) -- $cmd 2>/dev/null
    or return
    if set -q argv[1]
        set argv (string match -v -r '^-' -- $argv)
    end
    if set -q argv[1]
        printf '%%s\n' $argv
        return 1
    end
    return 0
end

function %[3]s
    set -l cmd (%[1]s)
    test -z "$cmd"
    and return 1
    for arg in $argv
        if contains -- $arg $cmd
            return 0
        end
    end
    return 1
end

function %[4]s
    set -l cmd (%[1]s)
    test -z "$cmd"
    and return 1
    contains -- $cmd[1] $argv
end
`, needsFn, optspecsFn, usingFn, usingPrimaryFn, tokenHelper)
}

func fishWriteSubEntries(
	g *Generator,
	sb *strings.Builder,
	subs []SubSpec,
	condition string,
) {
	for _, sub := range SortSubSpecs(subs) {
		fishWriteSubcommand(g, sb, sub.Name, sub.Terse, condition)
	}
}

func fishWriteSubTree(
	g *Generator,
	sb *strings.Builder,
	subs []SubSpec,
	usingSub, usingPrimarySub, parentCondition, pathPrefix, funcID string,
	tokenHelper string,
	inheritedSpecs []Spec,
	depth int,
	funcLookup map[string]string,
) {
	for _, sub := range SortSubSpecs(subs) {
		subPath := fishFuncPath(pathPrefix, sub.Name)
		allNames := append([]string{sub.Name}, sub.Aliases...)
		subSeenFunc := usingSub
		if depth == 1 {
			subSeenFunc = usingPrimarySub
		}
		subSeen := subSeenFunc + " " + strings.Join(allNames, " ")

		seenCondition := subSeen
		if parentCondition != "" {
			seenCondition = parentCondition + "; and " + subSeen
		}

		leafCondition := seenCondition
		if len(sub.Subs) > 0 {
			var childNames []string
			for _, child := range sub.Subs {
				childNames = append(childNames, child.Name)
				childNames = append(childNames, child.Aliases...)
			}
			leafCondition += "; and not " + usingSub + " " + strings.Join(childNames, " ")
		}

		subPersistent := SortVisibleSpecs(persistentSpecs(sub.Specs))
		subLocal := fishNonPersistentSpecs(sub.Specs)
		hasDynArgs := len(sub.DynamicArgs) > 0
		if len(subPersistent) == 0 && len(subLocal) == 0 && len(sub.Subs) == 0 && !sub.PathArgs &&
			!hasDynArgs {
			continue
		}

		fmt.Fprint(sb, "\n")

		if len(sub.Subs) > 0 {
			fishWriteSubEntries(g, sb, sub.Subs, leafCondition)
		}
		for _, spec := range subPersistent {
			fishWriteSpec(g, sb, spec, seenCondition, funcLookup, subPath)
		}
		for _, spec := range subLocal {
			fishWriteSpec(g, sb, spec, leafCondition, funcLookup, subPath)
		}
		positionalCondition := leafCondition
		if sub.HasMaxPositionalArgs {
			guardName := fmt.Sprintf("__%s_%s_accepts_positional", funcID, subPath)
			allSpecs := appendSpecs(inheritedSpecs, sub.Specs)
			fishWritePositionalCountHelper(
				sb,
				guardName,
				tokenHelper,
				allSpecs,
				depth,
				sub.MaxPositionalArgs,
			)
			if positionalCondition != "" {
				positionalCondition += "; and " + guardName
			} else {
				positionalCondition = guardName
			}
		}
		if sub.PathArgs {
			fmt.Fprintf(sb, "complete -c %s -n '%s' -F\n", g.AppName, positionalCondition)
		}
		if hasDynArgs {
			helperName := fmt.Sprintf("__%s_%s_dynamic_args", funcID, subPath)
			allSpecs := appendSpecs(inheritedSpecs, sub.Specs)
			fishWriteDynamicArgsHelper(
				g,
				sb,
				helperName,
				tokenHelper,
				allSpecs,
				sub.DynamicArgs,
				depth,
			)
			fmt.Fprintf(
				sb,
				`
complete -c %s -n '%s' -a "(%s)" -f
`,
				g.AppName,
				positionalCondition,
				helperName,
			)
		}
		if len(sub.Subs) > 0 {
			nextInherited := appendSpecs(inheritedSpecs, persistentSpecs(sub.Specs))
			fishWriteSubTree(
				g,
				sb,
				sub.Subs,
				usingSub,
				usingPrimarySub,
				seenCondition,
				subPath,
				funcID,
				tokenHelper,
				nextInherited,
				depth+1,
				funcLookup,
			)
		}
	}
}

func fishWriteCommaFunction(
	g *Generator,
	sb *strings.Builder,
	spec Spec,
	funcName string,
) {
	varName := fishVarName(spec.LongFlag)

	fmt.Fprintf(sb, "# Comma-separated %[1]s completion\nfunction %[2]s\n", spec.LongFlag, funcName)
	fmt.Fprintf(
		sb,
		"    set -l value (string replace -r '^--%s=' '' -- (commandline -ct))\n",
		spec.LongFlag,
	)
	if len(spec.ValueDescs) > 0 {
		// Value-description pairs: store parallel arrays for tab-described output.
		vals := make([]string, len(spec.ValueDescs))
		descs := make([]string, len(spec.ValueDescs))
		for i, vd := range spec.ValueDescs {
			vals[i] = vd.Value
			descs[i] = vd.Desc
		}
		fmt.Fprintf(sb, "    set -l %s %s\n", varName, fishQuotedWords(vals))
		fmt.Fprintf(sb, "    set -l %s_desc %s\n", varName, fishQuotedWords(descs))
		fmt.Fprintf(sb, `    if string match -qr '^(?<prefix>.*,)' -- $value
        set -l selected (string split ',' -- $prefix)
        for i in (seq (count $%[1]s))
            if not contains -- $%[1]s[$i] $selected
                printf '%%s\t%%s\n' "$prefix$%[1]s[$i]" $%[1]s_desc[$i]
            end
        end
    else
        for i in (seq (count $%[1]s))
            printf '%%s\t%%s\n' $%[1]s[$i] $%[1]s_desc[$i]
        end
    end
end
`, varName)
	} else {
		switch {
		case spec.Dynamic != "":
			fmt.Fprintf(sb, "    set -l %s (%s)\n", varName, fishDynamicCall(g, spec))
		default:
			fmt.Fprintf(sb, "    set -l %s %s\n", varName, fishQuotedWords(spec.Values))
		}
		fmt.Fprintf(sb, `    if string match -qr '^(?<prefix>.*,)' -- $value
        set -l selected (string split ',' -- $prefix)
        for col in $%[1]s
            if not contains -- $col $selected
                printf '%%s\n' "$prefix$col"
            end
        end
    else
        printf '%%s\n' $%[1]s
    end
end
`, varName)
	}
}

func fishWriteValueDescsFunction(
	sb *strings.Builder,
	spec Spec,
	funcName string,
) {
	varName := fishVarName(spec.LongFlag)

	vals := make([]string, len(spec.ValueDescs))
	descs := make([]string, len(spec.ValueDescs))
	for i, vd := range spec.ValueDescs {
		vals[i] = vd.Value
		descs[i] = vd.Desc
	}
	fmt.Fprintf(sb, "# %[1]s value completion\nfunction %[2]s\n", spec.LongFlag, funcName)
	fmt.Fprintf(sb, "    set -l %s %s\n", varName, fishQuotedWords(vals))
	fmt.Fprintf(sb, "    set -l %s_desc %s\n", varName, fishQuotedWords(descs))
	fmt.Fprintf(sb, `    for i in (seq (count $%[1]s))
        printf '%%s\t%%s\n' $%[1]s[$i] $%[1]s_desc[$i]
    end
end
`, varName)
}

func fishWriteSubcommand(g *Generator, sb *strings.Builder, name, terse, condition string) {
	var parts []string
	parts = append(parts, fmt.Sprintf("complete -c %s", g.AppName))
	if condition != "" {
		parts = append(parts, fmt.Sprintf("-n '%s'", condition))
	}
	parts = append(parts, fmt.Sprintf("-a %s", name))
	if terse != "" {
		parts = append(parts, fmt.Sprintf("-d %q", terse))
	}
	fmt.Fprintf(sb, "%s\n", strings.Join(parts, " "))
}

func fishWriteSpec(
	g *Generator,
	sb *strings.Builder,
	spec Spec,
	condition string,
	funcLookup map[string]string,
	subPath string,
) {
	var parts []string
	parts = append(parts, fmt.Sprintf("complete -c %s", g.AppName))

	if condition != "" {
		parts = append(parts, fmt.Sprintf("-n '%s'", condition))
	}
	if spec.ShortFlag != "" {
		parts = append(parts, fmt.Sprintf("-s %s", spec.ShortFlag))
	}
	if spec.LongFlag != "" {
		parts = append(parts, fmt.Sprintf("-l %s", spec.LongFlag))
	}
	parts = append(parts, fishSpecArgParts(g, spec, funcLookup, subPath)...)

	if spec.Terse != "" {
		parts = append(parts, fmt.Sprintf("-d %q", spec.Terse))
	}

	fmt.Fprintf(sb, "%s\n", strings.Join(parts, " "))
}

func fishSpecArgParts(
	g *Generator,
	spec Spec,
	funcLookup map[string]string,
	subPath string,
) []string {
	if !spec.HasArg {
		return nil
	}

	keepOrder := spec.Order == OrderKeep || (spec.Order == "" && g.Order == OrderKeep)
	if spec.Order == OrderShell {
		keepOrder = false
	}

	switch {
	case !CurrentWordPredictor(spec.Dynamic) && spec.CommaList && (spec.Dynamic != "" || len(spec.Values) > 0 || len(spec.ValueDescs) > 0):
		funcName := fishLookupFuncName(funcLookup, subPath, spec.LongFlag, g.AppName)
		if spec.Order != OrderShell {
			return []string{"-x", fmt.Sprintf(`-kra "(%s)"`, funcName)}
		}
		return []string{"-x", fmt.Sprintf(`-ra "(%s)"`, funcName)}
	case spec.Dynamic != "":
		parts := make([]string, 0, 3) //nolint:mnd // at most "-k", "-x", and "-a ..."
		if keepOrder {
			parts = append(parts, "-k")
		}
		parts = append(
			parts,
			"-x",
			fmt.Sprintf(`-a "(%s)"`, fishDynamicCall(g, spec)),
		)
		return parts
	case len(spec.ValueDescs) > 0:
		funcName := fishLookupFuncName(funcLookup, subPath, spec.LongFlag, g.AppName)
		parts := make([]string, 0, 3) //nolint:mnd // at most "-k", "-x", and "-ra ..."
		if keepOrder {
			parts = append(parts, "-k")
		}
		parts = append(parts, "-x", fmt.Sprintf(`-ra "(%s)"`, funcName))
		return parts
	case len(spec.Values) > 0:
		values := make([]string, len(spec.Values))
		for i, value := range spec.Values {
			values[i] = fishEscapeString(value)
		}
		parts := make([]string, 0, 3) //nolint:mnd // at most "-k", "-x", and "-a ..."
		if keepOrder {
			parts = append(parts, "-k")
		}
		parts = append(parts, "-x", fmt.Sprintf(`-a "%s"`, strings.Join(values, " ")))
		return parts
	case spec.Extension != "":
		suffixes := fishExtToSuffixes(spec.Extension)
		return []string{
			"-k",
			fmt.Sprintf(`-xa "(__fish_complete_suffix %s)"`, strings.Join(suffixes, " ")),
		}
	case spec.ValueHint != "":
		switch spec.ValueHint {
		case HintFile:
			return []string{"-r", "-F"}
		case HintDir:
			return []string{"-r", "-f", `-a "(__fish_complete_directories)"`}
		case HintCommand:
			return []string{"-r", "-f", `-a "(__fish_complete_command)"`}
		case HintUser:
			return []string{"-r", "-f", `-a "(__fish_complete_users)"`}
		case HintHost:
			return []string{"-r", "-f", `-a "(__fish_print_hostnames)"`}
		default:
			return []string{"-r", "-f"}
		}
	default:
		return []string{"-r"}
	}
}

func fishQuotedWords(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = fmt.Sprintf(`"%s"`, fishEscapeString(value))
	}
	return strings.Join(quoted, " ")
}

func fishExtToSuffixes(ext string) []string {
	parts := strings.Split(ext, ",")
	suffixes := make([]string, len(parts))
	for i, p := range parts {
		suffixes[i] = "." + strings.TrimSpace(p)
	}
	return suffixes
}

func fishNonPersistentSpecs(specs []Spec) []Spec {
	var local []Spec
	for _, spec := range specs {
		if !spec.Persistent {
			local = append(local, spec)
		}
	}
	return SortVisibleSpecs(local)
}
