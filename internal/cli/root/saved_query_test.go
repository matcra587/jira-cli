package root

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSavedQueryPreviewConstructsNoClient(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "inbox.jql"), []byte("project = ABC ORDER BY key ASC"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JIRA_QUERIES_PATH", dir)
	t.Setenv("JIRA_KEYRING", "false")
	for _, args := range [][]string{
		{"issue", "list", "--saved=inbox", "--as-jql"},
		{"issue", "mine", "--saved=inbox", "--as-jql"},
	} {
		exit, constructions := runQuiet(t, args)
		if exit != 0 || constructions != 0 {
			t.Fatalf("local saved query constructed a Jira client: args=%v exit=%d constructions=%d", args, exit, constructions)
		}
	}
}
