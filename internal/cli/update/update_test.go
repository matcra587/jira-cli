package update

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/selfupdate"
)

type fakeSource struct {
	release   selfupdate.Release
	latestErr error
	candidate *selfupdate.Candidate
	download  func(context.Context, selfupdate.Release, string, string) (*selfupdate.Candidate, error)
}

func (f *fakeSource) Latest(context.Context) (selfupdate.Release, error) {
	return f.release, f.latestErr
}

func (f *fakeSource) Download(ctx context.Context, release selfupdate.Release, goos, arch string) (*selfupdate.Candidate, error) {
	if f.download != nil {
		return f.download(ctx, release, goos, arch)
	}
	return f.candidate, nil
}

type fakeTarget struct {
	publication selfupdate.Publication
	err         error
	closeErr    error
	closed      bool
	publish     func(context.Context, *os.File, string) (selfupdate.Publication, error)
}

func (f *fakeTarget) Publish(ctx context.Context, file *os.File, digest string) (selfupdate.Publication, error) {
	if f.publish != nil {
		return f.publish(ctx, file, digest)
	}
	return f.publication, f.err
}

func (f *fakeTarget) Close() error {
	f.closed = true
	return f.closeErr
}

type fakeBrew struct {
	out selfupdate.BrewOutcome
	err error
}

func (f fakeBrew) Update(context.Context) (selfupdate.BrewOutcome, error) { return f.out, f.err }

func noUpdateIO(t *testing.T, installation selfupdate.Install) Deps {
	t.Helper()
	return Deps{
		Detect: func() (selfupdate.Install, error) { return installation, nil },
		Source: func() (Source, error) { t.Fatal("release reader constructed before authorization"); return nil, nil },
		Target: func(context.Context, string) (Target, error) {
			t.Fatal("archive owner opened before authorization")
			return nil, nil
		},
		Brew: func(context.Context, string) (BrewUpdater, error) {
			t.Fatal("brew owner opened before authorization")
			return nil, nil
		},
	}
}

func TestManagedUpdateReturnsLocalHint(t *testing.T) {
	t.Parallel()
	for _, channel := range []selfupdate.Channel{selfupdate.ChannelScoop, selfupdate.ChannelMise, selfupdate.ChannelGoInstall} {
		t.Run(string(channel), func(t *testing.T) {
			t.Parallel()
			installation := selfupdate.Install{Channel: channel, Hint: "owned installer update command"}
			out, err := run(t.Context(), noUpdateIO(t, installation), command.Invocation{Version: "v1.0.0"}, false)
			if err != nil || !out.Managed || out.Hint != installation.Hint || out.UpdateAvailable != nil || out.Attempted || out.Updated {
				t.Fatalf("managed update = %+v, %v", out, err)
			}
		})
	}
}

func TestUpdateRefusesUnsupportedAndHeadlessBeforeIO(t *testing.T) {
	t.Parallel()
	for _, channel := range []selfupdate.Channel{selfupdate.ChannelUnknown, selfupdate.ChannelArchive, selfupdate.ChannelBrew} {
		t.Run(string(channel), func(t *testing.T) {
			t.Parallel()
			out, err := run(t.Context(), noUpdateIO(t, selfupdate.Install{Channel: channel}), command.Invocation{Version: "v1.0.0", NoInput: true}, false)
			want := cli.CodeFlagRequired
			if channel == selfupdate.ChannelUnknown {
				want = cli.CodeUpdateUnsupported
			}
			if err == nil || cli.MapError(err).Code != want || out.Attempted || out.Updated || out.UpdateAvailable != nil {
				t.Fatalf("refusal = %+v, %v", out, err)
			}
		})
	}
}

func TestUpdatePreviewAndCurrentReleaseDoNotInstall(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		latest  string
		preview bool
		want    bool
	}{
		{name: "preview", latest: "v1.1.0", preview: true, want: true},
		{name: "current", latest: "v1.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			target := &fakeTarget{publish: func(context.Context, *os.File, string) (selfupdate.Publication, error) {
				t.Fatal("preview or current release attempted publication")
				return selfupdate.Publication{}, nil
			}}
			source := &fakeSource{
				release: selfupdate.Release{ID: 37, Version: tc.latest},
				download: func(context.Context, selfupdate.Release, string, string) (*selfupdate.Candidate, error) {
					t.Fatal("preview or current release downloaded an archive")
					return nil, nil
				},
			}
			deps := Deps{
				Detect: func() (selfupdate.Install, error) { return selfupdate.Install{Channel: selfupdate.ChannelArchive}, nil },
				Target: func(context.Context, string) (Target, error) { return target, nil },
				Source: func() (Source, error) { return source, nil },
			}
			out, err := run(t.Context(), deps, command.Invocation{Version: "v1.0.0", DryRun: tc.preview}, true)
			if err != nil || out.UpdateAvailable == nil || *out.UpdateAvailable != tc.want || out.Latest != tc.latest || out.Attempted || out.Updated || !target.closed {
				t.Fatalf("availability = %+v, %v, target closed %t", out, err, target.closed)
			}
		})
	}
}

func TestArchiveUpdateRetainsPublicationAndFrozenRelease(t *testing.T) {
	t.Parallel()
	file, err := os.Create(filepath.Join(t.TempDir(), "candidate"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if _, err := file.WriteString("verified candidate"); err != nil {
		t.Fatal(err)
	}
	release := selfupdate.Release{ID: 37, Version: "v1.1.0"}
	source := &fakeSource{release: release, download: func(_ context.Context, observed selfupdate.Release, goos, arch string) (*selfupdate.Candidate, error) {
		if observed.ID != release.ID || observed.Version != release.Version || goos != runtime.GOOS || arch != runtime.GOARCH {
			t.Fatalf("download changed release/platform: %+v, %s/%s", observed, goos, arch)
		}
		return &selfupdate.Candidate{File: file, SHA256: "owned verified digest"}, nil
	}}
	target := &fakeTarget{publish: func(_ context.Context, candidate *os.File, digest string) (selfupdate.Publication, error) {
		if candidate != file || digest != "owned verified digest" {
			t.Fatal("publication lost verified descriptor/digest")
		}
		return selfupdate.Publication{Published: true, Bytes: 18, SHA256: digest}, context.DeadlineExceeded
	}}
	deps := Deps{
		Detect: func() (selfupdate.Install, error) { return selfupdate.Install{Channel: selfupdate.ChannelArchive}, nil },
		Target: func(context.Context, string) (Target, error) { return target, nil }, Source: func() (Source, error) { return source, nil },
	}
	out, err := run(t.Context(), deps, command.Invocation{Version: "v1.0.0"}, true)
	if !errors.Is(err, context.DeadlineExceeded) || cli.MapError(err).Code != "timeout" || !out.Attempted || !out.Updated || out.Publication == nil || !out.Publication.Published || out.Publication.Durable || !target.closed {
		t.Fatalf("published timeout receipt = %+v, %v", out, err)
	}
	if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("candidate descriptor leaked: %v", err)
	}
}

func TestBrewCommandRetainsFailedPublication(t *testing.T) {
	t.Parallel()
	before := selfupdate.BrewInstallation{Executable: "/owned/old/jira", Version: "v1.0.0", SHA256: "old"}
	after := selfupdate.BrewInstallation{Executable: "/owned/new/jira", Version: "v1.1.0", SHA256: "new"}
	deps := Deps{
		Detect: func() (selfupdate.Install, error) { return selfupdate.Install{Channel: selfupdate.ChannelBrew}, nil },
		Brew: func(context.Context, string) (BrewUpdater, error) {
			return fakeBrew{out: selfupdate.BrewOutcome{Attempted: true, Updated: true, Before: before, After: &after, Shadow: "/owned/shadow/jira"}, err: context.Canceled}, nil
		}, Source: func() (Source, error) {
			return &fakeSource{release: selfupdate.Release{ID: 37, Version: "v1.1.0"}}, nil
		},
	}
	out, err := run(t.Context(), deps, command.Invocation{Version: "v1.0.0"}, true)
	if !errors.Is(err, context.Canceled) || !out.Attempted || !out.Updated || out.Uncertain || out.Brew == nil || out.Brew.After == nil || out.Brew.After.Version != after.Version || out.Brew.Shadow != "/owned/shadow/jira" {
		t.Fatalf("brew failed publication receipt = %+v, %v", out, err)
	}
}

func TestUpdateDiscoveryFailureStaysUnknownAndClosesTarget(t *testing.T) {
	t.Parallel()
	target := &fakeTarget{closeErr: io.ErrUnexpectedEOF}
	source := &fakeSource{latestErr: selfupdate.ErrReleaseUnavailable}
	deps := Deps{
		Detect: func() (selfupdate.Install, error) { return selfupdate.Install{Channel: selfupdate.ChannelArchive}, nil },
		Target: func(context.Context, string) (Target, error) { return target, nil }, Source: func() (Source, error) { return source, nil },
	}
	out, err := run(t.Context(), deps, command.Invocation{Version: "v1.0.0"}, true)
	if !errors.Is(err, selfupdate.ErrReleaseUnavailable) || !errors.Is(err, io.ErrUnexpectedEOF) || out.UpdateAvailable != nil || out.Updated || out.Attempted || !target.closed {
		t.Fatalf("failed discovery fabricated availability or lost cleanup: %+v, %v", out, err)
	}
}
