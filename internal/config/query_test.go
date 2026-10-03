package config_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
)

func TestQueryFilesRetainNamedMetadataAndLiteralJQL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body, display, description, project string
	}{
		{"plain", "\nproject = ABC AND summary ~ \"a,b & <c>\" ORDER BY key ASC\n", "inbox", "", ""},
		{"yaml", "---\nname: 'Personal inbox'\ndescription: 'Read pending work'\nproject: ABC\nfuture: retained outside JQL\n---\nproject = ABC AND summary ~ \"a,b & <c>\" ORDER BY key ASC\n", "Personal inbox", "Read pending work", "ABC"},
		{"toml", "+++\nname = 'Personal inbox'\ndescription = 'Read pending work'\nproject = 'ABC'\nfuture = 42\n+++\nproject = ABC AND summary ~ \"a,b & <c>\" ORDER BY key ASC\n", "Personal inbox", "Read pending work", "ABC"},
		{"crlf", "---\r\nname: 'Windows inbox'\r\n---\r\nproject = ABC AND summary ~ \"a,b & <c>\" ORDER BY key ASC\r\n", "Windows inbox", "", ""},
		{"empty yaml", "---\n---\nproject = ABC AND summary ~ \"a,b & <c>\" ORDER BY key ASC", "inbox", "", ""},
		{"empty toml", "+++\n+++\nproject = ABC AND summary ~ \"a,b & <c>\" ORDER BY key ASC", "inbox", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "inbox.jql"), []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("invalid other input"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(dir, "folder.jql"), 0o700); err != nil {
				t.Fatal(err)
			}
			queries, err := config.LoadQueries(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			q := queries["inbox"]
			if len(queries) != 1 || q.Name != tc.display || q.Description != tc.description || q.Project != tc.project || q.JQL != `project = ABC AND summary ~ "a,b & <c>" ORDER BY key ASC` {
				t.Fatalf("saved query metadata or literal body changed: %+v", queries)
			}
			queries["inbox"] = config.Query{}
			again, err := config.LoadQueries(t.Context(), dir)
			if err != nil || again["inbox"] != q {
				t.Fatal("caller changed the next query load or source file")
			}
		})
	}
}

func TestQueryFilesRejectMalformedAndEmptyDefinitions(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		"", " \n", "---\nname: Inbox", "+++\nname = 'Inbox'", "---\nname: [Inbox]\n---\nproject = ABC",
		"+++\nname = 1\n+++\nproject = ABC", "---\nname: 1\n---\nproject = ABC", "---\nname: Inbox\nname: Other\n---\nproject = ABC",
		"---\n---", "+++\n+++", "project = ABC\x00", "project = ABC\xff", "+++\ninvalid = [\n+++\nproject = ABC",
		"---\nname: null\n---\nproject = ABC", "---\ndescription: null\n---\nproject = ABC", "---\nproject: null\n---\nproject = ABC",
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "inbox.jql"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if queries, err := config.LoadQueries(t.Context(), dir); err == nil || queries != nil {
				t.Fatal("invalid saved definition produced a usable query map")
			}
		})
	}
}

func TestQueryLoadingHonorsCancellationAndDirectoryFailures(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := config.LoadQueries(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled saved-query load touched files or lost cancellation")
	}
	for _, dir := range []string{"", " ", filepath.Join(t.TempDir(), "missing")} {
		if _, err := config.LoadQueries(t.Context(), dir); err == nil {
			t.Fatal("invalid saved-query directory was silently substituted")
		}
	}
}

func TestQueryNamesCannotSelectPathsOrControls(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", " ", ".", "..", "../outside", `..\outside`, "/absolute", "inbox\nnext", "inbox\x00", "inbox\xff"} {
		if err := config.ValidateQueryName(name); err == nil {
			t.Fatalf("unsafe query name accepted: %q", name)
		}
	}
	for _, name := range []string{"inbox", "my.inbox", "my inbox", "Équipe"} {
		if err := config.ValidateQueryName(name); err != nil {
			t.Fatalf("literal query name rejected: %q: %v", name, err)
		}
	}
}

func TestQueryDirectoryHasIndependentLayerProvenance(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, "queries_path = 'file queries'\n")
	for _, tc := range []struct {
		name, override, want string
		source               config.Layer
	}{
		{"file", "", "file queries", config.LayerFile},
		{"environment", "environment queries", "environment queries", config.LayerEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resolved, err := config.Load(config.Options{Path: path, Environ: env("JIRA_QUERIES_PATH=" + tc.override)})
			if err != nil || resolved.Config.QueriesPath != tc.want || resolved.Config.Values()["queries_path"] != tc.want || resolved.Source("queries_path") != tc.source {
				t.Fatalf("saved-query preference lost value or provenance: %v", err)
			}
		})
	}
}

func TestQueryDefaultDirectoryRetainsBaselineLocationAndProvenance(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	want := filepath.Join(dir, "jira-cli", "queries")
	resolved, err := config.Load(config.Options{Path: filepath.Join(dir, "absent.toml"), Environ: env()})
	if err != nil || config.DefaultQueriesPath() != want || resolved.Config.QueriesPath != want || resolved.Source("queries_path") != config.LayerDefault {
		t.Fatalf("default saved-query location or provenance changed: %v", err)
	}
}
