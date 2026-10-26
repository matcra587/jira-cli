package native_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gechr/clib/complete"

	"github.com/matcra587/jira-cli/internal/cli/completion/native"
)

// Two independently generated applications share one host encoding. The
// barriers keep the first callback active while a second runspace attempts
// completion, then release them in order to expose stale encoding snapshots.
func TestPowerShellCompletionCoordinatesRunspaces(t *testing.T) {
	for _, mode := range []string{"success", "exception", "canceled waiter"} {
		t.Run(mode, func(t *testing.T) {
			executable := testPowerShell(t)
			dir := t.TempDir()
			for _, name := range []string{"fixture-one", "fixture-two"} {
				var script bytes.Buffer
				generator := complete.Generator{AppName: name, DynamicArgs: []string{"value"}}
				native.Configure(&generator)
				if err := generator.Print(&script, "pwsh"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name+".ps1"), script.Bytes(), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			fixture, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			const driver = `$ErrorActionPreference = 'Stop'
$initialEncoding = [Console]::OutputEncoding
$callerEncoding = [Text.Encoding]::GetEncoding(437, [Text.EncoderReplacementFallback]::new('encode-fallback'), [Text.DecoderReplacementFallback]::new('decode-fallback'))
$worker = {
    param($Directory, $Name, $FixturePath, $Started, $Entered, $Release, $Mode)
    $ErrorActionPreference = 'Stop'
    function Register-ArgumentCompleter {
        param([switch]$Native, [string]$CommandName, [scriptblock]$ScriptBlock)
        $script:ownedCompleter = $ScriptBlock
    }
    . (Join-Path $Directory ($Name + '.ps1'))
    Set-Item -Path ('Function:' + $Name) -Value {
        $Entered.Set()
        if (-not $Release.Wait(10000)) { throw 'Owned callback release timed out' }
        if ($Mode -eq 'exception') { throw 'owned concurrent exception' }
        & $FixturePath '--@complete=value'
        if ($LASTEXITCODE -ne 0) { throw 'Owned native callback failed' }
    }
    $line = $Name + ' ca'
    $tokens = $null; $errors = $null
    $ast = [System.Management.Automation.Language.Parser]::ParseInput($line, [ref]$tokens, [ref]$errors)
    $commandAst = $ast.Find({param($node) $node -is [System.Management.Automation.Language.CommandAst]}, $false)
    if ($errors.Count -ne 0 -or $null -eq $commandAst) { throw 'Owned command did not parse' }
    $global:LASTEXITCODE = 97
    $Started.Set()
    $caught = $false; $results = @()
    try { $results = @(& $script:ownedCompleter 'ca' $commandAst $line.Length) } catch {
        if ($Mode -ne 'exception' -or -not $_.Exception.Message.Contains('owned concurrent exception')) { throw }
        $caught = $true
    }
    if ($global:LASTEXITCODE -ne 97) { throw 'Concurrent completion changed LASTEXITCODE' }
    if ($Mode -eq 'exception') {
        if (-not $caught -or $results.Count -ne 0) { throw 'Owned exception did not enter' }
    } elseif ($results.Count -ne 1 -or $results[0].ListItemText -cne "café 🧪 古い" -or $results[0].ToolTip -cne 'Visible résumé') {
        throw 'Concurrent callback changed Unicode identity or description'
    }
    [pscustomobject]@{completed=$true; caught=$caught}
}
$startedOne = [Threading.ManualResetEventSlim]::new($false)
$startedTwo = [Threading.ManualResetEventSlim]::new($false)
$enteredOne = [Threading.ManualResetEventSlim]::new($false)
$enteredTwo = [Threading.ManualResetEventSlim]::new($false)
$releaseOne = [Threading.ManualResetEventSlim]::new($false)
$releaseTwo = [Threading.ManualResetEventSlim]::new($false)
$first = [PowerShell]::Create(); $second = [PowerShell]::Create()
try {
    [Console]::OutputEncoding = $callerEncoding
    $firstMode = if ($env:JIRA_TEST_PWSH_CONCURRENT_MODE -eq 'exception') { 'exception' } else { 'success' }
    $null = $first.AddScript($worker.ToString()).AddArgument($env:JIRA_TEST_PWSH_DIRECTORY).AddArgument('fixture-one').AddArgument($env:JIRA_TEST_PWSH_FIXTURE).AddArgument($startedOne).AddArgument($enteredOne).AddArgument($releaseOne).AddArgument($firstMode)
    $one = $first.BeginInvoke()
    if (-not $startedOne.Wait(10000) -or -not $enteredOne.Wait(10000)) { throw 'First completion did not enter callback' }
    $null = $second.AddScript($worker.ToString()).AddArgument($env:JIRA_TEST_PWSH_DIRECTORY).AddArgument('fixture-two').AddArgument($env:JIRA_TEST_PWSH_FIXTURE).AddArgument($startedTwo).AddArgument($enteredTwo).AddArgument($releaseTwo).AddArgument('success')
    $two = $second.BeginInvoke()
    if (-not $startedTwo.Wait(10000)) { throw 'Second runspace did not attempt completion' }
    $overlapped = $enteredTwo.Wait(1000)
    if ($env:JIRA_TEST_PWSH_CONCURRENT_MODE -eq 'canceled waiter') {
        $stop = $second.BeginStop($null, $null)
        if (-not $stop.AsyncWaitHandle.WaitOne(3000)) { throw 'Waiting completer did not cancel while first callback remained active' }
        $second.EndStop($stop)
        try { $null = $second.EndInvoke($two) } catch [System.Management.Automation.PipelineStoppedException] {}
        if ($enteredTwo.IsSet) { throw 'Canceled waiting completer entered callback' }
        $second.Dispose()
        $second = [PowerShell]::Create()
        $startedTwo.Reset()
        $null = $second.AddScript($worker.ToString()).AddArgument($env:JIRA_TEST_PWSH_DIRECTORY).AddArgument('fixture-two').AddArgument($env:JIRA_TEST_PWSH_FIXTURE).AddArgument($startedTwo).AddArgument($enteredTwo).AddArgument($releaseTwo).AddArgument('success')
        $two = $second.BeginInvoke()
        if (-not $startedTwo.Wait(10000)) { throw 'Replacement runspace did not attempt completion' }
    }
    $releaseOne.Set()
    $firstResults = @($first.EndInvoke($one))
    if (($first.HadErrors -and $firstMode -ne 'exception') -or $first.Streams.Error.Count -ne 0 -or $firstResults.Count -ne 1 -or -not $firstResults[0].completed) { throw ('First completion did not finish: ' + $first.Streams.Error) }
    if (-not $enteredTwo.Wait(10000)) { throw 'Second completion did not enter after release' }
    $releaseTwo.Set()
    $secondResults = @($second.EndInvoke($two))
    if ($second.HadErrors -or $secondResults.Count -ne 1 -or -not $secondResults[0].completed) { throw ('Second completion did not finish: ' + $second.Streams.Error) }
    if ($overlapped) { throw 'Concurrent completers overlapped encoding ownership' }
    if (-not [Console]::OutputEncoding.Equals($callerEncoding) -or [BitConverter]::ToString([Console]::OutputEncoding.GetPreamble()) -cne [BitConverter]::ToString($callerEncoding.GetPreamble())) { throw 'Concurrent completion changed caller encoding' }
    '{"completed":true,"callbacks":2,"encoding_restored":true}'
} finally {
    $releaseOne.Set(); $releaseTwo.Set()
    $first.Stop(); $second.Stop()
    $first.Dispose(); $second.Dispose()
    [Console]::OutputEncoding = $initialEncoding
    $startedOne.Dispose(); $startedTwo.Dispose(); $enteredOne.Dispose(); $enteredTwo.Dispose(); $releaseOne.Dispose(); $releaseTwo.Dispose()
}
`
			driverPath := filepath.Join(dir, "driver.ps1")
			if err := os.WriteFile(driverPath, []byte(driver), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
			defer cancel()
			process := exec.CommandContext(ctx, executable, "-NoProfile", "-NonInteractive", "-File", driverPath)
			pwshOwnedConsole(process)
			process.Dir = dir
			process.Env = append(pwshFixtureEnvironment(dir),
				"JIRA_TEST_PWSH_DIRECTORY="+dir, "JIRA_TEST_PWSH_FIXTURE="+fixture,
				"JIRA_TEST_PWSH_CONCURRENT_MODE="+mode,
				"JIRA_TEST_PWSH_CALLBACK=1", "JIRA_TEST_PWSH_LOG="+filepath.Join(dir, "native-callback.json"),
				"JIRA_TEST_PWSH_CANDIDATES=café 🧪 古い\tVisible résumé\n", "JIRA_TEST_PWSH_FAIL_CALLBACK=false")
			var stdout, stderr bytes.Buffer
			process.Stdout, process.Stderr = &stdout, &stderr
			if err := process.Run(); err != nil || stderr.Len() != 0 {
				t.Fatalf("concurrent PowerShell completion failed: err=%v stdout=%q stderr=%q", err, &stdout, &stderr)
			}
			var receipt struct {
				Completed        bool `json:"completed"`
				Callbacks        int  `json:"callbacks"`
				EncodingRestored bool `json:"encoding_restored"`
			}
			if json.Unmarshal(stdout.Bytes(), &receipt) != nil || !receipt.Completed || receipt.Callbacks != 2 || !receipt.EncodingRestored {
				t.Fatalf("concurrent PowerShell guard did not finish: %q", &stdout)
			}
		})
	}
}
