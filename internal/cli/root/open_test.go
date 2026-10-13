package root

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/adapters"
	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
	jqlcmd "github.com/matcra587/jira-cli/internal/cli/jql"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestOpenAndIssueWebUseSettingsWithoutAdapters(t *testing.T) {
	for _, route := range [][]string{{"open", "KEY-007"}, {"issue", "view", "KEY-7", "--web"}, {"issue", "comment", "list", "KEY-7", "--web"}} {
		t.Run(route[0]+route[1], func(t *testing.T) {
			deps := testDeps(t)
			siteCalls := 0
			deps.BuildPreferences = func() (jqlcmd.BuildPreferences, error) {
				siteCalls++
				return jqlcmd.BuildPreferences{BaseURL: "https://example.invalid/jira"}, nil
			}
			deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
				t.Fatal("browse route constructed a Jira client")
				return nil, errors.New("unexpected")
			}
			deps.Credentials = func() (core.CredentialStore, error) {
				t.Fatal("browse route opened credentials")
				return nil, errors.New("unexpected")
			}
			deps.Cache = func(ctx context.Context) (core.Cache, error) {
				t.Fatal("browse route opened cache")
				return nil, errors.New("unexpected")
			}
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(deps, &stdout, &stderr)
			args := append(append([]string{}, route...), "--output=json")
			if exit := execute(t.Context(), program, sink, args); exit != 0 {
				t.Fatalf("browse route failed: exit=%d out=%q err=%q", exit, stdout.String(), stderr.String())
			}
			var frame struct {
				OK   bool `json:"ok"`
				Data struct {
					WebURL string `json:"web_url"`
				} `json:"data"`
			}
			if json.Unmarshal(stdout.Bytes(), &frame) != nil || !frame.OK || frame.Data.WebURL != "https://example.invalid/jira/browse/KEY-7" || siteCalls < 1 {
				t.Fatalf("browse route lost explicit site: calls=%d out=%q", siteCalls, stdout.String())
			}
		})
	}
}

func TestOpenDiscoveryAndInvalidTargetsNeverResolveSite(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		exit int
	}{
		{name: "help", args: []string{"open", "--help"}, exit: 0},
		{name: "schema", args: []string{"agent", "schema", "--path=open", "--output=json"}, exit: 0},
		{name: "completion", args: []string{"completion", "bash"}, exit: 0},
		{name: "missing", args: []string{"open", "--output=json"}, exit: 3},
		{name: "many", args: []string{"open", "KEY-1", "KEY-2", "--output=json"}, exit: 3},
		{name: "many expression", args: []string{"open", "KEY-1:2", "--output=json"}, exit: 3},
		{name: "bad key", args: []string{"open", "bad", "--output=json"}, exit: 3},
		{name: "bad flag", args: []string{"open", "KEY-1", "--unknown", "--output=json"}, exit: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := testDeps(t)
			deps.BuildPreferences = func() (jqlcmd.BuildPreferences, error) {
				t.Fatal("discovery or refusal resolved site")
				return jqlcmd.BuildPreferences{}, nil
			}
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(deps, &stdout, &stderr)
			if exit := execute(t.Context(), program, sink, tc.args); exit != tc.exit {
				t.Fatalf("unexpected exit=%d want=%d out=%q err=%q", exit, tc.exit, stdout.String(), stderr.String())
			}
			if tc.name == "schema" {
				var frame struct {
					Data envelope.AgentSchemaOutput `json:"data"`
				}
				if json.Unmarshal(stdout.Bytes(), &frame) != nil {
					t.Fatal("open schema is not valid JSON")
				}
			}
		})
	}
}

func TestOpenHistoryUsesOnlyExplicitLocalScope(t *testing.T) {
	for _, route := range [][]string{{"open", "KEY-7"}, {"issue", "view", "KEY-7", "--web"}, {"issue", "comment", "list", "KEY-7", "--web"}} {
		t.Run(route[0]+route[1], func(t *testing.T) {
			var memory adapters.MemoryCache
			cacheCalls := 0
			deps := testDeps(t)
			deps.BuildPreferences = func() (jqlcmd.BuildPreferences, error) {
				return jqlcmd.BuildPreferences{BaseURL: "https://example.invalid", Account: "person@example.invalid"}, nil
			}
			deps.CacheFor = func(site, account string) (core.Cache, error) {
				cacheCalls++
				if site != "https://example.invalid" || account != "person@example.invalid" {
					t.Fatal("URL history guessed another namespace")
				}
				return &memory, nil
			}
			deps.Cache = func(ctx context.Context) (core.Cache, error) {
				t.Fatal("URL history opened credential-backed cache")
				return nil, nil
			}
			deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
				t.Fatal("URL history opened client")
				return nil, nil
			}
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(deps, &stdout, &stderr)
			args := append(append([]string{}, route...), "--output=json")
			if exit := execute(t.Context(), program, sink, args); exit != 0 {
				t.Fatalf("URL history changed operation: exit=%d out=%q err=%q", exit, stdout.String(), stderr.String())
			}
			keys, _, _ := cachecmd.RecentIssueKeys(&memory)
			if cacheCalls != 1 || !slices.Equal(keys, []string{"KEY-7"}) {
				t.Fatalf("URL history lost explicit scope: calls=%d keys=%v", cacheCalls, keys)
			}
		})
	}
}
