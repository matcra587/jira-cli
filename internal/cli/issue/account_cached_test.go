package issue

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func TestRegisteredAccountConsumersUseCachedIdentityOnlyInLocalPreview(t *testing.T) {
	// Warning collection is invocation-scoped process state; keep these
	// registered-command executions sequential, as the production CLI is.
	for _, consumer := range []struct {
		name  string
		build func(Deps) *cobra.Command
		args  []string
	}{
		{name: "create", build: newCreate, args: []string{"--project", "KEY", "--type", "Task", "--summary", "x", "--assignee", "@me", "--reporter", "@me"}},
		{name: "edit", build: newEdit, args: []string{"KEY-1", "--assignee", "@me", "--reporter", "@me"}},
		{name: "clone", build: newClone, args: []string{"KEY-1", "--assignee", "@me", "--reporter", "@me"}},
		{name: "watchers add", build: newWatchersAdd, args: []string{"KEY-1", "--user", "@me"}},
		{name: "watchers remove", build: newWatchersRemove, args: []string{"KEY-1", "--user", "@me"}},
	} {
		t.Run(consumer.name, func(t *testing.T) {
			cli.TakeWarnings()
			defer cli.TakeWarnings()
			reads := 0
			cmd := consumer.build(Deps{Preferences: func() (core.IssuePreferences, error) {
				reads++
				return core.IssuePreferences{CachedAccountID: "cached-account"}, nil
			}, JiraClient: func(context.Context) (core.JiraClient, error) {
				t.Fatal("local cached preview constructed an authenticated client")
				return nil, nil
			}})
			cmd.SetArgs(append(consumer.args, "--dry-run"))
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stdout)
			if err := cmd.ExecuteContext(cli.WithFormat(t.Context(), output.FormatJSON)); err != nil {
				t.Fatal("registered local preview failed", err)
			}
			var frame struct {
				OK       bool               `json:"ok"`
				Warnings []envelope.Warning `json:"warnings"`
				Data     json.RawMessage    `json:"data"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil || !frame.OK {
				t.Fatal("preview did not produce a successful envelope", err)
			}
			cached := 0
			for _, warning := range frame.Warnings {
				if warning.Type == "account_id_cached" {
					cached++
				}
				if warning.Type == "account_id_unresolved" {
					t.Fatal("matching cache still published an unresolved sentinel")
				}
			}
			if reads < 1 || cached != 1 {
				t.Fatal("registered consumer omitted local cache or unverified provenance")
			}
			if !bytes.Contains(frame.Data, []byte("cached-account")) || bytes.Contains(frame.Data, []byte("@me")) {
				t.Fatal("local composer did not use resolved cached identity")
			}
			if consumer.name == "clone" {
				var out envelope.IssueCloneOutput
				if json.Unmarshal(frame.Data, &out) != nil || !out.DryRun || len(out.AccountOverrides) != 2 || out.AccountOverrides["assignee"] != "cached-account" || out.AccountOverrides["reporter"] != "cached-account" {
					t.Fatal("clone preview did not retain both prepared account overrides")
				}
			}
		})
	}
}
