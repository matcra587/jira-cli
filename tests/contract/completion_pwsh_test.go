package contract

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/adapters"
	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestPowerShellNativeCompletionUsesSelectedConfigAndEligibleTransitions(t *testing.T) {
	name := os.Getenv("JIRA_TEST_PWSH_BINARY")
	explicit := name != ""
	if name == "" {
		name = "pwsh"
	}
	executable, err := exec.LookPath(name)
	if err != nil {
		if explicit {
			t.Fatalf("requested PowerShell executable is unavailable: %v", err)
		}
		t.Skip("native PowerShell completion requires pwsh")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, executable, "-NoProfile", "-NonInteractive", "-Command", "$PSVersionTable.PSVersion.ToString()").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(version), "7.") {
		t.Fatalf("requested executable is not PowerShell 7: err=%v output=%q", err, version)
	}
	env, selected := prepareBinaryMetadataCompletion(t)
	disk, err := adapters.NewDiskCache()
	if err != nil {
		t.Fatal(err)
	}
	store, err := adapters.NewScopedCache(disk, "https://example.com", "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	var list jira.Transitions
	if err := jsonv2.Unmarshal([]byte(transitionListNative), &list); err != nil {
		t.Fatal(err)
	}
	if err := cachecmd.RecordTransitionHints(t.Context(), store, "DEMO-1", &list, time.Now()); err != nil {
		t.Fatal(err)
	}
	script, stderr, exit := runBinaryEnv(t, env, "completion", "pwsh")
	if exit != 0 || stderr != "" {
		t.Fatalf("generate PowerShell exit=%d stderr=%q", exit, stderr)
	}
	dir := t.TempDir()
	fixtureName := "jira"
	if runtime.GOOS == "windows" {
		fixtureName += ".exe"
	}
	binary, err := os.ReadFile(jiraBinary(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fixtureName), binary, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "completion.ps1")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	legacySource := filepath.Join(dir, "legacy source.toml")
	if err := os.WriteFile(legacySource, []byte("default_profile='work'\n[[profiles]]\nname='work'\nbase_url='https://example.invalid'\nemail='work@example.invalid'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, command string
		want          []string
	}{
		{name: "prefixed selected config", command: "jira --config " + quote(selected) + " issue transition DEMO-1 3", want: []string{"31"}},
		{name: "equals selected config", command: "jira --config=" + quote(selected) + " issue transition DEMO-1 3", want: []string{"31"}},
		{name: "quoted issue key", command: "jira --config " + quote(selected) + " issue transition 'DEMO-1' 3", want: []string{"31"}},
		{name: "native input suppresses hints", command: "jira --config " + quote(selected) + " issue transition DEMO-1 --json-input=unread.json 3"},
		{name: "default project", command: "jira --config " + quote(selected) + " config set default_project DEM", want: []string{"DEMO"}},
		{name: "default issue type", command: "jira --config " + quote(selected) + " config set default_issue_type Ta", want: []string{"Task"}},
		{name: "default board", command: "jira --config " + quote(selected) + " config set default_board Exam", want: []string{"Example board"}},
		{name: "automatic theme setting", command: "jira --config " + quote(selected) + " config set theme au", want: []string{"auto"}},
		{name: "client refresh key", command: "jira --config " + quote(selected) + " config set refresh_i", want: []string{"refresh_interval"}},
		{name: "team metadata key", command: "jira --config " + quote(selected) + " config set team_ac", want: []string{"team_account_ids"}},
		{name: "automatic theme flag", command: "jira --config " + quote(selected) + " config theme --name au", want: []string{"auto"}},
		{name: "project CSV identity", command: "jira --config " + quote(selected) + " issue list --project DEM", want: []string{"DEMO"}},
		{name: "inline field identity", command: "jira --config " + quote(selected) + " issue list --fields=custom", want: []string{"--fields=customfield_10001"}},
		{name: "explicit legacy source", command: "jira config migrate --source " + quote(legacySource) + " --source-profile wo", want: []string{"work"}},
		{name: "inline legacy source and profile", command: "jira config migrate --source=" + quote(legacySource) + " --source-profile=wo", want: []string{"--source-profile=work"}},
		{name: "missing legacy source", command: "jira config migrate --source-profile wo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			driver := `$ErrorActionPreference='Stop'
. $env:JIRA_TEST_PWSH_SCRIPT
$line = $env:JIRA_TEST_PWSH_LINE
$result = TabExpansion2 -inputScript $line -cursorColumn $line.Length
$identities = @()
foreach ($match in $result.CompletionMatches) {
    $tokens=$null; $errors=$null
    $ast=[System.Management.Automation.Language.Parser]::ParseInput(('jira ' + $match.CompletionText),[ref]$tokens,[ref]$errors)
    $command=$ast.Find({param($node) $node -is [System.Management.Automation.Language.CommandAst]},$false)
    if ($errors.Count -ne 0 -or $ast.EndBlock.Statements.Count -ne 1 -or $command.CommandElements.Count -ne 2 -or
        ($command.CommandElements[1] -isnot [System.Management.Automation.Language.StringConstantExpressionAst] -and
         $command.CommandElements[1] -isnot [System.Management.Automation.Language.ConstantExpressionAst])) { throw 'Completion is not one literal identity' }
    $identities += [string]$command.CommandElements[1].Value
}
ConvertTo-Json -InputObject $identities -Compress
`
			driverPath := filepath.Join(dir, "driver.ps1")
			if err := os.WriteFile(driverPath, []byte(driver), 0o600); err != nil {
				t.Fatal(err)
			}
			extra := append(slices.Clone(env), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "JIRA_TEST_PWSH_SCRIPT="+path, "JIRA_TEST_PWSH_LINE="+tc.command)
			stdout, stderr, exit := runExecutableIOInDirStarted(t, executable, dir, extra, nil, nil, nil, "-NoProfile", "-NonInteractive", "-File", driverPath)
			var got []string
			if exit != 0 || stderr != "" || json.Unmarshal([]byte(stdout), &got) != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("native PowerShell exit=%d stdout=%q stderr=%q want=%q", exit, stdout, stderr, tc.want)
			}
		})
	}
}
