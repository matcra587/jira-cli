package selfupdate

import (
	"errors"
	"testing"
)

func TestUnsignedReleaseRefusesBeforeArchiveDownload(t *testing.T) {
	fixture := newReleaseFixture(t, "linux", "amd64", tarFixture(t, archiveMember{name: "jira", body: "fixture"}))
	fixture.changeMetadata(t, func(metadata map[string]any) {
		metadata["assets"] = metadata["assets"].([]any)[:2]
	})
	release, err := fixture.source.Latest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := fixture.source.Download(t.Context(), release, "linux", "amd64")
	if candidate != nil {
		if closeErr := candidate.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	if candidate != nil || !errors.Is(err, ErrIntegrity) {
		t.Fatalf("unsigned release was not refused: candidate=%t err=%v", candidate != nil, err)
	}
	fixture.assertRequestCount(t, "/assets/10", 0)
	fixture.assertPrivateCleanup(t)
}
