package root

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gechr/clog"
)

func BenchmarkNewProgram(b *testing.B) {
	original := clog.Default()
	b.Cleanup(func() { clog.SetDefault(original) })
	deps := &Deps{}
	b.ReportAllocs()
	for b.Loop() {
		if program := NewProgram(deps, io.Discard, io.Discard); program.Root == nil {
			b.Fatal("program has no command tree")
		}
	}
}

// BenchmarkVersionStartup includes package initialization, which in-process
// construction benchmarks exclude. Build and fixture setup stay outside Loop.
func BenchmarkVersionStartup(b *testing.B) {
	dir := b.TempDir()
	binary := filepath.Join(dir, "jira")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(b.Context(), "go", "build",
		"-mod=readonly", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w",
		"-o", binary, "../../../cmd/jira")
	if output, err := build.CombinedOutput(); err != nil {
		b.Fatalf("build startup benchmark: %v\n%s", err, output)
	}
	config := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(config, []byte("keyring = false\n"), 0o600); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		cmd := exec.CommandContext(b.Context(), binary, "--config", config, "version", "--output=json")
		cmd.Env = []string{"HOME=" + dir, "PATH=" + os.Getenv("PATH"), "TERM=dumb", "NO_COLOR=1", "JIRA_KEYRING=false"}
		if runtime.GOOS == "windows" {
			cmd.Env = append(cmd.Env,
				"PATHEXT="+os.Getenv("PATHEXT"), "USERPROFILE="+dir,
				"APPDATA="+filepath.Join(dir, "AppData", "Roaming"),
				"LOCALAPPDATA="+filepath.Join(dir, "AppData", "Local"))
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		data, err := cmd.Output()
		if err != nil || stderr.Len() != 0 {
			b.Fatalf("version startup: %v; stderr=%s", err, &stderr)
		}
		var result struct {
			OK   bool `json:"ok"`
			Meta struct {
				Command string `json:"command"`
			} `json:"meta"`
			Data struct {
				Version string `json:"version"`
			} `json:"data"`
		}
		if err := json.Unmarshal(data, &result); err != nil || !result.OK || result.Meta.Command != "version" || result.Data.Version == "" {
			b.Fatal("version startup did not produce a successful JSON envelope")
		}
	}
}
