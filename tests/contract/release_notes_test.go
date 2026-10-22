package contract

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	changelog "github.com/matcra587/jira-cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestReleaseNotesBinarySelectionModesAndRawMarkdown(t *testing.T) {
	t.Parallel()
	for _, selection := range []struct {
		name     string
		args     []string
		expected []changelog.Release
		markdown string
	}{
		{name: "history", args: []string{"release-notes"}, expected: changelog.Releases(), markdown: changelog.Full()},
		{name: "latest", args: []string{"release-notes", "--latest"}, expected: changelog.Releases()[:1], markdown: changelog.Releases()[0].Markdown},
		{name: "alias version", args: []string{"rn", "v0.7.7"}, expected: oneRelease(t, "0.7.7"), markdown: oneRelease(t, "0.7.7")[0].Markdown},
		{name: "trimmed version", args: []string{"release-notes", " \tv0.7.7\n"}, expected: oneRelease(t, "0.7.7"), markdown: oneRelease(t, "0.7.7")[0].Markdown},
	} {
		t.Run(selection.name, func(t *testing.T) {
			for _, mode := range []string{"json", "compact", "human"} {
				args := append(append([]string{}, selection.args...), "--output="+mode, "--no-input", "--no-pager")
				stdout, stderr, exit := runBinary(t, args...)
				if exit != 0 || stderr != "" {
					t.Fatalf("release notes %s failed: exit%d stderr%q stdout%q", mode, exit, stderr, stdout)
				}
				if mode == "human" {
					if stdout != strings.TrimSuffix(selection.markdown, "\n")+"\n" {
						t.Fatal("raw human Markdown differs from embedded artifact")
					}
					continue
				}
				data := json.RawMessage(stdout)
				if mode == "json" {
					var result frame
					if json.Unmarshal([]byte(stdout), &result) != nil || !result.OK || result.Meta.Command != "release.notes" {
						t.Fatal("release notes lost typed frame")
					}
					data = result.Data
				}
				var got envelope.ReleaseNotesOutput
				if err := json.Unmarshal(data, &got); err != nil {
					t.Fatal(err)
				}
				wantJSON, err := json.Marshal(envelope.ReleaseNotesOutput{Releases: selection.expected})
				if err != nil {
					t.Fatal(err)
				}
				var want envelope.ReleaseNotesOutput
				if err := json.Unmarshal(wantJSON, &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) || strings.Contains(string(data), `"markdown"`) {
					t.Fatal("machine release notes differ from selected structured catalog")
				}
			}
		})
	}
}

func oneRelease(t *testing.T, query string) []changelog.Release {
	t.Helper()
	release, ok := changelog.Find(query)
	if !ok {
		t.Fatalf("missing known release%s", query)
	}
	return []changelog.Release{release}
}

func TestReleaseNotesBinaryRefusesInvalidSelectionsAndRecovers(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"release-notes", "missing"}, {"release-notes", "0.7.7", "--latest"}, {"release-notes", "0.7.7", "extra"}} {
		for _, mode := range []string{"json", "compact"} {
			stdout, _, exit := runBinary(t, append(append([]string{}, args...), "--output="+mode)...)
			var result struct {
				OK     bool                                          `json:"ok"`
				Errors []struct{ Code, Field, Message, Hint string } `json:"errors"`
			}
			if exit != 3 || json.Unmarshal([]byte(stdout), &result) != nil || result.OK || len(result.Errors) != 1 || result.Errors[0].Hint == "" {
				t.Fatalf("selection error unframed: %d %q", exit, stdout)
			}
			wantCode := "arg_value_invalid"
			if args[len(args)-1] == "extra" {
				wantCode = "arg_count_invalid"
			}
			if result.Errors[0].Code != wantCode {
				t.Fatalf("wrong selection code: %q", result.Errors[0].Code)
			}
		}
	}
	stdout, _, exit := runBinary(t, "release-notes", "--latest", "--output=json")
	if exit != 0 || !strings.Contains(stdout, `"tag":"v0.16.0-beta.0"`) {
		t.Fatal("documented recovery command did not select newest release")
	}
}

func TestReleaseNotesBinaryDiscoveryHelpCompletionAndJQ(t *testing.T) {
	t.Parallel()
	stdout, _, exit := runBinary(t, "release-notes", "--latest", "--output=json", "--jq=.data.releases[0].version")
	if exit != 0 || stdout != "0.16.0-beta.0\n" {
		t.Fatalf("release projection failed: %d %q", exit, stdout)
	}
	help, _, exit := runBinary(t, "rn", "--help")
	if exit != 0 || !strings.Contains(help, "--no-pager") || !strings.Contains(help, "JIRA_PAGER") || !strings.Contains(help, "--latest") {
		t.Fatal("release alias help missing selection or paging policy")
	}
	stdout, _, exit = runBinary(t, "--@complete=release", "--", "release-notes")
	if exit != 0 || !strings.Contains(stdout, "0.16.0-beta.0\t2026-10-21") || !strings.Contains(stdout, "0.15.2\t2026-07-28") || !strings.Contains(stdout, "0.1.0\t") {
		t.Fatalf("release completion missing actual embedded versions: %d %q", exit, stdout)
	}
}
