package config_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
)

// writeConfig puts a TOML file in a temp dir and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// env builds an environ slice, so no test mutates the process
// environment — that would forbid t.Parallel and leak across tests.
func env(pairs ...string) func() []string {
	return func() []string { return pairs }
}

// TestPrecedence pins the layer order that everything else assumes:
// defaults lose to the file, the file loses to the environment.
func TestPrecedence(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, "base_url = \"https://from-file.example\"\nemail = \"file@example.com\"\n")

	tests := []struct {
		name     string
		environ  func() []string
		wantURL  string
		wantFrom config.Layer
	}{
		{
			name:     "file beats the default",
			environ:  env(),
			wantURL:  "https://from-file.example",
			wantFrom: config.LayerFile,
		},
		{
			name:     "environment beats the file",
			environ:  env("JIRA_BASE_URL=https://from-env.example"),
			wantURL:  "https://from-env.example",
			wantFrom: config.LayerEnv,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := config.Load(config.Options{Path: path, Environ: tt.environ})
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got.Config.BaseURL != tt.wantURL {
				t.Errorf("base_url = %q, want %q", got.Config.BaseURL, tt.wantURL)
			}
			if src := got.Source("base_url"); src != tt.wantFrom {
				t.Errorf("source = %q, want %q", src, tt.wantFrom)
			}
		})
	}
}

// TestProvenanceIsPerKey pins that the source is tracked per value, not
// per load. A user whose email came from a file and whose token came
// from the environment needs both answers from one `config current`.
func TestProvenanceIsPerKey(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, "email = \"file@example.com\"\n")
	got, err := config.Load(config.Options{
		Path:    path,
		Environ: env("JIRA_API_TOKEN=secret", "JIRA_READ_ONLY=true"),
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for key, want := range map[string]config.Layer{
		"email":     config.LayerFile,
		"api_token": config.LayerEnv,
		"read_only": config.LayerEnv,
		"base_url":  config.LayerDefault, // nothing set it
	} {
		if got := got.Source(key); got != want {
			t.Errorf("source(%q) = %q, want %q", key, got, want)
		}
	}
	// Provenance without the VALUE is half an answer: clearing the
	// decoded fields left every assertion above passing, so the source
	// could have been right about a value that never arrived.
	if got.Config.Email != "file@example.com" {
		t.Errorf("email = %q, want the file value to have decoded", got.Config.Email)
	}
	if got.Config.APIToken != "secret" {
		t.Errorf("api_token did not decode from the environment")
	}
	if !got.Config.ReadOnly {
		t.Error("read_only did not decode from the environment")
	}
}

// TestMissingFileIsNotAnError pins the common path: the CLI must run on
// environment variables alone. Treating absence as failure would break
// every invocation on a machine that never wrote a config file.
func TestMissingFileIsNotAnError(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "nope.toml")
	got, err := config.Load(config.Options{
		Path:    missing,
		Environ: env("JIRA_BASE_URL=https://env.example"),
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.FileExists {
		t.Error("FileExists = true for a file that is not there")
	}
	if got.Config.BaseURL != "https://env.example" {
		t.Errorf("base_url = %q, want the environment value", got.Config.BaseURL)
	}
}

// TestUnknownFileKeyFailsLoudly pins the scar: a key nobody claims must
// not be dropped in silence. A typo like base_uri looks exactly like a
// setting that did not take effect, with nothing saying why.
func TestUnknownFileKeyFailsLoudly(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, "base_uri = \"https://typo.example\"\n")
	_, err := config.Load(config.Options{Path: path, Environ: env()})
	if err == nil {
		t.Fatal("load succeeded with an unknown key; the typo would vanish silently")
	}
	if !strings.Contains(err.Error(), "base_uri") {
		t.Errorf("err = %v, want it to name the offending key", err)
	}
}

// TestUnknownEnvVarIsIgnored is the other half of that rule. Strictness
// belongs to the file, which the user wrote for this tool; the
// environment is shared with everything else on the machine, so an
// unrelated JIRA_* variable must not break every command.
func TestUnknownEnvVarIsIgnored(t *testing.T) {
	t.Parallel()
	got, err := config.Load(config.Options{
		Path:    filepath.Join(t.TempDir(), "none.toml"),
		Environ: env("JIRA_HOME=/opt/jira", "JIRA_BASE_URL=https://env.example"),
	})
	if err != nil {
		t.Fatalf("load: %v — an unrelated JIRA_* variable must not fail the load", err)
	}
	if got.Config.BaseURL != "https://env.example" {
		t.Errorf("base_url = %q, want the known variable still applied", got.Config.BaseURL)
	}
}

// TestReadOnlyCrossesTheStringBoundary pins the environment's typing
// problem: every value arrives as a string, and read_only is a bool.
// Without weak typing the transport gate could never be switched on
// from the environment, which is the only way to switch it on today.
func TestReadOnlyCrossesTheStringBoundary(t *testing.T) {
	t.Parallel()
	// Each case starts from a FILE that sets read_only = true, so a
	// "false" result proves the environment was decoded and overrode it.
	// Against an empty file, false is Go's zero value and the assertion
	// passed even when the value was thrown away entirely.
	for _, tt := range []struct {
		raw  string
		want bool
	}{
		{raw: "true", want: true},
		{raw: "1", want: true},
		{raw: "false", want: false},
		{raw: "0", want: false},
	} {
		t.Run(tt.raw, func(t *testing.T) {
			t.Parallel()
			path := writeConfig(t, "read_only = true\n")
			got, err := config.Load(config.Options{
				Path:    path,
				Environ: env("JIRA_READ_ONLY=" + tt.raw),
			})
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got.Config.ReadOnly != tt.want {
				t.Errorf("JIRA_READ_ONLY=%s gave %v, want %v", tt.raw, got.Config.ReadOnly, tt.want)
			}
			if src := got.Source("read_only"); src != config.LayerEnv {
				t.Errorf("source = %q, want %q — the environment must own the value it set", src, config.LayerEnv)
			}
		})
	}
}

// TestSecretsAreRedactedForDisplay pins that the token never renders.
// The marker is constant rather than length-preserving, because a row of
// asterisks matching the real length leaks the length, and a token's
// length identifies its type.
func TestSecretsAreRedactedForDisplay(t *testing.T) {
	t.Parallel()
	const token = "ATATT-a-very-real-looking-token"
	if got := config.Redacted("api_token", token); strings.Contains(got, token) {
		t.Errorf("Redacted leaked the token: %q", got)
	}
	if got := config.Redacted("api_token", token); got != "(set)" {
		t.Errorf("Redacted = %q, want a constant marker", got)
	}
	if got := config.Redacted("api_token", ""); got != "" {
		t.Errorf("Redacted(empty) = %q, want empty — absent must not read as set", got)
	}
	if got := config.Redacted("base_url", "https://x.example"); got != "https://x.example" {
		t.Errorf("Redacted redacted a non-secret: %q", got)
	}
}

// TestValidateDoesNotRunAtLoad pins that a half-configured state loads.
// `config current` must be able to show what is missing, and a command
// needing no client must not fail for want of credentials.
func TestValidateDoesNotRunAtLoad(t *testing.T) {
	t.Parallel()
	got, err := config.Load(config.Options{
		Path:    filepath.Join(t.TempDir(), "none.toml"),
		Environ: env(),
	})
	if err != nil {
		t.Fatalf("load: %v — an empty configuration must still load", err)
	}
	if err := got.Config.Validate(); err == nil {
		t.Error("Validate passed on an empty configuration")
	} else {
		for _, want := range []string{"base_url", "email", "api_token"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Validate error %q does not name %q", err, want)
			}
		}
	}
}

// TestEmptyEnvVarIsUnsetNotEmpty pins that an exported-but-empty
// variable does not overwrite a real value.
//
// `export JIRA_BASE_URL=$SOMETHING_UNSET` in a shell profile, a CI
// template, or a direnv file produces exactly this, and treating it as a
// value silently replaced a correct config file with "" — every command
// then reporting an incomplete configuration while pointing at a file
// that was fine. "" is not a legal value for any key here, so an empty
// variable can only have meant unset.
func TestEmptyEnvVarIsUnsetNotEmpty(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, "base_url = \"https://real.example\"\nemail = \"real@example.com\"\n")
	got, err := config.Load(config.Options{
		Path:    path,
		Environ: env("JIRA_BASE_URL=", "JIRA_EMAIL="),
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Config.BaseURL != "https://real.example" {
		t.Errorf("base_url = %q; an empty variable overwrote the file", got.Config.BaseURL)
	}
	if src := got.Source("base_url"); src != config.LayerFile {
		t.Errorf("source = %q, want %q — an empty variable must not claim the value", src, config.LayerFile)
	}
	// A variable with an actual value still wins, or the fix has simply
	// disabled the environment layer.
	got, err = config.Load(config.Options{Path: path, Environ: env("JIRA_BASE_URL=https://env.example")})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Config.BaseURL != "https://env.example" {
		t.Errorf("base_url = %q, want the environment to still win when it has a value", got.Config.BaseURL)
	}
}

// TestValidateRejectsUnusableBaseURLs pins the parsing that a prefix
// check could not do. "https://" alone has no host and would fail much
// later as an opaque request error; a URL carrying userinfo is a
// password sitting in a field nothing treats as secret.
func TestValidateRejectsUnusableBaseURLs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		url  string
		ok   bool
	}{
		{name: "ordinary cloud site", url: "https://example.atlassian.net", ok: true},
		{name: "behind a proxy path", url: "https://proxy.example/jira", ok: true},
		{name: "local http instance", url: "http://localhost:8080", ok: true},
		{name: "scheme with no host", url: "https://"},
		{name: "no scheme at all", url: "example.atlassian.net"},
		{name: "wrong scheme", url: "ftp://example.atlassian.net"},
		{name: "embedded password", url: "https://user:hunter2@example.atlassian.net"}, // betterleaks:allow -- synthetic URL used to test credential rejection and redaction.
		{name: "embedded user only", url: "https://user@example.atlassian.net"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Config{BaseURL: tt.url, Email: "a@b.c", APIToken: "tok"}
			err := cfg.Validate()
			if tt.ok && err != nil {
				t.Fatalf("Validate() = %v, want nil for a usable site", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("Validate() = nil for an unusable base_url")
			}
			// No error may quote the raw URL: that is how a password
			// reaches a terminal, a log, or a pasted bug report.
			if err != nil && strings.Contains(err.Error(), "hunter2") {
				t.Errorf("validation error leaked the password: %v", err)
			}
			// A bare site name is refused rather than expanded, because
			// guessing "acme" into a hostname can send this user's
			// credentials to a stranger's tenant. The error has to earn
			// that strictness by naming the exact string to type.
			if err != nil && !strings.Contains(err.Error(), "credentials") &&
				!strings.Contains(err.Error(), "atlassian.net") {
				t.Errorf("error %q does not show the expected URL shape", err)
			}
		})
	}
}

// TestWhitespaceIsNotACredential pins that a blank-looking value is
// treated as missing. "   " is a quoting accident in a shell profile or
// CI variable, and accepting it turns a config mistake into an auth
// failure that gets blamed on the token.
func TestWhitespaceIsNotACredential(t *testing.T) {
	t.Parallel()
	cfg := config.Config{BaseURL: "https://x.example", Email: "   ", APIToken: "\t\n"}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil for whitespace-only credentials")
	}
	for _, want := range []string{"email", "api_token"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q as missing", err, want)
		}
	}
}

// TestRedactedStripsURLCredentials pins the second half of the userinfo
// rule. Validate rejects such a URL, but redaction cannot lean on that:
// a half-configured state is deliberately inspectable BEFORE validation,
// which is exactly when the bad value is still there to be printed.
func TestRedactedStripsURLCredentials(t *testing.T) {
	t.Parallel()
	const withPassword = "https://user:hunter2@example.atlassian.net" // betterleaks:allow -- synthetic URL used to test credential rejection and redaction.
	got := config.Redacted("base_url", withPassword)
	if strings.Contains(got, "hunter2") {
		t.Errorf("Redacted leaked the password: %q", got)
	}
	if strings.Contains(got, "user") && !strings.Contains(got, "redacted") {
		t.Errorf("Redacted leaked the username: %q", got)
	}
	if plain := config.Redacted("base_url", "https://example.atlassian.net"); plain != "https://example.atlassian.net" {
		t.Errorf("Redacted altered an ordinary URL: %q", plain)
	}
}

// TestDefaultsLayerIsPresent pins that the defaults layer exists and is
// attributed. Every default currently equals a Go zero value, so
// deleting the layer entirely left the precedence tests green — they
// were comparing against a value the decoder would have produced anyway.
// Asserting the SOURCE is what distinguishes "the default supplied this"
// from "nothing did".
func TestDefaultsLayerIsPresent(t *testing.T) {
	t.Parallel()
	got, err := config.Load(config.Options{
		Path:    filepath.Join(t.TempDir(), "none.toml"),
		Environ: env(),
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, key := range config.Keys() {
		if src := got.Source(key); src != config.LayerDefault {
			t.Errorf("source(%q) = %q, want %q — the defaults layer must claim untouched keys", key, src, config.LayerDefault)
		}
	}
	// And the layer must actually carry every key, not merely be
	// indistinguishable from absence.
	if len(config.Defaults()) != len(config.Keys()) {
		t.Errorf("Defaults() has %d keys, Keys() has %d — a key with no default is invisible to provenance",
			len(config.Defaults()), len(config.Keys()))
	}
}

// TestInvalidFileKeySurvivesAnEnvOverride pins that strictness cannot be
// masked. koanf replaces a whole subtree when a later layer supplies a
// scalar at the same path, so a bad nested key in the file plus an
// environment variable at that key erased the mistake before the strict
// decode could see it — and the user's typo vanished silently, which is
// exactly what strictness exists to prevent.
func TestInvalidFileKeySurvivesAnEnvOverride(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, "[base_url]\ntypo = \"x\"\n")
	_, err := config.Load(config.Options{
		Path:    path,
		Environ: env("JIRA_BASE_URL=https://env.example"),
	})
	if err == nil {
		t.Fatal("load succeeded; an environment value masked an invalid file key")
	}
	if !strings.Contains(err.Error(), "base_url") {
		t.Errorf("err = %v, want it to name the offending file key", err)
	}
}

// TestFileLayerRejectsWrongTypes pins that the FILE is decoded strictly.
// TOML can express real types, so `read_only = 1` is a mistake the
// format let the user make — and weak conversion would read it as true,
// silently enabling a mode that refuses every write. The environment is
// exempt by nature: every value there is a string.
func TestFileLayerRejectsWrongTypes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("read_only = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := config.Load(config.Options{Path: path, Environ: func() []string { return nil }}); err == nil {
		t.Fatal("read_only = 1 was accepted from the file and would silently enable read-only mode")
	}
}

// TestGlyphsFileLayerRejectsUnknownValue pins GlyphSet's decode-time
// validation (config.go's own doc comment on GlyphSet.UnmarshalText): an
// unrecognized `glyphs` value fails the WHOLE load, the same way a
// wrongly-typed read_only does above — not a warning, and not a silent
// GlyphSetDefault. This is the deliberate asymmetry with `theme`, which
// stays a bare string here and is validated later, leniently, at render
// time.
func TestGlyphsFileLayerRejectsUnknownValue(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`glyphs = "chunky"`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := config.Load(config.Options{Path: path, Environ: func() []string { return nil }}); err == nil {
		t.Fatal(`glyphs = "chunky" was accepted from the file — an unrecognized glyph set must fail the load`)
	}
}

// TestGlyphsResolvesFromFileAndEnvironment pins the happy path both
// layers must reach: a valid file value decodes to the right constant,
// and JIRA_GLYPHS overrides it via the same string vocabulary — exactly
// like every other config key, with no special-casing for glyphs on the
// environment side.
func TestGlyphsResolvesFromFileAndEnvironment(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`glyphs = "nerd"`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := config.Load(config.Options{Path: path, Environ: func() []string { return nil }})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if r.Config.Glyphs != config.GlyphSetNerd {
		t.Errorf("glyphs = %q in the file, got %v, want GlyphSetNerd", "nerd", r.Config.Glyphs)
	}

	r, err = config.Load(config.Options{
		Path:    path,
		Environ: func() []string { return []string{"JIRA_GLYPHS=default"} },
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if r.Config.Glyphs != config.GlyphSetDefault {
		t.Errorf("JIRA_GLYPHS=default did not override the file's nerd, got %v", r.Config.Glyphs)
	}
}

// TestEnvironmentStillConvertsStrings is the other half: the strictness
// above must not reach the environment, where a boolean can only ever
// arrive as text.
func TestEnvironmentStillConvertsStrings(t *testing.T) {
	t.Parallel()
	r, err := config.Load(config.Options{
		Path:    filepath.Join(t.TempDir(), "absent.toml"),
		Environ: func() []string { return []string{"JIRA_READ_ONLY=true"} },
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !r.Config.ReadOnly {
		t.Error("JIRA_READ_ONLY=true did not resolve to true")
	}
}

// brokenStore holds a record that cannot be parsed.
type brokenStore struct{}

func (brokenStore) Get(ctx context.Context, _ string) (string, error) { return "not-a-record", nil }
func (brokenStore) Set(ctx context.Context, _, _ string) error        { return nil }
func (brokenStore) Delete(ctx context.Context, _ string) error        { return nil }

// TestUnreadableCredentialsDoNotFailTheLoad is the guard on the brick.
//
// Every command resolves configuration, so returning an error for a
// stored record that cannot be parsed takes the whole CLI down —
// including `jira auth logout`, which is what the message tells you to
// run, leaving an OS keyring tool as the only escape. The load must
// carry on without the layer and say so.
func TestUnreadableCredentialsDoNotFailTheLoad(t *testing.T) {
	t.Parallel()
	resolved, err := config.Resolve(t.Context(), config.Options{Path: filepath.Join(t.TempDir(), "absent.toml"), Environ: func() []string {
		return []string{"JIRA_BASE_URL=https://env.example"}
	}}, brokenStore{})
	if err != nil {
		t.Fatalf("an unreadable credential record failed the whole load: %v", err)
	}
	// The rest of the configuration still resolves.
	if resolved.Config.BaseURL != "https://env.example" {
		t.Errorf("base_url = %q, want the environment's value", resolved.Config.BaseURL)
	}
	// And it is not silent — a broken record the user cannot see is a
	// mystery, not a degradation.
	if len(resolved.Warnings) == 0 {
		t.Error("no warning reported for an unreadable credential record")
	}
}

// tupleStore holds a complete record for one specific site.
type tupleStore struct{ record string }

func (t tupleStore) Get(ctx context.Context, _ string) (string, error) { return t.record, nil }
func (tupleStore) Set(ctx context.Context, _, _ string) error          { return nil }
func (tupleStore) Delete(ctx context.Context, _ string) error          { return nil }

const storedForCompany = `{"version":1,"site":"https://company.atlassian.net","email":"person@company.example","token":"STORED-SECRET"}`

// TestStoredCredentialsNeverFollowTheSiteElsewhere is the security rule
// the layered config could not otherwise express.
//
// Layers merge key by key, so JIRA_BASE_URL alone overrides only the
// site — leaving the stored email and token in place and sending a
// credential to a host it was never issued for. No single key is wrong,
// which is exactly why the check has to be on the tuple.
func TestStoredCredentialsNeverFollowTheSiteElsewhere(t *testing.T) {
	t.Parallel()
	resolved, err := config.Resolve(t.Context(), config.Options{Path: filepath.Join(t.TempDir(), "absent.toml"), Environ: func() []string {
		return []string{"JIRA_BASE_URL=https://attacker.example"}
	}}, tupleStore{record: storedForCompany})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if resolved.Config.APIToken == "STORED-SECRET" {
		t.Error("the stored token was paired with a site it was never stored for; it would be sent to that host")
	}
	if resolved.Config.Email == "person@company.example" {
		t.Error("the stored account followed the site override")
	}
	// The override itself stands — the caller asked for that site.
	if resolved.Config.BaseURL != "https://attacker.example" {
		t.Errorf("base_url = %q, want the environment's value", resolved.Config.BaseURL)
	}
	// And it is not silent.
	if len(resolved.Warnings) == 0 {
		t.Error("no warning that the stored credentials were dropped")
	}
}

// TestStoredCredentialsApplyToTheirOwnSite is the other half: the check
// must not throw away a credential that does still apply.
func TestStoredCredentialsApplyToTheirOwnSite(t *testing.T) {
	t.Parallel()
	resolved, err := config.Resolve(t.Context(), config.Options{Path: filepath.Join(t.TempDir(), "absent.toml"), Environ: func() []string {
		return nil
	}}, tupleStore{record: storedForCompany})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if resolved.Config.APIToken != "STORED-SECRET" {
		t.Error("the stored token was dropped for its own site")
	}
	if got := resolved.Source("api_token"); got != config.LayerKeyring {
		t.Errorf("api_token source = %q, want %q", got, config.LayerKeyring)
	}
}

// TestIncompleteStoredCredentialsAreNotUsed pins that a half-record
// cannot lend its token to identity from another layer. The three
// values were verified together and only mean anything together.
func TestIncompleteStoredCredentialsAreNotUsed(t *testing.T) {
	t.Parallel()
	resolved, err := config.Resolve(t.Context(), config.Options{Path: filepath.Join(t.TempDir(), "absent.toml"), Environ: func() []string {
		return []string{"JIRA_BASE_URL=https://other.example", "JIRA_EMAIL=someone@other.example"}
	}}, tupleStore{record: `{"version":1,"site":"","email":"","token":"ORPHAN-SECRET"}`})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if resolved.Config.APIToken == "ORPHAN-SECRET" {
		t.Error("a token with no identity was paired with an unrelated account")
	}
	if len(resolved.Warnings) == 0 {
		t.Error("no warning that the stored credentials were incomplete")
	}
}

// TestStoredCredentialsSurviveASpellingDifference is the boundary the
// tuple check got wrong first time.
//
// The stored site is normalized on load; the resolved base_url was not,
// so a value differing only by a trailing slash — routine in a shell
// profile — read as a DIFFERENT host. The credential was dropped and
// the warning named a host identical to the one it was stored for.
// A security check that fires on a legitimate setup gets disabled by
// whoever it inconveniences.
func TestStoredCredentialsSurviveASpellingDifference(t *testing.T) {
	t.Parallel()
	for _, spelling := range []string{
		"https://company.atlassian.net/",
		"https://company.atlassian.net",
	} {
		t.Run(spelling, func(t *testing.T) {
			t.Parallel()
			resolved, err := config.Resolve(t.Context(), config.Options{Path: filepath.Join(t.TempDir(), "absent.toml"), Environ: func() []string {
				return []string{"JIRA_BASE_URL=" + spelling}
			}}, tupleStore{record: storedForCompany})
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if resolved.Config.APIToken != "STORED-SECRET" {
				t.Errorf("the stored token was dropped for the same host spelled %q", spelling)
			}
			if len(resolved.Warnings) != 0 {
				t.Errorf("warned about a host identical to the stored one: %v", resolved.Warnings)
			}
		})
	}
}
