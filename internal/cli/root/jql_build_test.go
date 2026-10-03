package root

import (
	"bytes"
	"errors"
	"testing"

	jqlcmd "github.com/matcra587/jira-cli/internal/cli/jql"
	"github.com/matcra587/jira-cli/internal/core"
)

func TestLocalJQLBuildAndDiscoveryNeverConstructCredentialsOrClient(t *testing.T) {
	for _, args := range [][]string{
		{"jql", "build", "--output=json"},
		{"jql", "build", "--project=KEY", "--output=human"},
		{"jql", "build", "--help"},
		{"agent", "schema", "--path=jql build", "--output=json"},
		{"completion", "fish"},
	} {
		t.Run(args[0]+"/"+args[len(args)-1], func(t *testing.T) {
			deps := testDeps(t)
			deps.JiraClient = func() (core.JiraClient, error) {
				t.Error("local build constructed Jira client")
				return nil, errors.New("unexpected")
			}
			deps.Credentials = func() (core.CredentialStore, error) {
				t.Error("local build opened credentials")
				return nil, errors.New("unexpected")
			}
			deps.Cache = func() (core.Cache, error) {
				t.Error("local build opened credential-backed cache")
				return nil, errors.New("unexpected")
			}
			deps.BuildPreferences = func() (jqlcmd.BuildPreferences, error) {
				if args[0] != "jql" || args[len(args)-1] == "--help" {
					t.Error("help/schema/completion resolved preferences")
				}
				return jqlcmd.BuildPreferences{}, nil
			}
			deps.CacheFor = func(string, string) (core.Cache, error) {
				t.Error("unscoped build opened cache")
				return nil, errors.New("unexpected")
			}
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(deps, &stdout, &stderr)
			if exit := execute(t.Context(), program, sink, args); exit != 0 {
				t.Fatalf("local build/discovery failed: exit=%d stdout=%s stderr=%s", exit, &stdout, &stderr)
			}
		})
	}
}

func TestBuildFlagRefusalsPrecedeAllDependencies(t *testing.T) {
	for _, flags := range [][]string{
		{"--board-id=0"},
		{"--board-id=-1"},
		{"--board=", "--board-id=42"},
		{"--board=Sprint", "--board-id=42"},
		{"--board=\n"},
		{"--updated="},
		{"--created= "},
		{"--resolved="},
		{"--order-by="},
		{"--key=KEY-0"},
		{"--project=KEY\n"},
	} {
		t.Run(flags[0], func(t *testing.T) {
			deps := testDeps(t)
			deps.BuildPreferences = func() (jqlcmd.BuildPreferences, error) {
				t.Error("bad flags resolved preferences")
				return jqlcmd.BuildPreferences{}, errors.New("unexpected")
			}
			deps.CacheFor = func(string, string) (core.Cache, error) {
				t.Error("bad flags opened cache")
				return nil, errors.New("unexpected")
			}
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(deps, &stdout, &stderr)
			args := append([]string{"jql", "build", "--output=json"}, flags...)
			if exit := execute(t.Context(), program, sink, args); exit != 3 {
				t.Fatalf("bad flags accepted/misclassified: exit=%d stdout=%s stderr=%s", exit, &stdout, &stderr)
			}
		})
	}
}
