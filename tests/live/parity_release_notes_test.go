//go:build live

package live

import (
	"encoding/json"
	"encoding/json/jsontext"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

type parityReleaseNotesFixture struct {
	Header          string
	Releases        []map[string]json.RawMessage
	CurrentReleases []map[string]json.RawMessage `json:"current_releases"`
}

func parityReleaseNotesFixtures(t *testing.T) parityReleaseNotesFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/parity-release-notes-oracles.json")
	var fixture parityReleaseNotesFixture
	if err != nil || !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &fixture) != nil || fixture.Header != "# Release Notes" || len(fixture.Releases) != 61 || len(fixture.CurrentReleases) != 1 {
		t.Fatal("independent release-note source fixtures unavailable")
	}
	var latest string
	if json.Unmarshal(fixture.CurrentReleases[0]["version"], &latest) != nil || latest != "0.16.0-beta.0" {
		t.Fatal("current release-note source fixture has an unexpected version")
	}
	return fixture
}

func parityReleaseNotesExpected(t *testing.T, fixture parityReleaseNotesFixture, version string) ([]byte, string) {
	t.Helper()
	releases := []map[string]json.RawMessage{}
	documents := []string{}
	for _, release := range fixture.Releases {
		var identity, markdown string
		if json.Unmarshal(release["version"], &identity) != nil || json.Unmarshal(release["markdown"], &markdown) != nil {
			t.Fatal("invalid release-note source identity or Markdown")
		}
		if version != "" && identity != version {
			continue
		}
		row := map[string]json.RawMessage{}
		for key, value := range release {
			if key != "markdown" {
				row[key] = value
			}
		}
		releases = append(releases, row)
		documents = append(documents, markdown)
	}
	if len(releases) == 0 || version != "" && len(releases) != 1 {
		t.Fatal("release selection has no unique independent source fixture")
	}
	markdown := strings.Join(documents, "\n\n")
	if version == "" {
		markdown = fixture.Header + "\n\n" + markdown + "\n"
	}
	expected, err := json.Marshal(map[string]any{"releases": releases})
	if err != nil {
		t.Fatal(err)
	}
	return expected, markdown
}

func TestParityActualBinaryReleaseNotes(t *testing.T) {
	fixtures := parityReleaseNotesFixtures(t)
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			fixture := fixtures
			if lane.name == "after" {
				current := append([]map[string]json.RawMessage{}, fixtures.CurrentReleases...)
				fixture.Releases = append(current, fixtures.Releases...)
			}
			var latest string
			if json.Unmarshal(fixture.Releases[0]["version"], &latest) != nil || latest == "" {
				t.Fatal("release-note source fixture has no latest version")
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				http.Error(w, "release notes entered HTTP", 500)
			}))
			defer server.Close()
			for _, selection := range []struct {
				name, version string
				args          []string
			}{
				{"history", "", []string{"release-notes"}},
				{"latest", latest, []string{"release-notes", "--latest"}},
				{"version", latest, []string{"release-notes", latest}},
				{"prefixed-version", "0.7.7", []string{"release-notes", "v0.7.7"}},
				{"alias", "0.7.7", []string{"rn", "0.7.7"}},
				{"trimmed-version", "0.7.7", []string{"release-notes", " \tv0.7.7\n"}},
			} {
				for _, mode := range []string{"json", "compact", "human"} {
					t.Run(selection.name+"/"+mode, func(t *testing.T) {
						home := t.TempDir()
						config := parityCachePublicConfig(t, home, lane.name, server.URL)
						before := parityLocalTree(t, home)
						if err := parityFingerprint(lane.binary, lane.digest); err != nil {
							t.Fatal(err)
						}
						args := append(append([]string{}, selection.args...), "--no-pager")
						result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_KEYRING=false", "JIRA_PAGER=" + filepath.Join(home, "unavailable-pager"), "PAGER="}, args...)
						evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
						if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load() != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
							t.Fatalf("local release notes failed or entered effects: exit %d; HTTP %d; stdout %s; stderr %s", result.Exit, calls.Load(), result.Stdout, result.Stderr)
						}
						expected, markdown := parityReleaseNotesExpected(t, fixture, selection.version)
						if mode == "human" {
							if lane.name == "after" {
								markdown = strings.TrimSuffix(markdown, "\n") + "\n"
							}
							if string(result.Stdout) != markdown {
								t.Fatalf("release-note human bytes differ from independent source: %q", result.Stdout)
							}
						} else {
							data := result.Stdout
							if mode == "json" {
								var err error
								data, err = json.Marshal(parityAuthoringCleanData(t, data, lane.name, "release.notes"))
								if err != nil {
									t.Fatal(err)
								}
							}
							if !jsontext.Value(result.Stdout).IsValid() || !parityAuthoringJSON(data, expected) {
								t.Fatalf("release-note catalog differs from independent source: %s", result.Stdout)
							}
						}
						check := "C062.notes." + selection.name + "." + mode
						evidence.Checks[check] = append(evidence.Checks[check], lane.name)
					})
				}
			}
			for _, refusal := range []struct {
				name string
				args []string
			}{{"conflict", []string{"release-notes", "0.7.7", "--latest"}}, {"unknown", []string{"rn", "999.999.999"}}} {
				for _, mode := range []string{"json", "compact"} {
					t.Run(refusal.name+"/"+mode, func(t *testing.T) {
						home := t.TempDir()
						config := parityCachePublicConfig(t, home, lane.name, server.URL)
						before := parityLocalTree(t, home)
						if err := parityFingerprint(lane.binary, lane.digest); err != nil {
							t.Fatal(err)
						}
						result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_KEYRING=false"}, refusal.args...)
						evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
						if calls.Load() != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) || !parityReleaseNotesRefusal(result.Stdout, lane.name, refusal.name, fixture, result.Exit) {
							t.Fatalf("release-note refusal changed local effect/classification: exit %d; stdout %s; stderr %s", result.Exit, result.Stdout, result.Stderr)
						}
						for _, mutation := range []struct{ name, original, replacement string }{
							{"wrong-command", `"command":"release-notes"`, `"command":"release.notes"`},
							{"wrong-reason", `pass a version or --latest, not both`, `unrelated refusal`},
						} {
							changed := strings.Replace(string(result.Stdout), mutation.original, mutation.replacement, 1)
							if changed == string(result.Stdout) {
								continue
							}
							t.Run(mutation.name, func(t *testing.T) {
								if parityReleaseNotesRefusal([]byte(changed), lane.name, refusal.name, fixture, result.Exit) {
									t.Fatal("changed release-note failure identity/reason earned acceptance")
								}
							})
						}
						check := "C062.notes.refusal." + refusal.name + "." + mode
						evidence.Checks[check] = append(evidence.Checks[check], lane.name)
					})
				}
			}
		})
	}
	saveParityLocal(t, "release-notes", evidence)
}

// Root failures retain the canonical command path in both lanes; successful
// release rendering uses the release.notes operation. Do not rename envelopes.
func parityReleaseNotesRefusal(stdout []byte, lane, refusal string, fixture parityReleaseNotesFixture, exit int) bool {
	code := "validation_failed"
	if lane == "after" {
		code = "arg_value_invalid"
	}
	if !parityLocalRefusal(stdout, lane, "release-notes", exit, code) {
		return false
	}
	var frame struct{ Errors []map[string]json.RawMessage }
	if json.Unmarshal(stdout, &frame) != nil {
		return false
	}
	var message string
	if json.Unmarshal(frame.Errors[0]["message"], &message) != nil {
		return false
	}
	expected := ""
	switch refusal {
	case "conflict":
		expected = "pass a version or --latest, not both"
		if lane == "before" {
			expected = "validation: " + expected
		}
	case "unknown":
		versions := []string{}
		for _, release := range fixture.Releases {
			var version string
			if json.Unmarshal(release["version"], &version) != nil || version == "" {
				return false
			}
			versions = append(versions, version)
		}
		expected = `no release notes for "999.999.999"; available: ` + strings.Join(versions, ", ")
	default:
		return false
	}
	if lane == "after" {
		var field string
		if json.Unmarshal(frame.Errors[0]["field"], &field) != nil || field != "version" {
			return false
		}
	}
	return message == expected
}
