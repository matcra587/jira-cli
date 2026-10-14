//go:build outputaudit

package nativeaudit

import (
	"runtime"
	"runtime/debug"
	"testing"
)

func TestImageBuildAdmission(t *testing.T) {
	for _, test := range []struct {
		name     string
		version  string
		settings []debug.BuildSetting
		admit    bool
	}{
		{name: "selected-pure-go", version: runtime.Version(), settings: []debug.BuildSetting{{Key: "CGO_ENABLED", Value: "0"}}, admit: true},
		{name: "missing-setting", version: runtime.Version()},
		{name: "cgo-enabled", version: runtime.Version(), settings: []debug.BuildSetting{{Key: "CGO_ENABLED", Value: "1"}}},
		{name: "invalid-setting", version: runtime.Version(), settings: []debug.BuildSetting{{Key: "CGO_ENABLED", Value: "false"}}},
		{name: "different-toolchain", version: "go0.0", settings: []debug.BuildSetting{{Key: "CGO_ENABLED", Value: "0"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateImageBuild(&debug.BuildInfo{GoVersion: test.version, Settings: test.settings})
			if (err == nil) != test.admit {
				t.Fatalf("image build admission differs: admitted=%v err=%v", test.admit, err)
			}
		})
	}
}
