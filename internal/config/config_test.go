package config_test

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
)

// TestValidateValuesRejectsABlankBaseURL pins the difference between
// absent and blank. Accepting "   " defers the complaint to the next
// command, which then reports "base_url not set" about a line the user
// can plainly see is set.
func TestValidateValuesRejectsABlankBaseURL(t *testing.T) {
	t.Parallel()
	if err := (config.Config{BaseURL: "   "}).ValidateValues(); err == nil {
		t.Error("a whitespace base_url was accepted")
	}
	if err := (config.Config{}).ValidateValues(); err != nil {
		t.Errorf("an absent base_url is not an error, got %v", err)
	}
}

// TestHTTPIsOnlyForLocalInstances pins the rule that keeps an API token
// off the wire in cleartext.
//
// The credential travels as Basic auth on the very first request, so a
// plain-http remote site leaks it before anything has verified the host
// is even yours. 0.15 restricted this; the rewrite dropped the
// restriction until a review caught it.
func TestHTTPIsOnlyForLocalInstances(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		url  string
		ok   bool
	}{
		{name: "https anywhere", url: "https://example.atlassian.net", ok: true},
		{name: "http to localhost", url: "http://localhost:8080", ok: true},
		{name: "http to 127.0.0.1", url: "http://127.0.0.1:2990", ok: true},
		{name: "http to another 127/8 address", url: "http://127.0.0.53", ok: true},
		{name: "http to ipv6 loopback", url: "http://[::1]:8080", ok: true},
		{name: "http to a remote host", url: "http://example.atlassian.net", ok: false},
		{name: "http to a bare ip", url: "http://203.0.113.10", ok: false},
		{name: "http to a lookalike host", url: "http://localhost.example.com", ok: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := config.ValidateBaseURL(tt.url)
			if tt.ok && err != nil {
				t.Errorf("%s was rejected: %v", tt.url, err)
			}
			if !tt.ok && err == nil {
				t.Errorf("%s was accepted; the token would cross the network in cleartext", tt.url)
			}
		})
	}
}

// TestGlyphSetUnmarshalText pins the vocabulary GlyphSet accepts and
// rejects, and that an accepted value round-trips through String() —
// config edit reads back what it wrote, so String() must produce
// exactly what UnmarshalText would accept a second time.
func TestGlyphSetUnmarshalText(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		text  string
		want  config.GlyphSet
		valid bool
	}{
		{name: "default", text: "default", want: config.GlyphSetDefault, valid: true},
		{name: "empty string means default", text: "", want: config.GlyphSetDefault, valid: true},
		{name: "nerd", text: "nerd", want: config.GlyphSetNerd, valid: true},
		{name: "unknown value", text: "chunky", valid: false},
		// Case and surrounding space are folded: a config file is
		// hand-written, and "Nerd" means what "nerd" means. The value is
		// a closed vocabulary, not an identifier whose case carries
		// meaning, so refusing these would be a rule with no purpose.
		{name: "mixed case", text: "Nerd", want: config.GlyphSetNerd, valid: true},
		{name: "upper case", text: "DEFAULT", want: config.GlyphSetDefault, valid: true},
		{name: "surrounding space", text: "  nerd  ", want: config.GlyphSetNerd, valid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var g config.GlyphSet
			err := g.UnmarshalText([]byte(tt.text))
			if !tt.valid {
				if err == nil {
					t.Fatalf("UnmarshalText(%q) = nil error, want a rejection", tt.text)
				}
				return
			}
			if err != nil {
				t.Fatalf("UnmarshalText(%q) = %v, want it accepted", tt.text, err)
			}
			if g != tt.want {
				t.Errorf("UnmarshalText(%q) = %v, want %v", tt.text, g, tt.want)
			}
		})
	}
}

// TestGlyphSetStringRoundTrips pins that every named constant's String()
// form is itself accepted by UnmarshalText — the round-trip config edit
// depends on (read the file, decode, later re-encode via config list).
func TestGlyphSetStringRoundTrips(t *testing.T) {
	t.Parallel()
	for _, want := range []config.GlyphSet{config.GlyphSetDefault, config.GlyphSetNerd} {
		var got config.GlyphSet
		if err := got.UnmarshalText([]byte(want.String())); err != nil {
			t.Fatalf("UnmarshalText(%q) = %v, want it accepted", want.String(), err)
		}
		if got != want {
			t.Errorf("round-trip of %v through %q produced %v", want, want.String(), got)
		}
	}
}

// TestEveryKeyHasAValue pins that Keys() and Values() cannot drift.
//
// `keyring` was listed as a key and missing from the map, so it
// rendered as an empty cell in `config list` — indistinguishable from a
// setting that is genuinely unset, for a bool that always has a value.
// A key nobody can see the value of is worse than one that does not
// exist, because the listing implies it was answered.
func TestEveryKeyHasAValue(t *testing.T) {
	t.Parallel()
	values := config.Config{}.Values()
	for _, key := range config.Keys() {
		if _, ok := values[key]; !ok {
			t.Errorf("key %q has no entry in Values(); it renders blank in config list", key)
		}
	}
	if len(values) != len(config.Keys()) {
		t.Errorf("Values() has %d entries for %d keys — one is not listed", len(values), len(config.Keys()))
	}
}
