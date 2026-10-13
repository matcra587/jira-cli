package root

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/config"
)

func TestDraftEditorSelectionUsesOnlySelectedPublicSettings(t *testing.T) {
	for _, name := range []string{"JIRA_EDITOR", "VISUAL", "EDITOR", "JIRA_CONFIG"} {
		t.Setenv(name, "")
	}
	// A broken token reference must remain irrelevant to local editor selection.
	t.Setenv("JIRA_API_TOKEN_ENV", "invalid reference!")
	t.Setenv("JIRA_SECRET_BACKEND", "1password")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "selected.toml")
	config.SetFlagLayer(config.FlagLayer{Path: path})
	t.Cleanup(func() { config.SetFlagLayer(config.FlagLayer{}) })
	// JSON quoting is also valid TOML for this printable command fixture.
	command := shell.Quote(executable) + " --literal '$HOME;ignored'"
	quoted, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(append([]byte("editor = "), quoted...), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	argv, err := editorCommand(t.Context())
	if err != nil || !slices.Equal(argv, []string{executable, "--literal", "$HOME;ignored"}) {
		t.Fatal("editor ignored selected public settings or expanded argv", argv, err)
	}
	if err := os.WriteFile(path, []byte("invalid = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := editorCommand(t.Context()); err == nil {
		t.Fatal("malformed selected settings were silently ignored")
	}
	t.Setenv("JIRA_EDITOR", shell.Quote(executable)+" --override")
	argv, err = editorCommand(t.Context())
	if err != nil || !slices.Equal(argv, []string{executable, "--override"}) {
		t.Fatal("explicit editor could not repair malformed public settings", argv, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if argv, err := editorCommand(ctx); len(argv) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled selection entered local work", argv, err)
	}
}
