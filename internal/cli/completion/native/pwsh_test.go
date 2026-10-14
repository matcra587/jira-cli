package native_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gechr/clib/complete"

	"github.com/matcra587/jira-cli/internal/cli/completion/native"
)

// The executable is a controlled native callback and argument-capture fixture.
// It handles fixture modes before Go interprets the completion flags.
func TestMain(m *testing.M) {
	if os.Getenv("JIRA_TEST_PWSH_CAPTURE") == "1" {
		if path := os.Getenv("JIRA_TEST_PWSH_CAPTURE_FILE"); path != "" {
			raw, err := json.Marshal(os.Args[1:])
			if err != nil || os.WriteFile(path, raw, 0o600) != nil {
				os.Exit(20)
			}
			os.Exit(0)
		}
		if err := json.NewEncoder(os.Stdout).Encode(os.Args[1:]); err != nil {
			os.Exit(20)
		}
		os.Exit(0)
	}
	if os.Getenv("JIRA_TEST_PWSH_CALLBACK") == "1" && slices.ContainsFunc(os.Args[1:], func(arg string) bool { return strings.HasPrefix(arg, "--@complete=") }) {
		raw, err := json.Marshal(os.Args[1:])
		if err != nil || os.WriteFile(os.Getenv("JIRA_TEST_PWSH_LOG"), raw, 0o600) != nil {
			os.Exit(21)
		}
		if os.Getenv("JIRA_TEST_PWSH_FAIL_CALLBACK") == "true" {
			os.Exit(29)
		}
		if _, err := fmt.Fprint(os.Stdout, os.Getenv("JIRA_TEST_PWSH_CANDIDATES")); err != nil {
			os.Exit(22)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func testPowerShell(t *testing.T) string {
	t.Helper()
	name := os.Getenv("JIRA_TEST_PWSH_BINARY")
	explicit := name != ""
	if name == "" {
		name = "pwsh"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		if explicit {
			t.Fatalf("requested PowerShell executable is unavailable: %v", err)
		}
		t.Skip("native PowerShell completion requires pwsh")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, path, "-NoProfile", "-NonInteractive", "-Command", "$PSVersionTable.PSVersion.ToString()")
	process.Dir = t.TempDir()
	process.Env = pwshFixtureEnvironment(process.Dir)
	version, err := process.CombinedOutput()
	if err != nil || !strings.HasPrefix(string(version), "7.") {
		t.Fatalf("requested executable is not PowerShell 7: err=%v output=%q", err, version)
	}
	return path
}

type pwshMatch struct {
	Text          string   `json:"text"`
	Tooltip       string   `json:"tooltip"`
	Literal       bool     `json:"literal"`
	Args          []string `json:"args"`
	CompletedArgs []string `json:"completed_args"`
}

// The capture driver executes only one command with literal arguments and no
// redirections. This predicate is also exercised with parse-only hostile inputs.
const pwshLiteralCommandGuard = `
function Test-FixtureLiteralCommand {
    param($Ast, $Errors, [bool]$AllowArrays)
    if ($Errors.Count -ne 0 -or $Ast.EndBlock.Statements.Count -ne 1) { return $false }
    $pipeline = $Ast.EndBlock.Statements[0]
    if ($pipeline -isnot [System.Management.Automation.Language.PipelineAst] -or $pipeline.PipelineElements.Count -ne 1) { return $false }
    $command = $pipeline.PipelineElements[0]
    if ($command -isnot [System.Management.Automation.Language.CommandAst] -or $command.Redirections.Count -ne 0) { return $false }
    foreach ($element in $command.CommandElements) {
        if ($AllowArrays -and $element -is [System.Management.Automation.Language.ArrayLiteralAst]) {
            foreach ($member in $element.Elements) {
                if ($member -isnot [System.Management.Automation.Language.StringConstantExpressionAst] -and $member -isnot [System.Management.Automation.Language.ConstantExpressionAst]) { return $false }
            }
        } elseif ($element -is [System.Management.Automation.Language.CommandParameterAst] -and $null -eq $element.Argument) {
            continue
        } elseif ($element -isnot [System.Management.Automation.Language.StringConstantExpressionAst] -and $element -isnot [System.Management.Automation.Language.ConstantExpressionAst]) { return $false }
    }
    $true
}
`

type pwshReplyOptions struct {
	codePage      int
	failCallback  bool
	throwCallback bool
}

func pwshFixtureEnvironment(dir string) []string {
	return append(os.Environ(), shellFixtureDirectories(dir)...)
}

func shellFixtureDirectories(dir string) []string {
	return []string{
		"HOME=" + dir, "USERPROFILE=" + dir,
		"APPDATA=" + filepath.Join(dir, "AppData", "Roaming"),
		"LOCALAPPDATA=" + filepath.Join(dir, "AppData", "Local"),
		"XDG_CONFIG_HOME=" + filepath.Join(dir, ".fixture", "config"),
		"XDG_CACHE_HOME=" + filepath.Join(dir, ".fixture", "cache"),
		"XDG_DATA_HOME=" + filepath.Join(dir, ".fixture", "data"),
	}
}

func pwshReplies(t *testing.T, generator *complete.Generator, line, cursorPrefix, candidates string) ([]pwshMatch, []string) {
	t.Helper()
	return pwshRepliesWithOptions(t, generator, line, cursorPrefix, candidates, pwshReplyOptions{})
}

func pwshRepliesWithOptions(t *testing.T, generator *complete.Generator, line, cursorPrefix, candidates string, options pwshReplyOptions) ([]pwshMatch, []string) {
	t.Helper()
	executable := testPowerShell(t)
	native.Register()
	var script bytes.Buffer
	if err := generator.Print(&script, "pwsh"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, generator.AppName)
	if runtime.GOOS == "windows" {
		fixture += ".exe"
	}
	// Copy rather than symlink so the same guard runs on native Windows.
	raw, err := os.ReadFile(binary)
	if err != nil || os.WriteFile(fixture, raw, 0o700) != nil {
		t.Fatal("cannot prepare the native completion fixture")
	}
	scriptPath := filepath.Join(dir, "completion.ps1")
	if err := os.WriteFile(scriptPath, script.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	const driver = pwshLiteralCommandGuard + `$ErrorActionPreference = 'Stop'
$initialEncoding = [Console]::OutputEncoding
try {
    if ($env:JIRA_TEST_PWSH_CODE_PAGE -ne '0') {
        [Console]::OutputEncoding = [Text.Encoding]::GetEncoding([int]$env:JIRA_TEST_PWSH_CODE_PAGE, [Text.EncoderReplacementFallback]::new('encode-fallback'), [Text.DecoderReplacementFallback]::new('decode-fallback'))
    }
    $callerEncoding = [Console]::OutputEncoding
    $callerOutputEncoding = $OutputEncoding
    if ($env:JIRA_TEST_PWSH_THROW_CALLBACK -eq 'true') {
        $script:registerCommand = Get-Command Register-ArgumentCompleter -CommandType Cmdlet
        function Register-ArgumentCompleter {
            param([switch]$Native, [string]$CommandName, [scriptblock]$ScriptBlock)
            $script:ownedCompleter = $ScriptBlock
            & $script:registerCommand -Native -CommandName $CommandName -ScriptBlock $ScriptBlock
        }
    }
    . $env:JIRA_TEST_PWSH_SCRIPT
    $line = $env:JIRA_TEST_PWSH_LINE
    $cursor = $line.Length
    if ($env:JIRA_TEST_PWSH_CURSOR_PREFIX) { $cursor = $env:JIRA_TEST_PWSH_CURSOR_PREFIX.Length }
    $value = 'caller-value'; $prefix = 'caller-prefix'; $parts = @('caller-parts'); $tokens = @('caller-tokens'); $i = 97; $command = 'caller-command'
    if ($env:JIRA_TEST_PWSH_UNSET_EXIT -eq '1') { Remove-Variable LASTEXITCODE -Scope Global -ErrorAction Ignore } else { $global:LASTEXITCODE = 97 }
    $errorCount = $Error.Count
    $before = ConvertTo-Json -InputObject @($value, $prefix, $parts, $tokens, $i, $command) -Compress
    if ($env:JIRA_TEST_PWSH_THROW_CALLBACK -eq 'true') {
        function fixture { throw 'owned callback exception' }
        $parseTokens = $null; $parseErrors = $null
        $ast = [System.Management.Automation.Language.Parser]::ParseInput($line, [ref]$parseTokens, [ref]$parseErrors)
        $commandAst = $ast.Find({param($node) $node -is [System.Management.Automation.Language.CommandAst]}, $false)
        $caught = $false
        try { & $script:ownedCompleter 'ca' $commandAst $cursor } catch {
            if (-not $_.Exception.Message.Contains('owned callback exception')) { throw }
            $caught = $true
        }
        if (-not $caught) { throw 'Controlled callback exception did not enter' }
        $result = [pscustomobject]@{CompletionMatches=@()}
    } else {
        $result = TabExpansion2 -inputScript $line -cursorColumn $cursor
        if ($Error.Count -ne $errorCount) { throw 'Completion changed caller errors' }
    }
    if (-not [Console]::OutputEncoding.Equals($callerEncoding) -or [BitConverter]::ToString([Console]::OutputEncoding.GetPreamble()) -cne [BitConverter]::ToString($callerEncoding.GetPreamble())) { throw 'Completion changed caller output encoding' }
    if (-not [object]::ReferenceEquals($OutputEncoding, $callerOutputEncoding)) { throw 'Completion changed caller stdin encoding' }
    if ($env:JIRA_TEST_PWSH_UNSET_EXIT -eq '1') {
        if ($null -ne (Get-Variable LASTEXITCODE -Scope Global -ErrorAction Ignore)) { throw 'Completion created LASTEXITCODE' }
    } elseif ($global:LASTEXITCODE -ne 97) { throw 'Completion changed LASTEXITCODE' }
    $after = ConvertTo-Json -InputObject @($value, $prefix, $parts, $tokens, $i, $command) -Compress
    if ($before -ne $after) { throw 'Completion changed caller variables' }
    $observed = @()
    $env:JIRA_TEST_PWSH_CAPTURE_FILE = Join-Path (Split-Path -Parent $env:JIRA_TEST_PWSH_LOG) 'captured-argv.json'
    foreach ($match in $result.CompletionMatches) {
        $tokens = $null; $errors = $null
        $ast = [System.Management.Automation.Language.Parser]::ParseInput(('fixture ' + $match.CompletionText), [ref]$tokens, [ref]$errors)
        $command = $ast.Find({param($node) $node -is [System.Management.Automation.Language.CommandAst]}, $false)
        $literal = (Test-FixtureLiteralCommand $ast $errors $false) -and $command.CommandElements.Count -eq 2
        $captured = @()
        if ($literal) {
            $invocation = "& '" + $env:JIRA_TEST_PWSH_FIXTURE.Replace("'", "''") + "' " + $match.CompletionText
            $env:JIRA_TEST_PWSH_CAPTURE = '1'
            try {
                $null = [scriptblock]::Create($invocation).Invoke()
                if ($LASTEXITCODE -ne 0) { throw 'Native argument capture failed' }
                $captured = @([IO.File]::ReadAllText($env:JIRA_TEST_PWSH_CAPTURE_FILE, [Text.Encoding]::UTF8) | ConvertFrom-Json)
            } finally { Remove-Item Env:JIRA_TEST_PWSH_CAPTURE }
        }
        $completed = $line.Remove($result.ReplacementIndex, $result.ReplacementLength).Insert($result.ReplacementIndex, $match.CompletionText)
        $completedArgs = @()
        if ($literal) {
            $completedTokens = $null; $completedErrors = $null
            $completedAst = [System.Management.Automation.Language.Parser]::ParseInput($completed, [ref]$completedTokens, [ref]$completedErrors)
            $completedCommand = $completedAst.Find({param($node) $node -is [System.Management.Automation.Language.CommandAst]}, $false)
            $safe = Test-FixtureLiteralCommand $completedAst $completedErrors $true
            if ($safe) {
                $native = "& '" + $env:JIRA_TEST_PWSH_FIXTURE.Replace("'", "''") + "'" + $completed.Substring($completedCommand.CommandElements[0].Extent.EndOffset)
                $env:JIRA_TEST_PWSH_CAPTURE = '1'
                try {
                    $null = [scriptblock]::Create($native).Invoke()
                    if ($LASTEXITCODE -ne 0) { throw 'Completed native argument capture failed' }
                    $completedArgs = @([IO.File]::ReadAllText($env:JIRA_TEST_PWSH_CAPTURE_FILE, [Text.Encoding]::UTF8) | ConvertFrom-Json)
                } finally { Remove-Item Env:JIRA_TEST_PWSH_CAPTURE }
            }
        }
        $observed += @{text=$match.CompletionText; tooltip=$match.ToolTip; literal=$literal; args=$captured; completed_args=$completedArgs}
    }
    ConvertTo-Json -InputObject $observed -Depth 5 -Compress -EscapeHandling EscapeNonAscii
} catch { Write-Error $_; exit 23 }
finally { [Console]::OutputEncoding = $initialEncoding }
`
	driverPath := filepath.Join(dir, "driver.ps1")
	if err := os.WriteFile(driverPath, []byte(driver), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "native-callback.json")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, executable, "-NoProfile", "-NonInteractive", "-File", driverPath)
	pwshOwnedConsole(process)
	process.Dir = dir
	process.Env = append(pwshFixtureEnvironment(dir), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"JIRA_TEST_PWSH_CALLBACK=1", "JIRA_TEST_PWSH_LOG="+logPath, "JIRA_TEST_PWSH_CANDIDATES="+candidates,
		"JIRA_TEST_PWSH_SCRIPT="+scriptPath, "JIRA_TEST_PWSH_FIXTURE="+fixture,
		"JIRA_TEST_PWSH_LINE="+line, "JIRA_TEST_PWSH_CURSOR_PREFIX="+cursorPrefix,
		"JIRA_TEST_PWSH_CODE_PAGE="+strconv.Itoa(options.codePage),
		"JIRA_TEST_PWSH_FAIL_CALLBACK="+strconv.FormatBool(options.failCallback),
		"JIRA_TEST_PWSH_THROW_CALLBACK="+strconv.FormatBool(options.throwCallback))
	var stdout, stderr bytes.Buffer
	process.Stdout, process.Stderr = &stdout, &stderr
	if err := process.Run(); err != nil || stderr.Len() != 0 {
		t.Fatalf("native PowerShell invocation err=%v stdout=%q stderr=%q", err, &stdout, &stderr)
	}
	var matches []pwshMatch
	if err := json.Unmarshal(stdout.Bytes(), &matches); err != nil {
		t.Fatalf("native PowerShell did not run the guard: %v stdout=%q", err, &stdout)
	}
	var args []string
	if raw, err := os.ReadFile(logPath); err == nil {
		if err := json.Unmarshal(raw, &args); err != nil {
			t.Fatal(err)
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return matches, args
}

func TestPowerShellNativeCompletionRefusesNonliteralPriorContext(t *testing.T) {
	generator := &complete.Generator{AppName: "fixture", DynamicArgs: []string{"value"}}
	marker := strings.ReplaceAll(filepath.Join(t.TempDir(), "expression-marker"), "'", "''")
	for _, line := range []string{"fixture $([IO.File]::WriteAllText('" + marker + "', 'executed')) 3", "fixture $unknown 3"} {
		matches, args := pwshReplies(t, generator, line, "", "31\tDisplay only\n")
		if len(matches) != 0 || len(args) != 0 {
			t.Fatalf("nonliteral context reached a predictor: matches=%#v argv=%q", matches, args)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("nonliteral expression executed: %v", err)
	}
}

func TestPowerShellNativeCompletionPreservesContextAndInsertedIdentity(t *testing.T) {
	generator := &complete.Generator{
		AppName: "fixture",
		Specs:   []complete.Spec{{LongFlag: "config", ShortFlag: "c", HasArg: true, Persistent: true, Forward: true}},
		Subs: []complete.SubSpec{{
			Name: "run", Aliases: []string{"r"}, DynamicArgs: []string{"first", "target"},
			Specs: []complete.Spec{{LongFlag: "value", HasArg: true, Dynamic: "value"}},
		}},
	}
	for _, tc := range []struct {
		name, line, cursor string
		want               []string
	}{
		{name: "positional description", line: "fixture run DEMO-1 3", want: []string{"--@complete=target", "--", "DEMO-1"}},
		{name: "prefixed config", line: "fixture --config 'space and ''quote.toml' run DEMO-1 3", want: []string{"--@complete=target", "--", "--config=space and 'quote.toml", "DEMO-1"}},
		{name: "equals config", line: "fixture --config='space and ''quote.toml' run DEMO-1 3", want: []string{"--@complete=target", "--", "--config=space and 'quote.toml", "DEMO-1"}},
		{name: "short equals config", line: "fixture -c='space and ''quote.toml' run DEMO-1 3", want: []string{"--@complete=target", "--", "--config=space and 'quote.toml", "DEMO-1"}},
		{name: "quoted alias and issue", line: "fixture 'r' 'DEMO-1' 3", want: []string{"--@complete=target", "--", "DEMO-1"}},
		{name: "non-forwarded value resembles config", line: "fixture run --value '--config=other.toml' DEMO-1 3", want: []string{"--@complete=target", "--", "DEMO-1"}},
		{name: "non-forwarded bare value resembles config", line: "fixture run --value '--config' DEMO-1 3", want: []string{"--@complete=target", "--", "DEMO-1"}},
		{name: "flag spelling after terminator", line: "fixture run -- --value 3", want: []string{"--@complete=target", "--", "--value"}},
		{name: "flag value description", line: "fixture run --value 3", want: []string{"--@complete=value", "--"}},
		{name: "cursor before future tokens", line: "fixture run DEMO-1 3 FUTURE-9", cursor: "fixture run DEMO-1 3", want: []string{"--@complete=target", "--", "DEMO-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matches, args := pwshReplies(t, generator, tc.line, tc.cursor, "31\tDisplay only\n")
			if !slices.Equal(args, tc.want) {
				t.Fatalf("native callback argv=%q want=%q", args, tc.want)
			}
			if len(matches) != 1 || !matches[0].Literal || !slices.Equal(matches[0].Args, []string{"31"}) || matches[0].Tooltip != "Display only" {
				t.Fatalf("native insertion or tooltip=%#v", matches)
			}
		})
	}
}

func TestPowerShellNativeCompletionQuotesDynamicValuesAndMatchesLiteralPrefix(t *testing.T) {
	generator := &complete.Generator{AppName: "fixture", DynamicArgs: []string{"value"}}
	for _, value := range []string{"space name", "apostrophe'name", "$(Get-Date)", "[literal]*", "semi;colon", "dollar$name", "back`tick"} {
		t.Run(value, func(t *testing.T) {
			matches, _ := pwshReplies(t, generator, "fixture ", "", value+"\tDisplay only\n")
			if len(matches) != 1 || !matches[0].Literal || !slices.Equal(matches[0].Args, []string{value}) {
				t.Fatalf("literal dynamic value changed: %#v", matches)
			}
		})
	}
	matches, _ := pwshReplies(t, generator, "fixture [", "", "[literal]\tDisplay only\nother\tNot this prefix\n")
	if len(matches) != 1 || !slices.Equal(matches[0].Args, []string{"[literal]"}) {
		t.Fatalf("literal prefix did not preserve its candidate: %#v", matches)
	}
}

func TestPowerShellNativeCompletionPreservesStaticAndCommaValues(t *testing.T) {
	for _, dynamic := range []bool{false, true} {
		name := "static"
		if dynamic {
			name = "dynamic"
		}
		t.Run(name, func(t *testing.T) {
			spec := complete.Spec{LongFlag: "value", HasArg: true, CommaList: true}
			if dynamic {
				spec.Dynamic = "value"
			} else {
				spec.ValueDescs = []complete.ValueDesc{{Value: "first", Desc: "Selected"}, {Value: "space name", Desc: "Display only"}}
			}
			generator := &complete.Generator{AppName: "fixture", Specs: []complete.Spec{spec}}
			matches, _ := pwshReplies(t, generator, "fixture --value 'first,sp'", "", "first\tSelected\nspace name\tDisplay only\n")
			if len(matches) != 1 || !matches[0].Literal || !slices.Equal(matches[0].CompletedArgs, []string{"--value", "first,space name"}) || matches[0].Tooltip != "Display only" {
				t.Fatalf("comma values lost identity, prefix or tooltip: %#v", matches)
			}
		})
	}
	for _, value := range []string{"space name", "apostrophe'name", "$(Get-Date)", "[literal]*", "semi;colon"} {
		t.Run(value, func(t *testing.T) {
			generator := &complete.Generator{AppName: "fixture", Specs: []complete.Spec{{LongFlag: "value", HasArg: true, ValueDescs: []complete.ValueDesc{{Value: value, Desc: "Display only"}}}}}
			matches, _ := pwshReplies(t, generator, "fixture --value ", "", "")
			if len(matches) != 1 || !matches[0].Literal || !slices.Equal(matches[0].Args, []string{value}) || matches[0].Tooltip != "Display only" {
				t.Fatalf("static value changed: %#v", matches)
			}
		})
	}
}

func TestPowerShellNativeCompletionPreservesBareCommaPrefix(t *testing.T) {
	generator := &complete.Generator{AppName: "fixture", Specs: []complete.Spec{{LongFlag: "value", HasArg: true, CommaList: true, Dynamic: "value"}}}
	for _, line := range []string{"fixture --value first,al", "fixture --value first,"} {
		matches, _ := pwshReplies(t, generator, line, "", "first\tSelected\nalpha\tDisplay only\nspace name\tNeeds a quoted argument\n")
		if len(matches) != 1 || !matches[0].Literal || !slices.Equal(matches[0].CompletedArgs, []string{"--value", "first,alpha"}) || matches[0].Tooltip != "Display only" {
			t.Fatalf("bare comma completion corrupted native argv: %#v", matches)
		}
	}
}

func TestPowerShellNativeCompletionPreservesInlineFlagValues(t *testing.T) {
	generator := &complete.Generator{AppName: "fixture", Subs: []complete.SubSpec{{Name: "run", Specs: []complete.Spec{{LongFlag: "value", ShortFlag: "v", HasArg: true, Dynamic: "value"}}}}}
	for _, flag := range []string{"--value", "-v"} {
		matches, _ := pwshReplies(t, generator, "fixture run "+flag+"=3", "", "31\tDisplay only\n")
		if len(matches) != 1 || !matches[0].Literal || !slices.Equal(matches[0].CompletedArgs, []string{"run", flag + "=31"}) || matches[0].Tooltip != "Display only" {
			t.Fatalf("inline flag completion lost native argv: %#v", matches)
		}
	}
}

func TestPowerShellNativeCompletionPreservesQuotedCSVEncoding(t *testing.T) {
	generator := &complete.Generator{AppName: "fixture", Specs: []complete.Spec{{LongFlag: "value", HasArg: true, CommaList: true, Dynamic: "value"}}}
	for _, tc := range []struct {
		name, line, candidates, want string
	}{
		{name: "comma member", line: `fixture --value 'first,sp'`, candidates: "space, name\tDisplay only\n", want: `first,"space, name"`},
		{name: "quote member", line: `fixture --value 'first,sp'`, candidates: "space \"quote\"\tDisplay only\n", want: `first,"space ""quote"""`},
		{name: "selected comma member", line: `fixture --value '"space, name",al'`, candidates: "space, name\tSelected\nalpha\tDisplay only\n", want: `"space, name",alpha`},
		{name: "selected quote member", line: `fixture --value '"space ""quote""",al'`, candidates: "space \"quote\"\tSelected\nalpha\tDisplay only\n", want: `"space ""quote""",alpha`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matches, _ := pwshReplies(t, generator, tc.line, "", tc.candidates)
			if len(matches) != 1 || !matches[0].Literal || !slices.Equal(matches[0].CompletedArgs, []string{"--value", tc.want}) || matches[0].Tooltip != "Display only" {
				t.Fatalf("quoted CSV encoding changed native argv: %#v", matches)
			}
		})
	}
}

func TestPowerShellNativeCompletionPreservesInlineCommaValues(t *testing.T) {
	generator := &complete.Generator{AppName: "fixture", Specs: []complete.Spec{{LongFlag: "value", ShortFlag: "v", HasArg: true, CommaList: true, Dynamic: "value"}}}
	for _, flag := range []string{"--value", "-v"} {
		for _, value := range []string{"first,al", "'first,al'"} {
			t.Run(flag+value, func(t *testing.T) {
				matches, _ := pwshReplies(t, generator, "fixture "+flag+"="+value, "", "first\tSelected\nalpha\tDisplay only\n")
				if flag == "-v" && value == "first,al" {
					if len(matches) != 0 {
						t.Fatalf("unrepairable short inline CSV received completion: %#v", matches)
					}
					return
				}
				if len(matches) != 1 || !matches[0].Literal || !slices.Equal(matches[0].CompletedArgs, []string{flag + "=first,alpha"}) {
					t.Fatalf("inline CSV changed native argv: %#v", matches)
				}
			})
		}
	}
}

func TestPowerShellNativeCompletionPreservesCompletedCommaContext(t *testing.T) {
	generator := &complete.Generator{AppName: "fixture", Specs: []complete.Spec{{LongFlag: "value", ShortFlag: "v", HasArg: true, CommaList: true, Dynamic: "value"}}}
	for _, tc := range []struct {
		prior string
		want  []string
	}{
		{prior: "-v='first,alpha'", want: []string{"-v=first,alpha", "--value", "first,alpha"}},
		{prior: "--value=first,alpha", want: []string{"--value=first,alpha", "--value", "first,alpha"}},
		{prior: "--value first,alpha", want: []string{"--value", "first,alpha", "--value", "first,alpha"}},
	} {
		t.Run(tc.prior, func(t *testing.T) {
			matches, _ := pwshReplies(t, generator, "fixture "+tc.prior+" --value 'first,al'", "", "alpha\tDisplay only\n")
			if len(matches) != 1 || !slices.Equal(matches[0].CompletedArgs, tc.want) {
				t.Fatalf("completed CSV suppressed or redirected later completion: %#v", matches)
			}
		})
	}
}

func TestPowerShellNativeCompletionRefusesAmbiguousCommaContext(t *testing.T) {
	generator := &complete.Generator{AppName: "fixture", Specs: []complete.Spec{{LongFlag: "value", HasArg: true, Dynamic: "value"}}}
	for _, prior := range []string{"first, alpha", "first , alpha", "first,'space name'", "first,$unknown"} {
		matches, args := pwshReplies(t, generator, "fixture --value "+prior+" --value 3", "", "31\n")
		if len(matches) != 0 || len(args) != 0 {
			t.Fatalf("ambiguous native comma argument reached callback: prior=%q matches=%#v argv=%q", prior, matches, args)
		}
	}
}

func TestPowerShellNativeCompletionOmitsNamesAfterTerminator(t *testing.T) {
	for _, spec := range []complete.Spec{{LongFlag: "value", HasArg: true, Dynamic: "value"}, {LongFlag: "verbose"}} {
		generator := &complete.Generator{AppName: "fixture", Specs: []complete.Spec{spec}, Subs: []complete.SubSpec{{Name: "child"}}}
		for _, line := range []string{"fixture -- --v", "fixture -- ch"} {
			matches, _ := pwshReplies(t, generator, line, "", "")
			if len(matches) != 0 {
				t.Fatalf("terminated options or subcommands leaked: %#v", matches)
			}
		}
	}
}

func TestPowerShellNativeCaptureRejectsRedirections(t *testing.T) {
	executable := testPowerShell(t)
	script := pwshLiteralCommandGuard + `
$ErrorActionPreference = 'Stop'
$lines = @('fixture ''31'' > ''unexpected-file''', 'fixture ''31'' 2> ''unexpected-file''', 'fixture ''31'' | Write-Output', 'fixture $(Get-Date)', 'fixture ''31''; Write-Output bad')
foreach ($line in $lines) {
    $tokens = $null; $errors = $null
    $ast = [System.Management.Automation.Language.Parser]::ParseInput($line, [ref]$tokens, [ref]$errors)
    foreach ($arrays in @($false, $true)) {
        if (Test-FixtureLiteralCommand $ast $errors $arrays) { throw 'Unsafe invocation accepted' }
    }
}
Write-Output 'guards-ran'
`
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, executable, "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "guards-ran" {
		t.Fatalf("parse-only redirection guards failed: err=%v output=%q", err, output)
	}
}

func TestPowerShellNativeCompletionPreservesAbsentExitStatus(t *testing.T) {
	t.Setenv("JIRA_TEST_PWSH_UNSET_EXIT", "1")
	for _, dynamic := range []bool{false, true} {
		generator := &complete.Generator{AppName: "fixture", Specs: []complete.Spec{{LongFlag: "value", HasArg: true, Values: []string{"31"}}}}
		if dynamic {
			generator.Specs[0].Values = nil
			generator.Specs[0].Dynamic = "value"
		}
		matches, _ := pwshReplies(t, generator, "fixture --value 3", "", "31\n")
		if len(matches) != 1 {
			t.Fatalf("completion did not run: %#v", matches)
		}
	}
}
