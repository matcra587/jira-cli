package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAliasThemeWarningsFollowSelectedConfig(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, origin, target, warning string
		env                           []string
	}{
		{name: "ignore origin preset", origin: "theme='bad-origin-theme'\n", target: "theme='dracula'\n"},
		{name: "selected preset", origin: "theme='nord'\n", target: "theme='bad-target-theme'\n", warning: "theme_unknown"},
		{name: "ignore origin palette", origin: "theme_path='missing-origin.toml'\n", target: "theme='dracula'\n"},
		{name: "selected palette", origin: "theme='nord'\n", target: "theme_path='missing-target.toml'\n", warning: "theme_invalid"},
		{name: "environment wins", origin: "theme='bad-origin-theme'\n", target: "theme_path='missing-target.toml'\n", env: []string{"JIRA_THEME=monokai"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			origin, target := filepath.Join(dir, "origin.toml"), filepath.Join(dir, "target.toml")
			if err := os.WriteFile(target, []byte(tc.target), 0o600); err != nil {
				t.Fatal(err)
			}
			contents := tc.origin + fmt.Sprintf("[aliases]\ninspect=%q\n", "--config="+target+" version")
			if err := os.WriteFile(origin, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			env := append([]string{"JIRA_KEYRING=false"}, tc.env...)
			stdout, stderr, exit := runBinaryEnv(t, env, "--config", origin, "inspect", "--output=json")
			var result struct {
				OK       bool `json:"ok"`
				Warnings []struct {
					Type string `json:"type"`
				} `json:"warnings"`
			}
			if exit != 0 || stderr != "" || json.Unmarshal([]byte(stdout), &result) != nil || !result.OK {
				t.Fatalf("selected theme broke local version: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			if tc.warning == "" {
				if len(result.Warnings) != 0 {
					t.Fatalf("unselected settings emitted warnings: %q", stdout)
				}
			} else if len(result.Warnings) != 1 || result.Warnings[0].Type != tc.warning || strings.Contains(stdout, "origin") {
				t.Fatalf("selected theme did not emit exactly its warning: %q", stdout)
			}
		})
	}
}

func TestStartupThemeWarningsSurviveEarlyRefusal(t *testing.T) {
	t.Parallel()
	path := aliasConfig(t, "theme='bad-origin-theme'\n[aliases]\ncycle='cycle'\n")
	for _, tc := range []struct {
		name, code string
		args       []string
	}{
		{name: "alias error", code: "alias_invalid", args: []string{"cycle"}},
		{name: "completion error", code: "flag_not_applicable", args: []string{"--print-completion"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--config", path, "--output=json"}, tc.args...)
			stdout, _, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, args...)
			var result struct {
				OK     bool `json:"ok"`
				Errors []struct {
					Code string `json:"code"`
				} `json:"errors"`
				Warnings []struct {
					Type string `json:"type"`
				} `json:"warnings"`
			}
			if exit != 3 || json.Unmarshal([]byte(stdout), &result) != nil || result.OK || len(result.Errors) != 1 || result.Errors[0].Code != tc.code || len(result.Warnings) != 1 || result.Warnings[0].Type != "theme_unknown" {
				t.Fatalf("early refusal lost shared error or theme warning: exit=%d stdout=%q", exit, stdout)
			}
		})
	}
}
