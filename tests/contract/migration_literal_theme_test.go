package contract

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
)

func TestLiteralThemeMigrationLoadsActualPaletteAndRollsBackExactBytes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			source := filepath.Join(dir, "legacy", "old.toml")
			target := filepath.Join(dir, "new", "settings.toml")
			recovery := filepath.Join(dir, "recovery")
			palette := filepath.Join(filepath.Dir(source), "~", "$PALETTE.toml")
			if err := os.MkdirAll(filepath.Dir(palette), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(palette, []byte("base='dark'\n[colors]\nhelp_command='#123456'\nhelp_section='#123456'\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			original := []byte("# preserve whole legacy document\n[theme]\nname='dark'\npath='~/$PALETTE.toml'\n[[profiles]]\nname='default'\nsecret_backend='env'\n")
			if err := os.WriteFile(source, original, 0o600); err != nil {
				t.Fatal(err)
			}
			decoy := filepath.Join(dir, "decoy")
			if err := os.Mkdir(decoy, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(decoy, "palette.toml"), []byte("base='light'\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			env := []string{"JIRA_KEYRING=false", "JIRA_SECRET_BACKEND=env", "JIRA_API_TOKEN=", "JIRA_API_TOKEN_ENV=", "JIRA_THEME=", "JIRA_THEME_PATH=", "JIRA_NO_UPDATE_CHECK=1", "PALETTE=palette", "HOME=" + decoy, "USERPROFILE=" + decoy}
			invoke := func(args ...string) string {
				t.Helper()
				stdout, stderr, exit := runBinaryEnv(t, env, append(args, "--output="+mode, "--no-input")...)
				if exit != 0 || stderr != "" {
					t.Fatalf("literal theme migration command failed: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
				}
				return stdout
			}
			invoke("config", "migrate", "--source="+source, "--config="+target, "--dry-run")
			for _, path := range []string{filepath.Dir(target), recovery} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("migration preview created a directory")
				}
			}
			invoke("config", "migrate", "--source="+source, "--config="+target, "--recovery-dir="+recovery, "--yes")
			file, err := config.ReadFile(t.Context(), target)
			if err != nil {
				t.Fatal(err)
			}
			path, err := file.Setting("theme_path")
			if err != nil || path.Value != palette {
				t.Fatal("migration expanded or redirected the literal palette name", err)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, "--config="+target, "--output=human", "--color=always")
			if exit != 0 || stdout != "" || !strings.Contains(stderr, "38;2;18;52;86") {
				t.Fatal("actual help did not load the literal migrated palette")
			}
			invoke("config", "rollback", "--journal="+filepath.Join(recovery, "migration.json"), "--yes")
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatal("rollback did not restore target absence")
			}
			got, err := os.ReadFile(source)
			if err != nil || !bytes.Equal(got, original) {
				t.Fatal("rollback changed literal source path or other source bytes", err)
			}
		})
	}
}
