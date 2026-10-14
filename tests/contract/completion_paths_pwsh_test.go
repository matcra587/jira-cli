package contract

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adapters"
	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
)

func TestContextualPowerShellPathCompletionExecutesLiteralArgv(t *testing.T) {
	name := os.Getenv("JIRA_TEST_PWSH_BINARY")
	explicit := name != ""
	if name == "" {
		name = "pwsh"
	}
	executable, err := exec.LookPath(name)
	if err != nil {
		if explicit {
			t.Fatal("requested PowerShell executable unavailable", err)
		}
		t.Skip("native PowerShell unavailable")
	}
	observer, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	metadataEnv, selected := prepareBinaryMetadataCompletion(t)
	contents, err := os.ReadFile(selected)
	if err != nil {
		t.Fatal(err)
	}
	selected = filepath.Join(t.TempDir(), "selected settings.toml")
	if err := os.WriteFile(selected, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	disk, err := adapters.NewDiskCache()
	if err != nil {
		t.Fatal(err)
	}
	store, err := adapters.NewScopedCache(disk, "https://example.com", "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := cachecmd.RecordIssueKeys(store, []string{"DEMO-1"}); err != nil {
		t.Fatal(err)
	}
	for _, resource := range cachecmd.MetadataResources() {
		if resource.Name == "issuetypes" {
			raw, err := json.Marshal(struct {
				Version int `json:"version"`
				cachecmd.MetadataValue
			}{Version: 1, MetadataValue: cachecmd.MetadataValue{Items: []json.RawMessage{json.RawMessage(`{"id":"1","name":"Forward/"}`), json.RawMessage(`{"id":"2","name":"Backslash\\"}`), json.RawMessage(`{"id":"3","name":"Café 🧪 古い $value'quote"}`)}, Count: 3, Pages: 1, Complete: true}})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Set(resource.Key("https://example.com"), raw); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, filename := range []string{"candidate space.txt", "candidate,comma.txt", "candidate$HOME;literal'quote.txt"} {
		for _, role := range []string{"attachment", "file", "csv", "inline", "theme", "nested", "directory", "csv directory", "selected", "files select issues", "ordinary slash", "ordinary backslash", "unicode metadata", "unicode directory", "download directory", "download nested directory"} {
			t.Run(role+"/"+filename, func(t *testing.T) {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, filename), []byte("unread"), 0o000); err != nil {
					t.Fatal(err)
				}
				env := []string{"JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_API_TOKEN=", "JIRA_EMAIL=", "JIRA_BASE_URL=https://unreachable.example.invalid", "XDG_CONFIG_HOME=" + filepath.Join(dir, "config"), "XDG_CACHE_HOME=" + filepath.Join(dir, "cache")}
				script, stderr, exit := runBinaryEnv(t, env, "completion", "pwsh")
				if exit != 0 || stderr != "" {
					t.Fatal("PowerShell generator failed", exit, stderr)
				}
				path := filepath.Join(dir, "completion.ps1")
				if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
					t.Fatal(err)
				}
				line := "jira issue attachment add KEY-1 candidate"
				want := []string{"issue", "attachment", "add", "KEY-1", filename}
				switch role {
				case "file":
					line = "jira issue attachment add KEY-1 --file candidate"
				case "csv":
					line = "jira issue attachment add KEY-1 --file '\"earlier,selected\",candidate'"
				case "inline":
					line = "jira issue attachment add KEY-1 --file=candidate"
				case "theme":
					line = "jira config set theme_path candidate"
					want = []string{"config", "set", "theme_path", filepath.Join(dir, filename)}
				}
				continuation := ""
				if role == "nested" || role == "directory" || role == "csv directory" || role == "unicode directory" || strings.HasPrefix(role, "download ") {
					if err := os.Remove(filepath.Join(dir, filename)); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(filepath.Join(dir, "candidate,dir"), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, "candidate,dir", "leaf.txt"), nil, 0o000); err != nil {
						t.Fatal(err)
					}
					line = "jira issue attachment add KEY-1 'candidate,dir/lea'"
					want = []string{"issue", "attachment", "add", "KEY-1", "candidate,dir/leaf.txt"}
					if role == "directory" || role == "unicode directory" {
						line = "jira issue attachment add KEY-1 candidate"
						continuation = "lea"
						want = []string{"issue", "attachment", "add", "KEY-1", filepath.Join("candidate,dir", "leaf.txt")}
					}
					if role == "unicode directory" {
						if err := os.Rename(filepath.Join(dir, "candidate,dir"), filepath.Join(dir, "candidate café")); err != nil {
							t.Fatal(err)
						}
						want = []string{"issue", "attachment", "add", "KEY-1", filepath.Join("candidate café", "leaf.txt")}
					}
					if role == "csv directory" {
						line = "jira issue attachment add KEY-1 --file candidate"
						continuation = "lea"
						want = []string{"issue", "attachment", "add", "KEY-1", "--file", `"` + filepath.Join("candidate,dir", "leaf.txt") + `"`}
					}
					if strings.HasPrefix(role, "download ") {
						const directory = "candidate,dir $value"
						if err := os.Rename(filepath.Join(dir, "candidate,dir"), filepath.Join(dir, directory)); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(dir, "candidate file.txt"), []byte("not a directory"), 0o000); err != nil {
							t.Fatal(err)
						}
						if err := os.Mkdir(filepath.Join(dir, directory, "nested directory"), 0o700); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(dir, directory, "nested file.txt"), []byte("not a directory"), 0o000); err != nil {
							t.Fatal(err)
						}
						line = "jira issue attachment download 10042 --to candidate"
						want = []string{"issue", "attachment", "download", "10042", "--to", directory}
						if role == "download nested directory" {
							line = "jira issue attachment download 10042 --to '" + filepath.Join(directory, "ne") + "'"
							want[5] = filepath.Join(directory, "nested directory")
						}
						env = append(env, "JIRA_TEST_PWSH_EXPECT_DIRECTORY=1")
					}
				}
				if role == "selected" || role == "files select issues" || role == "unicode metadata" || strings.HasPrefix(role, "ordinary ") {
					env = append(env, metadataEnv...)
					env = append(env, "JIRA_BASE_URL=", "JIRA_EMAIL=")
					quoted := "'" + strings.ReplaceAll(selected, "'", "''") + "'"
					line = "jira --json --config " + quoted + " config set default_project DE"
					want = []string{"--json", "--config", selected, "config", "set", "default_project", "DEMO"}
					if strings.HasPrefix(role, "ordinary ") {
						prefix, value := "For", "Forward/"
						if role == "ordinary backslash" {
							prefix, value = "Back", `Backslash\`
						}
						line = "jira --config " + quoted + " config set default_issue_type " + prefix
						want = []string{"--config", selected, "config", "set", "default_issue_type", value}
					}
					if role == "unicode metadata" {
						line = "jira --config " + quoted + " config set default_issue_type Ca"
						want = []string{"--config", selected, "config", "set", "default_issue_type", "Café 🧪 古い $value'quote"}
					}
					if role == "files select issues" {
						line = "jira --config " + quoted + " issue attachment add KEY-1 --file '\"earlier,selected\"' DEM"
						want = []string{"--config", selected, "issue", "attachment", "add", "KEY-1", "--file", `"earlier,selected"`, "DEMO-1"}
					}
				}
				const driver = `$ErrorActionPreference='Stop'
. $env:JIRA_TEST_PWSH_SCRIPT
$line=$env:JIRA_TEST_PWSH_LINE
$result=TabExpansion2 -inputScript $line -cursorColumn $line.Length
if ($result.CompletionMatches.Count -ne 1) { throw 'Expected one contextual path completion' }
$match=$result.CompletionMatches[0]
if ($env:JIRA_TEST_PWSH_EXPECT_DIRECTORY -eq '1' -and $match.ResultType -ne [System.Management.Automation.CompletionResultType]::ProviderContainer) { throw 'Expected directory provider completion' }
$completed=$line.Remove($result.ReplacementIndex,$result.ReplacementLength).Insert($result.ReplacementIndex,$match.CompletionText)
if ($env:JIRA_TEST_PWSH_CONTINUATION.Length -gt 0) {
    $completed += $env:JIRA_TEST_PWSH_CONTINUATION
    $result=TabExpansion2 -inputScript $completed -cursorColumn $completed.Length
    if ($result.CompletionMatches.Count -ne 1) { throw ('Expected one directory continuation: ' + $completed + ' (' + $result.CompletionMatches.Count + ')') }
    if ($env:JIRA_TEST_PWSH_EXPECT_DIRECTORY -eq '1' -and $result.CompletionMatches[0].ResultType -ne [System.Management.Automation.CompletionResultType]::ProviderContainer) { throw 'Expected directory continuation provider completion' }
    $completed=$completed.Remove($result.ReplacementIndex,$result.ReplacementLength).Insert($result.ReplacementIndex,$result.CompletionMatches[0].CompletionText)
}
$tokens=$null; $errors=$null
$ast=[System.Management.Automation.Language.Parser]::ParseInput($completed,[ref]$tokens,[ref]$errors)
$command=$ast.Find({param($node) $node -is [System.Management.Automation.Language.CommandAst]},$false)
if ($errors.Count -ne 0 -or $ast.EndBlock.Statements.Count -ne 1) { throw 'Inserted completion is not one command' }
$nativeArgs=@()
foreach ($element in @($command.CommandElements | Select-Object -Skip 1)) {
    if ($element -is [System.Management.Automation.Language.StringConstantExpressionAst] -or $element -is [System.Management.Automation.Language.ConstantExpressionAst]) { $nativeArgs += [string]$element.Value }
    elseif ($element -is [System.Management.Automation.Language.CommandParameterAst] -and $null -eq $element.Argument -and $element.Extent.Text -ceq ('-' + $element.ParameterName)) { $nativeArgs += $element.Extent.Text }
    else { throw 'Inserted argument is not literal' }
}
& $env:JIRA_TEST_PWSH_OBSERVER '-test.run=^TestNativeCompletionArgumentObserver$' '--' @nativeArgs
if ($LASTEXITCODE -ne 0) { throw 'Native argument observer failed' }
Write-Output 'native-argv-captured'
`
				driverPath := filepath.Join(dir, "driver.ps1")
				if err := os.WriteFile(driverPath, []byte(driver), 0o600); err != nil {
					t.Fatal(err)
				}
				coverage := filepath.Join(dir, "coverage")
				if err := os.Mkdir(coverage, 0o700); err != nil {
					t.Fatal(err)
				}
				// PowerShell on Windows requires a native .exe command name.
				fixtureName := "jira"
				if runtime.GOOS == "windows" {
					fixtureName += ".exe"
				}
				binary, err := os.ReadFile(jiraBinary(t))
				if err != nil {
					t.Fatal("read native CLI fixture", err)
				}
				if err := os.WriteFile(filepath.Join(dir, fixtureName), binary, 0o700); err != nil {
					t.Fatal("prepare native CLI fixture", err)
				}
				env = append(env, "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "JIRA_TEST_PWSH_SCRIPT="+path, "JIRA_TEST_PWSH_LINE="+line, "JIRA_TEST_PWSH_CONTINUATION="+continuation, "JIRA_TEST_PWSH_OBSERVER="+observer, "JIRA_TEST_COMPLETION_OBSERVER=1", "JIRA_TEST_COMPLETION_RECEIPT="+filepath.Join(dir, "argv.json"), "GOCOVERDIR="+coverage)
				stdout, stderr, exit := runExecutableConfiguredIOInDirStarted(t, executable, dir, env, nil, nil, nil, powerShellCompletionConsole, "-NoProfile", "-NonInteractive", "-File", driverPath)
				var got []string
				if exit != 0 || stderr != "" || strings.TrimSpace(stdout) != "native-argv-captured" {
					t.Fatal("native path insertion failed", exit, stdout, stderr)
				}
				// The native observer owns UTF-8 JSON. Reading its receipt directly
				// avoids a second decode/encode through the host's output code page.
				raw, err := os.ReadFile(filepath.Join(dir, "argv.json"))
				if err != nil || json.Unmarshal(raw, &got) != nil {
					t.Fatal("native argument receipt unavailable or malformed", err)
				}
				if role == "file" || role == "csv" || role == "inline" {
					var value string
					if role == "inline" {
						if len(got) != 5 || !strings.HasPrefix(got[4], "--file=") {
							t.Fatal("inline insertion changed native argv", got)
						}
						value = strings.TrimPrefix(got[4], "--file=")
					} else {
						if len(got) != 6 || got[4] != "--file" {
							t.Fatal("CSV insertion changed native argv", got)
						}
						value = got[5]
					}
					fields, err := csv.NewReader(strings.NewReader(value)).Read()
					wantFields := []string{filename}
					if role == "csv" {
						wantFields = append([]string{"earlier,selected"}, wantFields...)
					}
					if err != nil || !slices.Equal(fields, wantFields) {
						t.Fatal("native insertion changed CSV identity", got, fields, err)
					}
				} else if strings.HasPrefix(role, "download ") {
					// Native filesystem completion may include the current-directory
					// prefix or final separator. Admit only those spelling wrappers.
					if len(got) != len(want) || !slices.Equal(got[:5], want[:5]) || strings.TrimSuffix(strings.TrimPrefix(filepath.ToSlash(got[5]), "./"), "/") != filepath.ToSlash(want[5]) {
						t.Fatal("native download directory insertion changed literal identity", got, want)
					}
				} else if !slices.Equal(got, want) {
					t.Fatal("native insertion changed file identity", got, want)
				}
			})
		}
	}
}
