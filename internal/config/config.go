package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/matcra587/jira-cli/internal/jira"
)

// GlyphSet chooses which glyphs the human renderer is allowed to emit —
// today, whether the status pill caps a name with a Powerline half-circle
// or leaves it padded with plain spaces.
//
// GlyphSetDefault sits at iota 0 DELIBERATELY, against this codebase's
// usual "iota 0 is an Unknown sentinel" rule (see the naming skill's own
// guidance on StatusReady vs StatusUnknown at iota 0). That rule exists so
// a decoded-but-never-set field is distinguishable from one a caller
// genuinely chose — but an ABSENT `glyphs` key legitimately means "emit
// only glyphs any font renders," which is exactly GlyphSetDefault's
// behavior, and there is nothing a caller could do differently for
// "unset" versus "explicitly set to default": both want the padded,
// font-agnostic pill. A real Unknown sentinel would need its own branch
// somewhere that never fires, for a distinction nothing downstream reads.
type GlyphSet int

const (
	// GlyphSetDefault emits only glyphs any font renders — no Nerd Font
	// icons or Powerline symbols. The zero value, and the correct behavior
	// for a config file that never mentions `glyphs` at all: an agent's
	// terminal, a CI log, or a font this CLI has no way to probe must
	// never see a private-use-area codepoint it cannot render.
	GlyphSetDefault GlyphSet = iota
	// GlyphSetNerd allows Nerd Font-only glyphs — today, the Powerline
	// Extra Symbols half-circles that cap a status pill. Opt-in only:
	// unlike a color, a missing glyph in the wrong font renders as a
	// visible tofu box or a stray question mark, not merely "less pretty."
	GlyphSetNerd
)

// String renders g the way the config file spells it, so a value read
// back from `config list` matches what a caller would type into the
// file.
func (g GlyphSet) String() string {
	switch g {
	case GlyphSetNerd:
		return "nerd"
	default:
		return "default"
	}
}

// UnmarshalText parses the config file's string form, the same pattern
// clib's own theme.Theme uses for ITS string vocabulary (theme.go's
// applyThemeName calls theme.Theme.UnmarshalText to learn whether a name
// is usable). That precedent is exactly why glyphs decode-validates at
// all: Config.Theme is a bare string with no UnmarshalText of its own,
// because an unusable theme name must never break a command that needs
// no color — it is caught later, at render time, and downgraded to a
// warning (internal/cli/theme.go's applyTheme). Glyphs has no such
// argument for leniency: every value in its vocabulary is font-choice,
// not color, so there is no "still works, just plain" fallback worth
// inventing — an unrecognized value is a mistake in the file, and this
// method, wired through mapstructure's TextUnmarshallerHookFunc (see
// load.go's unmarshal), makes decoding the WHOLE config file fail loudly
// for it rather than silently keeping GlyphSetDefault. That is the
// intentional asymmetry with theme: a config file's typo the size of a
// misspelled color scheme should never crash a paging command, but a
// misspelled glyph mode is not "still working" in any real sense — it is
// asking for a font this CLI checked and rejected.
func (g *GlyphSet) UnmarshalText(text []byte) error {
	// Folded, not matched verbatim: a config file is hand-written, and
	// "Nerd" or " DEFAULT " means exactly what the lowercase spelling
	// means. Refusing them would be a rule with no purpose behind it —
	// the value is a closed vocabulary, not an identifier whose case
	// carries meaning.
	switch strings.ToLower(strings.TrimSpace(string(text))) {
	case "default", "":
		*g = GlyphSetDefault
	case "nerd":
		*g = GlyphSetNerd
	default:
		// The key names itself in the caller's own wrapping ("glyphs:
		// ..."), so this says only what was wrong with the VALUE.
		return fmt.Errorf("unknown value %q — valid: default, nerd", string(text))
	}
	return nil
}

// MarshalText is UnmarshalText's inverse, so the same TOML round-trips
// (config edit reads back what it wrote) and so anything that encodes a
// GlyphSet — a future `config set`, a test fixture — uses this vocabulary
// rather than %v's Go-syntax rendering of the underlying int.
func (g GlyphSet) MarshalText() ([]byte, error) {
	return []byte(g.String()), nil
}

// Layer names where a resolved value came from. The order of the
// constants IS the precedence order, lowest first, and Layers() depends
// on that — a value set in a later layer wins.
type Layer string

const (
	// LayerDefault is the built-in value, always present.
	LayerDefault Layer = "default"
	// LayerFile is the TOML config file.
	LayerFile Layer = "file"
	// LayerKeyring is the operating system credential store, written by
	// `jira auth login`. It sits above the file because a credential
	// belongs in a keyring rather than a config file, and below the
	// environment so CI can override without logging in.
	LayerKeyring Layer = "keyring"
	// LayerEnv is the JIRA_* environment.
	LayerEnv Layer = "env"
	// LayerFlag is a command-line flag. Reserved: no flag binds to config
	// yet, and the constant exists so precedence is written down once,
	// where it is enforced, rather than being invented later at the
	// binding site.
	LayerFlag Layer = "flag"
	// LayerUnknown is a key no layer claimed. It should be unreachable —
	// the defaults layer covers every key — so seeing it means the
	// defaults did not load, not that the value is a default.
	//
	// Reporting it honestly matters more than it looks: while Source fell
	// back to "default" for unattributed keys, provenance could not tell
	// "the defaults layer supplied this" from "no layer ran at all", and
	// a test asserting the defaults were present passed with the defaults
	// layer removed.
	LayerUnknown Layer = "unknown"
)

// Layers returns the precedence order, lowest priority first.
func Layers() []Layer { return []Layer{LayerDefault, LayerFile, LayerKeyring, LayerEnv, LayerFlag} }

// Config is the resolved settings for one invocation.
//
// Every field carries a koanf tag because the decode runs with
// ErrorUnused: an untagged field is invisible to the decoder, and a key
// matching nothing is a loud failure rather than a value silently
// dropped on the next save.
type Config struct {
	// BaseURL is the Jira site root.
	BaseURL string `koanf:"base_url"`
	// Email is the account the API token belongs to.
	Email string `koanf:"email"`
	// CredentialID selects a stable keyring slot independently of profile names.
	CredentialID string `koanf:"credential_id"`
	// APIToken authenticates against BaseURL. SECRET: never rendered —
	// see Redacted.
	APIToken string `koanf:"api_token"`
	// Keyring enables the operating system credential store. Turning it
	// off makes `jira` use only the file and environment layers, which
	// is what a CI runner, a shared machine, or a test harness wants —
	// a session credential store is not scoped by HOME, so it is the one
	// piece of state a sandbox cannot isolate any other way.
	Keyring bool `koanf:"keyring"`
	// ReadOnly refuses every write at the transport. Honored by the
	// adapter's client, so it holds for commands that never check it.
	ReadOnly bool `koanf:"read_only"`
	// Timeout bounds each HTTP request to Jira — connection, response and
	// body read together, since that is what http.Client.Timeout covers.
	//
	// Zero means NO deadline, and that is the default deliberately rather
	// than for want of a better number. This CLI streams attachment
	// uploads and downloads through the same client, and a whole-request
	// deadline that is generous for a JQL search is a hard failure partway
	// through a large file. A caller who wants a bound knows their own
	// network and their own payloads; the CLI guessing one for them would
	// break the slowest legitimate operation and call it a timeout.
	Timeout time.Duration `koanf:"timeout"`
	// Theme names the color theme for human output and prompts. Empty
	// means "decide from the terminal".
	//
	// Read at startup from the FILE layer only, before the command tree
	// exists — see internal/cli. That is why an unusable name here is a
	// warning rather than a load failure: the theme is resolved long
	// before anything can report an error properly, and a bad color
	// scheme must never stop a command running.
	Theme string `koanf:"theme"`
	// ThemePath selects an optional custom TOML palette. Relative paths are
	// resolved against the selected config file's directory, independently of
	// the process working directory. Empty retains the named preset.
	ThemePath string `koanf:"theme_path"`
	// Glyphs chooses which glyph vocabulary the human renderer may emit —
	// see GlyphSet's own doc comment for why, unlike Theme, an unusable
	// value here fails the WHOLE decode rather than falling back with a
	// warning. Resolved once per process by internal/cli, the same way
	// Theme is (ResolvedTheme/ResolvedGlyphs), not read by a sink
	// directly.
	Glyphs GlyphSet `koanf:"glyphs"`
	// WorkdaySeconds is the effort represented by 1d in worklog input.
	WorkdaySeconds int `koanf:"workday_seconds"`
	// QueriesPath is the directory of named .jql files, independent of auth.
	QueriesPath string `koanf:"queries_path"`
	// Editor is the external editor command, overridden by JIRA_EDITOR.
	// Parsing and executable lookup happen only when an editor is requested.
	Editor string `koanf:"editor"`
	// Aliases are file-only command expansions. Names and text remain literal;
	// expansion validation is a separate command concern.
	Aliases map[string]string `koanf:"aliases"`
}

// secretKeys are the config keys whose values must never be rendered.
// Kept as a set rather than a bool on each field so the redaction rule
// lives in one place and a new secret has one obvious place to join.
var secretKeys = map[string]bool{"api_token": true}

// IsSecret reports whether a key's value must be withheld from output.
func IsSecret(key string) bool { return secretKeys[key] }

// Keys lists every config key, in a stable order.
func Keys() []string {
	return []string{"base_url", "email", "credential_id", "api_token", "keyring", "read_only", "timeout", "theme", "theme_path", "glyphs", "workday_seconds", "queries_path", "editor", "aliases"}
}

// Defaults are the built-in values, the layer every load starts from.
// Empty strings are deliberate: a missing site or credential is a
// resolution failure the caller must report, not something to invent.
//
// glyphs' default is the STRING "default", not the bare GlyphSet zero
// value: this map feeds confmap.Provider, which every layer merges
// against before the final decode, and decoding a string through
// GlyphSet.UnmarshalText is the one path this Config actually exercises
// — keeping the defaults layer in that same shape means a bad value
// typo'd into Defaults itself would fail exactly the way a bad file
// value would, rather than skipping validation because it arrived as an
// already-typed Go value.
func Defaults() map[string]any {
	return map[string]any{
		"base_url":      "",
		"email":         "",
		"credential_id": "",
		"api_token":     "",
		"keyring":       true,
		"read_only":     false,
		// The string form, for the reason glyphs' default is a string: this
		// map feeds confmap.Provider and the decode hook that parses a
		// duration is the path a real file value takes, so the defaults
		// layer exercises it too.
		"timeout":         "0s",
		"glyphs":          GlyphSetDefault.String(),
		"theme":           "",
		"theme_path":      "",
		"workday_seconds": jira.DefaultWorkdaySeconds,
		"queries_path":    DefaultQueriesPath(),
		"editor":          "",
		"aliases":         map[string]string{},
	}
}

// Validate reports whether the resolved config can serve a request.
//
// It deliberately does NOT run at load time: `config current` must be
// able to show a half-configured state, and a command that needs no
// client (version, completion) must not fail because credentials are
// absent. The client constructor validates instead, at the point the
// values are actually needed.
func (c Config) Validate() error {
	// Whitespace is not a credential. A value of "   " is almost always a
	// quoting accident in a shell profile or CI variable, and accepting it
	// turns a config mistake into an authentication failure against Jira,
	// blamed on the token rather than on the blank that replaced it.
	var missing []string
	if strings.TrimSpace(c.BaseURL) == "" {
		missing = append(missing, "base_url")
	}
	if strings.TrimSpace(c.Email) == "" {
		missing = append(missing, "email")
	}
	if strings.TrimSpace(c.APIToken) == "" {
		missing = append(missing, "api_token")
	}
	if len(missing) > 0 {
		return fmt.Errorf("incomplete configuration: %s not set", strings.Join(missing, ", "))
	}
	return c.ValidateValues()
}

// ValidateValues checks the values that ARE set, without requiring any
// of them.
//
// This is the question a config FILE has to answer, and it is not the
// same question as Validate's. Credentials live in `jira auth`, so a
// file holding nothing but a preference is perfectly correct — but a
// base_url that is present and malformed is wrong the moment it is
// written, and saying so then beats surfacing it as an opaque request
// failure on the next command that needs the site.
func (c Config) ValidateValues() error {
	if err := validateCredentialID(c.CredentialID); err != nil {
		return err
	}
	switch {
	case c.BaseURL == "":
		// Absent. Nothing to check: the setting simply is not here.
		return nil
	case strings.TrimSpace(c.BaseURL) == "":
		// Present but blank — a quoting accident, not a choice. Saying so
		// now is the whole point: accepting it defers the complaint to
		// the next command, which reports "base_url not set" about a
		// line the user can plainly see is set.
		return fmt.Errorf("base_url is set to whitespace — remove it, or give it a value like %s", baseURLExample)
	}
	return validateBaseURL(c.BaseURL)
}

// ValidateBaseURL checks a site root, exported so an interactive prompt
// validates by the SAME rule the loader enforces. Two spellings of "is
// this a valid site" would eventually disagree, and the one that let a
// bad value through would be the one people met first.
func ValidateBaseURL(raw string) error { return validateBaseURL(raw) }

// baseURLExample is the shape the error messages teach. Kept as one
// constant so every message shows the same thing.
const baseURLExample = "https://your-site.atlassian.net"

// validateBaseURL parses the site root instead of prefix-matching it.
//
// A prefix test accepts "https://" with no host at all, which then fails
// much later as an opaque request error. Parsing also catches the case
// that matters more: userinfo. A valid URL can contain a password in a
// field nothing treats as secret, so configuration commands could print it.
//
// A bare site name is REJECTED rather than expanded. Turning "acme" into
// https://acme.atlassian.net would guess, and the guess can be wrong in
// the one direction that cannot be undone: a self-hosted instance
// reachable as a single-label host, or a typo, would send this user's
// email and API token to a stranger's tenant. The value is set once per
// machine, so an exact URL costs nothing — and the error below teaches
// the exact string, which is the ergonomics that shorthand was reaching
// for without the guess.
//
// NOTE: no error here quotes the raw URL. Echoing it is precisely how a
// password reaches a terminal, a log, or a bug report, and the user knows
// what they typed.
func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("base_url is not a valid URL — it should look like %s", baseURLExample)
	}
	if u.Scheme == "" || u.Hostname() == "" {
		return fmt.Errorf("base_url must be a full URL including the scheme, like %s", baseURLExample)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("base_url must use https (or http for a local instance), like %s", baseURLExample)
	}
	// http is for a LOCAL instance and nowhere else. The credential
	// travels as Basic auth on the first request, so plain http to a
	// remote host puts the token on the wire in cleartext — before
	// anything has verified the host is even yours. 0.15 restricted this
	// and the rule is worth keeping.
	if u.Scheme == "http" && !isLoopback(u.Hostname()) {
		return errors.New("base_url may only use http for a local instance (localhost, 127.0.0.1 or ::1) — a remote site must use https, or the API token crosses the network in cleartext")
	}
	if u.User != nil {
		return errors.New("base_url must not embed credentials — set email and api_token instead")
	}
	return nil
}

// isLoopback reports whether host addresses this machine.
//
// Twinned with adapters/jira.isLoopbackHost, which cannot import this
// package. Two layers, one rule: config decides what to accept, the
// transport decides what to send. Change one and look at the other.
func isLoopback(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	// Any 127.0.0.0/8 address, and the IPv6 loopback in other spellings.
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// Redacted returns the value to display for a key: the real value,
// except for a secret, which becomes a fixed marker.
//
// The marker is constant rather than length-preserving on purpose — a
// row of asterisks matching the token's length leaks its length, and an
// API token's length identifies its type.
func Redacted(key, value string) string {
	if IsSecret(key) && value != "" {
		return "(set)"
	}
	// A URL can carry a password in userinfo, so base_url is a secret
	// whenever it happens to hold one. Validate rejects such a URL, but
	// redaction cannot rely on that: a half-configured state is
	// deliberately inspectable BEFORE validation runs, which is exactly
	// when a bad value is still present to be printed.
	if key == "base_url" && value != "" {
		u, err := url.Parse(value)
		if err != nil {
			// Parsing errors may still contain credentials. Withhold the
			// whole malformed value instead of guessing its authority.
			return "(set)"
		}
		if u.User != nil {
			u.User = url.UserPassword("redacted", "redacted")
			return u.Redacted()
		}
	}
	return value
}

// Values returns every setting as a string keyed by config key.
//
// Keyed rather than reflected so the key names have exactly one
// definition — Keys(), Defaults() and this map are the same vocabulary,
// and a new setting that forgets one of them fails a guardrail rather
// than silently missing from `config list`.
//
// Values are RAW here. Redaction is the caller's decision at the point
// of display, because the same values feed the client, where the real
// token is required.
func (c Config) Values() map[string]string {
	aliases := c.Aliases
	if aliases == nil {
		aliases = map[string]string{}
	}
	// String maps always encode successfully. JSON exposes names and quoting
	// and escapes ASCII controls; human rendering sanitizes remaining controls.
	aliasJSON, _ := json.Marshal(aliases) //nolint:errcheck // string maps have no unsupported values or cycles
	return map[string]string{
		"base_url":        c.BaseURL,
		"email":           c.Email,
		"credential_id":   c.CredentialID,
		"api_token":       c.APIToken,
		"keyring":         formatBool(c.Keyring),
		"read_only":       formatBool(c.ReadOnly),
		"timeout":         c.Timeout.String(),
		"theme":           c.Theme,
		"theme_path":      c.ThemePath,
		"glyphs":          c.Glyphs.String(),
		"workday_seconds": strconv.Itoa(c.WorkdaySeconds),
		"queries_path":    c.QueriesPath,
		"editor":          c.Editor,
		"aliases":         string(aliasJSON),
	}
}

// formatBool renders a bool the way the config file spells it, so a
// value copied out of `config list` can be pasted back in.
func formatBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
