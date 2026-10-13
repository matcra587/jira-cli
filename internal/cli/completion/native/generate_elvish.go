// Backported from github.com/gechr/clib v0.7.17; licensed under the adjacent LICENSE.
package native

import (
	"fmt"
	"strings"

	xstrings "github.com/gechr/x/strings"
)

// GenerateElvish generates an Elvish completion script.
//
// Elvish arg-completers receive the whole command line as @words rather than a
// pre-tokenized prior list, so the generated script resolves a canonical
// "app;sub;subsub" command path itself (canonicalizing aliases at every depth),
// then branches on that path to emit candidates. Flag values are completed by
// inspecting the token preceding the cursor. Elvish applies its own prefix
// matcher to whatever the completer emits, so the script never filters by the
// seed word the way bash does.
func GenerateElvish(g *Generator) (string, error) {
	if err := ValidateGenerator(g); err != nil {
		return "", err
	}

	var sb strings.Builder
	id := elvID(g.AppName)

	fmt.Fprintf(&sb, "# %s Elvish completion\n\n", g.AppName)
	sb.WriteString("use str\n")
	sb.WriteString("use re\n")
	if hasCurrentWordPredictor(g) {
		elvWriteCurrentWordMatcher(&sb, id)
	}

	if forwardingActive(g) {
		elvWriteForwardedHelper(&sb, id, allForwardableSpecs(g))
	}
	if hasDynamicArgs(g) {
		elvWritePositionalsHelper(&sb, id)
	}

	contexts := elvCollect(g)
	hasValueFlag := hasValueFlag(g.Specs, g.Subs)

	fmt.Fprintf(&sb, "\nset edit:completion:arg-completer[%s] = {|@words|\n", elvQuote(g.AppName))
	sb.WriteString("    var n = (count $words)\n")
	sb.WriteString("    var cur = $words[-1]\n")
	sb.WriteString("    var prior = $words[1..(- $n 1)]\n")
	if hasCurrentWordPredictor(g) {
		sb.WriteString("    var inlineflag = ''\n")
		fmt.Fprintf(&sb, "    set _%s_word_matches = []\n    set _%s_word_seed = $cur\n    set _%s_word_command = $edit:current-command\n", id, id, id)
	}

	fmt.Fprintf(&sb, "    var command = %s\n", elvQuote(g.AppName))
	if len(g.Subs) > 0 {
		elvWriteResolution(&sb, g)
	}

	if hasValueFlag {
		sb.WriteString("    var prev = ''\n")
		sb.WriteString("    if (> (count $prior) 0) { set prev = $prior[-1] }\n")
	}

	sb.WriteString("\n")
	for i, ctx := range contexts {
		elvWriteArm(&sb, g, ctx, i == 0)
	}
	if len(contexts) > 0 {
		sb.WriteString("    }\n")
	}
	sb.WriteString("}\n")

	return sb.String(), nil
}

// Elvish exposes one matcher for all argument completers. Preserve its selected
// matcher and its complete input batch; admit root-matched replacement values
// only for the exact current command and seed that produced those values.
func elvWriteCurrentWordMatcher(sb *strings.Builder, id string) {
	fmt.Fprintf(sb, `
var _%[1]s_word_matches = []
var _%[1]s_word_seed = ''
var _%[1]s_word_command = ''
var _%[1]s_argument_matcher = $edit:match-prefix~
if (has-key $edit:completion:matcher '') { set _%[1]s_argument_matcher = $edit:completion:matcher[''] }
if (has-key $edit:completion:matcher argument) { set _%[1]s_argument_matcher = $edit:completion:matcher[argument] }
set edit:completion:matcher[argument] = {|seed|
    var candidates = [(all)]
    var matched = [(put $@candidates | $_%[1]s_argument_matcher $seed)]
    var active = (and (eq $seed $_%[1]s_word_seed) (eq $edit:current-command $_%[1]s_word_command))
    for i [(range (count $candidates))] {
        put (or $matched[$i] (and $active (has-value $_%[1]s_word_matches $candidates[$i])))
    }
}
fn _%[1]s_word_candidates {|&prefix=''|
    each {|candidate|
        var parts = [(str:split "\t" $candidate)]
        var value = $prefix$parts[0]
        set _%[1]s_word_matches = [$@_%[1]s_word_matches $value]
        if (> (count $parts) 1) {
            edit:complex-candidate $value &display=$candidate
        } else { put $value }
    }
}
`, id)
}

// elvID munges an app name into a token usable in Elvish function names,
// replacing every character outside [A-Za-z0-9_] with an underscore.
func elvID(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			return r
		default:
			return '_'
		}
	}, name)
}

// elvQuote renders s as an Elvish single-quoted string literal, doubling
// embedded single quotes and folding whitespace so the result stays on one line.
func elvQuote(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "'", "''")
	return "'" + s + "'"
}

// elvCand emits a single completion candidate. A bare value uses put; a value
// with a description uses edit:complex-candidate so the menu shows the
// description alongside the inserted stem.
func elvCand(sb *strings.Builder, indent, stem, desc string) {
	if xstrings.IsBlank(desc) {
		fmt.Fprintf(sb, "%sput %s\n", indent, elvQuote(stem))
		return
	}
	fmt.Fprintf(
		sb,
		"%sedit:complex-candidate %s &display=%s\n",
		indent,
		elvQuote(stem),
		elvQuote(stem+"  "+desc),
	)
}

// elvContext is a single command path and the completions valid at it.
type elvContext struct {
	path        string
	specs       []Spec
	subs        []SubSpec
	pathArgs    bool
	dynamicArgs []string
	hasMax      bool
	maxPos      int
	depth       int
}

func elvContextHasContent(ctx elvContext) bool {
	return len(ctx.specs) > 0 || len(ctx.subs) > 0 || len(ctx.dynamicArgs) > 0 || ctx.pathArgs
}

// elvCollect flattens the generator tree into the ordered list of command
// contexts to emit, mirroring the root-then-depth-first order pwsh uses.
func elvCollect(g *Generator) []elvContext {
	var ctxs []elvContext
	root := elvContext{
		path:        g.AppName,
		specs:       combineVisibleSpecs(g.Specs),
		subs:        g.Subs,
		dynamicArgs: g.DynamicArgs,
		hasMax:      g.HasMaxPositionalArgs,
		maxPos:      g.MaxPositionalArgs,
		depth:       1,
	}
	if elvContextHasContent(root) {
		ctxs = append(ctxs, root)
	}
	elvCollectSubs(
		&ctxs,
		g.AppName,
		g.Subs,
		persistentSpecs(g.Specs),
		2, //nolint:mnd // depth 2 = first subcommand level
	)
	return ctxs
}

func elvCollectSubs(
	ctxs *[]elvContext,
	parentPath string,
	subs []SubSpec,
	inherited []Spec,
	depth int,
) {
	for _, sub := range SortSubSpecs(subs) {
		childPath := parentPath + ";" + sub.Name
		ctx := elvContext{
			path:        childPath,
			specs:       combineVisibleSpecs(inherited, sub.Specs),
			subs:        sub.Subs,
			pathArgs:    sub.PathArgs,
			dynamicArgs: sub.DynamicArgs,
			hasMax:      sub.HasMaxPositionalArgs,
			maxPos:      sub.MaxPositionalArgs,
			depth:       depth,
		}
		if elvContextHasContent(ctx) {
			*ctxs = append(*ctxs, ctx)
		}
		if len(sub.Subs) == 0 {
			continue
		}
		next := appendSpecs(inherited, persistentSpecs(sub.Specs))
		elvCollectSubs(ctxs, childPath, sub.Subs, next, depth+1)
	}
}

// elvWriteResolution emits the command-path resolution loop: it walks the prior
// barewords, skips flags, and canonicalizes each subcommand (including aliases)
// into the running "app;sub" path, stopping at the first non-subcommand word.
func elvWriteResolution(sb *strings.Builder, g *Generator) {
	sb.WriteString("    var resolveskip = $false\n")
	sb.WriteString("    for w $prior {\n")
	sb.WriteString("        if $resolveskip { set resolveskip = $false; continue }\n")
	fmt.Fprintf(sb, "        if (has-value [%s] $w) { set resolveskip = $true; continue }\n", elvValueFlagList(g.Specs))
	sb.WriteString("        if (eq $w '--') { break }\n")
	sb.WriteString("        if (str:has-prefix $w '-') { continue }\n")
	sb.WriteString("        var next = ''\n")
	elvWriteTransitions(sb, g.AppName, g.Subs, "        ")
	sb.WriteString("        if (eq $next '') { break }\n")
	sb.WriteString("        set command = $next\n")
	sb.WriteString("    }\n")
}

// elvWriteTransitions emits the subcommand canonicalization rules as independent
// if statements. Each rule matches a unique (command, word) pair, so at most one
// fires per loop iteration - no elif chaining is needed (and a newline-separated
// elif would be misparsed as a command in Elvish).
func elvWriteTransitions(sb *strings.Builder, parentPath string, subs []SubSpec, indent string) {
	for _, sub := range SortSubSpecs(subs) {
		childPath := parentPath + ";" + sub.Name
		names := append([]string{sub.Name}, sub.Aliases...)
		var checks []string
		for _, name := range names {
			checks = append(checks, fmt.Sprintf("(eq $w %s)", elvQuote(name)))
		}
		cond := checks[0]
		if len(checks) > 1 {
			cond = "(or " + strings.Join(checks, " ") + ")"
		}
		fmt.Fprintf(
			sb,
			"%sif (and (eq $command %s) %s) { set next = %s }\n",
			indent,
			elvQuote(parentPath),
			cond,
			elvQuote(childPath),
		)
		if len(sub.Subs) > 0 {
			elvWriteTransitions(sb, childPath, sub.Subs, indent)
		}
	}
}

// elvWriteArm emits one command-path branch as part of an if/elif chain. The
// caller closes the chain with a final "}". Value-taking flags route through a
// $prev check; otherwise the branch lists flags, subcommands, and positional
// completions.
func elvWriteArm(sb *strings.Builder, g *Generator, ctx elvContext, first bool) {
	if first {
		fmt.Fprintf(sb, "    if (eq $command %s) {\n", elvQuote(ctx.path))
	} else {
		fmt.Fprintf(sb, "    } elif (eq $command %s) {\n", elvQuote(ctx.path))
	}

	var valueFlags []Spec
	for _, spec := range ctx.specs {
		if spec.HasArg && !spec.Hidden {
			valueFlags = append(valueFlags, spec)
		}
	}

	if len(valueFlags) > 0 {
		for _, spec := range valueFlags {
			if !CurrentWordPredictor(spec.Dynamic) {
				continue
			}
			for _, flag := range []string{"--" + spec.LongFlag, "-" + spec.ShortFlag} {
				if flag == "--" || flag == "-" {
					continue
				}
				prefix := elvQuote(flag + "=")
				fmt.Fprintf(sb, "        if (str:has-prefix $cur %s) { set prev = %s; set inlineflag = %s; set cur = (str:trim-prefix $cur %s) }\n", prefix, elvQuote(flag), prefix, prefix)
			}
		}
		for i, spec := range valueFlags {
			if i == 0 {
				fmt.Fprintf(sb, "        if %s {\n", elvPrevCond(spec))
			} else {
				fmt.Fprintf(sb, "        } elif %s {\n", elvPrevCond(spec))
			}
			elvWriteValueClause(sb, g, spec, "            ")
		}
		sb.WriteString("        } else {\n")
		elvWriteListing(sb, g, ctx, "            ")
		sb.WriteString("        }\n")
	} else {
		elvWriteListing(sb, g, ctx, "        ")
	}
}

// elvPrevCond builds the $prev test that matches any spelling of a flag.
func elvPrevCond(spec Spec) string {
	var checks []string
	if spec.LongFlag != "" {
		checks = append(checks, fmt.Sprintf("(eq $prev %s)", elvQuote("--"+spec.LongFlag)))
	}
	if spec.ShortFlag != "" {
		checks = append(checks, fmt.Sprintf("(eq $prev %s)", elvQuote("-"+spec.ShortFlag)))
	}
	if len(checks) == 1 {
		return checks[0]
	}
	return "(or " + strings.Join(checks, " ") + ")"
}

// elvWriteValueClause emits the completion for a single flag's value, mirroring
// the bash/fish/pwsh value-hint handling (file, dir, command, extension), enum
// values, value descriptions, comma lists, and dynamic predictors.
func elvWriteValueClause(sb *strings.Builder, g *Generator, spec Spec, indent string) {
	switch {
	case CurrentWordPredictor(spec.Dynamic):
		fmt.Fprintf(sb, "%stry { %s } catch _ { }\n", indent, elvDynamicPipe(g, spec.Dynamic))
	case spec.CommaList && spec.Dynamic != "":
		elvWriteCommaPrefix(sb, indent)
		fmt.Fprintf(sb, "%stry {\n", indent)
		fmt.Fprintf(sb, "%s    %s | each {|v|\n", indent, elvDynamicPipe(g, spec.Dynamic))
		fmt.Fprintf(sb, "%s        if (not (has-value $selected $v)) { put $prefix$v }\n", indent)
		fmt.Fprintf(sb, "%s    }\n", indent)
		fmt.Fprintf(sb, "%s} catch _ { }\n", indent)
	case spec.CommaList && len(spec.ValueDescs) > 0:
		elvWriteCommaStatic(sb, valueDescStrings(spec.ValueDescs), indent)
	case spec.CommaList && len(spec.Values) > 0:
		elvWriteCommaStatic(sb, spec.Values, indent)
	case spec.Dynamic != "":
		fmt.Fprintf(sb, "%stry { %s } catch _ { }\n", indent, elvDynamicPipe(g, spec.Dynamic))
	case len(spec.ValueDescs) > 0:
		for _, vd := range spec.ValueDescs {
			elvCand(sb, indent, vd.Value, vd.Desc)
		}
	case len(spec.Values) > 0:
		for _, v := range spec.Values {
			elvCand(sb, indent, v, "")
		}
	case spec.Extension != "":
		elvWriteExtension(sb, spec.Extension, indent)
	case spec.ValueHint == HintFile:
		fmt.Fprintf(sb, "%sedit:complete-filename $cur\n", indent)
	case spec.ValueHint == HintDir:
		fmt.Fprintf(sb, "%sedit:complete-dirname $cur\n", indent)
	case spec.ValueHint == HintCommand:
		// Elvish exposes no command-name completer, so fall back to filenames.
		fmt.Fprintf(sb, "%sedit:complete-filename $cur\n", indent)
	default:
		// No completion source: emit nothing and let the value be free-form.
		fmt.Fprintf(sb, "%snop\n", indent)
	}
}

// elvWriteExtension completes filenames, keeping directories (to navigate into)
// and files whose name ends with one of the configured extensions.
func elvWriteExtension(sb *strings.Builder, ext, indent string) {
	var checks []string
	checks = append(checks, "(str:has-suffix $s '/')")
	for part := range strings.SplitSeq(ext, ",") {
		part = strings.TrimSpace(part)
		checks = append(checks, fmt.Sprintf("(str:has-suffix $s %s)", elvQuote("."+part)))
	}
	cond := "(or " + strings.Join(checks, " ") + ")"
	fmt.Fprintf(sb, "%sedit:complete-filename $cur | each {|c|\n", indent)
	fmt.Fprintf(sb, "%s    var s = $c\n", indent)
	fmt.Fprintf(sb, "%s    if (not-eq (kind-of $c) string) { set s = $c[stem] }\n", indent)
	fmt.Fprintf(sb, "%s    if %s { put $c }\n", indent, cond)
	fmt.Fprintf(sb, "%s}\n", indent)
}

// elvWriteCommaPrefix emits the shared comma-list setup: it captures the prefix
// already typed (everything up to the final comma) and the set of values
// already chosen, so they can be skipped and re-prefixed.
func elvWriteCommaPrefix(sb *strings.Builder, indent string) {
	fmt.Fprintf(sb, "%svar prefix = ''\n", indent)
	fmt.Fprintf(
		sb,
		"%sif (re:match ',' $cur) { set prefix = (re:replace '[^,]*$' '' $cur) }\n",
		indent,
	)
	fmt.Fprintf(sb, "%svar selected = [(str:split ',' $prefix)]\n", indent)
}

func elvWriteCommaStatic(sb *strings.Builder, values []string, indent string) {
	elvWriteCommaPrefix(sb, indent)
	fmt.Fprintf(sb, "%sfor v [%s] {\n", indent, elvQuotedList(values))
	fmt.Fprintf(sb, "%s    if (not (has-value $selected $v)) { put $prefix$v }\n", indent)
	fmt.Fprintf(sb, "%s}\n", indent)
}

// elvWriteListing emits flag-name and subcommand candidates, plus file or
// dynamic positional completions, for a command's positional context.
func elvWriteListing(sb *strings.Builder, g *Generator, ctx elvContext, indent string) {
	for _, spec := range ctx.specs {
		if spec.Hidden {
			continue
		}
		if spec.LongFlag != "" {
			elvCand(sb, indent, "--"+spec.LongFlag, spec.Terse)
		}
		if spec.ShortFlag != "" {
			elvCand(sb, indent, "-"+spec.ShortFlag, spec.Terse)
		}
	}
	for _, sub := range SortSubSpecs(ctx.subs) {
		elvCand(sb, indent, sub.Name, sub.Terse)
	}

	if ctx.pathArgs {
		fmt.Fprintf(sb, "%sedit:complete-filename $cur\n", indent)
	}
	if len(ctx.dynamicArgs) > 0 {
		elvWriteDynArgs(sb, g, ctx, indent)
	}
}

// elvWriteDynArgs emits positional dynamic-completion logic: it counts the real
// positionals already on the line, selects the matching slot, and invokes the
// handler with forwarded context flags and preceding positionals.
func elvWriteDynArgs(sb *strings.Builder, g *Generator, ctx elvContext, indent string) {
	id := elvID(g.AppName)
	forward := forwardingActive(g)
	cmdSkip := ctx.depth - 1

	fmt.Fprintf(
		sb,
		"%svar positional = [(_%s_positionals %d [%s] $@prior)]\n",
		indent,
		id,
		cmdSkip,
		elvValueFlagList(ctx.specs),
	)
	fmt.Fprintf(sb, "%svar ncount = (count $positional)\n", indent)

	body := indent
	if ctx.hasMax {
		fmt.Fprintf(sb, "%sif (< $ncount %d) {\n", indent, ctx.maxPos)
		body = indent + "    "
	}

	fmt.Fprintf(sb, "%svar kind = ''\n", body)
	fmt.Fprintf(sb, "%svar first = $false\n", body)
	last := ctx.dynamicArgs[len(ctx.dynamicArgs)-1]
	fmt.Fprintf(sb, "%s", body)
	for i, da := range ctx.dynamicArgs {
		if i == 0 {
			fmt.Fprintf(
				sb,
				"if (eq $ncount %d) { set kind = %s; set first = $true }",
				i,
				elvQuote(da),
			)
		} else {
			fmt.Fprintf(sb, " elif (eq $ncount %d) { set kind = %s }", i, elvQuote(da))
		}
	}
	fmt.Fprintf(sb, " else { set kind = %s }\n", elvQuote(last))

	fmt.Fprintf(sb, "%svar callargs = ['--%s='$kind]\n", body, FlagComplete)
	fmt.Fprintf(sb, "%sif (str:has-prefix $kind 'word_') {\n", body)
	fmt.Fprintf(sb, "%s    set callargs = ['--@shell=elvish' '--%s='$kind '--' '%s'$cur]\n", body, FlagComplete, WordArgument)
	if forward {
		fmt.Fprintf(sb, "%s    set callargs = [$@callargs (_%s_forwarded_flags $@prior)]\n", body, id)
	}
	fmt.Fprintf(sb, "%s    set callargs = [$@callargs '--']\n%s    if (not $first) { set callargs = [$@callargs $@positional] }\n%s} else {\n", body, body, body)
	switch {
	case forward:
		fmt.Fprintf(
			sb,
			"%sset callargs = [$@callargs '--' (_%s_forwarded_flags $@prior)]\n",
			body,
			id,
		)
		fmt.Fprintf(sb, "%sif (not $first) { set callargs = [$@callargs $@positional] }\n", body)
	default:
		fmt.Fprintf(
			sb,
			"%sif (not $first) { set callargs = [$@callargs '--' $@positional] }\n",
			body,
		)
	}
	fmt.Fprintf(sb, "%s}\n", body)
	pipe := ""
	if hasCurrentWordPredictor(g) {
		pipe = fmt.Sprintf(" | each {|candidate| if (str:has-prefix $kind 'word_') { put $candidate | _%s_word_candidates } else { put $candidate } }", id)
	}
	fmt.Fprintf(
		sb,
		"%stry { (external %s) $@callargs 2>/dev/null | from-lines%s } catch _ { }\n",
		body,
		elvQuote(g.AppName),
		pipe,
	)

	if ctx.hasMax {
		fmt.Fprintf(sb, "%s}\n", indent)
	}
}

// elvDynamicPipe builds the pipeline that asks the app for dynamic flag-value
// completions, appending forwarded context flags when forwarding is active.
func elvDynamicPipe(g *Generator, predictor string) string {
	if CurrentWordPredictor(predictor) {
		call := fmt.Sprintf("(external %s) '--@shell=elvish' %s -- '%s'$cur", elvQuote(g.AppName), elvQuote("--"+FlagComplete+"="+predictor), WordArgument)
		if forwardingActive(g) {
			call += fmt.Sprintf(" (_%s_forwarded_flags $@prior)", elvID(g.AppName))
		}
		return call + fmt.Sprintf(" -- 2>/dev/null | from-lines | _%s_word_candidates &prefix=$inlineflag", elvID(g.AppName))
	}
	call := fmt.Sprintf(
		"(external %s) %s",
		elvQuote(g.AppName),
		elvQuote("--"+FlagComplete+"="+predictor),
	)
	if forwardingActive(g) {
		call += fmt.Sprintf(" -- (_%s_forwarded_flags $@prior)", elvID(g.AppName))
	}
	return call + " 2>/dev/null | from-lines"
}

// elvValueFlagList renders the value-taking flags of specs as an Elvish list
// body (space-separated, no brackets), used by the positional scanner to skip a
// flag and its value.
func elvValueFlagList(specs []Spec) string {
	var tokens []string
	for _, spec := range specs {
		if !spec.HasArg {
			continue
		}
		if spec.LongFlag != "" {
			tokens = append(tokens, elvQuote("--"+spec.LongFlag))
		}
		if spec.ShortFlag != "" {
			tokens = append(tokens, elvQuote("-"+spec.ShortFlag))
		}
	}
	return strings.Join(tokens, " ")
}

func elvQuotedList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = elvQuote(v)
	}
	return strings.Join(quoted, " ")
}

func valueDescStrings(descs []ValueDesc) []string {
	values := make([]string, len(descs))
	for i, vd := range descs {
		values[i] = vd.Value
	}
	return values
}

// elvWriteForwardedHelper emits a helper that scans the tokens for forwardable
// context flags and puts them normalized as --name=value, stopping at "--".
func elvWriteForwardedHelper(sb *strings.Builder, id string, fwd []forwardSpec) {
	fmt.Fprintf(sb, "\nfn _%s_forwarded_flags {|@tokens|\n", id)
	sb.WriteString("    var skipnext = $false\n")
	sb.WriteString("    var name = ''\n")
	sb.WriteString("    for t $tokens {\n")
	sb.WriteString("        if $skipnext {\n")
	sb.WriteString("            if (not-eq $name '') { put '--'$name'='$t; set name = '' }\n")
	sb.WriteString("            set skipnext = $false\n")
	sb.WriteString("            continue\n")
	sb.WriteString("        }\n")
	sb.WriteString("        if (eq $t '--') { break }\n")
	for _, f := range fwd {
		var bare []string
		if f.LongFlag != "" {
			bare = append(bare, fmt.Sprintf("(eq $t %s)", elvQuote("--"+f.LongFlag)))
		}
		if f.ShortFlag != "" {
			bare = append(bare, fmt.Sprintf("(eq $t %s)", elvQuote("-"+f.ShortFlag)))
		}
		cond := bare[0]
		if len(bare) > 1 {
			cond = "(or " + strings.Join(bare, " ") + ")"
		}
		fmt.Fprintf(
			sb,
			"        if %s { set skipnext = $true; set name = %s; continue }\n",
			cond,
			elvQuote(f.LongFlag),
		)
		if f.LongFlag != "" {
			eq := "--" + f.LongFlag + "="
			fmt.Fprintf(
				sb,
				"        if (str:has-prefix $t %s) { put $t; continue }\n",
				elvQuote(eq),
			)
		}
		if f.ShortFlag != "" && f.LongFlag != "" {
			shortEq := "-" + f.ShortFlag + "="
			longEq := "--" + f.LongFlag + "="
			fmt.Fprintf(
				sb,
				"        if (str:has-prefix $t %s) { put %s(str:trim-prefix $t %s); continue }\n",
				elvQuote(shortEq),
				elvQuote(longEq),
				elvQuote(shortEq),
			)
		}
	}
	sb.WriteString("    }\n")
	sb.WriteString("}\n")
}

// elvWritePositionalsHelper emits a helper that extracts the real positional
// arguments from the tokens, skipping flags, their values, the leading
// subcommand tokens (cmdskip), and honoring the "--" terminator.
const elvPositionalsHelper = `
fn _%s_positionals {|cmdskip valueflags @tokens|
    var positional = []
    var skipnext = $false
    var dashdash = $false
    var skip = $cmdskip
    for t $tokens {
        if $dashdash { set positional = [$@positional $t]; continue }
        if $skipnext { set skipnext = $false; continue }
        if (eq $t '--') { set dashdash = $true; continue }
        if (has-value $valueflags $t) { set skipnext = $true; continue }
        if (str:has-prefix $t '-') { continue }
        if (> $skip 0) { set skip = (- $skip 1); continue }
        set positional = [$@positional $t]
    }
    put $@positional
}
`

func elvWritePositionalsHelper(sb *strings.Builder, id string) {
	fmt.Fprintf(sb, elvPositionalsHelper, id)
}
