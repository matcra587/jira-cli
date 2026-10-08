package native

import (
	"fmt"
	"strings"

	xstrings "github.com/gechr/x/strings"
)

// GeneratePwsh generates a PowerShell completion script.
//
// PowerShell native completers receive the raw command-line AST rather than a
// pre-tokenized word list, so the generated script walks $commandAst itself: it
// first resolves a canonical "app;sub;subsub" command path (canonicalizing
// aliases at every depth), then switches on that path to emit candidates. Flag
// values are completed by inspecting the token preceding the cursor.
func GeneratePwsh(g *Generator) (string, error) {
	if err := ValidateGenerator(g); err != nil {
		return "", err
	}

	var sb strings.Builder
	id := pwshID(g.AppName)

	fmt.Fprintf(&sb, "# %s PowerShell completion\n\n", g.AppName)
	sb.WriteString("using namespace System.Management.Automation\n")
	sb.WriteString("using namespace System.Management.Automation.Language\n")

	pwshWriteLiteralHelpers(&sb, id)

	pwshWriteTokensHelper(&sb, id)
	if forwardingActive(g) {
		pwshWriteForwardedHelper(&sb, g)
	}
	if hasDynamicArgs(g) {
		pwshWritePositionalsHelper(&sb, id)
	}

	fmt.Fprintf(
		&sb,
		"\nRegister-ArgumentCompleter -Native -CommandName '%s' -ScriptBlock {\n",
		g.AppName,
	)
	sb.WriteString("    param($wordToComplete, $commandAst, $cursorPosition)\n\n")
	fmt.Fprintf(&sb, "    if (-not (__%s_ContextIsLiteral $commandAst $wordToComplete $cursorPosition)) { return }\n", id)
	sb.WriteString("    $savedExitCode = Get-Variable LASTEXITCODE -Scope Global -ErrorAction Ignore\n    $exitCodeValue = if ($null -ne $savedExitCode) { $savedExitCode.Value } else { $null }\n    try {\n    $inlineFlag = ''; $inlineText = ''; $inlineReplacement = $false; $filterWord = $null\n")
	fmt.Fprintf(&sb, "    $command = '%s'\n", g.AppName)

	pwshWriteValueFlagScopes(&sb, g)
	if len(g.Subs) > 0 {
		pwshWriteResolution(&sb, g)
	}
	pwshWritePrev(&sb, id)

	needFiles := hasFileCompletion(g)
	sb.WriteString("\n")
	if needFiles {
		// File/dir/command hints are collected here rather than emitted into
		// $completions: a `return` inside the @(switch ...) assignment is
		// swallowed, and their results must bypass the prefix filter applied to
		// flag and value candidates below.
		sb.WriteString("    $fileCompletions = $null\n")
	}
	sb.WriteString("    $completions = @(switch ($command) {\n")
	pwshWriteCommandCase(
		&sb,
		g,
		g.AppName,
		combineVisibleSpecs(g.Specs),
		g.Subs,
		false,
		g.DynamicArgs,
		g.HasMaxPositionalArgs,
		g.MaxPositionalArgs,
		1,
	)
	if len(g.Subs) > 0 {
		pwshWriteSubCases(
			&sb,
			g,
			g.AppName,
			g.Subs,
			persistentSpecs(g.Specs),
			2, //nolint:mnd // depth 2 = first subcommand level
		)
	}
	sb.WriteString("    })\n\n")

	if needFiles {
		sb.WriteString("    if ($null -ne $fileCompletions) {\n")
		sb.WriteString("        $fileCompletions | ForEach-Object { __" + id + "_InlineResult $_ $(if ($inlineReplacement) { $inlineFlag } else { '' }) }\n")
		sb.WriteString("    }\n")
	}
	sb.WriteString(
		"    if ($null -eq $filterWord) { $filterWord = __" + id + "_LiteralPrefix $wordToComplete }\n" +
			"    $completions | Where-Object { $_.ListItemText.StartsWith($filterWord, [StringComparison]::OrdinalIgnoreCase) } |\n",
	)
	sb.WriteString("        Sort-Object -Property ListItemText | ForEach-Object { __" + id + "_InlineResult $_ $(if ($inlineReplacement) { $inlineFlag } else { '' }) }\n")
	sb.WriteString("    } finally {\n        if ($null -ne $savedExitCode) { Set-Variable LASTEXITCODE -Scope Global -Value $exitCodeValue } else { Remove-Variable LASTEXITCODE -Scope Global -ErrorAction Ignore }\n    }\n}\n")

	return sb.String(), nil
}

func pwshID(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

func pwshInsertionExpr(id, value, resultType string) string {
	if resultType == "ParameterName" {
		return pwshQuote(value)
	}
	return fmt.Sprintf("(__%s_QuoteValue %s)", id, pwshQuote(value))
}

// Literal values are quoted for insertion, while matching and display retain
// the original identity. No PowerShell expression is evaluated to find a value.
func pwshWriteLiteralHelpers(sb *strings.Builder, id string) {
	fmt.Fprintf(sb, `
function __%[1]s_QuoteValue {
    param([string]$Value)
    "'" + $Value.Replace("'", "''") + "'"
}

function __%[1]s_LiteralPrefix {
    param([string]$Word)
    if ($Word.StartsWith("'")) {
        $tail = $Word.Substring(1)
        $quotes = $tail.Length - $tail.TrimEnd("'").Length
        if (($quotes %% 2) -eq 1) { $tail = $tail.Substring(0, $tail.Length - 1) }
        return $tail.Replace("''", "'")
    }
    if ($Word.StartsWith('"')) {
        $tail = $Word.Substring(1)
        if ($tail.EndsWith('"') -and -not $tail.EndsWith('`+"`"+`"')) { $tail = $tail.Substring(0, $tail.Length - 1) }
        return $tail.Replace('`+"`"+`"', '"').Replace('`+"``"+`', '`+"`"+`')
    }
    $Word
}

function __%[1]s_DynamicResult {
    param([string]$Line, [string]$Prefix = '')
    $parts = $Line -split "`+"`t"+`", 2
    $value = $parts[0]
    if ($value.Length -eq 0) { return }
    $tooltip = $value
    if ($parts.Count -gt 1 -and $parts[1].Length -gt 0) { $tooltip = $parts[1] }
    [CompletionResult]::new((__%[1]s_QuoteValue ($Prefix + $value)), ($Prefix + $value), [CompletionResultType]::ParameterValue, $tooltip)
}

function __%[1]s_InlineResult {
    param($Result, [string]$Flag)
    if ($Flag.Length -eq 0) { return $Result }
    [CompletionResult]::new(($Flag + $Result.CompletionText), $Result.ListItemText, $Result.ResultType, $Result.ToolTip)
}

function __%[1]s_CompletedElements {
    param($CommandAst, $WordToComplete, $CursorPosition)
    $elements = $CommandAst.CommandElements
    for ($i = 1; $i -lt $elements.Count; $i++) {
        $element = $elements[$i]
        if ($element.Extent.EndOffset -ge $CursorPosition) { break }
        $tailStart = $element.Extent.EndOffset - $CommandAst.Extent.StartOffset
        $tailLength = [Math]::Min($CursorPosition - $element.Extent.EndOffset, $CommandAst.Extent.Text.Length - $tailStart)
        if ($tailLength -gt 0 -and $CommandAst.Extent.Text.Substring($tailStart, $tailLength) -match '^,[^\s]*$') { continue }
        $element
    }
}

function __%[1]s_ContextIsLiteral {
    param($CommandAst, $WordToComplete, $CursorPosition)
    foreach ($element in @(__%[1]s_CompletedElements $CommandAst $WordToComplete $CursorPosition)) {
        if ($element -is [ArrayLiteralAst] -and (__%[1]s_ArrayIsLiteral $element)) { continue }
        if ($element -is [CommandParameterAst] -and $null -eq $element.Argument) { continue }
        if ($element -isnot [StringConstantExpressionAst] -and $element -isnot [ConstantExpressionAst]) { return $false }
    }
    $true
}

function __%[1]s_ArrayIsLiteral {
    param($Element)
    $joined = @($Element.Elements | ForEach-Object { $_.Extent.Text }) -join ','
    if ($Element.Extent.Text -cne $joined) { return $false }
    foreach ($member in $Element.Elements) {
        if ($member -isnot [StringConstantExpressionAst] -and $member -isnot [ConstantExpressionAst]) { return $false }
        # Only bare members have a predictable single native CSV argument.
        # Quoted members can retain authored quotes and split native argv.
        if ($member.Extent.Text -cne [string]$member.Value -or [string]$member.Value -cnotmatch '^[\p{L}\p{N}\p{M}._:/\\=-]+$') { return $false }
    }
    $true
}

function __%[1]s_CSVContext {
    param($CommandAst, $WordToComplete, $CursorPosition, [string]$InlineFlag, [string]$InlineText)
    $word = __%[1]s_LiteralPrefix $WordToComplete
    $argument = $word
    foreach ($element in $CommandAst.CommandElements) {
        if ($element.Extent.StartOffset -le $CursorPosition -and $element.Extent.EndOffset -ge $CursorPosition) {
            $text = $element.Extent.Text.Substring(0, $CursorPosition - $element.Extent.StartOffset)
            $argument = __%[1]s_LiteralPrefix $text
            if ($InlineFlag.Length -gt 0 -and $argument.StartsWith($InlineFlag)) { $argument = __%[1]s_LiteralPrefix $argument.Substring($InlineFlag.Length) }
        }
    }
    if ($InlineFlag.Length -gt 0) { $argument = __%[1]s_LiteralPrefix $InlineText.Substring($InlineFlag.Length) }
    $prefix = ''; $selected = @(); $field = ''; $quoted = $false; $closed = $false
    for ($i = 0; $i -lt $argument.Length; $i++) {
        $character = $argument[$i]
        if ($character -eq '"') {
            if ($quoted -and $i + 1 -lt $argument.Length -and $argument[$i + 1] -eq '"') { $field += '"'; $i++; continue }
            if ($quoted) { $quoted = $false; $closed = $true; continue }
            if ($field.Length -ne 0 -or $closed) { return @{Valid=$false} }
            $quoted = $true; continue
        }
        if ($character -eq ',' -and -not $quoted) {
            $selected += $field; $field = ''; $closed = $false
            $prefix = $argument.Substring(0, $i + 1); continue
        }
        if ($closed) { return @{Valid=$false} }
        $field += $character
    }
    @{Valid=$true; Prefix=$prefix; Selected=$selected; Field=$field; Whole=($word -eq $argument)}
}

function __%[1]s_CSVResult {
    param([string]$Value, [string]$Tooltip, $Context)
    if (-not $Context.Valid -or $Value.Length -eq 0 -or $Context.Selected -ccontains $Value) { return }
    $encoded = $Value
    if ($Value.Contains(',') -or $Value.Contains('"')) { $encoded = '"' + $Value.Replace('"', '""') + '"' }
    if ($Context.Whole) {
        $display = $Context.Prefix + $encoded
        $text = __%[1]s_QuoteValue $display
    } else {
        # Native PowerShell replaces only the final bare comma member. It
        # cannot quote an earlier prefix; unsafe members require a quoted
        # whole argument so the resulting native argv retains its identity.
        if ($encoded -cnotmatch '^[\p{L}\p{N}\p{M}._:/\\-]+$') { return }
        $display = $encoded; $text = $encoded
    }
    if ($Tooltip.Length -eq 0) { $Tooltip = $Value }
    [CompletionResult]::new($text, $Value, [CompletionResultType]::ParameterValue, $Tooltip)
}
`, id)
}

// pwshQuote renders s as a PowerShell single-quoted string literal, doubling
// embedded single quotes and folding whitespace so the result stays on one line.
func pwshQuote(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "'", "''")
	return "'" + s + "'"
}

// pwshTooltip returns desc when set, else text; CompletionResult rejects empty
// list-item and tooltip strings, so callers must always pass a non-empty value.
func pwshTooltip(text, desc string) string {
	if !xstrings.IsBlank(desc) {
		return desc
	}
	return text
}

func pwshResult(sb *strings.Builder, id, indent, completion, listItem, resultType, tooltip string) {
	fmt.Fprintf(
		sb,
		"%s[CompletionResult]::new(%s, %s, [CompletionResultType]::%s, %s)\n",
		indent,
		pwshInsertionExpr(id, completion, resultType),
		pwshQuote(listItem),
		resultType,
		pwshQuote(tooltip),
	)
}

// hasFileCompletion reports whether any flag value or positional uses
// PowerShell's built-in filesystem/command completers (extension filters, file,
// dir, or command hints, or PathArgs). These results are routed through the
// $fileCompletions variable so they bypass the prefix filter.
func hasFileCompletion(g *Generator) bool {
	specHasFile := func(spec Spec) bool {
		return spec.HasArg && spec.Dynamic == "" && !spec.CommaList &&
			len(spec.Values) == 0 && len(spec.ValueDescs) == 0 &&
			(spec.Extension != "" || spec.ValueHint == HintFile ||
				spec.ValueHint == HintDir || spec.ValueHint == HintCommand)
	}
	return anySpecInTree(g.Specs, g.Subs, specHasFile,
		func(sub SubSpec) bool { return sub.PathArgs })
}

// pwshWriteResolution emits the command-path resolution loop. It walks bareword
// elements, skips flags, and uses a transition switch to canonicalize each
// subcommand (including aliases) into the running "app;sub" path.
func pwshWriteValueFlagScopes(sb *strings.Builder, g *Generator) {
	fmt.Fprint(sb, "    $valueFlagsByCommand = @{\n")
	var walk func(string, []Spec, []SubSpec)
	walk = func(path string, specs []Spec, subs []SubSpec) {
		fmt.Fprintf(sb, "        %s = %s\n", pwshQuote(path), pwshValueFlagArray(specs))
		for _, sub := range SortSubSpecs(subs) {
			walk(path+";"+sub.Name, appendSpecs(persistentSpecs(specs), sub.Specs), sub.Subs)
		}
	}
	walk(g.AppName, g.Specs, g.Subs)
	fmt.Fprint(sb, "    }\n")
}

func pwshWriteResolution(sb *strings.Builder, g *Generator) {
	id := pwshID(g.AppName)
	fmt.Fprint(sb, "    $skipValue = $false\n")
	fmt.Fprintf(sb, "    foreach ($value in @(__%s_Tokens $commandAst $wordToComplete $cursorPosition)) {\n", id)
	fmt.Fprint(sb, "        if ($skipValue) { $skipValue = $false; continue }\n")
	fmt.Fprint(sb, "        if ($value -eq '--') { break }\n")
	fmt.Fprint(sb, "        if ($valueFlagsByCommand[$command] -contains $value) { $skipValue = $true; continue }\n")
	fmt.Fprint(sb, "        if ($value.StartsWith('-')) { continue }\n")
	fmt.Fprint(sb, "        $next = switch (\"$command;$value\") {\n")
	pwshWriteTransitions(sb, g.AppName, g.Subs)
	fmt.Fprint(sb, "            default { '' }\n        }\n")
	fmt.Fprint(sb, "        if ($next -eq '') { break }\n        $command = $next\n    }\n")
}

func pwshWriteTransitions(sb *strings.Builder, parentPath string, subs []SubSpec) {
	for _, sub := range SortSubSpecs(subs) {
		childPath := parentPath + ";" + sub.Name
		for _, name := range append([]string{sub.Name}, sub.Aliases...) {
			fmt.Fprintf(
				sb,
				"            '%s;%s' { '%s'; break }\n",
				parentPath,
				name,
				childPath,
			)
		}
		if len(sub.Subs) > 0 {
			pwshWriteTransitions(sb, childPath, sub.Subs)
		}
	}
}

// pwshWritePrev emits the logic that captures the token immediately before the
// cursor, used to decide whether a flag value is being completed.
func pwshWritePrev(sb *strings.Builder, id string) {
	fmt.Fprintf(sb, "    $priorTokens = @(__%s_Tokens $commandAst $wordToComplete $cursorPosition)\n", id)
	fmt.Fprint(sb, `    $prev = ''; $terminated = $false; $consumeValue = $false
    foreach ($token in $priorTokens) {
        if ($consumeValue) { $consumeValue = $false; continue }
        if ($token -eq '--') { $terminated = $true; break }
        if ($valueFlagsByCommand[$command] -contains $token) { $consumeValue = $true }
    }
    if (-not $terminated) {
        if ($priorTokens.Count -gt 0 -and $consumeValue) { $prev = $priorTokens[-1] }
        $currentText = $wordToComplete
        foreach ($element in $commandAst.CommandElements) {
            if ($element.Extent.StartOffset -le $cursorPosition -and $element.Extent.EndOffset -ge $cursorPosition) {
                $currentText = $element.Extent.Text.Substring(0, $cursorPosition - $element.Extent.StartOffset)
            }
        }
        foreach ($element in $commandAst.CommandElements) {
            if ($element -is [CommandParameterAst] -and $element.Extent.EndOffset -lt $cursorPosition) {
                $start = $element.Extent.StartOffset - $commandAst.Extent.StartOffset
                $length = $cursorPosition - $element.Extent.StartOffset
                $text = $commandAst.Extent.Text.Substring($start, [Math]::Min($length, $commandAst.Extent.Text.Length - $start))
                if ($text -match '^-[^\s,]+=.*,[^\s]*$') { $currentText = $text }
            }
        }
        foreach ($flag in $valueFlagsByCommand[$command]) {
            if ($currentText.StartsWith($flag + '=')) {
                $inlineFlag = $flag + '='; $inlineText = $currentText; $prev = $flag
                if ($wordToComplete.StartsWith($inlineFlag)) {
                    $inlineReplacement = $true; $wordToComplete = $wordToComplete.Substring($inlineFlag.Length)
                }
                # A bare short inline comma value is a PowerShell parameter
                # parse error. Replacing only its final member cannot repair it.
                if (-not $inlineReplacement -and -not $flag.StartsWith('--')) { return }
                break
            }
        }
    }
`)
}

func pwshWriteSubCases(
	sb *strings.Builder,
	g *Generator,
	parentPath string,
	subs []SubSpec,
	inheritedSpecs []Spec,
	depth int,
) {
	for _, sub := range SortSubSpecs(subs) {
		childPath := parentPath + ";" + sub.Name
		visibleSpecs := combineVisibleSpecs(inheritedSpecs, sub.Specs)

		pwshWriteCommandCase(
			sb,
			g,
			childPath,
			visibleSpecs,
			sub.Subs,
			sub.PathArgs,
			sub.DynamicArgs,
			sub.HasMaxPositionalArgs,
			sub.MaxPositionalArgs,
			depth,
		)

		if len(sub.Subs) == 0 {
			continue
		}
		nextInherited := appendSpecs(inheritedSpecs, persistentSpecs(sub.Specs))
		pwshWriteSubCases(sb, g, childPath, sub.Subs, nextInherited, depth+1)
	}
}

// pwshWriteCommandCase emits a single switch arm for the given command path.
// The arm routes flag-value completion through a $prev switch (when the command
// has value-taking flags) and otherwise lists flags, subcommands, and positional
// completions.
func pwshWriteCommandCase(
	sb *strings.Builder,
	g *Generator,
	path string,
	specs []Spec,
	subs []SubSpec,
	pathArgs bool,
	dynamicArgs []string,
	hasMax bool,
	maxPos int,
	depth int,
) {
	hasContent := len(specs) > 0 || len(subs) > 0 || len(dynamicArgs) > 0 || pathArgs
	if !hasContent {
		return
	}

	fmt.Fprintf(sb, "        '%s' {\n", path)

	var valueFlags []Spec
	for _, spec := range specs {
		if spec.HasArg {
			valueFlags = append(valueFlags, spec)
		}
	}

	if len(valueFlags) > 0 {
		sb.WriteString("            switch ($prev) {\n")
		for _, spec := range valueFlags {
			pwshWriteValueClause(sb, g, spec, "                ")
		}
		sb.WriteString("                default {\n")
		pwshWriteListing(
			sb,
			g,
			specs,
			subs,
			pathArgs,
			dynamicArgs,
			hasMax,
			maxPos,
			depth,
			"                    ",
		)
		sb.WriteString("                }\n")
		sb.WriteString("            }\n")
	} else {
		pwshWriteListing(
			sb,
			g,
			specs,
			subs,
			pathArgs,
			dynamicArgs,
			hasMax,
			maxPos,
			depth,
			"            ",
		)
	}

	sb.WriteString("            break\n")
	sb.WriteString("        }\n")
}

func pwshFlagPatterns(spec Spec) []string {
	var patterns []string
	if spec.LongFlag != "" {
		patterns = append(patterns, pwshQuote("--"+spec.LongFlag))
	}
	if spec.ShortFlag != "" {
		patterns = append(patterns, pwshQuote("-"+spec.ShortFlag))
	}
	return patterns
}

// pwshWriteValueClause emits one $prev arm completing the value of a single
// flag. File/dir/command hints assign PowerShell's built-in completers to
// $fileCompletions (emitted after the switch, bypassing the prefix filter);
// enum, value-description, comma-list, and dynamic completions emit
// CompletionResult objects for the shared prefix filter.
func pwshWriteValueClause(sb *strings.Builder, g *Generator, spec Spec, indent string) {
	patterns := pwshFlagPatterns(spec)
	if len(patterns) == 0 {
		return
	}
	fmt.Fprintf(sb, "%s{ $_ -in %s } {\n", indent, strings.Join(patterns, ", "))
	inner := indent + "    "

	switch {
	case spec.CommaList && spec.Dynamic != "":
		pwshWriteCommaDynamic(sb, g, spec, inner)
	case spec.CommaList && len(spec.ValueDescs) > 0:
		pwshWriteCommaStatic(sb, g, valueDescPairs(spec.ValueDescs), inner)
	case spec.CommaList && len(spec.Values) > 0:
		pairs := make([][2]string, len(spec.Values))
		for i, v := range spec.Values {
			pairs[i] = [2]string{v, v}
		}
		pwshWriteCommaStatic(sb, g, pairs, inner)
	case spec.Dynamic != "":
		pwshWriteDynamicValues(sb, g, spec.Dynamic, inner)
	case len(spec.ValueDescs) > 0:
		for _, vd := range spec.ValueDescs {
			pwshResult(
				sb,
				pwshID(g.AppName),
				inner,
				vd.Value,
				vd.Value,
				"ParameterValue",
				pwshTooltip(vd.Value, vd.Desc),
			)
		}
	case len(spec.Values) > 0:
		for _, v := range spec.Values {
			pwshResult(sb, pwshID(g.AppName), inner, v, v, "ParameterValue", v)
		}
	case spec.Extension != "":
		fmt.Fprintf(
			sb,
			"%s$fileCompletions = [CompletionCompleters]::CompleteFilename($wordToComplete) | Where-Object {\n",
			inner,
		)
		fmt.Fprintf(
			sb,
			"%s    $_.ResultType -eq [CompletionResultType]::ProviderContainer -or $_.ListItemText -match '%s'\n",
			inner,
			pwshExtRegex(spec.Extension),
		)
		fmt.Fprintf(sb, "%s}\n", inner)
	case spec.ValueHint == HintFile:
		fmt.Fprintf(
			sb,
			"%s$fileCompletions = [CompletionCompleters]::CompleteFilename($wordToComplete)\n",
			inner,
		)
	case spec.ValueHint == HintDir:
		fmt.Fprintf(
			sb,
			"%s$fileCompletions = [CompletionCompleters]::CompleteFilename($wordToComplete) | Where-Object {\n",
			inner,
		)
		fmt.Fprintf(
			sb,
			"%s    $_.ResultType -eq [CompletionResultType]::ProviderContainer\n",
			inner,
		)
		fmt.Fprintf(sb, "%s}\n", inner)
	case spec.ValueHint == HintCommand:
		fmt.Fprintf(
			sb,
			"%s$fileCompletions = [CompletionCompleters]::CompleteCommand($wordToComplete)\n",
			inner,
		)
	}

	fmt.Fprintf(sb, "%sbreak\n", inner)
	fmt.Fprintf(sb, "%s}\n", indent)
}

func valueDescPairs(descs []ValueDesc) [][2]string {
	pairs := make([][2]string, len(descs))
	for i, vd := range descs {
		pairs[i] = [2]string{vd.Value, pwshTooltip(vd.Value, vd.Desc)}
	}
	return pairs
}

// pwshWriteListing emits flag-name and subcommand candidates, plus file or
// dynamic positional completions, for a command's positional context.
func pwshWriteListing(
	sb *strings.Builder,
	g *Generator,
	specs []Spec,
	subs []SubSpec,
	pathArgs bool,
	dynamicArgs []string,
	hasMax bool,
	maxPos int,
	depth int,
	indent string,
) {
	fmt.Fprintf(sb, "%sif (-not $terminated) {\n", indent)
	for _, spec := range specs {
		if spec.Hidden {
			continue
		}
		if spec.LongFlag != "" {
			flag := "--" + spec.LongFlag
			pwshResult(sb, pwshID(g.AppName), indent, flag, flag, "ParameterName", pwshTooltip(flag, spec.Terse))
		}
		if spec.ShortFlag != "" {
			flag := "-" + spec.ShortFlag
			pwshResult(sb, pwshID(g.AppName), indent, flag, flag, "ParameterName", pwshTooltip(flag, spec.Terse))
		}
	}
	for _, sub := range SortSubSpecs(subs) {
		pwshResult(
			sb,
			pwshID(g.AppName),
			indent,
			sub.Name,
			sub.Name,
			"ParameterValue",
			pwshTooltip(sub.Name, sub.Terse),
		)
	}

	fmt.Fprintf(sb, "%s}\n", indent)

	if pathArgs {
		fmt.Fprintf(
			sb,
			"%s$fileCompletions = [CompletionCompleters]::CompleteFilename($wordToComplete)\n",
			indent,
		)
	}
	if len(dynamicArgs) > 0 {
		pwshWriteDynArgs(sb, g, specs, dynamicArgs, hasMax, maxPos, depth, indent)
	}
}

// pwshWriteDynArgs emits the positional dynamic-completion logic: it counts the
// real positionals on the command line, selects the matching slot, and invokes
// the handler with forwarded context flags and preceding positionals.
func pwshWriteDynArgs(
	sb *strings.Builder,
	g *Generator,
	specs []Spec,
	dynamicArgs []string,
	hasMax bool,
	maxPos int,
	depth int,
	indent string,
) {
	id := pwshID(g.AppName)
	forward := forwardingActive(g)
	cmdSkip := depth - 1

	fmt.Fprintf(
		sb,
		"%s$tokens = @(__%s_Tokens $commandAst $wordToComplete $cursorPosition)\n",
		indent,
		id,
	)
	fmt.Fprintf(
		sb,
		"%s$positional = @(__%s_Positionals $tokens %d %s)\n",
		indent,
		id,
		cmdSkip,
		pwshValueFlagArray(specs),
	)
	fmt.Fprintf(sb, "%s$n = $positional.Count\n", indent)

	body := indent
	if hasMax {
		fmt.Fprintf(sb, "%sif ($n -lt %d) {\n", indent, maxPos)
		body = indent + "    "
	}

	fmt.Fprintf(sb, "%s$kind = ''\n", body)
	fmt.Fprintf(sb, "%s$first = $false\n", body)
	fmt.Fprintf(sb, "%sswitch ($n) {\n", body)
	for i, da := range dynamicArgs {
		first := "$false"
		if i == 0 {
			first = "$true"
		}
		fmt.Fprintf(sb, "%s    %d { $kind = %s; $first = %s }\n", body, i, pwshQuote(da), first)
	}
	fmt.Fprintf(
		sb,
		"%s    default { $kind = %s }\n",
		body,
		pwshQuote(dynamicArgs[len(dynamicArgs)-1]),
	)
	fmt.Fprintf(sb, "%s}\n", body)

	if forward {
		fmt.Fprintf(sb, "%s$fwd = @(__%s_ForwardedFlags $tokens)\n", body, id)
	}
	fmt.Fprintf(sb, "%s$callArgs = @(\"--%s=$kind\")\n", body, FlagComplete)
	switch {
	case forward:
		fmt.Fprintf(sb, "%s$callArgs += '--'\n", body)
		fmt.Fprintf(sb, "%s$callArgs += $fwd\n", body)
		fmt.Fprintf(sb, "%sif (-not $first) { $callArgs += $positional }\n", body)
	default:
		fmt.Fprintf(sb, "%sif (-not $first) {\n", body)
		fmt.Fprintf(sb, "%s    $callArgs += '--'\n", body)
		fmt.Fprintf(sb, "%s    $callArgs += $positional\n", body)
		fmt.Fprintf(sb, "%s}\n", body)
	}
	fmt.Fprintf(
		sb,
		"%s& '%s' @callArgs 2>$null | Where-Object { $_ } | ForEach-Object {\n",
		body,
		g.AppName,
	)
	fmt.Fprintf(
		sb,
		"%s    __%s_DynamicResult $_\n",
		body,
		pwshID(g.AppName),
	)
	fmt.Fprintf(sb, "%s}\n", body)

	if hasMax {
		fmt.Fprintf(sb, "%s}\n", indent)
	}
}

// pwshValueFlagArray renders the value-taking flags of specs as a PowerShell
// string array, used by the positional scanner to skip a flag and its value.
func pwshValueFlagArray(specs []Spec) string {
	var tokens []string
	for _, spec := range specs {
		if !spec.HasArg {
			continue
		}
		if spec.LongFlag != "" {
			tokens = append(tokens, pwshQuote("--"+spec.LongFlag))
		}
		if spec.ShortFlag != "" {
			tokens = append(tokens, pwshQuote("-"+spec.ShortFlag))
		}
	}
	if len(tokens) == 0 {
		return "@()"
	}
	return "@(" + strings.Join(tokens, ", ") + ")"
}

func pwshDynamicCall(g *Generator, predictor string) string {
	if forwardingActive(g) {
		return fmt.Sprintf(
			"& '%s' --%s=%s -- $fwd 2>$null",
			g.AppName,
			FlagComplete,
			predictor,
		)
	}
	return fmt.Sprintf("& '%s' --%s=%s 2>$null", g.AppName, FlagComplete, predictor)
}

func pwshWriteDynamicValues(sb *strings.Builder, g *Generator, predictor, indent string) {
	if forwardingActive(g) {
		fmt.Fprintf(
			sb,
			"%s$fwd = @(__%s_ForwardedFlags @(__%s_Tokens $commandAst $wordToComplete $cursorPosition))\n",
			indent,
			pwshID(g.AppName),
			pwshID(g.AppName),
		)
	}
	fmt.Fprintf(
		sb,
		"%s%s | Where-Object { $_ } | ForEach-Object {\n",
		indent,
		pwshDynamicCall(g, predictor),
	)
	fmt.Fprintf(
		sb,
		"%s    __%s_DynamicResult $_\n",
		indent,
		pwshID(g.AppName),
	)
	fmt.Fprintf(sb, "%s}\n", indent)
}

// pwshWriteCommaStatic emits comma-separated value completion: it splits the
// in-progress word on commas, drops already-selected values, and re-emits each
// remaining candidate carrying the accumulated prefix.
func pwshWriteCommaStatic(sb *strings.Builder, g *Generator, pairs [][2]string, indent string) {
	fmt.Fprintf(sb, "%s$comma = __%s_CSVContext $commandAst $wordToComplete $cursorPosition $inlineFlag $inlineText\n", indent, pwshID(g.AppName))
	fmt.Fprintf(sb, "%s$filterWord = $comma.Field\n", indent)
	fmt.Fprintf(sb, "%sforeach ($pair in @(\n", indent)
	for _, p := range pairs {
		fmt.Fprintf(sb, "%s    [pscustomobject]@{Value=%s; Tooltip=%s}\n", indent, pwshQuote(p[0]), pwshQuote(p[1]))
	}
	fmt.Fprintf(sb, "%s)) {\n", indent)
	fmt.Fprintf(
		sb,
		"%s    __%s_CSVResult $pair.Value $pair.Tooltip $comma\n",
		indent,
		pwshID(g.AppName),
	)
	fmt.Fprintf(sb, "%s}\n", indent)
}

func pwshWriteCommaDynamic(sb *strings.Builder, g *Generator, spec Spec, indent string) {
	fmt.Fprintf(sb, "%s$comma = __%s_CSVContext $commandAst $wordToComplete $cursorPosition $inlineFlag $inlineText\n", indent, pwshID(g.AppName))
	fmt.Fprintf(sb, "%s$filterWord = $comma.Field\n", indent)
	if forwardingActive(g) {
		fmt.Fprintf(
			sb,
			"%s$fwd = @(__%s_ForwardedFlags @(__%s_Tokens $commandAst $wordToComplete $cursorPosition))\n",
			indent,
			pwshID(g.AppName),
			pwshID(g.AppName),
		)
	}
	fmt.Fprintf(
		sb,
		"%s%s | Where-Object { $_ } | ForEach-Object {\n",
		indent,
		pwshDynamicCall(g, spec.Dynamic),
	)
	fmt.Fprintf(sb, "%s    $parts = $_ -split \"`t\", 2\n", indent)
	fmt.Fprintf(sb, "%s    $description = ''; if ($parts.Count -gt 1) { $description = $parts[1] }\n", indent)
	fmt.Fprintf(
		sb,
		"%s    __%s_CSVResult $parts[0] $description $comma\n",
		indent,
		pwshID(g.AppName),
	)
	fmt.Fprintf(sb, "%s}\n", indent)
}

// pwshExtRegex builds a case-insensitive suffix regex for an extension filter
// such as "yaml,yml" -> "\.(yaml|yml)$".
func pwshExtRegex(ext string) string {
	parts := strings.Split(ext, ",")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return `\.(` + strings.Join(parts, "|") + `)$`
}

//nolint:gosec // G101: shell helper source ("Tokens"), not a credential
const pwshTokensHelper = `
function __%[1]s_Tokens {
    param($CommandAst, $WordToComplete, $CursorPosition)
    $tokens = @()
    foreach ($element in @(__%[1]s_CompletedElements $CommandAst $WordToComplete $CursorPosition)) {
        if ($element -is [ArrayLiteralAst] -and (__%[1]s_ArrayIsLiteral $element)) {
            $tokens += (@($element.Elements | ForEach-Object { [string]$_.Value }) -join ','); continue
        }
        if ($element -is [CommandParameterAst] -and $null -eq $element.Argument) { $tokens += $element.Extent.Text; continue }
        if ($element -isnot [StringConstantExpressionAst] -and $element -isnot [ConstantExpressionAst]) { return }
        $tokens += [string]$element.Value
    }
    $tokens
}
`

func pwshWriteTokensHelper(sb *strings.Builder, id string) {
	fmt.Fprintf(sb, pwshTokensHelper, id)
}

// pwshWriteForwardedHelper emits a helper that scans the tokens for forwardable
// context flags and returns them normalized as --name=value, stopping at "--".
func pwshWriteForwardedHelper(sb *strings.Builder, g *Generator) {
	fmt.Fprintf(sb, "\nfunction __%s_ForwardedFlags {\n", pwshID(g.AppName))
	fmt.Fprint(sb, "    param([string[]]$Tokens)\n")
	pwshWriteValueFlagScopes(sb, g)
	fmt.Fprint(sb, "    $forwardByCommand = @{\n")
	var walk func(string, []Spec, []SubSpec)
	walk = func(path string, specs []Spec, subs []SubSpec) {
		fmt.Fprintf(sb, "        %s = @{", pwshQuote(path))
		for _, f := range forwardableSpecs(specs) {
			fmt.Fprintf(sb, "%s=%s;", pwshQuote("--"+f.LongFlag), pwshQuote(f.LongFlag))
			if f.ShortFlag != "" {
				fmt.Fprintf(sb, "%s=%s;", pwshQuote("-"+f.ShortFlag), pwshQuote(f.LongFlag))
			}
		}
		fmt.Fprint(sb, "}\n")
		for _, sub := range SortSubSpecs(subs) {
			walk(path+";"+sub.Name, appendSpecs(persistentSpecs(specs), sub.Specs), sub.Subs)
		}
	}
	walk(g.AppName, g.Specs, g.Subs)
	fmt.Fprintf(sb, "    }\n    $command = %s\n", pwshQuote(g.AppName))
	fmt.Fprint(sb, `    $forwarded = @(); $skipNext = $false; $name = ''; $resolving = $true
    foreach ($t in $Tokens) {
        if ($skipNext) {
            if ($name) { $forwarded += "--$name=$t"; $name = '' }
            $skipNext = $false; continue
        }
        if ($t -eq '--') { break }
        if ($valueFlagsByCommand[$command] -contains $t) {
            $skipNext = $true; $name = $forwardByCommand[$command][$t]; continue
        }
        if ($t.StartsWith('-')) {
            $equals = $t.IndexOf('=')
            if ($equals -gt 0) {
                $name = $forwardByCommand[$command][$t.Substring(0, $equals)]
                if ($name) { $forwarded += '--' + $name + $t.Substring($equals) }
                $name = ''
            }
            continue
        }
        if (-not $resolving) { continue }
        $next = switch ("$command;$t") {
`)
	pwshWriteTransitions(sb, g.AppName, g.Subs)
	fmt.Fprint(sb, `            default { '' }
        }
        if ($next) { $command = $next } else { $resolving = $false }
    }
    $forwarded
}
`)
}

// pwshWritePositionalsHelper emits a helper that extracts real positional
// arguments from the tokens, skipping flags, their values, leading subcommand
// tokens (CmdSkip), and honoring the "--" terminator.
const pwshPositionalsHelper = `
function __%s_Positionals {
    param([string[]]$Tokens, [int]$CmdSkip, [string[]]$ValueFlags)
    $positional = @()
    $skipNext = $false
    $afterDoubleDash = $false
    $skip = $CmdSkip
    foreach ($t in $Tokens) {
        if ($afterDoubleDash) { $positional += $t; continue }
        if ($skipNext) { $skipNext = $false; continue }
        if ($t -eq '--') { $afterDoubleDash = $true; continue }
        if ($ValueFlags -contains $t) { $skipNext = $true; continue }
        if ($t -match '^-.+=') { continue }
        if ($t -like '-*') { continue }
        if ($skip -gt 0) { $skip--; continue }
        $positional += $t
    }
    $positional
}
`

func pwshWritePositionalsHelper(sb *strings.Builder, id string) {
	fmt.Fprintf(sb, pwshPositionalsHelper, id)
}
