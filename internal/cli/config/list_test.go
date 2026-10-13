package config

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gechr/clog"
	"github.com/gechr/x/ansi"

	cfg "github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// load resolves a configuration from an injected environment and a
// non-existent file, which is the state a machine that never configured
// anything is in.
func load(t *testing.T, environ ...string) *cfg.Resolved {
	t.Helper()
	return loadWithFile(t, "", environ...)
}

// loadWithFile is load with actual file contents behind the environment,
// so a precedence claim has something to be precedent OVER.
func loadWithFile(t *testing.T, contents string, environ ...string) *cfg.Resolved {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if contents != "" {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}
	r, err := cfg.Load(cfg.Options{
		Path:    path,
		Environ: func() []string { return environ },
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return r
}

// TestListNeverRendersTheToken pins the rule that makes this command
// safe to run anywhere. The payload is what gets piped, logged, and
// pasted into issues, so the token must not survive into it — and the
// secret flag has to say a value was withheld, or a consumer cannot tell
// redaction from a literal.
func TestListNeverRendersTheToken(t *testing.T) {
	t.Parallel()
	const token = "ATATT-a-very-real-looking-token"
	out := buildOutput(load(t, "JIRA_API_TOKEN="+token))

	var buf bytes.Buffer
	if err := renderHuman(output.NewView(&buf, clog.ColorNever), envelope.Envelope{Data: out}); err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, where := range []struct{ name, body string }{
		{name: "human output", body: buf.String()},
		{name: "payload", body: settingsString(out)},
	} {
		if strings.Contains(where.body, token) {
			t.Errorf("%s leaked the token:\n%s", where.name, where.body)
		}
	}
	row := settingFor(t, out, "api_token")
	if !row.Secret {
		t.Error("api_token row is not marked secret; a consumer cannot tell it was withheld")
	}
	if row.Source != string(cfg.LayerEnv) {
		t.Errorf("source = %q, want %q — redaction must not cost provenance", row.Source, cfg.LayerEnv)
	}
}

// TestListReportsEffectiveValuesNotFileContents is the constraint the
// whole command exists for. A settings view that shows the file while an
// environment variable is winning contradicts the command that just ran,
// and is worse than no view at all because it is believed.
func TestListReportsEffectiveValuesNotFileContents(t *testing.T) {
	t.Parallel()
	// A real file underneath, holding DIFFERENT values: without one this
	// only proves the environment is read, never that it wins — and the
	// command could stop consulting the file at all with nothing failing.
	out := buildOutput(loadWithFile(t,
		"base_url = \"https://file.example\"\nemail = \"file@example.com\"\nread_only = false\n",
		"JIRA_BASE_URL=https://env.example",
		"JIRA_READ_ONLY=true",
	))
	for _, tt := range []struct{ key, value, source string }{
		{key: "base_url", value: "https://env.example", source: string(cfg.LayerEnv)},
		{key: "read_only", value: "true", source: string(cfg.LayerEnv)},
		// Untouched by the environment, so the FILE must still win over
		// the default — the other half of the precedence claim.
		{key: "email", value: "file@example.com", source: string(cfg.LayerFile)},
	} {
		row := settingFor(t, out, tt.key)
		if row.Value != tt.value {
			t.Errorf("%s value = %q, want %q", tt.key, row.Value, tt.value)
		}
		if row.Source != tt.source {
			t.Errorf("%s source = %q, want %q", tt.key, row.Source, tt.source)
		}
	}
}

// TestListCoversEveryKey pins that the view cannot silently omit a
// setting. A key that exists but never renders is invisible to the one
// command whose job is showing what is configured.
func TestListCoversEveryKey(t *testing.T) {
	t.Parallel()
	// Written out, not taken from cfg.Keys(): deriving the oracle from
	// the same function the implementation uses makes the test agree with
	// any answer. A new setting must be added HERE by hand, which is the
	// moment someone decides whether it renders and whether it is secret.
	// timeout renders and is not secret: it is a duration the caller
	// chose, and the whole point of showing it is that "why did this
	// request give up?" is answerable from `config current`.
	want := []string{"base_url", "cloud_id", "email", "credential_id", "account_id", "account_id_site", "account_id_email", "account_id_cloud_id", "api_token", "api_token_env", "default_project", "default_issue_type", "default_board", "refresh_interval", "team_account_ids", "keyring", "secret_backend", "onepassword_account", "onepassword_vault", "onepassword_item", "read_only", "timeout", "theme", "theme_path", "glyphs", "workday_seconds", "queries_path", "editor", "aliases"}

	if got := cfg.Keys(); !slices.Equal(got, want) {
		t.Fatalf("config declares keys %v, want %v — a new setting has to be added to this test deliberately", got, want)
	}
	out := buildOutput(load(t))
	if len(out.Settings) != len(want) {
		t.Fatalf("rendered %d settings, want %d", len(out.Settings), len(want))
	}
	for _, key := range want {
		if row := settingFor(t, out, key); row.Source == "" {
			t.Errorf("%s renders without a source", key)
		}
	}
}

// TestListStatesThePathEvenWhenAbsent pins the answer to "where do I
// change this?" — the reason no separate path command needs to exist.
// The path of a file that does not exist yet is the path to create.
func TestListStatesThePathEvenWhenAbsent(t *testing.T) {
	t.Parallel()
	out := buildOutput(load(t))
	if out.Path == "" {
		t.Fatal("no config path reported")
	}
	if out.FileExists {
		t.Error("FileExists = true for a file that is not there")
	}
	var buf bytes.Buffer
	if err := renderHuman(output.NewView(&buf, clog.ColorNever), envelope.Envelope{Data: out}); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(buf.String(), out.Path) {
		t.Errorf("human output does not name the config file:\n%s", buf.String())
	}
}

func settingFor(t *testing.T, out envelope.ConfigListOutput, key string) envelope.ConfigSetting {
	t.Helper()
	for _, s := range out.Settings {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("no row for %q", key)
	return envelope.ConfigSetting{}
}

func settingsString(out envelope.ConfigListOutput) string {
	var b strings.Builder
	for _, s := range out.Settings {
		b.WriteString(s.Key + "=" + s.Value + " (" + s.Source + ")\n")
	}
	return b.String()
}

// TestListMarksARedactedBaseURLAsSecret covers the case buildOutput's
// own comment names and nothing tested: a base_url carrying userinfo is
// rewritten before it renders, so the value shown is NOT the value
// configured. Reporting secret=false there tells a scripted caller the
// mangled URL is literal.
func TestListMarksARedactedBaseURLAsSecret(t *testing.T) {
	t.Parallel()
	password := t.Name()
	out := buildOutput(load(t, "JIRA_BASE_URL=https://user:"+password+"@example.atlassian.net"))

	row := settingFor(t, out, "base_url")
	if strings.Contains(row.Value, password) {
		t.Errorf("the password survived into the payload: %q", row.Value)
	}
	if !row.Secret {
		t.Errorf("base_url renders as %q but is not marked secret — a consumer reads that as the literal value", row.Value)
	}
}

// TestListMarksUntouchedValuesAsNotSecret is the other half: secret must
// distinguish withheld from shown, so a value that renders exactly as
// configured must not claim to be redacted.
func TestListMarksUntouchedValuesAsNotSecret(t *testing.T) {
	t.Parallel()
	out := buildOutput(load(t, "JIRA_BASE_URL=https://example.atlassian.net"))

	for _, key := range []string{"base_url", "email", "read_only"} {
		if row := settingFor(t, out, key); row.Secret {
			t.Errorf("%s = %q is shown in full, yet marked secret", key, row.Value)
		}
	}
}

// TestUnsetValuesShowADashInHumanOnly pins both halves of the
// placeholder's contract.
//
// A gap in a column of real values reads as a rendering fault rather
// than as information, so the human format says "-" out loud. The
// envelope must NOT: a machine reading "-" as the value of base_url
// would be wrong, and folding the substitution into the output builder
// instead of the renderer is exactly how that would happen.
func TestUnsetValuesShowADashInHumanOnly(t *testing.T) {
	t.Parallel()
	// Nothing configured, so base_url is empty.
	out := buildOutput(load(t))
	env := envelope.Envelope{Data: out}

	var human bytes.Buffer
	if err := renderHuman(output.NewView(&human, clog.ColorNever), env); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human.String(), " -  ") && !strings.Contains(human.String(), " - ") {
		t.Errorf("human output has no dash for the unset value:\n%s", human.String())
	}

	// The envelope keeps the empty string.
	for _, s := range out.Settings {
		if s.Key == "base_url" && s.Value != "" {
			t.Errorf("envelope value for base_url = %q, want the empty string — the dash is a rendering choice, not data", s.Value)
		}
	}
}

// TestColumnsAlignByDisplayWidth pins that padding counts CELLS, not
// bytes.
//
// `len` on "José@example.com" is 17 and its width is 16, so a byte-padded
// column puts the source one cell out for any account with an accented
// character — and six out for a site in a non-Latin script. Jira content
// is user-authored, so this is the ordinary case.
func TestColumnsAlignByDisplayWidth(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"jose@example.com", "José@example.com", "日本のサイト"} {
		if got, want := ansi.StringWidth(pad(value, 20)), 20; got != want {
			t.Errorf("pad(%q, 20) is %d cells wide, want %d", value, got, want)
		}
	}
	// A value wider than the column is left intact rather than cut.
	long := "a-very-long-value-past-the-column"
	if got := pad(long, 5); got != long {
		t.Errorf("pad truncated an over-wide value: %q", got)
	}
}
