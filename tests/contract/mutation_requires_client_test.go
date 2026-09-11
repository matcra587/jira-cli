package contract

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNonDryRunMutationsRequireConfiguredJiraClient(t *testing.T) {
	cfg := emptyBaseURLConfig(t)
	bin := buildJiraBinary(t)
	editPayload := writeIssueEditPayload(t)
	createPayload := writeIssueCreatePayload(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"issue create", []string{"issue", "create", "--no-input", "--json-input", createPayload, "--output=json"}},
		{"issue edit", []string{"issue", "edit", "PROJ-1", "--no-input", "--json-input", editPayload, "--output=json"}},
		{"issue comment", []string{"issue", "comment", "PROJ-1", "--markdown", "hello", "--no-input", "--output=json"}},
		{"worklog add", []string{"worklog", "add", "PROJ-1", "--time-spent", "45m", "--no-input", "--output=json"}},
		{"epic add", []string{"epic", "add", "PROJ-1", "EPIC-1", "--no-input", "--output=json"}},
		{"epic remove", []string{"epic", "remove", "PROJ-1", "--no-input", "--output=json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, append([]string{"--config", cfg}, tc.args...)...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err == nil {
				t.Fatalf("%s succeeded without configured Jira client:\nstdout=%s", tc.name, stdout.String())
			}
			// Machine mode: the failure envelope on stdout must carry the
			// "base URL" remediation; stderr stays free of a human clog line.
			var env map[string]any
			decodeErrorEnvelopeFromStdout(t, stdout.Bytes(), stderr.Bytes(), cmd.Args, &env)
			errs, _ := env["errors"].([]any)
			if len(errs) == 0 {
				t.Fatalf("%s envelope.errors is empty:\nstdout=%s", tc.name, stdout.String())
			}
			if !strings.Contains(stdout.String(), "base URL") {
				t.Fatalf("%s envelope missing base URL remediation:\nstdout=%s", tc.name, stdout.String())
			}
		})
	}
}

func writeIssueEditPayload(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "edit.json")
	if err := os.WriteFile(path, []byte(`{"fields":{"summary":"x"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func writeIssueCreatePayload(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "create.json")
	if err := os.WriteFile(path, []byte(`{"project_key":"PROJ","issue_type":"Task","summary":"hello"}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func emptyBaseURLConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `default_profile = "default"

[[profiles]]
name = "default"
auth_type = "token"
secret_backend = "keyring"
refresh_interval = 30
timeout = 30
workday_seconds = 28800
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
