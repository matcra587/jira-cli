package root

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	jqlcmd "github.com/matcra587/jira-cli/internal/cli/jql"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/output"
)

type credentialContextKey struct{}

func TestCredentialFactoriesReceiveOperationContext(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		cache bool
	}{
		{name: "jira client", args: []string{"user", "search", "fixture"}},
		{name: "cache primer scope", args: []string{"cache", "labels"}, cache: true},
		{name: "issue metadata root bridge", args: []string{"issue", "edit", "DEMO-1", "--generate-json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marker := &struct{ name string }{name: tc.name}
			caller, cancel := context.WithDeadline(context.WithValue(t.Context(), credentialContextKey{}, marker), time.Now().Add(time.Minute))
			defer cancel()
			deadline, _ := caller.Deadline()
			calls := 0
			inspect := func(ctx context.Context) error {
				calls++
				if ctx.Value(credentialContextKey{}) != marker {
					t.Error("factory lost caller context value")
				}
				if observed, ok := ctx.Deadline(); !ok || !observed.Equal(deadline) {
					t.Errorf("factory deadline = %v, %t; want caller deadline %v", observed, ok, deadline)
				}
				if format, ok := cli.FormatFromContext(ctx); !ok || format != output.FormatJSON {
					t.Errorf("factory did not receive decorated operation context: format=%v present=%t", format, ok)
				}
				if err := ctx.Err(); err != nil {
					t.Errorf("factory received an already canceled context: %v", err)
				}
				cancel()
				if ctx.Err() != context.Canceled {
					t.Errorf("factory did not observe caller cancellation: %v", ctx.Err())
				}
				return context.Canceled
			}
			deps := testDeps(t)
			deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
				if tc.cache {
					t.Error("cache scope failure reached Jira client")
					return nil, context.Canceled
				}
				return nil, inspect(ctx)
			}
			deps.Cache = func(ctx context.Context) (core.Cache, error) {
				if !tc.cache {
					t.Error("failed client construction reached cache")
					return nil, context.Canceled
				}
				return nil, inspect(ctx)
			}
			deps.BuildPreferences = func() (jqlcmd.BuildPreferences, error) {
				return jqlcmd.BuildPreferences{BaseURL: "https://example.invalid", Account: "public@example.invalid"}, nil
			}
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(deps, &stdout, &stderr)
			// --timeout configures the HTTP transport, not a new command deadline.
			// The factory must retain the caller's deadline despite this override.
			args := append(append([]string{}, tc.args...), "--output=json", "--timeout=1s")
			if exit := execute(caller, program, sink, args); exit != cli.ExitCanceled || calls != 1 {
				t.Fatalf("factory path did not run exactly once with cancellation: exit=%d calls=%d stdout=%s stderr=%s", exit, calls, stdout.String(), stderr.String())
			}
		})
	}
}

func TestCredentialFactoriesDoNotMaskContextErrorsWithEnvironmentToken(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			opts := config.Options{Path: filepath.Join(t.TempDir(), "missing.toml"), Environ: func() []string {
				return []string{"JIRA_BASE_URL=https://example.invalid", "JIRA_EMAIL=public@example.invalid", "JIRA_API_TOKEN=synthetic-environment-token"}
			}}
			store := failedCredentialStore{err: fmt.Errorf("fixture backend: %w", cause)}
			site, err := siteFrom(t.Context(), opts, store)
			if !errors.Is(err, cause) || site.Token != "" {
				t.Fatalf("site ignored credential context failure: error=%v token_present=%t", err, site.Token != "")
			}
			client, err := jiraClientFrom(t.Context(), opts, store)
			if !errors.Is(err, cause) || client != nil {
				t.Fatalf("client ignored credential context failure: error=%v client_present=%t", err, client != nil)
			}
		})
	}
}

func TestConfigListDoesNotPublishSuccessAfterCredentialCancellation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("JIRA_API_TOKEN", "synthetic-environment-token")
	deps := testDeps(t)
	deps.Credentials = func() (core.CredentialStore, error) { return failedCredentialStore{err: context.Canceled}, nil }
	var stdout, stderr bytes.Buffer
	program, sink := newProgram(deps, &stdout, &stderr)
	if exit := execute(t.Context(), program, sink, []string{"config", "list", "--output=json"}); exit != cli.ExitCanceled {
		t.Fatalf("config list ignored cancellation: exit=%d", exit)
	}
	if bytes.Contains(stdout.Bytes(), []byte("synthetic-environment-token")) || bytes.Contains(stdout.Bytes(), []byte(`"ok":true`)) {
		t.Fatal("canceled configuration read published success or a secret")
	}
}
