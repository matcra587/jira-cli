// Package selfupdate owns installation discovery and executable replacement.
// It never constructs a Jira client or reads Jira credentials.
package selfupdate

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// Module identifies the release repository and the go-install package.
const Module = "github.com/matcra587/jira-cli"

// buildSource is stamped by the release builder. A version alone is not
// installation provenance: Go also embeds versions in locally built binaries.
var buildSource string

// Channel names the owner of an installation.
type Channel string

const (
	ChannelBrew      Channel = "brew"
	ChannelScoop     Channel = "scoop"
	ChannelMise      Channel = "mise"
	ChannelGoInstall Channel = "go-install"
	ChannelArchive   Channel = "github-archive"
	ChannelUnknown   Channel = "unknown"
)

// Install contains local facts, without opening an updater or checking releases.
type Install struct {
	Channel    Channel
	Executable string
	Hint       string
}

// Managed reports whether the installing tool must perform the update itself.
func (i Install) Managed() bool {
	return i.Channel == ChannelScoop || i.Channel == ChannelMise || i.Channel == ChannelGoInstall
}

// DetectInstall resolves the running executable before classifying its owner.
// A broken symlink or unavailable executable is an error, not an archive guess.
func DetectInstall() (Install, error) {
	executable, err := os.Executable()
	if err != nil {
		return Install{}, fmt.Errorf("locate running executable: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return Install{}, fmt.Errorf("resolve running executable: %w", err)
	}
	info, _ := debug.ReadBuildInfo()
	return classifyInstall(executable, info, buildSource), nil
}

func classifyInstall(executable string, info *debug.BuildInfo, source string) Install {
	// Accept both separators when interpreting Windows build/test facts on Unix.
	path := strings.ToLower(strings.ReplaceAll(executable, "\\", "/"))
	var channel Channel
	switch {
	case strings.Contains(path, "/scoop/apps/"), strings.Contains(path, "/scoop/shims/"):
		channel = ChannelScoop
	case strings.Contains(path, "/cellar/"), strings.Contains(path, "/homebrew/"), strings.Contains(path, "/linuxbrew/"):
		channel = ChannelBrew
	case strings.Contains(path, "/mise/installs/") && !strings.Contains(path, "/mise/installs/go/"):
		channel = ChannelMise
	case source == "goreleaser":
		channel = ChannelArchive
	case goInstalled(info):
		channel = ChannelGoInstall
	default:
		channel = ChannelUnknown
	}
	return Install{Channel: channel, Executable: executable, Hint: channelHint(channel)}
}

func goInstalled(info *debug.BuildInfo) bool {
	if info == nil || info.Main.Path != Module || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return false
	}
	// A checkout build may have a real module version on Go 1.24 and later.
	// go install package@version omits these checkout VCS stamps.
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && setting.Value != "" {
			return false
		}
	}
	return info.Main.Replace == nil
}

func channelHint(channel Channel) string {
	switch channel {
	case ChannelScoop:
		return "scoop update jira"
	case ChannelMise:
		return "mise up github:matcra587/jira-cli"
	case ChannelGoInstall:
		return "go install " + Module + "/cmd/jira@latest"
	case ChannelBrew:
		return "brew upgrade matcra587/tap/jira"
	case ChannelUnknown:
		return "Install a published release, or rebuild using the original installation method."
	default:
		return ""
	}
}
