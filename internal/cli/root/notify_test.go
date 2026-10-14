package root

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gechr/clive/notify"
	concobra "github.com/gechr/conductor/cli/cobra"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	jqlcmd "github.com/matcra587/jira-cli/internal/cli/jql"
	updatecmd "github.com/matcra587/jira-cli/internal/cli/update"
	"github.com/matcra587/jira-cli/internal/selfupdate"
)

type notificationTool struct {
	calls  atomic.Int32
	lookup func(context.Context) (string, error)
}

func (*notificationTool) BinaryName() string              { return "jira" }
func (*notificationTool) DisplayName() string             { return "jira-cli" }
func (*notificationTool) VersionLink(value string) string { return value }
func (tool *notificationTool) LatestRef(ctx context.Context, _ *http.Client) (string, error) {
	tool.calls.Add(1)
	if tool.lookup != nil {
		return tool.lookup(ctx)
	}
	return "v1.2.3", nil
}

func notificationProgram(t *testing.T, tool *notificationTool) (*concobra.Program, *failureSink, string) {
	t.Helper()
	t.Setenv("AGENT", "0")
	t.Setenv("JIRA_NO_UPDATE_CHECK", "")
	dir := t.TempDir()
	deps := testDeps(t)
	deps.BuildPreferences = func() (jqlcmd.BuildPreferences, error) {
		return jqlcmd.BuildPreferences{BaseURL: "https://example.invalid"}, nil
	}
	deps.NotifyTool = tool
	deps.NotifyOptions = []notify.Option{notify.WithCacheDir(dir), notify.WithCurrentVersion("v1.0.0")}
	deps.Update = updatecmd.Deps{Detect: func() (selfupdate.Install, error) {
		return selfupdate.Install{Channel: selfupdate.ChannelMise, Hint: "mise up github:matcra587/jira-cli"}, nil
	}}
	program, sink := newProgram(deps, io.Discard, io.Discard)
	probe := &cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error { return nil }}
	probe.Flags().Bool(cli.FlagDryRun, false, "Preview only")
	program.Root.AddCommand(probe)
	t.Cleanup(func() {
		if !notify.AwaitRefresh(3 * time.Second) {
			t.Error("background release refresh did not finish")
		}
	})
	return program, sink, dir
}

func TestPassiveUpdateSkipsDiscoveryAndPreview(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "root", args: nil},
		{name: "open", args: []string{"open", "KEY-1", "--output=json"}},
		{name: "help", args: []string{"help"}},
		{name: "leaf help", args: []string{"probe", "--help"}},
		{name: "version", args: []string{"version"}},
		{name: "release notes", args: []string{"release-notes", "--latest"}},
		{name: "release notes alias", args: []string{"rn", "--latest"}},
		{name: "guide", args: []string{"guide", "--output=json"}},
		{name: "agent", args: []string{"agent", "schema", "--output=json"}},
		{name: "update", args: []string{"update", "--force"}},
		{name: "update alias", args: []string{"up", "--force"}},
		{name: "preview", args: []string{"probe", "--dry-run"}},
		{name: "headless", args: []string{"probe", "--no-input"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool := &notificationTool{}
			program, sink, dir := notificationProgram(t, tool)
			if exit := execute(t.Context(), program, sink, tc.args); exit != 0 {
				t.Fatalf("local invocation exited %d", exit)
			}
			if !notify.AwaitRefresh(time.Second) {
				t.Fatal("unexpected outstanding refresh")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 || tool.calls.Load() != 0 {
				t.Fatalf("skipped check performed I/O: calls=%d entries=%v err=%v", tool.calls.Load(), entries, err)
			}
		})
	}
}

func TestPassiveUpdateSkipsIssueInputPreparation(t *testing.T) {
	tool := &notificationTool{}
	program, sink, cacheDir := notificationProgram(t, tool)
	fixture := t.TempDir()
	input := filepath.Join(fixture, "input.json")
	metadata := filepath.Join(fixture, "metadata.json")
	if err := os.WriteFile(input, []byte(`{"fields":{"project":"KEY","issuetype":"Task"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metadata, []byte(`{"fields":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if exit := execute(t.Context(), program, sink, []string{"issue", "input", "prepare", "--create", "--input", input, "--metadata", metadata, "--output=human"}); exit != 0 {
		t.Fatalf("local authoring failed: exit=%d", exit)
	}
	if !notify.AwaitRefresh(time.Second) {
		t.Fatal("local authoring left a release refresh running")
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil || len(entries) != 0 || tool.calls.Load() != 0 {
		t.Fatalf("local authoring performed release I/O: calls=%d entries=%v error=%v", tool.calls.Load(), entries, err)
	}
}

func TestPassiveUpdateKillSwitchAndAgentSkipRefresh(t *testing.T) {
	for _, variable := range []string{"JIRA_NO_UPDATE_CHECK", "AGENT"} {
		t.Run(variable, func(t *testing.T) {
			tool := &notificationTool{}
			program, sink, dir := notificationProgram(t, tool)
			t.Setenv(variable, "1")
			if exit := execute(t.Context(), program, sink, []string{"probe", "--output=human"}); exit != 0 {
				t.Fatalf("invocation exited %d", exit)
			}
			if !notify.AwaitRefresh(time.Second) {
				t.Fatal("unexpected outstanding refresh")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 || tool.calls.Load() != 0 {
				t.Fatalf("disabled check performed I/O: calls=%d entries=%v err=%v", tool.calls.Load(), entries, err)
			}
		})
	}
}

func TestPassiveUpdateUsesResolvedFormatAndStableComparison(t *testing.T) {
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			tool := &notificationTool{}
			program, sink, dir := notificationProgram(t, tool)
			if exit := execute(t.Context(), program, sink, []string{"probe", "--output=" + mode}); exit != 0 {
				t.Fatalf("invocation exited %d", exit)
			}
			if !notify.AwaitRefresh(time.Second) || tool.calls.Load() != 1 {
				t.Fatalf("ordinary invocation did not refresh once: calls=%d", tool.calls.Load())
			}
			result, pending := notify.Pending(tool, program.Runtime.App.NotifyOptions...)
			if result.LatestRef != "v1.2.3" || result.Track != "stable" || pending != (mode == "human") || tool.calls.Load() != 1 {
				t.Fatalf("resolved notification verdict: %+v pending=%v calls=%d", result, pending, tool.calls.Load())
			}
			if _, err := os.Stat(filepath.Join(dir, "last-update", "notify-stable")); !os.IsNotExist(err) {
				t.Fatalf("non-TTY invocation stamped a printed hint: %v", err)
			}
			for _, current := range []string{"v1.2.3", "v1.2.3-gabc-dev", "not-a-version"} {
				opts := append([]notify.Option(nil), program.Runtime.App.NotifyOptions...)
				opts = append(opts, notify.WithCurrentVersion(current))
				if _, pending := notify.Pending(tool, opts...); pending {
					t.Fatalf("fabricated update for running version %q", current)
				}
			}
		})
	}
}

func TestPassiveUpdateRefreshIsBoundedAndNonblocking(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan error, 1)
	tool := &notificationTool{lookup: func(ctx context.Context) (string, error) {
		close(started)
		<-ctx.Done()
		finished <- ctx.Err()
		return "", ctx.Err()
	}}
	program, sink, _ := notificationProgram(t, tool)
	start := time.Now()
	if exit := execute(t.Context(), program, sink, []string{"probe", "--output=json"}); exit != 0 {
		t.Fatalf("invocation exited %d", exit)
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("startup waited for release lookup: %s", elapsed)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("ordinary invocation did not schedule refresh")
	}
	if !notify.AwaitRefresh(3 * time.Second) {
		t.Fatal("background release lookup escaped Clive's bound")
	}
	if err := <-finished; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("release lookup cancellation = %v", err)
	}
}
