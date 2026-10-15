//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	xos "github.com/gechr/x/os"
	koanftoml "github.com/knadh/koanf/parsers/toml"
)

type parityLocalEvidence struct {
	Checks      map[string][]string     `json:"checks"`
	Invocations []parityLocalInvocation `json:"invocations"`
}

type parityLocalInvocation struct {
	Lane         string          `json:"lane"`
	Digest       string          `json:"sha256"`
	Argv         []string        `json:"argv"`
	Exit         int             `json:"exit"`
	Stdout       json.RawMessage `json:"stdout,omitempty"`
	OutputBytes  []byte          `json:"output_bytes,omitempty"`
	StderrBytes  []byte          `json:"stderr_bytes"`
	InputBytes   []byte          `json:"input_bytes,omitempty"`
	PublicConfig []byte          `json:"public_config_bytes"`
}

// Optional receipts are synthetic and local. Never reuse the credential-backed
// live owner merely to test commands that must work without credentials.
func saveParityLocal(t *testing.T, name string, evidence parityLocalEvidence) {
	t.Helper()
	base := os.Getenv("JIRA_PARITY_LOCAL_EVIDENCE_DIR")
	if base == "" {
		return
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(evidence, "", "  ")
	if err == nil {
		err = xos.AtomicWrite(filepath.Join(base, name+".json"), data, 0o600)
	}
	if err != nil {
		t.Fatal("save local paired evidence: ", err)
	}
}

func parityAliasFile(t *testing.T, path, lane, site string, want map[string]string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	var stored struct {
		Aliases        map[string]string `json:"aliases"`
		DefaultProfile string            `json:"default_profile"`
		Profiles       []struct {
			Name          string `json:"name"`
			BaseURL       string `json:"base_url"`
			Email         string `json:"email"`
			SecretBackend string `json:"secret_backend"`
		} `json:"profiles"`
	}
	if err != nil {
		t.Fatal("read owned alias settings: ", err)
	}
	// Decode literal public fields without invoking configuration resolution,
	// profile selection or aliases' production save/load owners.
	settings, err := koanftoml.Parser().Unmarshal(data)
	if err != nil {
		t.Fatal("decode owned public TOML: ", err)
	}
	projection, err := json.Marshal(settings)
	if err != nil || json.Unmarshal(projection, &stored) != nil || !maps.Equal(stored.Aliases, want) || strings.Contains(string(data), "synthetic-fixture-token") {
		t.Fatal("independent local alias state differs from the authored literal map")
	}
	if lane == "after" {
		if !parityAliasCurrentPreferences(data) {
			t.Fatal("local alias command changed unrelated authored preferences")
		}
	} else if lane != "before" || stored.DefaultProfile != "fixture" || len(stored.Profiles) != 1 || stored.Profiles[0].Name != "fixture" || stored.Profiles[0].BaseURL != site || stored.Profiles[0].Email != "fixture@example.invalid" || stored.Profiles[0].SecretBackend != "env" {
		t.Fatal("local alias command changed the owned legacy public binding")
	}
	return data
}

func parityAliasCurrentPreferences(data []byte) bool {
	settings, err := koanftoml.Parser().Unmarshal(data)
	if err != nil {
		return false
	}
	delete(settings, "aliases")
	return reflect.DeepEqual(settings, map[string]any{"theme": "dark", "timeout": "2m"})
}

func parityAliasImportReceipt(stdout []byte, lane, path, mode string) bool {
	if (lane != "before" && lane != "after") || (mode != "preview" && mode != "merge" && mode != "clobber") {
		return false
	}
	var frame struct {
		Data struct {
			Path     string
			Imported *int
			Aliases  []string
			Skipped  map[string]string
			DryRun   *bool `json:"dry_run"`
			Changed  *bool `json:"changed"`
		}
	}
	accepted := []string{"own.reference"}
	reason := "name already taken"
	if lane == "after" {
		reason = "name already exists; use --clobber to replace it"
	}
	skipped := map[string]string{"keep": reason}
	if mode == "clobber" {
		accepted = []string{"keep", "own.reference"}
		skipped = map[string]string{}
	}
	if json.Unmarshal(stdout, &frame) != nil || frame.Data.Imported == nil || *frame.Data.Imported != len(accepted) || !slices.Equal(frame.Data.Aliases, accepted) || frame.Data.Skipped == nil || !maps.Equal(frame.Data.Skipped, skipped) || frame.Data.DryRun == nil || *frame.Data.DryRun != (mode == "preview") {
		return false
	}
	return lane == "before" || (lane == "after" && frame.Data.Path == path && frame.Data.Changed != nil && *frame.Data.Changed != (mode == "preview"))
}

func TestParityActualBinaryAliasLifecycle(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			if err := parityFingerprint(lane.binary, lane.digest); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusForbidden)
			}))
			t.Cleanup(server.Close)
			home := t.TempDir()
			path := filepath.Join(home, "alias-settings.toml")
			initial := "# owned public fixture\ntheme='dark'\ntimeout='2m'\n[aliases]\nkeep='version'\n"
			if lane.name == "before" {
				initial = fmt.Sprintf("# owned public fixture\ndefault_profile='fixture'\n[aliases]\nkeep='version'\n[[profiles]]\nname='fixture'\nbase_url=%q\nemail='fixture@example.invalid'\nsecret_backend='env'\n", server.URL)
			}
			if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"keep": "version"}
			invoke := func(operation string, args ...string) (liveFrame, []byte, int) {
				t.Helper()
				if err := parityFingerprint(lane.binary, lane.digest); err != nil {
					t.Fatal(err)
				}
				result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: path}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_KEYRING=false", "JIRA_READ_ONLY=true"}, args...)
				stdout, exit := result.Stdout, result.Exit
				var frame liveFrame
				if json.Unmarshal(stdout, &frame) != nil {
					t.Fatalf("local alias returned no frame: exit %d; stdout %s; stderr %s", result.Exit, result.Stdout, result.Stderr)
				}
				configBytes, readErr := os.ReadFile(path)
				evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: exit, Stdout: slices.Clone(stdout), StderrBytes: result.Stderr, PublicConfig: configBytes})
				saveParityLocal(t, "aliases", evidence)
				if readErr != nil {
					t.Fatal("read owned public configuration after local invocation: ", readErr)
				}
				if calls.Load() != 0 || parityValidateFrame(stdout, lane.name, operation, exit) != nil {
					t.Fatalf("local alias invocation contacted Jira or lost its envelope: %s", stdout)
				}
				if lane.name == "after" && frame.OK && strings.HasPrefix(operation, "alias.") {
					var result struct{ Data struct{ Path string } }
					if json.Unmarshal(stdout, &result) != nil || result.Data.Path != path {
						t.Fatal("alias receipt did not bind the owned settings path")
					}
				}
				return frame, stdout, exit
			}
			check := func(name string) {
				t.Helper()
				if !slices.Contains(evidence.Checks[name], lane.name) {
					evidence.Checks[name] = append(evidence.Checks[name], lane.name)
				}
				saveParityLocal(t, "aliases", evidence)
				t.Logf("local paired check %s: %s", name, lane.name)
			}
			unchanged := func(before []byte) {
				t.Helper()
				if string(parityAliasFile(t, path, lane.name, server.URL, want)) != string(before) {
					t.Fatal("local alias preview/refusal changed the exact file preimage")
				}
			}
			set := func(preview bool) {
				t.Helper()
				args := []string{"alias", "set", "own.quick", " version "}
				if preview {
					args = append(args, "--dry-run")
				}
				before := parityAliasFile(t, path, lane.name, server.URL, want)
				frame, stdout, exit := invoke("alias.set", args...)
				var result struct {
					Data struct {
						Name, Expansion string
						DryRun          *bool `json:"dry_run"`
						Changed         *bool `json:"changed"`
					}
				}
				if json.Unmarshal(stdout, &result) != nil || exit != 0 || !frame.OK || result.Data.Name != "own.quick" || result.Data.Expansion != " version " || result.Data.DryRun == nil || *result.Data.DryRun != preview || (lane.name == "after" && (result.Data.Changed == nil || *result.Data.Changed != !preview)) {
					t.Fatalf("alias set lost its literal candidate or local effect: %s", stdout)
				}
				if preview {
					unchanged(before)
				} else {
					want["own.quick"] = " version "
					parityAliasFile(t, path, lane.name, server.URL, want)
				}
				check(fmt.Sprintf("C015.set.preview=%t", preview))
			}
			set(true)
			set(false)
			for _, verb := range []string{"list", "ls"} {
				frame, stdout, exit := invoke("alias.list", "alias", verb)
				var result struct {
					Data struct {
						Aliases map[string]string
						Count   *int
					}
				}
				if json.Unmarshal(stdout, &result) != nil || exit != 0 || !frame.OK || result.Data.Count == nil || *result.Data.Count != len(want) || !maps.Equal(result.Data.Aliases, want) {
					t.Fatal("alias list differs from independently observed local state")
				}
				check("C014." + verb + ".local")
			}
			frame, stdout, exit := invoke("version", "own.quick")
			var version struct{ Data struct{ Version string } }
			expected := "v0.15.2"
			if lane.name == "after" {
				expected = "v0.16.0-dev"
			}
			if json.Unmarshal(stdout, &version) != nil || exit != 0 || !frame.OK || version.Data.Version != expected {
				t.Fatal("stored alias did not execute the selected binary's version command")
			}
			check("C015.dispatch.local")
			input := filepath.Join(home, "aliases.yaml")
			if err := os.WriteFile(input, []byte("keep: 'jql reference'\nown.reference: 'jql reference'\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"preview", "merge", "clobber"} {
				before := parityAliasFile(t, path, lane.name, server.URL, want)
				args := []string{"alias", "import", input}
				if mode == "preview" {
					args = append(args, "--dry-run")
				}
				if mode == "clobber" {
					args = append(args, "--clobber")
				}
				frame, stdout, exit = invoke("alias.import", args...)
				if exit != 0 || !frame.OK || !parityAliasImportReceipt(stdout, lane.name, path, mode) {
					t.Fatalf("alias import lost accepted/skipped names or preview state: %s", stdout)
				}
				if mode == "preview" {
					unchanged(before)
				} else {
					want["own.reference"] = "jql reference"
					if mode == "clobber" {
						want["keep"] = "jql reference"
					}
					parityAliasFile(t, path, lane.name, server.URL, want)
				}
				check("C013." + mode + ".local")
			}
			before := parityAliasFile(t, path, lane.name, server.URL, want)
			frame, stdout, exit = invoke("alias.delete", "alias", "delete", "own.quick")
			code := "flag_required"
			if lane.name == "before" {
				code = "validation_failed"
			}
			var refusal struct{ Errors []struct{ Message string } }
			if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != code || json.Unmarshal(stdout, &refusal) != nil || len(refusal.Errors) != 1 || !strings.Contains(refusal.Errors[0].Message, "alias delete requires --force") {
				t.Fatal("headless alias delete did not require consent")
			}
			unchanged(before)
			check("C012.headless_refusal.local")
			deleteAlias := func(verb string, preview, existed bool) {
				t.Helper()
				args := []string{"alias", verb, "own.quick"}
				if preview {
					args = append(args, "--dry-run")
				} else {
					args = append(args, "--force")
				}
				before = parityAliasFile(t, path, lane.name, server.URL, want)
				frame, stdout, exit = invoke("alias.delete", args...)
				var result struct {
					Data struct {
						Name    string
						Deleted *bool
						DryRun  *bool `json:"dry_run"`
						Changed *bool `json:"changed"`
					}
				}
				if json.Unmarshal(stdout, &result) != nil || exit != 0 || !frame.OK || result.Data.Name != "own.quick" || result.Data.Deleted == nil || *result.Data.Deleted != existed || result.Data.DryRun == nil || *result.Data.DryRun != preview || (lane.name == "after" && (result.Data.Changed == nil || *result.Data.Changed != (!preview && existed))) {
					t.Fatalf("alias delete lost its existence, preview or acknowledged effect: %s", stdout)
				}
				if preview {
					unchanged(before)
				} else {
					delete(want, "own.quick")
					parityAliasFile(t, path, lane.name, server.URL, want)
				}
			}
			deleteAlias("delete", true, true)
			check("C012.delete.preview.local")
			for _, verb := range []string{"delete", "del", "rm"} {
				if _, exists := want["own.quick"]; !exists {
					set(false)
				}
				deleteAlias(verb, false, true)
				check("C012." + verb + ".live.local")
			}
			deleteAlias("delete", false, false)
			check("C012.missing.local")
		})
	}
}

func TestParityAliasReceiptRejectsFalseEffects(t *testing.T) {
	const good = `{"data":{"path":"owned.toml","imported":2,"aliases":["keep","own.reference"],"skipped":{},"dry_run":false,"changed":true}}`
	if !parityAliasImportReceipt([]byte(good), "after", "owned.toml", "clobber") {
		t.Fatal("independent complete import receipt refused")
	}
	for name, replacement := range map[string][2]string{
		"foreign_path":   {`"path":"owned.toml"`, `"path":"other.toml"`},
		"false_change":   {`"changed":true`, `"changed":false`},
		"missing_change": {`,"changed":true`, ``},
		"missing_skips":  {`"skipped":{},`, ``},
		"null_skips":     {`"skipped":{}`, `"skipped":null`},
		"invented_skip":  {`"skipped":{}`, `"skipped":{"keep":"not changed"}`},
	} {
		t.Run(name, func(t *testing.T) {
			changed := strings.Replace(good, replacement[0], replacement[1], 1)
			if changed == good || parityAliasImportReceipt([]byte(changed), "after", "owned.toml", "clobber") {
				t.Fatal("false import effect or skipped outcome admitted")
			}
		})
	}
	const settings = "theme='dark'\ntimeout='2m'\n[aliases]\nkeep='version'\n"
	if !parityAliasCurrentPreferences([]byte(settings)) {
		t.Fatal("authored unrelated settings refused")
	}
	for _, extra := range []string{"base_url='https://fixture.example.invalid'\n", "email='fixture@example.invalid'\n", "read_only=true\n", "secret_backend='env'\n"} {
		if parityAliasCurrentPreferences([]byte(extra + settings)) {
			t.Fatal("environment-only preferences were persisted")
		}
	}
}
