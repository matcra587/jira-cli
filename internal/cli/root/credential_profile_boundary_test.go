package root

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/matcra587/jira-cli/internal/config"
)

func TestEmptyNamedProfilesLogoutPreservesFlatCredential(t *testing.T) {
	// Upstream keyring mocks replace a process-global native driver. Isolate
	// that mutation so this regression cannot change another test's provider.
	if os.Getenv("JIRA_TEST_PROFILE_BOUNDARY_CHILD") != "1" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		child := exec.CommandContext(t.Context(), executable, "-test.run=^TestEmptyNamedProfilesLogoutPreservesFlatCredential$")
		child.Env = []string{"JIRA_TEST_PROFILE_BOUNDARY_CHILD=1", "JIRA_NO_UPDATE_CHECK=1", "NO_COLOR=1"}
		for _, name := range []string{"PATH", "PATHEXT", "SystemRoot", "WINDIR", "TEMP", "TMP"} {
			if value := os.Getenv(name); value != "" {
				child.Env = append(child.Env, name+"="+value)
			}
		}
		if output, err := child.CombinedOutput(); err != nil {
			t.Fatalf("isolated credential boundary: %v\n%s", err, output)
		}
		return
	}
	keyring.MockInit()
	const legacy = `{"version":2,"site":"https://legacy.example.invalid","email":"legacy@example.invalid","token":"synthetic-legacy-token"}`
	if err := keyring.Set("jira-cli", "default", legacy); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(path, []byte("format_version=1\ndefault_profile=''\n[profiles]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JIRA_EMAIL", "selected@example.invalid")
	config.SetFlagLayer(config.FlagLayer{Path: path})
	t.Cleanup(func() { config.SetFlagLayer(config.FlagLayer{}) })
	var stdout, stderr bytes.Buffer
	deps := defaultDeps()
	deps.NotifyTool = nil
	program, sink := newProgram(deps, &stdout, &stderr)
	if exit := execute(t.Context(), program, sink, []string{"--config", path, "--output=json", "auth", "logout", "--yes"}); exit != 0 {
		t.Fatalf("logout exit=%d: %s\n%s", exit, stdout.String(), stderr.String())
	}
	retained, err := keyring.Get("jira-cli", "default")
	if err != nil || retained != legacy {
		t.Fatalf("empty named configuration touched the legacy default credential: %v", err)
	}
	if bytes.Contains(stdout.Bytes(), []byte("legacy@example.invalid")) {
		t.Fatal("logout inspected an unselected legacy identity")
	}
}
