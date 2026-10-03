package version

import (
	"runtime/debug"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

// TestApplyBuildSettings proves the three provenance fields the contract
// declares actually populate. Every test binary is built with
// -buildvcs=false (hermetic corpus builds), so without this the fields
// would be declared and schema-published but never emitted by anything —
// exactly what the M8 axis forbids.
func TestApplyBuildSettings(t *testing.T) {
	t.Parallel()
	yes, no := true, false
	tests := []struct {
		name      string
		settings  []debug.BuildSetting
		commit    string
		buildDate string
		dirty     *bool
	}{
		{name: "unstamped build leaves all three absent"},
		{
			name: "clean stamped build",
			settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abc123"},
				{Key: "vcs.time", Value: "2026-08-02T18:00:00Z"},
				{Key: "vcs.modified", Value: "false"},
			},
			commit: "abc123", buildDate: "2026-08-02T18:00:00Z", dirty: &no,
		},
		{
			name: "dirty stamped build",
			settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "def456"},
				{Key: "vcs.modified", Value: "true"},
			},
			commit: "def456", dirty: &yes,
		},
		{
			name:     "unrelated settings are ignored",
			settings: []debug.BuildSetting{{Key: "-tags", Value: "outputaudit"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out envelope.VersionOutput
			applyBuildSettings(&out, tt.settings)

			if out.Commit != tt.commit {
				t.Errorf("commit = %q, want %q", out.Commit, tt.commit)
			}
			if out.BuildDate != tt.buildDate {
				t.Errorf("build_date = %q, want %q", out.BuildDate, tt.buildDate)
			}
			switch {
			case tt.dirty == nil && out.Dirty != nil:
				t.Errorf("dirty = %v, want absent", *out.Dirty)
			case tt.dirty != nil && out.Dirty == nil:
				t.Errorf("dirty absent, want %v", *tt.dirty)
			case tt.dirty != nil && *out.Dirty != *tt.dirty:
				t.Errorf("dirty = %v, want %v", *out.Dirty, *tt.dirty)
			}
		})
	}
}
