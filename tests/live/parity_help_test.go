//go:build live

package live

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

func parityHelpTarget(raw []byte, path string) bool {
	line := regexp.MustCompile(`(?m)^Usage:?[\t ]*\r?\n(?:[\t ]*\r?\n)*([\t ]+[^\r\n]+)`).FindSubmatch(raw)
	if len(line) != 2 || bytes.ContainsAny(raw, "\x00\x1b") {
		return false
	}
	usage := strings.TrimSpace(string(line[1]))
	if usage == path {
		return true
	}
	rest, ok := strings.CutPrefix(usage, path+" ")
	return ok && (strings.HasPrefix(rest, "[") || strings.HasPrefix(rest, "<"))
}

// Explicit help is new in this layout. Exercise its selected target rather
// than crediting a child's -h discovery as execution of the help command.
func TestParityActualBinaryHelpTargets(t *testing.T) {
	binary, digest := os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")
	if binary == "" {
		t.Skip("requires selected actual current binary")
	}
	reference, err := os.ReadFile("testdata/parity-schema-after.txt")
	if err != nil {
		t.Fatalf("read schema reference: %v", err)
	}
	type target struct{ canonical, spelling string }
	var targets []target
	for _, path := range strings.Split(strings.TrimSpace(string(reference)), "\n") {
		if path == "jira config" || strings.HasPrefix(path, "jira config ") {
			continue
		}
		targets = append(targets, target{path, path})
	}
	if len(targets) != 118 {
		t.Fatal("explicit non-config help census changed; review the independent inventory")
	}
	targets = append(targets, []target{
		{"jira alias delete", "jira alias del"},
		{"jira alias delete", "jira alias rm"},
		{"jira alias list", "jira alias ls"},
		{"jira release-notes", "jira rn"},
		{"jira update", "jira up"},
		{"jira api delete", "jira api DELETE"},
		{"jira api get", "jira api GET"},
		{"jira api head", "jira api HEAD"},
		{"jira api options", "jira api OPTIONS"},
		{"jira api patch", "jira api PATCH"},
		{"jira api post", "jira api POST"},
		{"jira api put", "jira api PUT"},
	}...)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusForbidden) }))
	defer server.Close()
	home := t.TempDir()
	config := parityCachePublicConfig(t, home, "after", server.URL)
	before := parityLocalTree(t, home)
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	run := func(t *testing.T, mode string, args ...string) parityFixtureResult {
		t.Helper()
		if err := parityFingerprint(binary, digest); err != nil {
			t.Fatal(err)
		}
		result := parityFixtureProcessAt(t, "after", binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_KEYRING=false", "JIRA_READ_ONLY=true"}, args...)
		evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: "after", Digest: digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
		if calls.Load() != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
			t.Fatal("local help entered HTTP or changed its owned workspace")
		}
		if err := parityFingerprint(binary, digest); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, selected := range targets {
		for _, mode := range []string{"json", "compact", "human"} {
			t.Run(selected.spelling+"/"+mode, func(t *testing.T) {
				path := strings.Fields(selected.spelling)[1:]
				result := run(t, mode, append([]string{"help"}, path...)...)
				reference := run(t, mode, append(path, "-h")...)
				if result.Exit != 0 || reference.Exit != 0 || len(result.Stderr) != 0 || len(reference.Stderr) != 0 || !bytes.Equal(result.Stdout, reference.Stdout) || !parityHelpTarget(result.Stdout, selected.canonical) {
					t.Fatalf("explicit help did not render the exact selected short-help target: exit %d reference %d stdout %s stderr %s", result.Exit, reference.Exit, result.Stdout, result.Stderr)
				}
				evidence.Checks["C090.targets."+strings.ReplaceAll(selected.spelling, " ", ".")+"."+mode] = []string{"after"}
			})
		}
	}
	// Cobra's delivered help keeps the nearest existing parent for an unknown
	// topic. Bind that limitation explicitly instead of calling it strict refusal.
	for _, selected := range []struct{ name, path, parent string }{
		{"unknown-root", "__missing__", "jira"}, {"unknown-child", "issue __missing__", "jira issue"}, {"unknown-leaf-child", "issue edit __missing__", "jira issue edit"},
	} {
		for _, mode := range []string{"json", "compact", "human"} {
			t.Run(selected.name+"/"+mode, func(t *testing.T) {
				result := run(t, mode, append([]string{"help"}, strings.Fields(selected.path)...)...)
				reference := run(t, mode, append(strings.Fields(selected.parent)[1:], "-h")...)
				if result.Exit != 0 || len(result.Stderr) != 0 || reference.Exit != 0 || len(reference.Stderr) != 0 || !bytes.Equal(result.Stdout, reference.Stdout) || !parityHelpTarget(result.Stdout, selected.parent) {
					t.Fatal("delivered unknown-topic parent fallback changed")
				}
				evidence.Checks["C090.fallback."+selected.name+"."+mode] = []string{"after"}
			})
		}
	}
	saveParityLocal(t, "help", evidence)
}

func TestParityHelpTargetGuards(t *testing.T) {
	const valid = "Usage\n\n  jira issue edit [options] <ISSUE_KEY>…\n\n    Change fields.\n"
	for _, tc := range []struct {
		name, text string
		valid      bool
	}{
		{"complete", valid, true},
		{"wrong-command", strings.Replace(valid, "jira issue edit", "jira issue create", 1), false},
		{"child-command", strings.Replace(valid, "jira issue edit", "jira issue edit prepare", 1), false},
		{"prefix-lookalike", strings.Replace(valid, "jira issue edit", "jira issue editor", 1), false},
		{"example-only", "Usage\n\n  jira issue [options]\n\nExamples\n\n  jira issue edit [options]\n", false},
		{"control", valid + "\x1b[31m", false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if parityHelpTarget([]byte(tc.text), "jira issue edit") != tc.valid {
				t.Fatalf("help target guard changed for %s", tc.name)
			}
		})
	}
}
