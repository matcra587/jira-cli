package config_test

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
)

func TestAliasTableLoadsLiteralKeysAndFileOnlyProvenance(t *testing.T) {
	t.Parallel()
	want := map[string]string{"my.inbox": "issue list --status 'To Do,In Progress'", "quote # name": "issue view 'KEY-123'", "empty": ""}
	path := writeConfig(t, "read_only = true\n[aliases]\n'my.inbox' = \"issue list --status 'To Do,In Progress'\"\n'quote # name' = \"issue view 'KEY-123'\"\nempty = ''\n")
	r, err := config.Load(config.Options{Path: path, Environ: env("JIRA_ALIASES=ignored", "JIRA_READ_ONLY=false")})
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(r.Config.Aliases, want) || r.Source("aliases") != config.LayerFile || r.Config.ReadOnly || r.Source("read_only") != config.LayerEnv {
		t.Fatal("literal alias keys, file-only isolation or adjacent provenance changed")
	}
	var displayed map[string]string
	if err := json.Unmarshal([]byte(r.Config.Values()["aliases"]), &displayed); err != nil || !maps.Equal(displayed, want) {
		t.Fatal("settings rendering lost a literal alias name or expansion")
	}
}

func TestAliasTableAbsentAndEmptyRemainInspectable(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"", "[aliases]\n"} {
		t.Run(text, func(t *testing.T) {
			t.Parallel()
			r, err := config.Load(config.Options{Path: writeConfig(t, text), Environ: env("JIRA_ALIASES=invalid", "JIRA_ALIASES_NEW=ignored")})
			if err != nil || len(r.Config.Aliases) != 0 || r.Config.Values()["aliases"] != "{}" {
				t.Fatalf("empty aliases changed or accepted an environment overlay: %v", err)
			}
			want := config.LayerDefault
			if text != "" {
				want = config.LayerFile
			}
			if r.Source("aliases") != want {
				t.Fatal("empty alias table lost its source")
			}
		})
	}
}

func TestAliasTableRejectsWrongTypesEvenWithEnvironmentOverride(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"aliases = 'scalar'\n", "aliases = ['array']\n", "[aliases]\na = false\n", "[aliases]\na = 123\n", "[aliases.a]\nextra = 'version'\n"} {
		t.Run(text, func(t *testing.T) {
			t.Parallel()
			if _, err := config.Load(config.Options{Path: writeConfig(t, text), Environ: env("JIRA_ALIASES=must not hide invalid file data")}); err == nil {
				t.Fatal("malformed file aliases passed strict decoding")
			}
		})
	}
}

func TestAliasValuesEscapeControlsWithoutChangingStoredData(t *testing.T) {
	t.Parallel()
	const hostile = "label\x1b[2J\a\u0085hidden\nnext"
	c := config.Config{Aliases: map[string]string{hostile: hostile}}
	value := c.Values()["aliases"]
	if strings.ContainsAny(value, "\x1b\a\n") {
		t.Fatal("alias settings rendering emitted executable terminal controls")
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(value), &decoded); err != nil || !maps.Equal(decoded, c.Aliases) {
		t.Fatal("visible alias rendering changed the stored control-bearing value")
	}
}
