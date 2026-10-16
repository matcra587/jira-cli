//go:build linux && nativekeyring && live

package contract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/json/jsontext"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSelectedNativeKeyringAuthLifecycle runs the exact external CLI in the
// existing private bus owner. It never substitutes a test-built CLI or mock store.
func TestSelectedNativeKeyringAuthLifecycle(t *testing.T) {
	binary, digest := os.Getenv("JIRA_NATIVE_AUTH_BINARY"), os.Getenv("JIRA_NATIVE_AUTH_SHA256")
	if !filepath.IsAbs(binary) || len(digest) != 64 {
		t.Fatal("selected native auth requires an absolute binary path and its admitted SHA-256")
	}
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		t.Fatal("selected native auth digest is not SHA-256")
	}
	fingerprint := func() {
		t.Helper()
		file, err := os.Open(binary)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.New()
		_, readErr := io.Copy(hash, file)
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || hex.EncodeToString(hash.Sum(nil)) != digest {
			t.Fatal("selected native auth binary does not match its admitted SHA-256")
		}
	}
	fingerprint()
	stdout, stderr, exit := runExecutableIOInDirStarted(t, binary, t.TempDir(), nil, nil, nil, nil, "version", "--output=json")
	var version struct {
		OK   bool
		Meta struct{ Command string }
		Data struct{ Version string }
	}
	if exit != 0 || stderr != "" || !jsontext.Value(stdout).IsValid() || json.Unmarshal([]byte(stdout), &version) != nil ||
		!version.OK || version.Meta.Command != "version" || version.Data.Version == "" {
		t.Fatal("selected native auth binary did not execute its version command")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd, _ := nativeKeyringSession(t, ctx, binary, false)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("selected CLI private native lifecycle: %v\n%s", err, out)
	}
	fingerprint()
	t.Logf("selected CLI %s; SHA-256 %s\n%s", version.Data.Version, digest, out)
}
