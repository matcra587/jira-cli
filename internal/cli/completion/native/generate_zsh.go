package native

import (
	"encoding/hex"
	"fmt"
	"strings"
)

const zshDefaultCompleter = "_default"

// GenerateZsh generates a zsh shell completion script.
func GenerateZsh(g *Generator) (string, error) {
	if err := ValidateGenerator(g); err != nil {
		return "", err
	}

	var sb strings.Builder
	funcName := zshFuncName(g.AppName)
	rootSpecs := zshCompletionSpecs(g.Specs)
	inheritedSpecs := persistentSpecs(g.Specs)
	sortedSubs := SortSubSpecs(g.Subs)

	fmt.Fprintf(&sb, `#compdef %[1]s

autoload -U is-at-least

_%[2]s() {
    typeset -A opt_args
    typeset -a _arguments_options
    local ret=1

    if is-at-least 5.2; then
        _arguments_options=(-s -S -C)
    else
        _arguments_options=(-s -C)
    fi

    local context curcontext="$curcontext" state line
`, g.AppName, funcName)

	if fwdFn := zshForwardHelperName(g); fwdFn != "" {
		// Collect forwardable context flags once; dynamic value completions
		// pass them to the handler via "${__fwd[@]}".
		fmt.Fprintf(&sb, "    local -a __fwd=()\n    %s\n", fwdFn)
	}
	zshWriteUnlistedPrefixFlags(&sb, g.Specs)

	if len(sortedSubs) > 0 {
		zshWriteArguments(
			g,
			&sb,
			rootSpecs,
			false,
			g.DynamicArgs,
			g.HasMaxPositionalArgs,
			g.MaxPositionalArgs,
			funcName,
			g.AppName,
			"    ",
		)
		zshWriteSubcommandCase(g, &sb, sortedSubs, inheritedSpecs, SubSpec{
			Name: g.AppName, Specs: rootSpecs, DynamicArgs: g.DynamicArgs,
			HasMaxPositionalArgs: g.HasMaxPositionalArgs, MaxPositionalArgs: g.MaxPositionalArgs,
		}, funcName, "    ")
	} else {
		zshWriteArguments(
			g,
			&sb,
			rootSpecs,
			false,
			g.DynamicArgs,
			g.HasMaxPositionalArgs,
			g.MaxPositionalArgs,
			"",
			"",
			"    ",
		)
	}

	fmt.Fprint(&sb, "}\n")

	if helper := zshDynamicValuesHelperName(g); helper != "" {
		zshWriteDynamicValuesHelper(&sb, helper)
	}

	if fwdFn := zshForwardHelperName(g); fwdFn != "" {
		zshWriteForwardedFlagsHelper(&sb, fwdFn, allForwardableSpecs(g))
	}

	if len(sortedSubs) > 0 {
		zshWriteDescribeTree(&sb, sortedSubs, funcName, g.AppName)
	}

	fmt.Fprintf(&sb, `
if [ "$funcstack[1]" = "_%[1]s" ]; then
    _%[1]s "$@"
else
    compdef _%[1]s %[2]s
fi
`, funcName, g.AppName)

	return sb.String(), nil
}

// clib removes hidden specs before calling a registered generator. Unlisted
// boolean flags before the command (including hidden output aliases) must not
// become command-position words. Known values retain their original scope.
func zshWriteUnlistedPrefixFlags(sb *strings.Builder, specs []Spec) {
	exact, equals := argValuePatterns(specs)
	var known []string
	for _, spec := range specs {
		if spec.LongFlag != "" {
			known = append(known, "--"+spec.LongFlag, "--"+spec.LongFlag+"=*")
		}
		if spec.ShortFlag != "" {
			known = append(known, "-"+spec.ShortFlag, "-"+spec.ShortFlag+"=*")
		}
	}
	fmt.Fprint(sb, "    local __prefix_index=2 __prefix_skip=0 __prefix_token\n    while (( __prefix_index < CURRENT )); do\n        __prefix_token=${(Q)words[__prefix_index]}\n        if (( __prefix_skip )); then __prefix_skip=0; (( __prefix_index++ )); continue; fi\n        case $__prefix_token in\n            --) break ;;\n")
	if len(exact) != 0 {
		fmt.Fprintf(sb, "            %s) __prefix_skip=1 ;;\n", strings.Join(exact, "|"))
	}
	if len(equals) != 0 {
		fmt.Fprintf(sb, "            %s) ;;\n", strings.Join(equals, "|"))
	}
	if len(known) != 0 {
		fmt.Fprintf(sb, "            %s) ;;\n", strings.Join(known, "|"))
	}
	fmt.Fprint(sb, "            -*) words=(\"${(@)words[1,__prefix_index-1]}\" \"${(@)words[__prefix_index+1,-1]}\"); (( CURRENT-- )); continue ;;\n            *) break ;;\n        esac\n        (( __prefix_index++ ))\n    done\n")
}

func zshWriteArguments(
	g *Generator,
	sb *strings.Builder,
	specs []Spec,
	pathArgs bool,
	dynamicArgs []string,
	hasMaxPositionalArgs bool,
	maxPositionalArgs int,
	funcName, stateName, indent string,
) {
	mixedParent := funcName != "" && (pathArgs || len(dynamicArgs) > 0)
	snapshotName := zshSnapshotName(funcName, stateName)
	if funcName != "" {
		// _arguments shifts words/CURRENT and removes -- for child dispatch.
		// Its NORMARG index lets us preserve the authored child scope exactly.
		fmt.Fprintf(sb, "%slocal -a __parent_words_%s=(\"${words[@]}\")\n", indent, snapshotName)
		fmt.Fprintf(sb, "%slocal __parent_current_%s=$CURRENT __parent_context_%s=\"$curcontext\"\n", indent, snapshotName, snapshotName)
		fmt.Fprintf(sb, "%slocal NORMARG=-1\n", indent)
		fmt.Fprintf(sb, "%s_arguments \"${_arguments_options[@]}\" -n : \\\n", indent)
	} else {
		fmt.Fprintf(sb, "%s_arguments \"${_arguments_options[@]}\" : \\\n", indent)
	}

	specIndent := indent + "    "
	for _, spec := range specs {
		zshWriteSpec(g, sb, spec, specIndent)
	}

	switch {
	case funcName != "":
		if mixedParent && (!hasMaxPositionalArgs || maxPositionalArgs > 0) {
			fmt.Fprintf(sb, "%[1]s':: :->%[2]s_first' \\\n%[1]s'*::: :->%[2]s' \\\n", specIndent, stateName)
		} else {
			fmt.Fprintf(sb, "%[1]s':: :_%[2]s_commands' \\\n%[1]s'*::: :->%[3]s' \\\n", specIndent, funcName, stateName)
		}
	case pathArgs:
		if hasMaxPositionalArgs {
			for i := range maxPositionalArgs {
				fmt.Fprintf(sb, "%s'%d:file:_files' \\\n", specIndent, i+1)
			}
		} else {
			fmt.Fprintf(sb, "%s'*:file:_files' \\\n", specIndent)
		}
	case len(dynamicArgs) > 0:
		limit := len(dynamicArgs)
		if hasMaxPositionalArgs {
			limit = maxPositionalArgs
		}
		for i := range limit {
			fmt.Fprintf(sb, "%s'%d: :->dyn_%d' \\\n", specIndent, i+1, i+1)
		}
		if !hasMaxPositionalArgs {
			fmt.Fprintf(sb, "%s'*: :->dyn_rest' \\\n", specIndent)
		}
	}

	fmt.Fprintf(sb, "%s&& ret=0\n", indent)

	if mixedParent && (!hasMaxPositionalArgs || maxPositionalArgs > 0) {
		// The optional command slot and its tail can both select a state.
		// Complete their shared first slot once before dispatching a child.
		fmt.Fprintf(sb, "\n%sif (( ${state[(I)%s_first]} )); then\n", indent, stateName)
		fmt.Fprintf(sb, "%s    _%s_commands && ret=0\n", indent, funcName)
		if pathArgs {
			fmt.Fprintf(sb, "%s    _files && ret=0\n", indent)
		} else {
			fmt.Fprintf(sb, "%s    local -a items\n", indent)
			fmt.Fprintf(sb, "%s    items=(${(f)\"%s\"})\n", indent, zshDynamicArgsExpr(g, dynamicArgs[0], true))
			fmt.Fprintf(sb, "%s    %s -- \"${(@)items}\" && ret=0\n", indent, zshDynamicValuesHelperName(g))
		}
		fmt.Fprintf(sb, "%s    state=()\n%sfi\n", indent, indent)
	}

	if funcName == "" && !pathArgs && len(dynamicArgs) > 0 {
		zshWriteDynamicArgsCases(
			g,
			sb,
			specs,
			dynamicArgs,
			hasMaxPositionalArgs,
			maxPositionalArgs,
			indent,
		)
	}
}

func zshWriteDynamicArgsCases(
	g *Generator,
	sb *strings.Builder,
	specs []Spec,
	dynamicArgs []string,
	hasMaxPositionalArgs bool,
	maxPositionalArgs int,
	indent string,
) {
	inner := indent + "    "
	// Forwardable flags are classified as ordinary value flags so they are
	// skipped rather than collected as positionals; forwarded context lives in
	// __fwd and is passed separately to the handler.
	exact, equals := argValuePatterns(specs)
	fmt.Fprintf(sb, "\n%scase $state in\n", indent)
	limit := len(dynamicArgs)
	if hasMaxPositionalArgs {
		limit = maxPositionalArgs
	}
	for i := range limit {
		da := dynamicArgs[min(i, len(dynamicArgs)-1)]
		fmt.Fprintf(sb, "%s(dyn_%d)\n", indent, i+1)
		zshWriteDynTokenParser(sb, inner, exact, equals)
		fmt.Fprintf(sb, "%slocal -a items\n", inner)
		fmt.Fprintf(sb, "%sitems=(${(f)\"%s\"})\n", inner, zshDynamicArgsExpr(g, da, i == 0))
		options := "--"
		if CurrentWordPredictor(da) {
			options = "-U --"
		}
		fmt.Fprintf(sb, "%s%s %s \"${(@)items}\"\n", inner, zshDynamicValuesHelperName(g), options)
		fmt.Fprintf(sb, "%s;;\n", indent)
	}
	if !hasMaxPositionalArgs {
		fmt.Fprintf(sb, "%s(dyn_rest)\n", indent)
		zshWriteDynTokenParser(sb, inner, exact, equals)
		fmt.Fprintf(sb, "%slocal -a items\n", inner)
		fmt.Fprintf(
			sb,
			"%sitems=(${(f)\"%s\"})\n",
			inner,
			zshDynamicArgsExpr(g, dynamicArgs[len(dynamicArgs)-1], false),
		)
		options := "--"
		if CurrentWordPredictor(dynamicArgs[len(dynamicArgs)-1]) {
			options = "-U --"
		}
		fmt.Fprintf(sb, "%s%s %s \"${(@)items}\"\n", inner, zshDynamicValuesHelperName(g), options)
		fmt.Fprintf(sb, "%s;;\n", indent)
	}
	fmt.Fprintf(sb, "%sesac\n", indent)
}

// zshDynamicArgsExpr builds the handler command substitution for a positional
// dynamic completion slot. first==true means no preceding positionals; when
// forwarding is active, collected context flags precede the real positionals.
func zshDynamicArgsExpr(g *Generator, da string, first bool) string {
	if CurrentWordPredictor(da) {
		forward, positional := "", ""
		if zshForwardHelperName(g) != "" {
			forward = ` "${__fwd[@]}"`
		}
		if !first {
			positional = ` "${__pos[@]}"`
		}
		return fmt.Sprintf(`$(%s --@shell=zsh --%s=%s -- "%s${(Q)words[CURRENT]}"%s --%s 2>/dev/null)`, g.AppName, FlagComplete, da, WordArgument, forward, positional)
	}
	forward := zshForwardHelperName(g) != ""
	switch {
	case forward && first:
		return fmt.Sprintf(
			`$(%s --%s=%s -- "${__fwd[@]}" 2>/dev/null)`,
			g.AppName,
			FlagComplete,
			da,
		)
	case forward:
		return fmt.Sprintf(
			`$(%s --%s=%s -- "${__fwd[@]}" "${__pos[@]}" 2>/dev/null)`,
			g.AppName,
			FlagComplete,
			da,
		)
	case first:
		return fmt.Sprintf("$(%s --%s=%s 2>/dev/null)", g.AppName, FlagComplete, da)
	default:
		return fmt.Sprintf(
			`$(%s --%s=%s -- "${__pos[@]}" 2>/dev/null)`,
			g.AppName,
			FlagComplete,
			da,
		)
	}
}

func zshWriteDynTokenParser(
	sb *strings.Builder,
	inner string,
	exact, equals []string,
) {
	fmt.Fprintf(sb, "%slocal -a __pos=()\n", inner)
	fmt.Fprintf(sb, "%slocal __skip_next=0\n", inner)
	fmt.Fprintf(sb, "%slocal __after_dd=0\n", inner)
	fmt.Fprintf(sb, "%slocal token i\n", inner)
	fmt.Fprintf(sb, "%sfor ((i=2; i<CURRENT; i++)); do\n", inner)
	fmt.Fprintf(sb, "%s    token=${(Q)words[i]}\n", inner)
	fmt.Fprintf(sb, "%s    if (( __after_dd )); then\n", inner)
	fmt.Fprintf(sb, "%s        __pos+=(\"$token\")\n", inner)
	fmt.Fprintf(sb, "%s        continue\n", inner)
	fmt.Fprintf(sb, "%s    fi\n", inner)
	fmt.Fprintf(sb, "%s    if (( __skip_next )); then\n", inner)
	fmt.Fprintf(sb, "%s        __skip_next=0\n", inner)
	fmt.Fprintf(sb, "%s        continue\n", inner)
	fmt.Fprintf(sb, "%s    fi\n", inner)
	fmt.Fprintf(sb, "%s    if [[ $token == -- ]]; then\n", inner)
	fmt.Fprintf(sb, "%s        __after_dd=1\n", inner)
	fmt.Fprintf(sb, "%s        continue\n", inner)
	fmt.Fprintf(sb, "%s    fi\n", inner)
	fmt.Fprintf(sb, "%s    case $token in\n", inner)
	if len(exact) > 0 {
		fmt.Fprintf(
			sb,
			"%s        (%s)\n%s            __skip_next=1\n%s            ;;\n",
			inner,
			strings.Join(exact, "|"),
			inner,
			inner,
		)
	}
	if len(equals) > 0 {
		fmt.Fprintf(
			sb,
			"%s        (%s)\n%s            ;;\n",
			inner,
			strings.Join(equals, "|"),
			inner,
		)
	}
	fmt.Fprintf(sb, "%s        (-*)\n%s            ;;\n", inner, inner)
	fmt.Fprintf(
		sb,
		"%s        (*)\n%s            __pos+=(\"$token\")\n%s            ;;\n",
		inner,
		inner,
		inner,
	)
	fmt.Fprintf(sb, "%s    esac\n", inner)
	fmt.Fprintf(sb, "%sdone\n", inner)
}

func zshWriteSpec(g *Generator, sb *strings.Builder, spec Spec, indent string) {
	help := zshEscapeHelp(spec.Terse)
	excl := zshExclusion(spec)
	if spec.Hidden {
		// Zsh's ! prefix parses a flag without offering it as a completion.
		excl = "!" + excl
	}

	if !spec.HasArg {
		if spec.ShortFlag != "" {
			fmt.Fprintf(sb, "%s'%s-%s[%s]' \\\n", indent, excl, spec.ShortFlag, help)
		}
		if spec.LongFlag != "" {
			fmt.Fprintf(sb, "%s'%s--%s[%s]' \\\n", indent, excl, spec.LongFlag, help)
		}
		return
	}

	completer := zshCompleter(g, spec)
	valueName := " "
	if spec.Dynamic != "" || len(spec.Values) > 0 || len(spec.ValueDescs) > 0 ||
		spec.Extension != "" ||
		spec.ValueHint != "" {
		valueName = spec.LongFlag
		if valueName == "" {
			valueName = spec.ShortFlag
		}
	}

	if spec.ShortFlag != "" {
		fmt.Fprintf(
			sb,
			"%s'%s-%s+[%s]:%s:%s' \\\n",
			indent,
			excl,
			spec.ShortFlag,
			help,
			valueName,
			completer,
		)
	}
	if spec.LongFlag != "" {
		fmt.Fprintf(
			sb,
			"%s'%s--%s=[%s]:%s:%s' \\\n",
			indent,
			excl,
			spec.LongFlag,
			help,
			valueName,
			completer,
		)
	}
}

func zshCompleter(g *Generator, spec Spec) string {
	switch {
	case CurrentWordPredictor(spec.Dynamic):
		return zshDynamicValuesAction(g, spec.Dynamic)
	case spec.CommaList && spec.Dynamic != "":
		return zshDynamicSequenceAction(g, spec.Dynamic)
	case spec.CommaList && len(spec.Values) > 0:
		escaped := make([]string, len(spec.Values))
		for i, v := range spec.Values {
			escaped[i] = zshEscapeValue(v)
		}
		return "{_sequence compadd - " + strings.Join(escaped, " ") + "}"
	case spec.Dynamic != "":
		return zshDynamicValuesAction(g, spec.Dynamic)
	case len(spec.ValueDescs) > 0:
		var parts []string
		for _, vd := range spec.ValueDescs {
			if vd.Desc != "" {
				parts = append(parts, zshEscapeValue(vd.Value)+`\:`+zshEscapeValue(vd.Desc))
			} else {
				parts = append(parts, zshEscapeValue(vd.Value))
			}
		}
		return "((" + strings.Join(parts, " ") + "))"
	case len(spec.Values) > 0:
		escaped := make([]string, len(spec.Values))
		for i, v := range spec.Values {
			escaped[i] = zshEscapeValue(v)
		}
		return "(" + strings.Join(escaped, " ") + ")"
	case spec.Extension != "":
		return `_files -g "` + zshExtGlob(spec.Extension) + `"`
	case spec.ValueHint != "":
		return zshHintCompleter(spec.ValueHint)
	default:
		return zshDefaultCompleter
	}
}

func zshDynamicValuesAction(g *Generator, predictor string) string {
	options := " -- "
	if CurrentWordPredictor(predictor) {
		options = ` -U -P "$IPREFIX" -- `
	}
	prelude := ""
	if CurrentWordPredictor(predictor) {
		prelude = `local __completion_word="${(Q)words[CURRENT]}"; [[ $IPREFIX == -*=* ]] && __completion_word="${__completion_word#*=}"; `
	}
	return `{ ` + prelude + `local -a items; items=(${(f)"` +
		zshDynamicExpr(g, predictor) +
		`"}); ` + zshDynamicValuesHelperName(g) + options + `"${(@)items}" }`
}

func zshDynamicSequenceAction(g *Generator, predictor string) string {
	return `{ local -a items; items=(${(f)"` +
		zshDynamicExpr(g, predictor) +
		`"}); _sequence ` + zshDynamicValuesHelperName(g) + ` - -- "${(@)items}" }`
}

func zshDynamicValuesHelperName(g *Generator) string {
	if len(g.DynamicArgs) == 0 && !anySpecInTree(g.Specs, g.Subs,
		func(s Spec) bool { return s.Dynamic != "" },
		func(sub SubSpec) bool { return len(sub.DynamicArgs) > 0 }) {
		return ""
	}
	return "_" + zshFuncName(g.AppName) + "_dynamic_values"
}

// Dynamic records contain a value followed by an optional tab description.
// Keep descriptions out of inserted values while preserving _sequence options.
func zshWriteDynamicValuesHelper(sb *strings.Builder, helperName string) {
	fmt.Fprintf(sb, "\n%s() {\n", helperName)
	fmt.Fprint(sb, `    local item value
    local -a options values descriptions
    while (( $# )) && [[ $1 != -- ]]; do
        options+=("$1")
        shift
    done
    (( $# )) && shift
    for item in "$@"; do
        value="${item%%$'\t'*}"
        if [[ $value == */ || $value == */\" ]]; then
            local -a directory_description=("$value")
            compadd "${(@)options}" -S '' -d directory_description -- "$value"
            continue
        fi
        values+=("${value}")
        if [[ $item == *$'\t'* && -n ${item#*$'\t'} ]]; then
            descriptions+=("${value} -- ${item#*$'\t'}")
        else
            descriptions+=("${value}")
        fi
    done
    compadd "${(@)options}" -d descriptions -a values
}
`)
}

// zshForwardHelperName returns the name of the shared forwarded-flags helper
// for g, or "" when no flag-value dynamic completion would consume it.
func zshForwardHelperName(g *Generator) string {
	if !forwardingActive(g) {
		return ""
	}
	return "_" + zshFuncName(g.AppName) + "_forwarded_flags"
}

// zshDynamicExpr returns the command substitution that produces dynamic flag
// completions, forwarding collected context flags when forwarding is active.
func zshDynamicExpr(g *Generator, predictor string) string {
	if CurrentWordPredictor(predictor) {
		forward := ""
		if zshForwardHelperName(g) != "" {
			forward = ` "${__fwd[@]}"`
		}
		return fmt.Sprintf(`$(%s --@shell=zsh --%s=%s -- "%s${__completion_word}"%s -- 2>/dev/null)`, g.AppName, FlagComplete, predictor, WordArgument, forward)
	}
	if zshForwardHelperName(g) != "" {
		return fmt.Sprintf(
			`$(%s --%s=%s -- "${__fwd[@]}" 2>/dev/null)`,
			g.AppName,
			FlagComplete,
			predictor,
		)
	}
	return fmt.Sprintf("$(%s --%s=%s)", g.AppName, FlagComplete, predictor)
}

// zshWriteForwardedFlagsHelper emits a function that scans the command line and
// populates __fwd with each forwardable context flag as --name=value.
func zshWriteForwardedFlagsHelper(sb *strings.Builder, helperName string, fwd []forwardSpec) {
	fmt.Fprintf(sb, "\n%s() {\n", helperName)
	fmt.Fprint(sb, "    local __skip_next=0 __fwd_name=\"\" token i\n")
	fmt.Fprint(sb, "    __fwd=()\n")
	fmt.Fprint(sb, "    for ((i=2; i<CURRENT; i++)); do\n")
	fmt.Fprint(sb, "        token=${(Q)words[i]}\n")
	fmt.Fprint(sb, "        if (( __skip_next )); then\n")
	fmt.Fprint(sb, "            if [[ -n \"${__fwd_name}\" ]]; then\n")
	fmt.Fprint(sb, "                __fwd+=(\"--${__fwd_name}=${token}\")\n")
	fmt.Fprint(sb, "                __fwd_name=\"\"\n")
	fmt.Fprint(sb, "            fi\n")
	fmt.Fprint(sb, "            __skip_next=0\n")
	fmt.Fprint(sb, "            continue\n")
	fmt.Fprint(sb, "        fi\n")
	fmt.Fprint(sb, "        if [[ $token == -- ]]; then\n")
	fmt.Fprint(sb, "            break\n")
	fmt.Fprint(sb, "        fi\n")
	fmt.Fprint(sb, "        case $token in\n")
	for _, f := range fwd {
		var patterns []string
		if f.LongFlag != "" {
			patterns = append(patterns, "--"+f.LongFlag)
		}
		if f.ShortFlag != "" {
			patterns = append(patterns, "-"+f.ShortFlag)
		}
		fmt.Fprintf(
			sb,
			"            (%s)\n                __skip_next=1\n                __fwd_name=%q\n                ;;\n",
			strings.Join(patterns, "|"),
			f.LongFlag,
		)
		if f.ShortFlag != "" && f.LongFlag != "" {
			fmt.Fprintf(
				sb,
				"            (--%s=*)\n                __fwd+=(\"$token\")\n                ;;\n",
				f.LongFlag,
			)
			fmt.Fprintf(
				sb,
				"            (-%s=*)\n                __fwd+=(\"--%s=${token#-%s=}\")\n                ;;\n",
				f.ShortFlag,
				f.LongFlag,
				f.ShortFlag,
			)
		} else if f.LongFlag != "" {
			fmt.Fprintf(
				sb,
				"            (--%s=*)\n                __fwd+=(\"$token\")\n                ;;\n",
				f.LongFlag,
			)
		}
	}
	fmt.Fprint(sb, "            (*)\n                ;;\n        esac\n    done\n")
	fmt.Fprint(sb, "}\n")
}

func zshWriteSubcommandCase(
	g *Generator,
	sb *strings.Builder,
	subs []SubSpec,
	inheritedSpecs []Spec,
	parent SubSpec,
	parentFuncName, indent string,
) {
	stateName := parent.Name
	snapshotName := zshSnapshotName(parentFuncName, stateName)
	inner := indent + "    "
	fmt.Fprintf(sb, `
%[1]scase $state in
%[1]s(%[2]s)
%[3]swords=("${(@)__parent_words_%[4]s[NORMARG,-1]}")
%[3]s(( CURRENT = __parent_current_%[4]s - NORMARG + 1 ))
%[3]scurcontext="${curcontext%%%%:*:*}:%[2]s-command-$line[1]:"
%[3]scase $line[1] in
`, indent, stateName, inner, snapshotName)

	for _, sub := range subs {
		names := append([]string{sub.Name}, sub.Aliases...)
		pattern := strings.Join(names, "|")

		caseIndent := inner + "    "
		bodyIndent := caseIndent + "    "
		fmt.Fprintf(sb, "%s(%s)\n", caseIndent, pattern)

		allSpecs := zshCompletionSpecs(appendSpecs(inheritedSpecs, sub.Specs))
		nextInherited := appendSpecs(inheritedSpecs, persistentSpecs(sub.Specs))
		sortedChildSubs := SortSubSpecs(sub.Subs)
		childFuncName := parentFuncName + "__" + zshFuncName(sub.Name)

		if len(sortedChildSubs) > 0 {
			zshWriteArguments(
				g,
				sb,
				allSpecs,
				sub.PathArgs,
				sub.DynamicArgs,
				sub.HasMaxPositionalArgs,
				sub.MaxPositionalArgs,
				childFuncName,
				sub.Name,
				bodyIndent,
			)
			parent := sub
			parent.Specs = allSpecs
			zshWriteSubcommandCase(
				g,
				sb,
				sortedChildSubs,
				nextInherited,
				parent,
				childFuncName,
				bodyIndent,
			)
		} else {
			zshWriteArguments(
				g,
				sb,
				allSpecs,
				sub.PathArgs,
				sub.DynamicArgs,
				sub.HasMaxPositionalArgs,
				sub.MaxPositionalArgs,
				"",
				"",
				bodyIndent,
			)
		}

		fmt.Fprintf(sb, "%s;;\n", caseIndent)
	}

	if parent.PathArgs || len(parent.DynamicArgs) > 0 {
		bodyIndent := inner + "        "
		fmt.Fprintf(sb, "%s    (*)\n", inner)
		fmt.Fprintf(sb, "%swords=(\"${__parent_words_%s[@]}\")\n", bodyIndent, snapshotName)
		fmt.Fprintf(sb, "%sCURRENT=$__parent_current_%s\n", bodyIndent, snapshotName)
		fmt.Fprintf(sb, "%scurcontext=\"$__parent_context_%s\"\n", bodyIndent, snapshotName)
		fmt.Fprintf(sb, "%sstate=\"\"\n%sline=()\n", bodyIndent, bodyIndent)
		zshWriteArguments(g, sb, parent.Specs, parent.PathArgs, parent.DynamicArgs,
			parent.HasMaxPositionalArgs, parent.MaxPositionalArgs, "", "", bodyIndent)
		fmt.Fprintf(sb, "%s    ;;\n", inner)
	}
	fmt.Fprintf(sb, "%[1]sesac\n%[2]s;;\n%[2]sesac\n", inner, indent)
}

func zshWriteDescribeTree(
	sb *strings.Builder,
	subs []SubSpec,
	funcName, displayName string,
) {
	zshWriteDescribeFunc(sb, subs, funcName, displayName)

	for _, sub := range subs {
		if len(sub.Subs) == 0 {
			continue
		}

		childFuncName := funcName + "__" + zshFuncName(sub.Name)
		childDisplayName := displayName + " " + sub.Name
		zshWriteDescribeTree(sb, SortSubSpecs(sub.Subs), childFuncName, childDisplayName)
	}
}

func zshWriteDescribeFunc(sb *strings.Builder, subs []SubSpec, funcName, displayName string) {
	fmt.Fprintf(
		sb,
		"\n(( $+functions[_%[1]s_commands] )) ||\n_%[1]s_commands() {\n    local commands; commands=(\n",
		funcName,
	)

	for _, sub := range subs {
		fmt.Fprintf(sb, "        '%s:%s' \\\n", sub.Name, zshEscapeHelp(sub.Terse))
	}

	fmt.Fprintf(
		sb,
		"    )\n    _describe -t commands '%s commands' commands \"$@\"\n}\n",
		displayName,
	)
}

func zshFuncName(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

func zshExclusion(spec Spec) string {
	if spec.ShortFlag != "" && spec.LongFlag != "" {
		return fmt.Sprintf("(-%s --%s)", spec.ShortFlag, spec.LongFlag)
	}
	return ""
}

func zshHintCompleter(hint string) string {
	switch hint {
	case HintFile:
		return "_files"
	case HintDir:
		return "_files -/"
	case HintCommand:
		return "_command_names -e"
	case HintUser:
		return "_users"
	case HintHost:
		return "_hosts"
	case HintURL:
		return "_urls"
	case HintEmail:
		return "_email_addresses"
	default:
		return zshDefaultCompleter
	}
}

func zshEscapeHelp(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `'\''`)
	s = strings.ReplaceAll(s, `[`, `\[`)
	s = strings.ReplaceAll(s, `]`, `\]`)
	s = strings.ReplaceAll(s, `:`, `\:`)
	s = strings.ReplaceAll(s, `$`, `\$`)
	s = strings.ReplaceAll(s, "`", "\\`")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func zshEscapeValue(s string) string {
	s = zshEscapeHelp(s)
	s = strings.ReplaceAll(s, `(`, `\(`)
	s = strings.ReplaceAll(s, `)`, `\)`)
	s = strings.ReplaceAll(s, ` `, `\ `)
	return s
}

func zshExtGlob(ext string) string {
	if !strings.Contains(ext, ",") {
		return "*." + ext
	}
	return "*.{" + ext + "}"
}

// zshCompletionSpecs keeps hidden forwarding metadata alongside visible flags.
// _arguments must recognize their values even though it must not suggest them.
func zshCompletionSpecs(specs []Spec) []Spec {
	result := SortVisibleSpecs(specs)
	for _, spec := range specs {
		if spec.Hidden {
			result = append(result, spec)
		}
	}
	return result
}

// zshSnapshotName is an injective encoding of the command scope. Shell-safe
// command names can contain dots and @, which are not valid variable names.
func zshSnapshotName(funcName, stateName string) string {
	return hex.EncodeToString([]byte(funcName + "\x00" + stateName))
}
