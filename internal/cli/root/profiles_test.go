package root

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestProfileCommandsStayLocalAndPreserveOtherProfiles(t *testing.T) {
	t.Setenv("JIRA_PROFILE", "")
	if err := os.Unsetenv("JIRA_PROFILE"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settings.toml")
	calls := 0
	deps := testDeps(t)
	deps.Credentials = func() (core.CredentialStore, error) { calls++; return nil, errors.New("unexpected credentials") }
	deps.JiraClient = func() (core.JiraClient, error) { calls++; return nil, errors.New("unexpected Jira") }
	deps.Cache = func() (core.Cache, error) { calls++; return nil, errors.New("unexpected cache") }
	run := func(args ...string) []byte {
		t.Helper()
		args = append(args, "--config", path, "--output=json", "--no-input")
		applyGlobalFlags(args)
		t.Cleanup(func() { applyGlobalFlags(nil) })
		var stdout, stderr bytes.Buffer
		program, sink := newProgram(deps, &stdout, &stderr)
		if code := execute(t.Context(), program, sink, args); code != 0 {
			t.Fatalf("%v: exit=%d out=%s err=%s", args, code, stdout.String(), stderr.String())
		}
		if calls != 0 {
			t.Fatalf("local profiles opened %d adapters", calls)
		}
		return stdout.Bytes()
	}
	run("config", "profile", "add", "work", "--base-url=https://work.example.invalid", "--email=person@example.invalid")
	run("config", "profile", "add", "personal", "--base-url=https://personal.example.invalid", "--email=other@example.invalid")
	run("config", "set", "read_only", "true", "--profile=work")
	var read struct {
		Data envelope.ConfigGetOutput `json:"data"`
	}
	if err := json.Unmarshal(run("config", "get", "read_only", "-P", "work"), &read); err != nil {
		t.Fatal(err)
	}
	if !read.Data.Present || read.Data.Value != "true" {
		t.Fatalf("wrong work setting: %+v", read.Data)
	}
	if err := json.Unmarshal(run("config", "get", "read_only", "--profile=personal"), &read); err != nil {
		t.Fatal(err)
	}
	if read.Data.Present {
		t.Fatalf("work preference escaped: %+v", read.Data)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	run("config", "profile", "rename", "work", "office", "--dry-run")
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("preview changed file: %v", err)
	}
	run("config", "profile", "rename", "work", "office")
	run("config", "profile", "default", "personal")
	var listing struct {
		Data envelope.ConfigProfileListOutput `json:"data"`
	}
	if err := json.Unmarshal(run("config", "profile", "list", "--profile=office"), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Data.Profiles) != 2 || listing.Data.Profiles[0].Name != "office" || !listing.Data.Profiles[0].Selected || listing.Data.Profiles[0].Default || !listing.Data.Profiles[1].Default {
		t.Fatalf("wrong selected/default profiles: %+v", listing.Data)
	}
	run("config", "profile", "remove", "office", "--force")
	if err := json.Unmarshal(run("config", "profile", "list"), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Data.Profiles) != 1 || listing.Data.Profiles[0].Name != "personal" {
		t.Fatalf("wrong removal: %+v", listing.Data)
	}
}

func TestProfileCompletionUsesSelectedFileWithoutCredentials(t *testing.T) {
	t.Setenv("JIRA_PROFILE", "")
	if err := os.Unsetenv("JIRA_PROFILE"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settings.toml")
	data := "format_version = 1\ndefault_profile = 'work'\n[profiles.work]\nbase_url = 'https://example.invalid'\nemail = 'person@example.invalid'\n[profiles.other]\nbase_url = 'https://other.example.invalid'\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	names := profileCandidates([]string{"--config", path})
	if !slices.Equal(names, []string{"other\tConfigured profile", "work\tConfigured profile"}) {
		t.Fatalf("wrong completion: %q", names)
	}
	layer := globalFlagLayer([]string{"issue", "list", "-P", "other", "--config", path})
	if layer.Profile != "other" || !layer.ProfileSet || layer.Path != path {
		t.Fatalf("global selection lost: %+v", layer)
	}
	applyGlobalFlags(nil)
	program, _ := newProgram(testDeps(t), &bytes.Buffer{}, &bytes.Buffer{})
	flag := program.Root.PersistentFlags().Lookup("profile")
	if flag == nil || flag.Shorthand != "P" {
		t.Fatal("profile flag missing from real tree")
	}
}

func TestUnknownProfileRefusesBeforeProvider(t *testing.T) {
	t.Setenv("JIRA_PROFILE", "")
	if err := os.Unsetenv("JIRA_PROFILE"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(path, []byte("format_version=1\ndefault_profile='work'\n[profiles.work]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"config", "get", "email", "--profile=typo", "--config", path, "--output=json"}
	applyGlobalFlags(args)
	t.Cleanup(func() { applyGlobalFlags(nil) })
	var stdout, stderr bytes.Buffer
	program, sink := newProgram(testDeps(t), &stdout, &stderr)
	if code := execute(t.Context(), program, sink, args); code != 3 || !strings.Contains(stdout.String(), "typo") || !strings.Contains(stdout.String(), "config_invalid") {
		t.Fatalf("unknown profile accepted: exit=%d out=%s err=%s", code, stdout.String(), stderr.String())
	}
}
