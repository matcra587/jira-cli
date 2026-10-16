//go:build live

package live

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParityLiveWorkingDirectoryHelper(t *testing.T) {
	if os.Getenv("JIRA_PARITY_CWD_HELPER") != "1" {
		return
	}
	directory, err := os.Getwd()
	if err != nil {
		os.Exit(19)
	}
	raw, err := json.Marshal(directory)
	if err != nil {
		os.Exit(20)
	}
	_, err = os.Stdout.Write(raw)
	if err != nil {
		os.Exit(21)
	}
	os.Exit(0)
}

func TestParityLiveWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "downloads")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	expected, err := liveInvocationDirectory(root, directory)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	environment := []string{"JIRA_PARITY_CWD_HELPER=1", "HOME=" + root, "USERPROFILE=" + root, "APPDATA=" + root, "LOCALAPPDATA=" + root, "XDG_CONFIG_HOME=" + root, "XDG_CACHE_HOME=" + root}
	if value := os.Getenv("PATHEXT"); value != "" {
		environment = append(environment, "PATHEXT="+value)
	}
	r := &liveRun{dir: root}
	stdout, exit := r.invokeEnvironmentRawAt(t, t.Context(), executable, directory, environment, "-test.run=^TestParityLiveWorkingDirectoryHelper$")
	var observed string
	if exit != 0 || json.Unmarshal(stdout, &observed) != nil || observed != expected {
		t.Fatalf("owned subprocess cwd was not actually selected: exit%d stdout%s", exit, stdout)
	}
	current, err := os.Getwd()
	if err != nil || current != original {
		t.Fatal("subprocess working-directory selection changed the test process cwd")
	}
	file := filepath.Join(root, "regular-file")
	if err := os.WriteFile(file, []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"relative": "downloads", "outside": t.TempDir(), "missing": filepath.Join(root, "missing"), "regular-file": file} {
		t.Run(name, func(t *testing.T) {
			if _, err := liveInvocationDirectory(root, path); err == nil {
				t.Fatal("working-directory guard admitted an absent, foreign or non-directory path")
			}
		})
	}
	t.Run("symlink-escape", func(t *testing.T) {
		link := filepath.Join(root, "outside-link")
		if err := os.Symlink(t.TempDir(), link); err != nil {
			t.Skipf("native symlink creation unavailable: %v", err)
		}
		if _, err := liveInvocationDirectory(root, link); err == nil {
			t.Fatal("working-directory guard followed a link outside its owned root")
		}
	})
}
