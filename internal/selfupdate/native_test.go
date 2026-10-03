package selfupdate

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	xos "github.com/gechr/x/os"
)

// TestNativeExecutablePublication uses externally built, explicitly selected
// artifacts. Ordinary tests skip it; no test compiles or updates the host CLI.
func TestNativeExecutablePublication(t *testing.T) {
	original := os.Getenv("JIRA_UPDATE_TEST_ORIGINAL")
	candidatePath := os.Getenv("JIRA_UPDATE_TEST_CANDIDATE")
	if original == "" && candidatePath == "" {
		t.Skip("requires two explicitly selected native executable artifacts")
	}
	if original == "" || candidatePath == "" {
		t.Fatal("both native executable artifacts are required")
	}
	path := filepath.Join(t.TempDir(), filepath.Base(original))
	if err := xos.CopyFile(original, path); err != nil {
		t.Fatal(err)
	}
	before := nativeVersion(t, path)
	want := nativeVersion(t, candidatePath)
	if before == want {
		t.Fatal("native fixtures must have different versions")
	}
	target, err := OpenTarget(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := target.Close(); err != nil {
			t.Error(err)
		}
	}()
	candidate, err := os.Open(candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := candidate.Close(); err != nil {
			t.Error(err)
		}
	}()
	hash := sha256.New()
	if _, err := io.Copy(hash, candidate); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", hash.Sum(nil))
	out, err := target.Publish(t.Context(), candidate, digest)
	if err != nil || !out.Published || out.Uncertain || out.SHA256 != digest {
		t.Fatalf("native publication = %+v, error %v", out, err)
	}
	if got := nativeVersion(t, path); got != want {
		t.Fatalf("installed executable reports %q, want %q", got, want)
	}
	if got := nativeVersion(t, original); got != before {
		t.Fatalf("source artifact changed from %q to %q", before, got)
	}
	assertNoStage(t, filepath.Dir(path))
}

func nativeVersion(t *testing.T, path string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), path, "version", "--output=compact")
	// Version is a local read. Keep the command independent of user preferences
	// and automatic output detection while exercising the executable itself.
	cmd.Env = append(os.Environ(), "JIRA_CONFIG_DIR="+t.TempDir(), "JIRA_AGENT=1")
	bytes, err := cmd.Output()
	if err != nil {
		t.Fatalf("native version command did not execute successfully: %v", err)
	}
	var out struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(bytes, &out); err != nil || out.Version == "" {
		t.Fatalf("native version receipt invalid: %v", err)
	}
	return out.Version
}
