package config_test

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
)

func TestWorkdayConfigurationLayers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, file, environment string
		seconds                 int
		layer                   config.Layer
	}{
		{"default", "", "", 28800, config.LayerDefault},
		{"file", "workday_seconds = 21600\n", "", 21600, config.LayerFile},
		{"environment", "workday_seconds = 21600\n", "JIRA_WORKDAY_SECONDS=25200", 25200, config.LayerEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "absent.toml")
			if tc.file != "" {
				path = writeConfig(t, tc.file)
			}
			got, err := config.Load(config.Options{Path: path, Environ: env(tc.environment)})
			if err != nil || got.Config.WorkdaySeconds != tc.seconds || got.Source("workday_seconds") != tc.layer || got.Config.Values()["workday_seconds"] != strconv.Itoa(tc.seconds) {
				t.Fatalf("workday value or provenance lost: %+v %v", got, err)
			}
		})
	}
}

func TestWorkdayConfigurationRejectsInvalidValues(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"0", "-1", "1.5", "true", `"28800"`} {
		t.Run("file "+value, func(t *testing.T) {
			t.Parallel()
			path := writeConfig(t, "workday_seconds = "+value+"\n")
			if _, err := config.Load(config.Options{Path: path, Environ: env()}); err == nil || !strings.Contains(err.Error(), "workday_seconds") {
				t.Fatalf("invalid workday file accepted: %s %v", value, err)
			}
		})
	}
	for _, value := range []string{"0", "-1", "1.5", "true", "9223372036854775808"} {
		t.Run("environment "+value, func(t *testing.T) {
			t.Parallel()
			if _, err := config.Load(config.Options{Path: filepath.Join(t.TempDir(), "none.toml"), Environ: env("JIRA_WORKDAY_SECONDS=" + value)}); err == nil || !strings.Contains(err.Error(), "workday_seconds") {
				t.Fatalf("invalid workday environment accepted: %s %v", value, err)
			}
		})
	}
}
