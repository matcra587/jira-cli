package selfupdate

import (
	"errors"
	"io"
	"testing"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestDownloadProgressFollowsAcceptedBytesAndIntegrityBoundary(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "tampered"}[corrupt], func(t *testing.T) {
			archive := tarFixture(t, archiveMember{name: "jira", body: "fixture executable"})
			fixture := newReleaseFixture(t, xos.PlatformLinux, xos.ArchARM64, archive)
			if corrupt {
				fixture.mu.Lock()
				fixture.assets["10"] = append([]byte(nil), archive...)
				fixture.assets["10"][0] ^= 1
				fixture.mu.Unlock()
			}
			var observed []core.Progress
			ctx := core.WithProgressObserver(t.Context(), func(progress core.Progress) { observed = append(observed, progress) })
			release, err := fixture.source.Latest(ctx)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := fixture.source.Download(ctx, release, xos.PlatformLinux, xos.ArchARM64)
			if corrupt {
				if !errors.Is(err, ErrIntegrity) || candidate != nil {
					t.Fatalf("tampered archive accepted: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer func() {
						if err := candidate.Close(); err != nil {
							t.Error(err)
						}
					}()
					if _, err := candidate.File.Seek(0, io.SeekStart); err != nil {
						t.Fatal(err)
					}
					body, readErr := io.ReadAll(candidate.File)
					if readErr != nil || string(body) != "fixture executable" {
						t.Fatalf("progress changed candidate bytes: %q, %v", body, readErr)
					}
				}()
			}
			var last int64
			completed, extracted := false, false
			for _, progress := range observed {
				if progress.Phase == core.ProgressUpdateDownload {
					if progress.Completed < last || progress.Total != int64(len(archive)) || progress.Completed > progress.Total {
						t.Fatalf("transfer progress invented bytes: %+v", observed)
					}
					last, completed = progress.Completed, progress.Complete
				}
				extracted = extracted || progress.Phase == core.ProgressUpdateExtract
			}
			if !completed || last != int64(len(archive)) || extracted == corrupt {
				t.Fatalf("download/extraction boundary lost: %+v", observed)
			}
			fixture.assertPrivateCleanup(t)
		})
	}
}
