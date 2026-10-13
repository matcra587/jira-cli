package selfupdate

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	xos "github.com/gechr/x/os"
)

// TestNativeReleaseDownloadPublication downloads an explicitly selected native
// artifact through the real release SDK and publishes it to a disposable copy.
// Both source artifacts stay untouched; no test builds or updates the host CLI.
func TestNativeReleaseDownloadPublication(t *testing.T) {
	original := os.Getenv("JIRA_UPDATE_TEST_ORIGINAL")
	candidatePath := os.Getenv("JIRA_UPDATE_TEST_CANDIDATE")
	if original == "" && candidatePath == "" {
		t.Skip("requires two explicitly selected native executable artifacts")
	}
	if original == "" || candidatePath == "" {
		t.Fatal("both native executable artifacts are required")
	}
	name := "renamed-jira"
	if runtime.GOOS == xos.PlatformWindows {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := xos.CopyFile(original, path); err != nil {
		t.Fatal(err)
	}
	before := nativeVersion(t, path)
	want := nativeVersion(t, candidatePath)
	if before == want || want != "v1.2.3" {
		t.Fatalf("native versions = %q / %q; candidate must be 1.2.3", before, want)
	}
	data, err := os.ReadFile(candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	archive := tarFixture(t, archiveMember{name: "jira", body: string(data)})
	if runtime.GOOS == xos.PlatformWindows {
		archive = zipFixture(t, archiveMember{name: "jira.exe", body: string(data)})
	}
	f := newReleaseFixture(t, runtime.GOOS, runtime.GOARCH, archive)
	release, err := f.source.Latest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	target, err := OpenTarget(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := f.source.Download(t.Context(), release, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		if closeErr := target.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal(err)
	}
	publication, publishErr := target.Publish(t.Context(), candidate.File, candidate.SHA256)
	if err := errors.Join(publishErr, target.Close(), candidate.Close()); err != nil || !publication.Published || publication.Uncertain || publication.SHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) {
		t.Fatalf("native release publication = %+v, %v", publication, err)
	}
	f.assertPrivateCleanup(t)
	if got := nativeVersion(t, path); got != want {
		t.Fatalf("installed version=%q, want %q", got, want)
	}
	if got := nativeVersion(t, original); got != before {
		t.Fatalf("original version changed to %q", got)
	}
	f.assertRequestCount(t, "/releases/latest", 1)
	f.assertRequestCount(t, "/assets/10", 1)
	f.assertRequestCount(t, "/assets/11", 1)
	assertNoStage(t, filepath.Dir(path))
}
