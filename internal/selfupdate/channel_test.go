package selfupdate

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"
)

func TestDetectReleaseStampedInstallation(t *testing.T) {
	if buildSource != "goreleaser" {
		t.Skip("requires release build stamp; execute separately with the release ldflag")
	}
	got, err := DetectInstall()
	if err != nil || got.Channel != ChannelArchive || got.Managed() || got.Hint != "" {
		t.Fatalf("release-stamped installation = %+v, error %v", got, err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil || got.Executable != resolved {
		t.Fatalf("executable = %q, want %q, error %v", got.Executable, resolved, err)
	}
}

func TestInstallationChannel(t *testing.T) {
	module := func(version string, settings ...debug.BuildSetting) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Path: Module, Version: version}, Settings: settings}
	}
	cases := []struct {
		name, path, source string
		info               *debug.BuildInfo
		want               Channel
		managed            bool
	}{
		{name: "scoop_app", path: `C:\Users\person\scoop\apps\jira\current\jira.exe`, want: ChannelScoop, managed: true},
		{name: "scoop_shim", path: `C:\SCOOP\shims\jira.exe`, source: "goreleaser", want: ChannelScoop, managed: true},
		{name: "brew_keg", path: "/opt/homebrew/Cellar/jira/1.2.3/bin/jira", source: "goreleaser", want: ChannelBrew},
		{name: "brew_prefix", path: "/home/linuxbrew/.linuxbrew/bin/jira", want: ChannelBrew},
		{name: "mise_asset", path: "/home/person/.local/share/mise/installs/github-matcra587-jira-cli/1.2.3/jira", source: "goreleaser", want: ChannelMise, managed: true},
		{name: "mise_go_toolchain_is_not_jira_owner", path: "/home/person/.local/share/mise/installs/go/1.27/bin/jira", info: module("v1.2.3"), want: ChannelGoInstall, managed: true},
		{name: "go_install", path: "/home/person/go/bin/jira", info: module("v1.2.3"), want: ChannelGoInstall, managed: true},
		{name: "checkout_version_with_vcs", path: "/tmp/jira", info: module("v1.2.3", debug.BuildSetting{Key: "vcs.revision", Value: "revision"}), want: ChannelUnknown},
		{name: "devel", path: "/tmp/jira", info: module("(devel)"), want: ChannelUnknown},
		{name: "missing_build_info", path: "/tmp/jira", want: ChannelUnknown},
		{name: "release_stamp", path: "/usr/local/bin/renamed-jira", source: "goreleaser", info: module("v1.2.3"), want: ChannelArchive},
		{name: "wrong_module", path: "/tmp/jira", info: &debug.BuildInfo{Main: debug.Module{Path: "example.org/other", Version: "v1.2.3"}}, want: ChannelUnknown},
		{name: "path_lookalike", path: "/home/person/notmise/installs/jira", want: ChannelUnknown},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := classifyInstall(test.path, test.info, test.source)
			if got.Channel != test.want || got.Executable != test.path || got.Managed() != test.managed {
				t.Fatalf("installation = %+v, managed %t; want %s, managed %t", got, got.Managed(), test.want, test.managed)
			}
			if got.Channel != ChannelArchive && got.Hint == "" {
				t.Fatal("installation owner has no actionable update hint")
			}
		})
	}
}

func TestReplacedModuleIsNotGoInstall(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Path: Module, Version: "v1.2.3", Replace: &debug.Module{Path: "../checkout"}}}
	if got := classifyInstall("/tmp/jira", info, ""); got.Channel != ChannelUnknown {
		t.Fatalf("replaced local module classified as %s", got.Channel)
	}
}

func TestManagedInstallationHints(t *testing.T) {
	for channel, want := range map[Channel]string{
		ChannelScoop:     "scoop update jira",
		ChannelMise:      "mise up github:matcra587/jira-cli",
		ChannelGoInstall: "go install github.com/matcra587/jira-cli/cmd/jira@latest",
		ChannelBrew:      "brew upgrade matcra587/tap/jira",
	} {
		t.Run(string(channel), func(t *testing.T) {
			if got := channelHint(channel); got != want {
				t.Fatalf("hint = %q, want %q", got, want)
			}
		})
	}
}
