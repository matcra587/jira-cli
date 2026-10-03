package version

import (
	"context"
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// New returns the `jira version` command.
func New() *cobra.Command {
	return command.MustNew(command.Spec[envelope.VersionOutput]{
		Op:    "version",
		Use:   "version",
		Short: "Print version and build information",
		Long: "Print the version of jira, with the build's provenance where the " +
			"binary carries it: commit, dirty state, build date, Go toolchain, " +
			"and target platform.\n\n" +
			"The human output is the bare version string — the same line `-V` " +
			"prints. Machine modes (`--output=json`, `--output=compact`) carry " +
			"the full detail.",
		Example: "# print the version\n" +
			"$ jira version\n\n" +
			"# full build provenance as json\n" +
			"$ jira version --output=json\n\n" +
			"# bare data for scripts and agents\n" +
			"$ jira version --output=compact\n\n" +
			"# pull one field out; strings print raw, so this pipes\n" +
			"$ jira version --jq '.data.go_version'\n\n" +
			"# reshape into an object of your own\n" +
			"$ jira version --jq '{go: .data.go_version, os: .data.platform.os}'\n\n" +
			"# compare against a value without pasting it into the expression\n" +
			"$ jira version --jq 'select(.data.platform.os == $os)' --jq-arg os=linux",
		Human: renderHuman,
		// The resolved version arrives on the invocation, read from the
		// same field cobra's -V template prints — one source, so the
		// flag and the command cannot disagree.
		Run: func(_ context.Context, inv command.Invocation) (envelope.VersionOutput, error) {
			return buildOutput(inv.Version), nil
		},
	})
}

// buildOutput assembles the payload from the two places a build writes
// its identity: the resolved version string (clive's ldflag, surfaced on
// the root by conductor) and the VCS metadata Go embeds. Fields the
// build did not stamp stay absent — see the VersionOutput contract.
func buildOutput(version string) envelope.VersionOutput {
	out := envelope.VersionOutput{
		Version:   version,
		GoVersion: runtime.Version(),
		Platform:  envelope.PlatformInfo{OS: runtime.GOOS, Arch: runtime.GOARCH},
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		applyBuildSettings(&out, info.Settings)
	}
	return out
}

// applyBuildSettings folds Go's VCS stamps into the output. Split from
// buildOutput so the populated path is testable: a binary built without
// VCS metadata (every test build, and any `go build -buildvcs=false`)
// would otherwise leave commit, build_date and dirty declared but never
// once emitted.
func applyBuildSettings(out *envelope.VersionOutput, settings []debug.BuildSetting) {
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			out.Commit = s.Value
		case "vcs.time":
			// Go stamps RFC 3339 UTC — already the contract's timestamp
			// format, so it passes through verbatim.
			out.BuildDate = s.Value
		case "vcs.modified":
			dirty := s.Value == "true"
			out.Dirty = &dirty
		}
	}
}

// renderHuman writes the one-line human surface: exactly the version
// string, exactly as `-V` prints it. Anything richer belongs to the
// machine modes — a person asking "which version" wants one copyable
// token.
func renderHuman(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.VersionOutput)
	if !ok {
		return fmt.Errorf("version: human renderer received %T, want envelope.VersionOutput", env.Data)
	}
	v.Line(out.Version)
	return nil
}
