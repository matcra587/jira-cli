package command

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

func TestArgumentFileInputKeepsPositionalRecoveryAndSharedBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, path, input string
		terminal          bool
	}{
		{name: "terminal", path: StdinPath, terminal: true},
		{name: "missing", path: filepath.Join(t.TempDir(), "missing.yaml")},
		{name: "oversize", path: StdinPath, input: strings.Repeat("x", maxBodyBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadArgumentFileContext(t.Context(), "FILE", tc.path, strings.NewReader(tc.input), tc.terminal)
			input, ok := errors.AsType[*cli.InputError](err)
			if !ok || input.Code != cli.CodeFlagValueInvalid || input.Field != "FILE" || !strings.Contains(input.Error(), "FILE") || strings.Contains(input.Error(), "--FILE") {
				t.Fatalf("positional input lost honest recovery or its shared refusal: %v", err)
			}
		})
	}
	got, err := ReadArgumentFileContext(t.Context(), "FILE", StdinPath, strings.NewReader("literal: version\n"), false)
	if err != nil || string(got) != "literal: version\n" {
		t.Fatalf("positional standard input was changed: %q %v", got, err)
	}
}
