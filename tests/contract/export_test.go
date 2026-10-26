package contract

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/agentguides"
	agentcmd "github.com/matcra587/jira-cli/internal/cli/agent"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func readExportOutput(t *testing.T, stdout, mode string, success bool) envelope.GuideExportOutput {
	t.Helper()
	payload := []byte(stdout)
	if mode == "json" || !success {
		var f frame
		if json.Unmarshal(payload, &f) != nil || f.OK != success || f.Meta.Command != "agent.export" {
			t.Fatalf("export lost its shared invocation frame: %q", stdout)
		}
		payload = f.Data
	}
	var out envelope.GuideExportOutput
	if json.Unmarshal(payload, &out) != nil || out.ContractVersion != "1" || out.Directory == "" || len(out.Artifacts) != 12 || out.WrittenPaths == nil {
		t.Fatalf("export receipt lost its prepared corpus: %q", payload)
	}
	var raw any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatal(err)
	}
	assertSnakeCaseKeys(t, "$", raw)
	return out
}

func TestGuideExportFilesModesAndDryRun(t *testing.T) {
	t.Parallel()
	guides, err := agentguides.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"agent-skill", "claude-skill"} {
		for _, mode := range []string{"json", "compact"} {
			t.Run(format+"/"+mode, func(t *testing.T) {
				directory := filepath.Join(t.TempDir(), "exported")
				args := []string{"agent", "export", "--format", format, "--dir", directory, "--output=" + mode}
				env := []string{"AGENT=1", "JIRA_KEYRING=false", "JIRA_EMAIL=", "JIRA_API_TOKEN=", "JIRA_READ_ONLY=true"}
				stdout, stderr, exit := runBinaryEnv(t, env, append(slices.Clone(args), "--dry-run")...)
				if exit != 0 || stderr != "" {
					t.Fatal("credential-free export preview failed")
				}
				preview := readExportOutput(t, stdout, mode, true)
				if !preview.DryRun || preview.Attempted || len(preview.WrittenPaths) != 0 {
					t.Fatal("preview reported publication")
				}
				if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("preview created an export destination")
				}
				stdout, stderr, exit = runBinaryEnv(t, env, args...)
				if exit != 0 || stderr != "" {
					t.Fatal("credential-free local export failed under Jira read-only policy")
				}
				out := readExportOutput(t, stdout, mode, true)
				plan, err := agentguides.PlanExport(guides, format)
				if err != nil || out.DryRun || !out.Attempted || len(out.WrittenPaths) != 12 || out.Format != format {
					t.Fatal("live local export lost publication facts")
				}
				for i, artifact := range out.Artifacts {
					body, err := os.ReadFile(filepath.Join(out.Directory, filepath.FromSlash(artifact.Path)))
					if err != nil || !slices.Equal(body, plan[i].Content) || artifact.SHA256 != fmt.Sprintf("%x", sha256.Sum256(body)) || artifact.SHA256 != preview.Artifacts[i].SHA256 || artifact.Bytes != len(body) || !artifact.Written || out.WrittenPaths[i] != artifact.Path {
						t.Fatal("publication diverged from the byte-identical prepared plan")
					}
				}
			})
		}
	}
}

func TestGuideExportScopesAndHarnessResolution(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		env                     []string
		args                    []string
		harness, format, suffix string
	}{
		{[]string{"CODEX=1"}, nil, "codex", "agent-skill", ".agents/skills"},
		{[]string{"AI_AGENT=claude-code_2-1-211_agent"}, nil, "claude-code", "claude-skill", ".claude/skills"},
		{[]string{"CLAUDECODE=1"}, []string{"--harness=codex"}, "codex", "agent-skill", ".agents/skills"},
		{nil, []string{"--harness=claude-code", "--format=agent-skill"}, "claude-code", "agent-skill", ".claude/skills"},
	} {
		for _, scope := range []string{"project", "user"} {
			home := t.TempDir()
			env := slices.Concat(tc.env, []string{"HOME=" + home, "JIRA_KEYRING=false", "JIRA_EMAIL=", "JIRA_API_TOKEN="})
			args := slices.Concat([]string{"agent", "export", "--scope=" + scope, "--output=json"}, tc.args)
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != 0 || stderr != "" {
				t.Fatalf("scope resolution failed: %s %s", tc.harness, scope)
			}
			out := readExportOutput(t, stdout, "json", true)
			if out.Scope != scope || out.Harness != tc.harness || out.Format != tc.format || !strings.HasSuffix(filepath.ToSlash(out.Directory), tc.suffix) {
				t.Fatal("scope or explicit harness/format override changed destination semantics")
			}
			if scope == "user" && out.Directory != filepath.Join(home, filepath.FromSlash(tc.suffix)) {
				t.Fatal("user scope escaped the injected home directory")
			}
		}
	}
}

func TestGuideExportCollisionRecoveryAndPartialReceipts(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		directory := filepath.Join(t.TempDir(), "a directory's skills")
		parent := filepath.Join(directory, "jira-discover-cli")
		if err := os.MkdirAll(parent, 0o750); err != nil {
			t.Fatal(err)
		}
		original := filepath.Join(parent, "SKILL.md")
		if err := os.WriteFile(original, []byte("retain user edit"), 0o600); err != nil {
			t.Fatal(err)
		}
		args := []string{"agent", "export", "--format=agent-skill", "--dir", directory, "--output=" + mode}
		stdout, _, exit := runBinary(t, args...)
		out := readExportOutput(t, stdout, mode, false)
		body, err := os.ReadFile(original)
		if exit != 3 || err != nil || string(body) != "retain user edit" || len(out.WrittenPaths) != 0 || out.AttemptedPath != "jira-discover-cli/SKILL.md" {
			t.Fatal("collision modified a user edit or discarded preparation evidence")
		}
		var failed struct {
			Errors []struct {
				Code, Field string
				Suggestions []string
			}
		}
		if json.Unmarshal([]byte(stdout), &failed) != nil || len(failed.Errors) != 1 || failed.Errors[0].Code != "flag_conflict" || failed.Errors[0].Field != "force" || len(failed.Errors[0].Suggestions) != 1 {
			t.Fatal("collision did not offer an acknowledged executable correction")
		}
		correction, err := shell.Split(strings.TrimPrefix(failed.Errors[0].Suggestions[0], "jira "))
		if err != nil {
			t.Fatal(err)
		}
		stdout, stderr, exit := runBinary(t, correction...)
		if exit != 0 || stderr != "" {
			t.Fatal("literal collision correction failed with a quoted destination")
		}
		readExportOutput(t, stdout, "json", true)
		// Force acknowledges existing artifacts; a blocked later parent then
		// produces a real partial outcome after the first file is published.
		if err := os.RemoveAll(filepath.Join(directory, "jira-machine-output")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "jira-machine-output"), []byte("blocked parent"), 0o600); err != nil {
			t.Fatal(err)
		}
		stdout, _, exit = runBinary(t, append(args, "--force")...)
		out = readExportOutput(t, stdout, mode, false)
		if exit != 3 || !slices.Equal(out.WrittenPaths, []string{"jira-discover-cli/SKILL.md"}) || !out.Artifacts[0].Written || out.Artifacts[1].Written || out.AttemptedPath != "jira-machine-output/SKILL.md" {
			t.Fatal("partial filesystem failure discarded or invented publication facts")
		}
	}
}

func TestGuideExportRejectsInputAndExecutesHelp(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{},
		{"--dir=skills"},
		{"--format=other", "--dir=skills"},
		{"--dir=skills", "--scope=project"},
		{"--scope=other", "--harness=codex"},
		{"--scope=project", "--harness=other"},
		{"--scope=project"},
		{"--dir=skills", "--format=agent-skill", "--harness=codex"},
		{"--format=agent-skill", "--dir=bad\npath"},
	} {
		stdout, _, exit := runBinary(t, slices.Concat([]string{"agent", "export"}, args, []string{"--output=json"})...)
		var f frame
		if exit != 3 || json.Unmarshal([]byte(stdout), &f) != nil || f.OK || string(f.Data) != "null" {
			t.Fatalf("bad export flags reached filesystem preparation: %v", args)
		}
	}
	guides, err := agentguides.Load()
	if err != nil {
		t.Fatal(err)
	}
	cmd, _, err := agentcmd.New(agentcmd.Deps{Guides: guides}).Find([]string{"export"})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for line := range strings.SplitSeq(cmd.Example, "\n") {
		if !strings.HasPrefix(line, "$ jira ") {
			continue
		}
		args, err := shell.Split(strings.TrimPrefix(line, "$ jira "))
		if err != nil {
			t.Fatal(err)
		}
		stdout, stderr, exit := runBinaryEnv(t, []string{"AGENT=1", "JIRA_KEYRING=false"}, args...)
		if exit != 0 || stderr != "" {
			t.Fatalf("published export help recipe failed: %q", line)
		}
		mode := "json"
		if slices.Contains(args, "--output=compact") {
			mode = "compact"
		}
		readExportOutput(t, stdout, mode, true)
		count++
	}
	if count != 3 {
		t.Fatal("export help changed without execution coverage")
	}
}
