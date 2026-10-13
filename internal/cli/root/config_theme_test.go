package root

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestConfigThemeNeverConstructsCredentialsClientsOrCache(t *testing.T) {
	for _, preview := range []bool{true, false} {
		path := filepath.Join(t.TempDir(), "settings.toml")
		var calls atomic.Int32
		deps := testDeps(t)
		deps.Credentials = func() (core.CredentialStore, error) {
			calls.Add(1)
			return nil, errors.New("unexpected credential construction")
		}
		deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
			calls.Add(1)
			return nil, errors.New("unexpected client construction")
		}
		deps.JiraClientFor = func(string, string, string, string) (core.JiraClient, error) {
			calls.Add(1)
			return nil, errors.New("unexpected explicit-account construction")
		}
		deps.Cache = func(ctx context.Context) (core.Cache, error) {
			calls.Add(1)
			return nil, errors.New("unexpected cache construction")
		}
		var stdout, stderr bytes.Buffer
		args := []string{"config", "theme", "--name=dracula", "--config", path, "--output=json", "--no-input"}
		if preview {
			args = append(args, "--dry-run")
		}
		applyGlobalFlags(args)
		t.Cleanup(func() { applyGlobalFlags(nil) })
		program, sink := newProgram(deps, &stdout, &stderr)
		exit := execute(t.Context(), program, sink, args)
		var result struct {
			OK   bool                       `json:"ok"`
			Data envelope.ConfigThemeOutput `json:"data"`
		}
		if exit != 0 || calls.Load() != 0 || json.Unmarshal(stdout.Bytes(), &result) != nil || !result.OK || result.Data.Name != "dracula" || result.Data.ThemePath != "" || result.Data.Path != path || result.Data.DryRun != preview || !result.Data.WouldChange || result.Data.Changed == preview {
			t.Fatalf("theme workflow crossed an adapter boundary: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout.String(), stderr.String())
		}
	}
}
