package root

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestConfigGetNeverConstructsCredentialsClientsOrCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(path, []byte("read_only=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
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
	args := []string{"config", "get", "read_only", "--config", path, "--output=json"}
	// Match Run's pre-construction selection. execute is the lower boundary
	// and does not seed the process configuration flags on its own.
	applyGlobalFlags(args)
	t.Cleanup(func() { applyGlobalFlags(nil) })
	program, sink := newProgram(deps, &stdout, &stderr)
	exit := execute(t.Context(), program, sink, args)
	var result struct {
		OK   bool                     `json:"ok"`
		Data envelope.ConfigGetOutput `json:"data"`
	}
	if exit != 0 || calls.Load() != 0 || json.Unmarshal(stdout.Bytes(), &result) != nil || !result.OK || result.Data.Value != "false" || !result.Data.Present || result.Data.Path != path {
		t.Fatalf("stored get crossed an adapter boundary: exit=%d calls=%d out=%q stderr=%q", exit, calls.Load(), stdout.String(), stderr.String())
	}
}
