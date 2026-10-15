//go:build live

package live

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// Frozen by independent text extraction from the two admitted source trees.
// No production guide loader, renderer or invocation supplies expected content.
//
//go:embed testdata/parity-guide-oracles.json
var parityGuideOracles []byte

type parityGuideOracle struct {
	Slug        string            `json:"slug"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	When        string            `json:"when_to_use"`
	Commands    []string          `json:"commands"`
	Aliases     []string          `json:"aliases"`
	Order       int               `json:"order"`
	Runbook     string            `json:"runbook_sha256"`
	Sections    map[string]string `json:"sections"`
}

type parityGuideCorpus struct {
	Contract string              `json:"contract_version"`
	Index    string              `json:"index_sha256"`
	All      string              `json:"all_sha256"`
	AllHuman string              `json:"all_human_sha256"`
	Guides   []parityGuideOracle `json:"guides"`
}

func parityGuideDigest(value string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(value))) }

func parityGuideDocument(t *testing.T, result parityFixtureResult, lane, mode, operation, kind, slug, section string, corpus parityGuideCorpus, selected []parityGuideOracle) string {
	t.Helper()
	if lane == "before" || mode == "human" {
		return string(result.Stdout)
	}
	data := result.Stdout
	if mode == "json" {
		fields := parityAuthoringCleanData(t, data, lane, operation)
		var err error
		data, err = json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
	}
	document, valid := parityGuideTypedDocument(data, kind, slug, section, corpus, selected)
	if !valid {
		t.Fatal("guide lost its typed selection, metadata or document")
	}
	return document
}

func parityGuideTypedDocument(data []byte, kind, slug, section string, corpus parityGuideCorpus, selected []parityGuideOracle) (string, bool) {
	if !jsontext.Value(data).IsValid() {
		return "", false
	}
	var actual struct {
		Document *string `json:"document"`
	}
	if json.Unmarshal(data, &actual) != nil || actual.Document == nil {
		return "", false
	}
	expected := make([]map[string]any, 0, len(selected))
	for _, g := range selected {
		summary := map[string]any{"slug": g.Slug, "title": g.Title, "description": g.Description, "when_to_use": g.When, "commands": g.Commands, "order": g.Order}
		if len(g.Aliases) > 0 {
			summary["aliases"] = g.Aliases
		}
		expected = append(expected, summary)
	}
	want := map[string]any{"contract_version": corpus.Contract, "kind": kind, "document": *actual.Document, "guides": expected}
	if slug != "" {
		want["slug"] = slug
	}
	if section != "" {
		want["section"] = section
	}
	encoded, err := json.Marshal(want)
	return *actual.Document, err == nil && parityAuthoringJSON(data, encoded)
}

func parityLocalRefusal(stdout []byte, lane, operation string, exit int, code string) bool {
	if !jsontext.Value(stdout).IsValid() || exit != 3 || parityValidateFrame(stdout, lane, operation, exit) != nil {
		return false
	}
	var frame struct {
		Data     json.RawMessage
		Errors   []struct{ Code string }
		Warnings json.RawMessage
	}
	return json.Unmarshal(stdout, &frame) == nil && string(frame.Data) == "null" && len(frame.Errors) == 1 && frame.Errors[0].Code == code && parityAuthoringJSON(frame.Warnings, []byte(`[]`))
}

func TestParityActualBinaryGuides(t *testing.T) {
	var corpora map[string]parityGuideCorpus
	if json.Unmarshal(parityGuideOracles, &corpora) != nil || len(corpora["before"].Guides) != 9 || len(corpora["after"].Guides) != 11 {
		t.Fatal("invalid independently frozen guide corpus")
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			corpus := corpora[lane.name]
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusForbidden) }))
			defer server.Close()
			home := t.TempDir()
			config := filepath.Join(home, "public.toml")
			public := "theme='dark'\n"
			if lane.name == "before" {
				public = fmt.Sprintf("default_profile='fixture'\n[[profiles]]\nname='fixture'\nbase_url=%q\nemail='fixture@example.invalid'\nsecret_backend='env'\n", server.URL)
			}
			if err := os.WriteFile(config, []byte(public), 0o600); err != nil {
				t.Fatal(err)
			}
			preimage := parityLocalTree(t, home)
			for _, door := range []struct{ id, path, op string }{{"C009", "agent guide", "agent.guide"}, {"C052", "guide", "guide"}} {
				invoke := func(t *testing.T, mode string, args ...string) parityFixtureResult {
					t.Helper()
					if err := parityFingerprint(lane.binary, lane.digest); err != nil {
						t.Fatal(err)
					}
					argv := append(strings.Fields(door.path), args...)
					result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_READ_ONLY=true"}, argv...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					if calls.Load() != 0 || (result.Exit == 0 && len(result.Stderr) != 0) || !reflect.DeepEqual(parityLocalTree(t, home), preimage) {
						t.Fatalf("guide touched HTTP/files or emitted diagnostics: HTTP %d; stderr %s", calls.Load(), result.Stderr)
					}
					return result
				}
				for _, mode := range []string{"json", "compact", "human"} {
					check := func(t *testing.T, name, kind, slug, section, want string, selected []parityGuideOracle, args ...string) {
						t.Helper()
						r := invoke(t, mode, args...)
						if r.Exit != 0 {
							t.Fatalf("guide failed: exit %d; stdout %s", r.Exit, r.Stdout)
						}
						doc := parityGuideDocument(t, r, lane.name, mode, door.op, kind, slug, section, corpus, selected)
						if got := parityGuideDigest(doc); got != want {
							t.Fatalf("guide content mismatch: sha256 %s; want %s; stdout %s", got, want, r.Stdout)
						}
						id := door.id + ".guide." + name + "." + mode
						evidence.Checks[id] = append(evidence.Checks[id], lane.name)
					}
					t.Run(door.path+"/"+mode+"/index", func(t *testing.T) { check(t, "index", "index", "", "", corpus.Index, corpus.Guides) })
					t.Run(door.path+"/"+mode+"/all", func(t *testing.T) {
						want := corpus.All
						if lane.name == "after" && mode == "human" {
							want = corpus.AllHuman
						}
						check(t, "all", "all", "", "", want, corpus.Guides, "--all")
					})
					t.Run(door.path+"/"+mode+"/canonical-set", func(t *testing.T) {
						for _, g := range corpus.Guides {
							t.Run(g.Slug, func(t *testing.T) {
								r := invoke(t, mode, g.Slug)
								if r.Exit != 0 {
									t.Fatalf("guide failed: exit %d; stdout %s", r.Exit, r.Stdout)
								}
								doc := parityGuideDocument(t, r, lane.name, mode, door.op, "runbook", g.Slug, "", corpus, []parityGuideOracle{g})
								if parityGuideDigest(doc) != g.Runbook {
									t.Fatalf("canonical runbook differs from frozen source: %s", g.Slug)
								}
							})
						}
						if !t.Failed() {
							id := door.id + ".guide.canonical-set." + mode
							evidence.Checks[id] = append(evidence.Checks[id], lane.name)
						}
					})
					for _, g := range corpus.Guides {
						if g.Slug != "safe-mutation" {
							continue
						}
						for _, section := range []string{"Decide", "Run", "Save", "Preconditions", "Recover", "Next"} {
							t.Run(door.path+"/"+mode+"/section/"+section, func(t *testing.T) {
								check(t, "section."+section, "section", g.Slug, section, g.Sections[section], []parityGuideOracle{g}, g.Slug, "--section", strings.ToLower(section))
							})
						}
						for _, name := range []string{"SAFE-MUTATION", "safe_mutation", "safe"} {
							t.Run(door.path+"/"+mode+"/resolve/"+name, func(t *testing.T) {
								check(t, "resolve."+name, "runbook", g.Slug, "", g.Runbook, []parityGuideOracle{g}, name)
							})
						}
					}
					if lane.name == "after" {
						for _, g := range corpus.Guides {
							if g.Slug == "machine-output" {
								t.Run(door.path+"/"+mode+"/alias", func(t *testing.T) {
									check(t, "alias.output-contract", "runbook", g.Slug, "", g.Runbook, []parityGuideOracle{g}, "output-contract")
								})
							}
						}
					}
				}
				ambiguous := "issues"
				if lane.name == "after" {
					ambiguous = "configure"
				}
				for _, tc := range []struct {
					name          string
					args          []string
					before, after string
				}{
					{"unknown", []string{"no-such-parity-guide"}, "agent_topic_unknown", "arg_value_invalid"},
					{"ambiguous", []string{ambiguous}, "agent_topic_unknown", "arg_value_invalid"},
					{"section-unknown", []string{"safe-mutation", "--section", "Unknown"}, "agent_topic_unknown", "flag_value_invalid"},
					{"section-without-slug", []string{"--section", "Run"}, "validation_failed", "flag_not_applicable"},
					{"all-with-slug", []string{"safe-mutation", "--all"}, "validation_failed", "flag_conflict"},
					{"all-with-section", []string{"--all", "--section", "Run"}, "validation_failed", "validation_failed"},
				} {
					t.Run(door.path+"/refusal/"+tc.name, func(t *testing.T) {
						r := invoke(t, "json", tc.args...)
						want := tc.before
						if lane.name == "after" {
							want = tc.after
						}
						if !parityLocalRefusal(r.Stdout, lane.name, door.op, r.Exit, want) {
							t.Fatalf("guide refusal mismatch: want %s; stdout %s", want, r.Stdout)
						}
						id := door.id + ".guide.refusal." + tc.name
						evidence.Checks[id] = append(evidence.Checks[id], lane.name)
					})
				}
			}
		})
	}
	saveParityLocal(t, "guides", evidence)
}

func TestParityGuideSelectionRejectsUnexpectedMembers(t *testing.T) {
	const valid = `{"contract_version":"1","kind":"index","document":"independent fixture","guides":[]}`
	corpus := parityGuideCorpus{Contract: "1"}
	if _, ok := parityGuideTypedDocument([]byte(valid), "index", "", "", corpus, nil); !ok {
		t.Fatal("independent valid guide refused")
	}
	for _, member := range []string{`"slug":null`, `"slug":""`, `"section":null`, `"section":""`, `"foreign":true`} {
		t.Run(member, func(t *testing.T) {
			changed := strings.TrimSuffix(valid, "}") + "," + member + "}"
			if _, ok := parityGuideTypedDocument([]byte(changed), "index", "", "", corpus, nil); ok {
				t.Fatal("unexpected typed selection member earned guide credit")
			}
		})
	}
}

func TestParityGuideRefusalRejectsDuplicateEnvelopeMembers(t *testing.T) {
	const valid = `{"ok":false,"meta":{"command":"guide","contract_version":"1","timestamp":"2026-01-01T00:00:00Z","request_id":"independent-fixture"},"data":null,"errors":[{"code":"arg_value_invalid"}],"warnings":[]}`
	if !parityLocalRefusal([]byte(valid), "after", "guide", 3, "arg_value_invalid") {
		t.Fatal("independent refusal fixture refused")
	}
	for _, tc := range []struct{ name, original, replacement string }{
		{"root", `"data":null`, `"data":{},"data":null`},
		{"nested", `"code":"arg_value_invalid"`, `"code":"foreign","code":"arg_value_invalid"`},
		{"escaped", `"warnings":[]`, `"warnings":[],"\u0077arnings":[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if parityLocalRefusal([]byte(strings.Replace(valid, tc.original, tc.replacement, 1)), "after", "guide", 3, "arg_value_invalid") {
				t.Fatal("duplicate refusal JSON earned guide credit")
			}
		})
	}
}
