package envelope

// VersionOutput is the `version` operation's data payload: what this
// binary is, stated from what the build actually stamped. Version is the
// exact string the `-V` flag prints — the command and the flag read the
// same source (the root's resolved version), so the two surfaces cannot
// disagree, even in an unstamped build where that string is a
// placeholder rather than a semver.
//
// The VCS trio (commit, dirty, build_date) is absent — not null, not
// zero — when the build embedded no VCS metadata (-buildvcs=off, a
// tarball checkout): absence means exactly "not stamped", and inventing
// a value would claim provenance the binary does not have.
type VersionOutput struct {
	Version string `json:"version"`
	// Commit is the full VCS revision the binary was built from.
	Commit string `json:"commit,omitempty"`
	// Dirty is a pointer so the three states stay distinct: absent (no
	// VCS stamp), false (clean tree), true (uncommitted changes built
	// in). A plain bool would collapse "unknown" into "clean".
	Dirty *bool `json:"dirty,omitempty"`
	// BuildDate is the commit's timestamp as Go stamps it: RFC 3339,
	// UTC — already in the contract's timestamp format, emitted
	// verbatim.
	BuildDate string       `json:"build_date,omitempty"`
	GoVersion string       `json:"go_version"`
	Platform  PlatformInfo `json:"platform"`
}

// PlatformInfo is the build target as an object, so `.data.platform.os`
// is addressable without parsing a "linux/amd64" compound.
type PlatformInfo struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

// The overrides carry only prose and formats — everything structural
// derives from the struct, so the shape cannot drift from the bytes.
func init() {
	Register("version", VersionOutput{}, map[string]any{
		"description": "Version and build provenance of the jira binary.",
		"properties": map[string]any{
			"version": map[string]any{
				"description": "Resolved version string, identical to what `-V` prints.",
			},
			"commit": map[string]any{
				"description": "Full VCS revision; absent when the build embedded no VCS metadata.",
			},
			"dirty": map[string]any{
				"description": "True when the binary was built from a tree with uncommitted changes; absent when unstamped.",
			},
			"build_date": map[string]any{
				"description": "Commit timestamp, RFC 3339 UTC; absent when unstamped.",
				"format":      "date-time",
			},
			"go_version": map[string]any{
				"description": "Go toolchain that built the binary.",
			},
			"platform": map[string]any{
				"description": "Build target operating system and architecture.",
			},
		},
	})
}
