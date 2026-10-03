package config

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func aliasFixture(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAliasFilePreservesLiteralNamesTextAndDeclaredSettings(t *testing.T) {
	t.Parallel()
	const original = "# preserve preferences\nread_only = true\ntimeout = '2m'\nworkday_seconds = 36000\n[aliases]\n'my.inbox' = '''issue list --status 'To Do,In Progress' --query 'literal #tag' '''\n"
	path := aliasFixture(t, original)
	f, err := OpenAliasFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	const expansion = "issue list --status 'To Do,In Progress' --query 'literal #tag' "
	aliases := f.Aliases()
	if len(aliases) != 1 || aliases["my.inbox"] != expansion {
		t.Fatal("file snapshot changed a literal alias name or quoted expansion")
	}
	aliases["copy"] = `issue view 'KEY-123' --output=json`
	changed, err := f.SaveAliases(t.Context(), aliases)
	if err != nil || !changed {
		t.Fatalf("save aliases: changed=%t error=%v", changed, err)
	}
	r, err := Load(Options{Path: path, Environ: func() []string { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(r.Config.Aliases, aliases) || !r.Config.ReadOnly || r.Config.Timeout.String() != "2m0s" || r.Config.WorkdaySeconds != 36000 {
		t.Fatal("alias update dropped another alias or changed declared settings")
	}
	if r.Source("aliases") != LayerFile || r.Source("read_only") != LayerFile || r.Source("base_url") != LayerDefault {
		t.Fatal("alias update lost map or scalar provenance")
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), "base_url") || strings.Contains(string(data), "keyring") || strings.Contains(string(data), "api_token") {
		t.Fatal("alias update added defaults or credential fields")
	}
	if f.Aliases()["copy"] != "" || f.Aliases()["my.inbox"] != expansion {
		t.Fatal("caller edits or serialization changed the immutable snapshot")
	}
}

func TestAliasFileNoopPreservesBytesMetadataAndMissingDirectory(t *testing.T) {
	t.Parallel()
	path := aliasFixture(t, "# user formatting\n[aliases]\na = 'version'\n")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := OpenAliasFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := f.SaveAliases(t.Context(), map[string]string{"a": "version"})
	if err != nil || changed {
		t.Fatalf("unchanged alias map was rewritten: %t %v", changed, err)
	}
	after, err := os.Stat(path)
	data, readErr := os.ReadFile(path)
	if err != nil || readErr != nil || !after.ModTime().Equal(before.ModTime()) || after.Mode() != before.Mode() || string(data) != "# user formatting\n[aliases]\na = 'version'\n" {
		t.Fatal("no-op changed bytes or file metadata")
	}
	missing := filepath.Join(t.TempDir(), "absent", "config.toml")
	f, err = OpenAliasFile(t.Context(), missing)
	if err != nil {
		t.Fatal(err)
	}
	changed, err = f.SaveAliases(t.Context(), nil)
	if err != nil || changed {
		t.Fatalf("empty first-run aliases created local state: %t %v", changed, err)
	}
	if _, err := os.Stat(filepath.Dir(missing)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("no-op created a config directory")
	}
}

func TestAliasFileEmptyTablesAllowFirstAddAndLastDelete(t *testing.T) {
	t.Parallel()
	for _, initial := range []string{"", "[aliases]\n"} {
		t.Run(initial, func(t *testing.T) {
			t.Parallel()
			path := aliasFixture(t, initial)
			f, err := OpenAliasFile(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			aliases := f.Aliases()
			if aliases == nil || len(aliases) != 0 {
				t.Fatal("empty alias table cannot accept a first alias")
			}
			aliases["first"] = "version"
			if len(f.Aliases()) != 0 {
				t.Fatal("editing an empty returned table changed the snapshot")
			}
			if changed, err := f.SaveAliases(t.Context(), aliases); err != nil || !changed {
				t.Fatalf("first alias save: %t %v", changed, err)
			}
			f, err = OpenAliasFile(t.Context(), path)
			if err != nil || !maps.Equal(f.Aliases(), map[string]string{"first": "version"}) {
				t.Fatalf("first alias did not persist: %v", err)
			}
			if changed, err := f.SaveAliases(t.Context(), nil); err != nil || !changed {
				t.Fatalf("last alias delete: %t %v", changed, err)
			}
			loaded, err := Load(Options{Path: path, Environ: func() []string { return nil }})
			if err != nil || len(loaded.Config.Aliases) != 0 || loaded.Source("aliases") != LayerFile {
				t.Fatalf("last alias delete did not persist an empty file table: %v", err)
			}
		})
	}
}

func TestAliasFileRefusesChangedCreatedRemovedOrRedirectedSnapshot(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"change", "create", "create empty", "remove", "remove empty", "redirect"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			if operation == "redirect" && runtime.GOOS == "windows" {
				t.Skip("creating symlinks requires additional Windows privileges")
			}
			path := aliasFixture(t, "theme = 'original'\n")
			selected := path
			if operation == "create" || operation == "create empty" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if operation == "remove empty" {
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if operation == "redirect" {
				selected = filepath.Join(filepath.Dir(path), "link.toml")
				if err := os.Symlink(path, selected); err != nil {
					t.Fatal(err)
				}
			}
			f, err := OpenAliasFile(t.Context(), selected)
			if err != nil {
				t.Fatal(err)
			}
			switch operation {
			case "change", "create":
				if err := os.WriteFile(path, []byte("theme = 'other writer'\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "create empty":
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "remove", "remove empty":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "redirect":
				other := filepath.Join(filepath.Dir(path), "other.toml")
				if err := os.WriteFile(other, []byte("theme = 'other target'\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(selected); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, selected); err != nil {
					t.Fatal(err)
				}
			}
			changed, err := f.SaveAliases(t.Context(), map[string]string{"inbox": "version"})
			if changed || !errors.Is(err, ErrFileChanged) {
				t.Fatalf("stale snapshot was applied: changed=%t error=%v", changed, err)
			}
			if operation == "remove" || operation == "remove empty" {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("removed snapshot was recreated")
				}
			} else {
				data, err := os.ReadFile(selected)
				if err != nil || strings.Contains(string(data), "inbox") {
					t.Fatal("refused alias update changed another writer's file")
				}
			}
		})
	}
}

func TestAliasFileFirstSavePermissionsAndSymlinkOwnership(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	newPath := filepath.Join(root, "new", "config.toml")
	f, err := OpenAliasFile(t.Context(), newPath)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := f.SaveAliases(t.Context(), map[string]string{"inbox": "version"}); err != nil || !changed {
		t.Fatalf("first save: %t %v", changed, err)
	}
	if runtime.GOOS != "windows" {
		for path, want := range map[string]os.FileMode{newPath: 0o600, filepath.Dir(newPath): 0o700} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != want {
				t.Fatalf("new local state permissions: %v, want %o", err, want)
			}
		}
	}
	shared := filepath.Join(root, "shared")
	if err := os.Mkdir(shared, 0o755); err != nil { //nolint:gosec // fixture pins preservation of an existing shared directory's permissions
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o755); err != nil { //nolint:gosec // neutralize umask so the shared-directory preservation assertion is meaningful
		t.Fatal(err)
	}
	f, err = OpenAliasFile(t.Context(), filepath.Join(shared, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.SaveAliases(t.Context(), map[string]string{"inbox": "version"}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(shared); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Fatal("alias update changed an existing shared directory's permissions")
	}
	if runtime.GOOS == "windows" {
		return
	}
	link := filepath.Join(root, "link.toml")
	if err := os.Symlink(newPath, link); err != nil {
		t.Fatal(err)
	}
	f, err = OpenAliasFile(t.Context(), link)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.SaveAliases(t.Context(), map[string]string{"updated": "version"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("alias update detached a config symlink")
	}
	loaded, err := Load(Options{Path: newPath, Environ: func() []string { return nil }})
	if err != nil || !maps.Equal(loaded.Config.Aliases, map[string]string{"updated": "version"}) {
		t.Fatal("alias update did not reach the config symlink target")
	}
}

func TestAliasFileCanceledSaveHasNoSideEffects(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "missing", "config.toml")
	f, err := OpenAliasFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if changed, err := f.SaveAliases(ctx, map[string]string{"inbox": "version"}); changed || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled save ignored cancellation: %t %v", changed, err)
	}
	if _, err := OpenAliasFile(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled open ignored cancellation")
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled operation created a directory")
	}
}

func TestAliasFileRejectsMalformedConfigWithoutWriting(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"[aliases\n", "[aliases]\na = true\n", "[aliases.a]\nextra = 'version'\n", "unknown = true\n", "workday_seconds = 1.5\n", "workday_seconds = 0\n", "base_url = 'remote'\n"} {
		t.Run(text, func(t *testing.T) {
			t.Parallel()
			path := aliasFixture(t, text)
			if _, err := OpenAliasFile(t.Context(), path); err == nil {
				t.Fatal("malformed config became an alias update target")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != text {
				t.Fatal("invalid configuration was rewritten")
			}
		})
	}
}

func TestAliasFileUpdatesIgnoreEnvironmentAndFlagValues(t *testing.T) {
	t.Setenv("JIRA_BASE_URL", "https://environment.invalid")
	t.Setenv("JIRA_THEME", "environment")
	t.Setenv("JIRA_WORKDAY_SECONDS", "1")
	t.Setenv("JIRA_API_TOKEN", "fixture-environment-credential")
	t.Setenv("JIRA_ALIASES", "must not replace the table")
	SetFlagLayer(FlagLayer{TimeoutSet: true, Timeout: 1})
	t.Cleanup(func() { SetFlagLayer(FlagLayer{}) })
	path := aliasFixture(t, "theme = 'file'\ntimeout = '2m'\nworkday_seconds = 36000\n")
	f, err := OpenAliasFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.SaveAliases(t.Context(), map[string]string{"inbox": "version"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), "environment") || strings.Contains(string(data), "base_url") || strings.Contains(string(data), "api_token") || !strings.Contains(string(data), "36000") || !strings.Contains(string(data), "2m") {
		t.Fatal("alias update persisted an effective environment or flag value")
	}
}

func TestAliasFileUsesExplicitFlagAndDefaultPaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	flagPath := aliasFixture(t, "[aliases]\nflag = 'version'\n")
	explicitPath := aliasFixture(t, "[aliases]\nexplicit = 'version'\n")
	SetFlagLayer(FlagLayer{Path: flagPath})
	t.Cleanup(func() { SetFlagLayer(FlagLayer{}) })
	for _, tc := range []struct {
		name string
		path string
		want string
		key  string
	}{
		{name: "flag", want: flagPath, key: "flag"},
		{name: "explicit beats flag", path: explicitPath, want: explicitPath, key: "explicit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := OpenAliasFile(t.Context(), tc.path)
			if err != nil || f.Path() != tc.want || !maps.Equal(f.Aliases(), map[string]string{tc.key: "version"}) {
				t.Fatalf("wrong selected config: %v", err)
			}
		})
	}
	SetFlagLayer(FlagLayer{})
	if runtime.GOOS != "linux" {
		return // XDG_CONFIG_HOME does not isolate the native config location on every platform.
	}
	want, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	f, err := OpenAliasFile(t.Context(), "")
	if err != nil || f.Path() != want || len(f.Aliases()) != 0 {
		t.Fatalf("missing default config was not selected: %v", err)
	}
}

func TestAliasFileNeverDetachesDanglingSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires additional Windows privileges")
	}
	t.Parallel()
	for _, createdLater := range []bool{false, true} {
		t.Run(map[bool]string{false: "already dangling", true: "created after snapshot"}[createdLater], func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			path := filepath.Join(root, "link.toml")
			target := filepath.Join(root, "absent.toml")
			var f *AliasFile
			if createdLater {
				var err error
				f, err = OpenAliasFile(t.Context(), path)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			if createdLater {
				if changed, err := f.SaveAliases(t.Context(), map[string]string{"inbox": "version"}); changed || !errors.Is(err, ErrFileChanged) {
					t.Fatalf("new dangling link was replaced: %t %v", changed, err)
				}
			} else if _, err := OpenAliasFile(t.Context(), path); err == nil {
				t.Fatal("dangling link was mistaken for an absent file")
			}
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatal("dangling config link was detached")
			}
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("dangling link check created its target")
			}
		})
	}
}
