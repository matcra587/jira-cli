package update

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/selfupdate"
)

func TestUpdateInstalledStatusRequiresConfirmedPublication(t *testing.T) {
	for _, tt := range []struct {
		name        string
		publication selfupdate.Publication
		failure     error
		installed   bool
	}{
		{name: "installed", publication: selfupdate.Publication{Published: true, Durable: true}, installed: true},
		{name: "unpublished"},
		{name: "published timeout", publication: selfupdate.Publication{Published: true, Uncertain: true}, failure: context.DeadlineExceeded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "candidate")
			if err != nil {
				t.Fatal(err)
			}
			var phases []core.ProgressPhase
			ctx := core.WithProgressObserver(t.Context(), func(progress core.Progress) { phases = append(phases, progress.Phase) })
			target := &fakeTarget{publication: tt.publication, err: tt.failure}
			source := &fakeSource{release: selfupdate.Release{Version: "v1.1.0"}, candidate: &selfupdate.Candidate{File: file, SHA256: "fixture"}}
			deps := Deps{
				Detect: func() (selfupdate.Install, error) { return selfupdate.Install{Channel: selfupdate.ChannelArchive}, nil },
				Target: func(context.Context, string) (Target, error) { return target, nil },
				Source: func() (Source, error) { return source, nil },
			}
			out, err := run(ctx, deps, command.Invocation{Version: "v1.0.0"}, true)
			if !errors.Is(err, tt.failure) || out.Updated != tt.publication.Published || !target.closed {
				t.Fatalf("progress changed publication receipt: %+v, %v", out, err)
			}
			if len(phases) < 2 || phases[0] != core.ProgressUpdateDiscover || phases[1] != core.ProgressUpdateInstall {
				t.Fatalf("missing observed update stages: %v", phases)
			}
			installed := phases[len(phases)-1] == core.ProgressUpdateInstalled
			if installed != tt.installed {
				t.Fatalf("unconfirmed installation reported: %v", phases)
			}
		})
	}
}
