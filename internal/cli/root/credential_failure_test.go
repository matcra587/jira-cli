package root

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

type failedCredentialStore struct{ err error }

func (s failedCredentialStore) Get(ctx context.Context, _ string) (string, error) { return "", s.err }

func (failedCredentialStore) Set(ctx context.Context, _, _ string) error {
	panic("unexpected credential write")
}

func (failedCredentialStore) Delete(ctx context.Context, _ string) error {
	panic("unexpected credential deletion")
}

func TestSiteResolutionReportsCredentialStoreFailure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		token string
	}{
		{name: "no fallback"},
		{name: "whitespace is no fallback", token: "   "},
		{name: "explicit environment token", token: "synthetic-env-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.toml")
			if err := os.WriteFile(path, []byte("read_only=true\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cause := errors.New("fixture private backend detail")
			site, err := siteFrom(t.Context(), config.Options{Path: path, Environ: func() []string {
				return []string{"JIRA_BASE_URL=https://fixture.example.invalid", "JIRA_EMAIL=fixture@example.invalid", "JIRA_API_TOKEN=" + tc.token}
			}}, failedCredentialStore{err: cause})
			if strings.TrimSpace(tc.token) != "" {
				if err != nil || site.Token != tc.token || site.TokenSource != string(config.LayerEnv) {
					t.Fatalf("usable environment fallback was blocked: %v", err)
				}
				return
			}
			if !errors.Is(err, cause) {
				t.Fatalf("authentication failure lost its native cause: %v", err)
			}
			classified := cli.MapError(err)
			if classified.Code != "auth_store_unavailable" || classified.Exit != cli.ExitAuth {
				t.Fatalf("failed store has wrong machine classification: %+v", classified)
			}
			if strings.Contains(classified.Message, cause.Error()) || strings.Contains(classified.Hint, "login") {
				t.Fatal("public failure leaks backend detail or tells the user to replace an unread credential")
			}
		})
	}
}

func TestJiraClientResolutionReportsCredentialStoreFailure(t *testing.T) {
	for _, token := range []string{"", "   ", "synthetic-env-token"} {
		name := "no fallback"
		if token == "   " {
			name = "whitespace is no fallback"
		} else if token != "" {
			name = "explicit environment token"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.toml")
			if err := os.WriteFile(path, []byte("read_only=true\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cause := errors.New("fixture private backend detail")
			client, err := jiraClientFrom(t.Context(), config.Options{Path: path, Environ: func() []string {
				return []string{"JIRA_BASE_URL=https://fixture.example.invalid", "JIRA_EMAIL=fixture@example.invalid", "JIRA_API_TOKEN=" + token}
			}}, failedCredentialStore{err: cause})
			if strings.TrimSpace(token) != "" {
				if err != nil || client == nil {
					t.Fatalf("usable fallback could not construct the client: %v", err)
				}
				return
			}
			classified := cli.MapError(err)
			if client != nil || !errors.Is(err, cause) || classified.Code != "auth_store_unavailable" || classified.Exit != cli.ExitAuth {
				t.Fatalf("client failure lost the backend classification or native cause: %+v", classified)
			}
			if strings.Contains(classified.Message, cause.Error()) {
				t.Fatal("client failure exposes private backend detail")
			}
		})
	}
}

func TestConfigListReportsUnavailableCredentialStoreWithoutPrivateDetail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(path, []byte("read_only=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JIRA_BASE_URL", "https://fixture.example.invalid")
	t.Setenv("JIRA_EMAIL", "fixture@example.invalid")
	t.Setenv("JIRA_API_TOKEN", "")
	cli.TakeWarnings()
	t.Cleanup(func() { cli.TakeWarnings() })
	cause := errors.New("fixture private backend detail")
	deps := testDeps(t)
	deps.Credentials = func() (core.CredentialStore, error) {
		return failedCredentialStore{err: cause}, nil
	}
	args := []string{"config", "list", "--config", path, "--output=json"}
	applyGlobalFlags(args)
	t.Cleanup(func() { applyGlobalFlags(nil) })
	var stdout, stderr bytes.Buffer
	program, sink := newProgram(deps, &stdout, &stderr)
	exit := execute(t.Context(), program, sink, args)
	var frame struct {
		OK       bool                      `json:"ok"`
		Data     envelope.ConfigListOutput `json:"data"`
		Warnings []envelope.Warning        `json:"warnings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil {
		t.Fatal(err)
	}
	if exit != 0 || !frame.OK || len(frame.Warnings) != 1 || frame.Warnings[0].Type != "credential_store_unavailable" {
		t.Fatalf("local settings lost their usable result or backend diagnostic: exit=%d warnings=%+v", exit, frame.Warnings)
	}
	if strings.Contains(stdout.String()+stderr.String(), cause.Error()) {
		t.Fatal("public settings output exposes private backend detail")
	}
}
