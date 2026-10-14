//go:build flagmatrix

package matrix

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/docent"
)

func TestAgentSchemaFlagEffects(t *testing.T) {
	t.Parallel()
	selected := fwParse(t, fwExec(t, "", nil, "", "agent", "schema", "--path=api post"))
	data, err := json.Marshal(selected.Data["schema"])
	if err != nil {
		t.Fatal(err)
	}
	var subtree docent.Command
	if json.Unmarshal(data, &subtree) != nil || subtree.Path != "jira api post" || len(subtree.Children) != 0 || subtree.OutputSchema == nil || subtree.HasOutputSchema {
		t.Fatal("--path did not select and embed the named subtree")
	}
	Prove(t, "jira agent schema", "path", "selected exactly api post with an embedded registered output shape and no sibling commands")
	plain := fwParse(t, fwExec(t, "", nil, "", "agent", "schema"))
	data, err = json.Marshal(plain.Data["schema"])
	if err != nil {
		t.Fatal(err)
	}
	var routing docent.Command
	if json.Unmarshal(data, &routing) != nil {
		t.Fatal("routing schema failed to decode")
	}
	write, ok := docent.FindByPath(routing, "jira api post")
	if !ok || !write.HasOutputSchema || write.OutputSchema != nil {
		t.Fatal("default routing schema did not omit the shape body")
	}
	full := fwParse(t, fwExec(t, "", nil, "", "agent", "schema", "--shapes"))
	data, err = json.Marshal(full.Data["schema"])
	if err != nil {
		t.Fatal(err)
	}
	var embedded docent.Command
	if json.Unmarshal(data, &embedded) != nil {
		t.Fatal("embedded schema failed to decode")
	}
	write, ok = docent.FindByPath(embedded, "jira api post")
	if !ok || write.HasOutputSchema || write.OutputSchema == nil || len(embedded.Defs) == 0 {
		t.Fatal("--shapes did not embed and pool the full tree's shapes")
	}
	Prove(t, "jira agent schema", "shapes", "default tree omits bodies, while --shapes embeds them and pools repeated shapes into the artifact's definitions")
}

func TestGuideFlagEffects(t *testing.T) {
	t.Parallel()
	for _, route := range []struct {
		path string
		args []string
	}{{"jira agent guide", []string{"agent", "guide"}}, {"jira guide", []string{"guide"}}} {
		all := fwParse(t, fwExec(t, "", nil, "", slices.Concat(route.args, []string{"--all"})...))
		if all.Data["kind"] != "all" || !strings.Contains(all.Data["document"].(string), "\f\n") {
			t.Fatal("--all did not return the complete form-feed-separated corpus")
		}
		Prove(t, route.path, "all", "selected the complete corpus with canonical form-feed separators instead of the default index")
		section := fwParse(t, fwExec(t, "", nil, "", slices.Concat(route.args, []string{"raw-api", "--section=Save"})...))
		if section.Data["kind"] != "section" || section.Data["section"] != "Save" || !strings.HasPrefix(section.Data["document"].(string), "\nRetain the validated") || strings.Contains(section.Data["document"].(string), "## Run") {
			t.Fatal("--section did not select only the named section")
		}
		Prove(t, route.path, "section", "selected only the Save section body and its canonical heading instead of the full runbook")
	}
}

func TestGuideExportFlagEffects(t *testing.T) {
	t.Parallel()
	base := []string{"agent", "export", "--format=agent-skill", "--dir=chosen-dir"}
	preview := fwParse(t, fwExec(t, "", nil, "", append(slices.Clone(base), "--dry-run")...))
	if preview.Data["dry_run"] != true || preview.Data["attempted"] != false || len(preview.Data["written_paths"].([]any)) != 0 {
		t.Fatal("--dry-run did not retain a preparation-only receipt")
	}
	Prove(t, "jira agent export", "dry-run", "dry-run=true with attempted=false and no published paths")
	if !strings.HasSuffix(preview.Data["directory"].(string), "chosen-dir") {
		t.Fatal("--dir lost the selected destination")
	}
	Prove(t, "jira agent export", "dir", "selected destination ends in the literal chosen-dir")
	claude := fwParse(t, fwExec(t, "", nil, "", "agent", "export", "--format=claude-skill", "--dir=chosen-dir", "--dry-run"))
	if preview.Data["format"] != "agent-skill" || claude.Data["format"] != "claude-skill" || preview.Data["artifacts"].([]any)[0].(map[string]any)["sha256"] == claude.Data["artifacts"].([]any)[0].(map[string]any)["sha256"] {
		t.Fatal("--format did not change the rendered artifact bytes")
	}
	Prove(t, "jira agent export", "format", "portable and Claude previews have different rendered content digests")
	project := fwSuccess(t, fwExec(t, "", nil, "", "agent", "export", "--scope=project", "--harness=codex", "--dry-run"))
	home := t.TempDir()
	env := []string{"HOME=" + home}
	if runtime.GOOS == "windows" {
		env = append(env, "USERPROFILE="+home,
			"APPDATA="+filepath.Join(home, "AppData", "Roaming"),
			"LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"))
	}
	user := fwSuccess(t, fwExecEnv(t, "", nil, "", env, "agent", "export", "--scope=user", "--harness=codex", "--dry-run"))
	if project.Data["scope"] != "project" || user.Data["scope"] != "user" || user.Data["directory"] != filepath.Join(home, ".agents", "skills") || project.Data["directory"] == user.Data["directory"] {
		t.Fatal("--scope did not select working-directory versus home installation")
	}
	Prove(t, "jira agent export", "scope", "project and user scopes resolve distinct directories and retain their source scope")
	native := fwSuccess(t, fwExec(t, "", nil, "", "agent", "export", "--scope=project", "--harness=claude-code", "--dry-run"))
	if project.Data["harness"] != "codex" || native.Data["harness"] != "claude-code" || native.Data["format"] != "claude-skill" || !strings.HasSuffix(native.Data["directory"].(string), filepath.Join(".claude", "skills")) {
		t.Fatal("--harness did not select native format and directory")
	}
	Prove(t, "jira agent export", "harness", "selected harness changes native format and skills directory")
	directory := t.TempDir()
	parent := filepath.Join(directory, "jira-discover-cli")
	if err := os.Mkdir(parent, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "SKILL.md"), []byte("existing edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	forceArgs := []string{"agent", "export", "--format=agent-skill", "--dir", directory}
	blocked := fwExec(t, "", nil, "", forceArgs...)
	if blocked.Exit != 3 {
		t.Fatal("existing artifact was replaced without acknowledgement")
	}
	forced := fwParse(t, fwExec(t, "", nil, "", append(forceArgs, "--force")...))
	if len(forced.Data["written_paths"].([]any)) != 11 {
		t.Fatal("--force did not publish the acknowledged replacement")
	}
	Prove(t, "jira agent export", "force", "unacknowledged collision fails; --force publishes the complete eleven-guide corpus")
}
