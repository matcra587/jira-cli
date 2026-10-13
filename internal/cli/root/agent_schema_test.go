package root

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/docent"
	"github.com/matcra587/docent/docenttest"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

var updateAgentSchema = flag.Bool("update-agent-schema", false, "Update the canonical raw-write discovery fixture")

func TestAgentSchemaGolden(t *testing.T) {
	var stdout bytes.Buffer
	program, sink := newProgram(testDeps(t), &stdout, io.Discard)
	if exit := execute(t.Context(), program, sink, []string{"agent", "schema", "--path=api post", "--output=json"}); exit != 0 {
		t.Fatalf("schema execution exited %d", exit)
	}
	var frame struct {
		OK   bool                       `json:"ok"`
		Data envelope.AgentSchemaOutput `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil || !frame.OK {
		t.Fatal("schema execution did not emit its shared success envelope")
	}
	var write docent.Command
	if err := json.Unmarshal(frame.Data.Schema, &write); err != nil || write.Path != "jira api post" {
		t.Fatal("raw write operation missing from emitted schema")
	}
	// This pins a real operation's flags, safety help and registered output
	// shape. Binary tests separately cover routing, selection and envelopes.
	docenttest.SchemaGolden(t, write, "testdata/agent-api-post-schema.json", *updateAgentSchema)
}

func TestAgentCommandsNeverConstructJiraAdapters(t *testing.T) {
	markdown := filepath.Join(t.TempDir(), "notes.md")
	native := filepath.Join(t.TempDir(), "native.json")
	if err := os.WriteFile(markdown, []byte("Hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(native, []byte(`{"type":"doc","version":1,"content":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"user", "--help"},
		{"user", "search", "--help"},
		{"agent", "schema", "--path=user search", "--output=json"},
		{"agent", "fieldtypes", "--output=json"},
		{"agent", "fieldtypes", "--output=human"},
		{"agent", "fieldtypes", "--help"},
		{"agent", "adf-matrix", "--output=json"},
		{"agent", "adf-matrix", "--output=human"},
		{"agent", "adf-matrix", "--help"},
		{"adf", "convert", "--input=" + markdown, "--output=json"},
		{"adf", "render", "--input=" + native, "--output=compact"},
		{"adf", "convert", "--help"},
		{"adf", "render", "--help"},
		{"--output=json"},
		{"--output=compact"},
		{"--output=human"},
		{"--jq=.data.schema.path"},
		{"agent", "schema", "--output=json"},
		{"agent", "schema", "--output=compact", "--shapes"},
		{"agent", "schema", "--output=human", "--path=api post"},
		{"agent", "schema", "--jq=.data.schema.path"},
		{"agent", "schema", "--help"},
		{"agent", "schema", "--path=missing", "--output=json"},
		{"agent", "guide", "--output=json"},
		{"agent", "guide", "--all", "--output=compact"},
		{"guide", "raw-api", "--section=Run", "--output=human"},
		{"guide", "--help"},
		{"agent", "guide", "missing", "--output=json"},
		{"agent", "export", "--format=agent-skill", "--dir=" + t.TempDir() + "/skills", "--dry-run", "--output=json"},
		{"agent", "export", "--format=agent-skill", "--dir=" + t.TempDir() + "/skills", "--output=json"},
		{"agent", "export", "--help"},
		{"agent", "export", "--scope=other", "--output=json"},
	} {
		t.Run(args[len(args)-1], func(t *testing.T) {
			deps := testDeps(t)
			deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
				t.Fatal("discovery constructed a Jira client")
				return nil, nil
			}
			deps.JiraClientFor = func(string, string, string) (core.JiraClient, error) {
				t.Fatal("discovery constructed an explicit-account Jira client")
				return nil, nil
			}
			deps.Credentials = func() (core.CredentialStore, error) {
				t.Fatal("discovery opened the credential store")
				return nil, nil
			}
			deps.Cache = func(ctx context.Context) (core.Cache, error) {
				t.Fatal("discovery constructed a disk cache")
				return nil, nil
			}
			program, sink := newProgram(deps, io.Discard, io.Discard)
			want := 0
			if len(args) > 2 && (args[2] == "--path=missing" || args[2] == "missing" || args[2] == "--scope=other") {
				want = 3
			}
			if exit := execute(t.Context(), program, sink, args); exit != want {
				t.Fatalf("schema exit=%d, want %d", exit, want)
			}
		})
	}
}
