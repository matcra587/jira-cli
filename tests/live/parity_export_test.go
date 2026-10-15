//go:build live

package live

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// Independently extracted from the admitted raw guide files, without invoking
// the production loader, renderer, export planner or selected binaries.
//
//go:embed testdata/parity-export-oracles.json
var parityExportOracles []byte

type parityExportOracle struct {
	Format string `json:"format"`
	Slug   string `json:"slug"`
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

func parityExportReceipt(data []byte, directory, scope, harness string, dry, attempted bool, attemptedPath string, written []string, artifacts []parityExportOracle) bool {
	if len(artifacts) == 0 {
		return false
	}
	rows := make([]map[string]any, 0, len(artifacts))
	for _, a := range artifacts {
		rows = append(rows, map[string]any{"slug": a.Slug, "path": a.Path, "bytes": a.Bytes, "sha256": a.SHA256, "written": slices.Contains(written, a.Path)})
	}
	want := map[string]any{"contract_version": "1", "format": artifacts[0].Format, "directory": directory, "dry_run": dry, "attempted": attempted, "written_paths": written, "artifacts": rows}
	if scope != "" {
		want["scope"] = scope
	}
	if harness != "" {
		want["harness"] = harness
	}
	if attemptedPath != "" {
		want["attempted_path"] = attemptedPath
	}
	encoded, err := json.Marshal(want)
	return err == nil && parityAuthoringJSON(data, encoded)
}

func parityExportResult(t *testing.T, result parityFixtureResult, lane, mode, directory, scope, harness string, detected, dry bool, artifacts []parityExportOracle) {
	t.Helper()
	var paths []string
	for _, a := range artifacts {
		paths = append(paths, a.Path)
	}
	if result.Exit != 0 || len(result.Stderr) != 0 {
		t.Fatalf("export failed: exit %d; stdout %s; stderr %s", result.Exit, result.Stdout, result.Stderr)
	}
	if lane == "before" {
		want := ""
		if scope != "" {
			display := directory
			if scope == "project" {
				display = ".agents/skills"
				if harness == "claude-code" {
					display = ".claude/skills"
				}
			}
			source := "selected"
			if detected {
				source = "detected"
			}
			want = "# " + source + " " + harness + "; exporting to " + filepath.FromSlash(display) + "\n"
		}
		want += strings.Join(paths, "\n") + "\n"
		if string(result.Stdout) != want {
			t.Fatalf("legacy export lost ordered written paths: %s", result.Stdout)
		}
		return
	}
	if mode == "human" {
		want := "# " + artifacts[0].Format + " " + harness + " " + directory + "\n"
		state := "written"
		if dry {
			state = "would write"
		}
		for _, path := range paths {
			want += state + " " + path + "\n"
		}
		if string(result.Stdout) != want {
			t.Fatalf("current export human receipt changed: %s", result.Stdout)
		}
		return
	}
	data := result.Stdout
	if mode == "json" {
		var err error
		data, err = json.Marshal(parityAuthoringCleanData(t, data, lane, "agent.export"))
		if err != nil {
			t.Fatal(err)
		}
	}
	written := paths
	if dry {
		written = []string{}
	}
	if !parityExportReceipt(data, directory, scope, harness, dry, !dry, "", written, artifacts) {
		t.Fatalf("export receipt differs from independent artifacts/effects: %s", result.Stdout)
	}
}

func parityExportFiles(t *testing.T, directory string, artifacts []parityExportOracle) {
	t.Helper()
	tree := parityLocalTree(t, directory)
	want := make(map[string]bool)
	want["."] = true
	for _, a := range artifacts {
		path := filepath.FromSlash(a.Path)
		want[path] = true
		want[filepath.Dir(path)] = true
		info, err := os.Lstat(filepath.Join(directory, path))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatal("export artifact is absent or not an ordinary file")
		}
		data, err := os.ReadFile(filepath.Join(directory, path))
		if err != nil || len(data) != a.Bytes || parityGuideDigest(string(data)) != a.SHA256 {
			t.Fatalf("export bytes differ from independently pinned source: %s", a.Path)
		}
	}
	if len(tree) != len(want) {
		t.Fatal("export left missing or extra directory members")
	}
	for path := range tree {
		if !want[path] {
			t.Fatalf("unexpected exported member %s", path)
		}
	}
	t.Run("posix-modes", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX modes do not establish Windows ACLs")
		}
		for path, file := range tree {
			if file.Mode.IsRegular() && file.Mode.Perm() != 0o600 {
				t.Fatalf("export artifact has unexpected mode: %s", path)
			}
		}
	})
}

func parityExportCollision(t *testing.T, result parityFixtureResult, directory string, artifacts []parityExportOracle) {
	t.Helper()
	data := parityAuthoringData(t, result.Stdout, "after", "agent.export", result.Exit)
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var frame struct {
		Errors   []struct{ Code string }
		Warnings json.RawMessage
	}
	if json.Unmarshal(result.Stdout, &frame) != nil || result.Exit != 3 || len(frame.Errors) != 1 || frame.Errors[0].Code != "flag_conflict" || !parityAuthoringJSON(frame.Warnings, []byte(`[]`)) || !parityExportReceipt(encoded, directory, "", "", false, true, artifacts[0].Path, []string{}, artifacts) {
		t.Fatalf("existing artifact refusal lost its exact receipt: %s", result.Stdout)
	}
}

func TestParityActualBinaryGuideExports(t *testing.T) {
	var expected map[string][]parityExportOracle
	if json.Unmarshal(parityExportOracles, &expected) != nil || len(expected["before"]) != 18 || len(expected["after"]) != 22 {
		t.Fatal("invalid independent export fixtures")
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusForbidden) }))
			defer server.Close()
			owned := func(t *testing.T) (string, string, string) {
				t.Helper()
				home := t.TempDir()
				project := filepath.Join(home, "project space")
				if err := os.Mkdir(project, 0o700); err != nil {
					t.Fatal(err)
				}
				config := filepath.Join(home, "public.toml")
				public := "theme='dark'\n"
				if lane.name == "before" {
					public = fmt.Sprintf("default_profile='fixture'\n[[profiles]]\nname='fixture'\nbase_url=%q\nemail='fixture@example.invalid'\nsecret_backend='env'\n", server.URL)
				}
				if err := os.WriteFile(config, []byte(public), 0o600); err != nil {
					t.Fatal(err)
				}
				return home, project, config
			}
			invoke := func(t *testing.T, home, project, config, mode string, extra []string, args ...string) parityFixtureResult {
				t.Helper()
				if err := parityFingerprint(lane.binary, lane.digest); err != nil {
					t.Fatal(err)
				}
				extra = append([]string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_READ_ONLY=true"}, extra...)
				result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode, WorkingDirectory: project}, extra, append([]string{"agent", "export"}, args...)...)
				evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
				if calls.Load() != 0 {
					t.Fatal("local guide export contacted Jira")
				}
				return result
			}
			record := func(name string) {
				id := "C007.export." + name
				evidence.Checks[id] = append(evidence.Checks[id], lane.name)
			}
			artifacts := func(format string) []parityExportOracle {
				var selected []parityExportOracle
				for _, a := range expected[lane.name] {
					if a.Format == format {
						selected = append(selected, a)
					}
				}
				return selected
			}
			type recipe struct{ name, format, scope, harness, detected string }
			var recipes []recipe
			for _, format := range []string{"agent-skill", "claude-skill"} {
				recipes = append(recipes, recipe{name: "explicit." + format, format: format})
			}
			for _, harness := range []string{"codex", "claude-code"} {
				for _, scope := range []string{"project", "user"} {
					recipes = append(recipes, recipe{name: scope + "." + harness, scope: scope, harness: harness})
				}
				override := "agent-skill"
				if harness == "codex" {
					override = "claude-skill"
				}
				recipes = append(recipes, recipe{name: "override." + harness, scope: "project", harness: harness, format: override})
				recipes = append(recipes, recipe{name: "detected." + harness, scope: "project", detected: harness})
			}
			for _, recipe := range recipes {
				for _, mode := range []string{"json", "compact", "human"} {
					t.Run(recipe.name+"/"+mode, func(t *testing.T) {
						home, project, config := owned(t)
						before := parityLocalTree(t, home)
						directory := filepath.Join(home, "export with spaces")
						var args []string
						extra := []string{}
						format, harness := recipe.format, recipe.harness
						if recipe.scope == "" {
							args = []string{"--dir", directory, "--format", format}
						} else {
							args = []string{"--scope", recipe.scope}
							if harness != "" {
								args = append(args, "--harness", harness)
								extra = []string{"CLAUDECODE=1", "CODEX=1"}
							} else {
								harness = recipe.detected
								if harness == "codex" {
									extra = []string{"CODEX=1"}
								} else {
									extra = []string{"CLAUDECODE=1"}
								}
							}
							if format != "" {
								args = append(args, "--format", format)
							} else {
								format = "agent-skill"
								if harness == "claude-code" {
									format = "claude-skill"
								}
							}
							base := project
							if recipe.scope == "user" {
								base = home
							}
							leaf := ".agents"
							if harness == "claude-code" {
								leaf = ".claude"
							}
							directory = filepath.Join(base, leaf, "skills")
						}
						result := invoke(t, home, project, config, mode, extra, args...)
						selected := artifacts(format)
						parityExportResult(t, result, lane.name, mode, directory, recipe.scope, harness, recipe.detected != "", false, selected)
						parityExportFiles(t, directory, selected)
						after := parityLocalTree(t, home)
						for path, file := range before {
							if after[path] != file {
								t.Fatalf("export changed unrelated owned member: %s", path)
							}
						}
						allowed := make(map[string]bool)
						relative, err := filepath.Rel(home, directory)
						if err != nil || !filepath.IsLocal(relative) {
							t.Fatal("export destination escaped the owned home")
						}
						for path := relative; path != "."; path = filepath.Dir(path) {
							allowed[path] = true
						}
						for _, artifact := range selected {
							path := filepath.Join(relative, filepath.FromSlash(artifact.Path))
							allowed[path], allowed[filepath.Dir(path)] = true, true
						}
						for path := range after {
							if _, existed := before[path]; !existed && !allowed[path] {
								t.Fatalf("export published an unrelated owned member: %s", path)
							}
						}
						record(recipe.name + "." + mode)
					})
				}
			}
			for _, format := range []string{"agent-skill", "claude-skill"} {
				t.Run("collision/"+format, func(t *testing.T) {
					home, project, config := owned(t)
					directory := filepath.Join(home, "collision")
					selected := artifacts(format)
					first := filepath.Join(directory, filepath.FromSlash(selected[0].Path))
					if err := os.MkdirAll(filepath.Dir(first), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(first, []byte("owned preimage"), 0o600); err != nil {
						t.Fatal(err)
					}
					before := parityLocalTree(t, home)
					args := []string{"--dir", directory, "--format", format}
					result := invoke(t, home, project, config, "json", nil, args...)
					if lane.name == "before" {
						parityExportResult(t, result, lane.name, "json", directory, "", "", false, false, selected)
						parityExportFiles(t, directory, selected)
					} else {
						parityExportCollision(t, result, directory, selected)
						if !reflect.DeepEqual(parityLocalTree(t, home), before) {
							t.Fatalf("existing artifact refusal lost its exact unchanged state/receipt: %s", result.Stdout)
						}
						record("collision." + format)
						result = invoke(t, home, project, config, "json", nil, append(args, "--force")...)
						parityExportResult(t, result, lane.name, "json", directory, "", "", false, false, selected)
						parityExportFiles(t, directory, selected)
						record("force." + format)
						return
					}
					record("collision." + format)
				})
				if lane.name == "after" {
					for _, mode := range []string{"json", "compact", "human"} {
						t.Run("preview/"+format+"/"+mode, func(t *testing.T) {
							home, project, config := owned(t)
							directory := filepath.Join(home, "regular file")
							if err := os.WriteFile(directory, []byte("owned file target"), 0o600); err != nil {
								t.Fatal(err)
							}
							before := parityLocalTree(t, home)
							result := invoke(t, home, project, config, mode, nil, "--dir", directory, "--format", format, "--dry-run")
							parityExportResult(t, result, lane.name, mode, directory, "", "", false, true, artifacts(format))
							if !reflect.DeepEqual(parityLocalTree(t, home), before) {
								t.Fatal("export preview changed its destination or owned workspace")
							}
							record("preview." + format + "." + mode)
						})
					}
				}
			}
			t.Run("hard-link-target", func(t *testing.T) {
				home, project, config := owned(t)
				directory := filepath.Join(home, "hard-link export")
				selected := artifacts("agent-skill")
				first := filepath.Join(directory, filepath.FromSlash(selected[0].Path))
				target := filepath.Join(home, "owned target.md")
				if err := os.MkdirAll(filepath.Dir(first), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte("owned linked preimage"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(target, first); err != nil {
					if errors.Is(err, errors.ErrUnsupported) {
						t.Skip("filesystem does not support the owned hard-link fixture: ", err)
					}
					t.Fatal("create owned hard-link fixture: ", err)
				}
				before := parityLocalTree(t, home)
				args := []string{"--dir", directory, "--format", "agent-skill"}
				result := invoke(t, home, project, config, "json", nil, args...)
				if lane.name == "after" {
					parityExportCollision(t, result, directory, selected)
					if !reflect.DeepEqual(parityLocalTree(t, home), before) {
						t.Fatal("hard-link collision changed its owned preimage")
					}
					result = invoke(t, home, project, config, "json", nil, append(args, "--force")...)
				}
				parityExportResult(t, result, lane.name, "json", directory, "", "", false, false, selected)
				parityExportFiles(t, directory, selected)
				observed, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				if lane.name == "before" {
					if parityGuideDigest(string(observed)) != selected[0].SHA256 {
						t.Fatal("legacy overwrite did not reproduce its linked-target effect")
					}
				} else if string(observed) != "owned linked preimage" {
					t.Fatal("explicit replacement changed a linked target outside its artifact")
				}
				record("hard-link-target")
			})
			for _, tc := range []struct {
				name          string
				args          []string
				before, after string
			}{
				{"missing-destination", nil, "validation_failed", "flag_required"},
				{"missing-format", []string{"--dir", "OWNED"}, "validation_failed", "flag_required"},
				{"invalid-format", []string{"--dir", "OWNED", "--format", "unknown"}, "agent_topic_unknown", "flag_value_invalid"},
				{"invalid-scope", []string{"--scope", "unknown", "--harness", "codex"}, "validation_failed", "flag_value_invalid"},
				{"invalid-harness", []string{"--scope", "project", "--harness", "unknown"}, "validation_failed", "flag_value_invalid"},
				{"harness-without-scope", []string{"--dir", "OWNED", "--format", "agent-skill", "--harness", "codex"}, "validation_failed", "flag_not_applicable"},
				{"conflicting-destination", []string{"--dir", "OWNED", "--scope", "project", "--format", "agent-skill", "--harness", "codex"}, "validation_failed", "validation_failed"},
				{"empty-destination", []string{"--dir=", "--format", "agent-skill"}, "validation_failed", "flag_required"},
				{"undetected-harness", []string{"--scope", "project"}, "validation_failed", "flag_value_invalid"},
			} {
				t.Run("refusal/"+tc.name, func(t *testing.T) {
					home, project, config := owned(t)
					before := parityLocalTree(t, home)
					args := slices.Clone(tc.args)
					for i, arg := range args {
						if arg == "OWNED" {
							args[i] = filepath.Join(home, "refused export")
						}
					}
					result := invoke(t, home, project, config, "json", nil, args...)
					want := tc.before
					if lane.name == "after" {
						want = tc.after
					}
					if !parityLocalRefusal(result.Stdout, lane.name, "agent.export", result.Exit, want) || !reflect.DeepEqual(parityLocalTree(t, home), before) {
						t.Fatalf("export refusal lost classification or changed owned workspace: want %s; exit %d; stdout %s; stderr %s", want, result.Exit, result.Stdout, result.Stderr)
					}
					record("refusal." + tc.name)
				})
			}
		})
	}
	saveParityLocal(t, "exports", evidence)
}

func TestParityExportReceiptRejectsFalsePublication(t *testing.T) {
	const digest = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	artifacts := []parityExportOracle{{Format: "agent-skill", Slug: "one", Path: "jira-one/SKILL.md", Bytes: 3, SHA256: digest}}
	const valid = `{"contract_version":"1","format":"agent-skill","directory":"/owned","dry_run":false,"attempted":true,"written_paths":["jira-one/SKILL.md"],"artifacts":[{"slug":"one","path":"jira-one/SKILL.md","bytes":3,"sha256":"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad","written":true}]}`
	check := func(data string) bool {
		return parityExportReceipt([]byte(data), "/owned", "", "", false, true, "", []string{"jira-one/SKILL.md"}, artifacts)
	}
	if !check(valid) {
		t.Fatal("independent valid publication receipt refused")
	}
	for _, tc := range []struct{ name, original, replacement string }{
		{"foreign-path", `"path":"jira-one/SKILL.md"`, `"path":"jira-other/SKILL.md"`},
		{"false-digest", digest, strings.Repeat("0", 64)},
		{"false-bytes", `"bytes":3`, `"bytes":4`},
		{"null-written-paths", `"written_paths":["jira-one/SKILL.md"]`, `"written_paths":null`},
		{"null-attempted", `"attempted":true`, `"attempted":null`},
		{"missing-dry-run", `"dry_run":false,`, ``},
		{"unexpected-scope", `"format":"agent-skill"`, `"format":"agent-skill","scope":null`},
		{"false-written", `"written":true`, `"written":false`},
		{"duplicate", `"attempted":true`, `"attempted":false,"attempted":true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if check(strings.Replace(valid, tc.original, tc.replacement, 1)) {
				t.Fatal("false publication earned export credit")
			}
		})
	}
}
