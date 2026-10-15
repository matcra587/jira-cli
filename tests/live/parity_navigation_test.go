//go:build live

package live

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

type parityLocalFile struct {
	Mode fs.FileMode
	Hash [sha256.Size]byte
}

// The directory is an owned synthetic home, never the user's settings or stores.
func parityLocalTree(t *testing.T, root string) map[string]parityLocalFile {
	t.Helper()
	files := make(map[string]parityLocalFile)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		file := parityLocalFile{Mode: info.Mode()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			file.Hash = sha256.Sum256(data)
		}
		files[relative] = file
		return nil
	})
	if err != nil {
		t.Fatal("inspect owned synthetic workspace: ", err)
	}
	return files
}

func parityNavigationHelp(stdout []byte, path, children string) bool {
	help := string(stdout)
	usage := regexp.MustCompile(`(?m)^Usage:?[\t ]*\r?\n(?:[\t ]*\r?\n)*([\t ]+[^\r\n]+)`).FindStringSubmatch(help)
	if len(usage) != 2 || !regexp.MustCompile(`^[\t ]+jira `+regexp.QuoteMeta(path)+`(?:[\t ]+(?:<|\[)|[\t ]*$)`).MatchString(usage[1]) {
		return false
	}
	for _, child := range strings.Fields(children) {
		if !regexp.MustCompile(`(?m)^[\t ]+` + regexp.QuoteMeta(child) + `(?:[\t ]{2,}|$)`).MatchString(help) {
			return false
		}
	}
	return true
}

// These are nonrunnable navigation groups. Their real operation is local help;
// no help invocation here supplies acceptance for a runnable child command.
func TestParityActualBinaryNavigation(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusForbidden)
			}))
			defer server.Close()
			home := t.TempDir()
			config := filepath.Join(home, "public-settings.toml")
			public := "theme='dark'\n"
			if lane.name == "before" {
				public = fmt.Sprintf("default_profile='fixture'\n[[profiles]]\nname='fixture'\nbase_url=%q\nemail='fixture@example.invalid'\nsecret_backend='env'\n", server.URL)
			}
			if err := os.WriteFile(config, []byte(public), 0o600); err != nil {
				t.Fatal(err)
			}
			before := parityLocalTree(t, home)
			for _, group := range []struct{ id, path, before, after string }{
				{"C002", "adf", "convert render", "convert edit explain-node lint render validate"},
				{"C005", "agent", "adf-matrix export fieldtypes guide schema", "adf-matrix export fieldtypes guide schema"},
				{"C011", "alias", "delete import list set", "delete import list set"},
				{"C016", "auth", "login logout migrate refresh status switch token whoami", "login logout status token whoami"},
				{"C025", "boards", "list", "list"},
				{"C027", "cache", "boards clear epics fields issuekeys issuetypes labels linktypes priorities projects refresh resolutions statuses", "boards clear epics fields inspect issuekeys issuetypes labels linktypes priorities projects refresh resolutions statuses"},
				{"C047", "epic", "add board list remove", "add board list remove"},
				{"C053", "issue", "attachment clone comment create delete edit link list mine move rank transition unwatch view watch watchers weblink", "attachment clone comment create delete edit input link list mine move rank transition view watchers weblink"},
				{"C054", "issue attachment", "add delete download list", "add delete download list"},
				{"C055", "issue watchers", "add list remove", "add list remove"},
				{"C056", "jql", "build reference validate", "build reference validate"},
				{"C068", "user", "search", "search"},
				{"C071", "worklog", "add list", "add list"},
				{"C063", "search", "jql saved", ""},
				{"C074", "adf edit", "", "apply merge prepare"},
				{"C081", "api", "", "delete get head options patch post put"},
				{"C091", "issue comment", "", "add delete edit list"},
				{"C092", "issue input", "", "prepare"},
				{"C093", "issue link", "", "create delete list types"},
				{"C094", "issue weblink", "", "add delete list"},
			} {
				if lane.name == "before" && group.before == "" {
					continue
				}
				path, children := group.path, group.before
				if lane.name == "after" {
					children = group.after
					if group.id == "C063" {
						path = "issue list"
					}
				}
				for _, mode := range []string{"json", "compact", "human"} {
					for _, spelling := range []string{"bare", "long-help", "short-help"} {
						t.Run(group.path+"/"+mode+"/"+spelling, func(t *testing.T) {
							args := strings.Fields(path)
							switch spelling {
							case "long-help":
								args = append(args, "--help")
							case "short-help":
								args = append(args, "-h")
							case "bare":
								if group.id == "C063" && lane.name == "after" {
									args = append(args, "--help")
								}
							}
							if err := parityFingerprint(lane.binary, lane.digest); err != nil {
								t.Fatal(err)
							}
							result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_READ_ONLY=true"}, args...)
							evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
							if result.Exit != 0 || calls.Load() != 0 || len(result.Stderr) != 0 || !parityNavigationHelp(result.Stdout, path, children) {
								t.Fatalf("navigation failed: exit %d; HTTP %d; stdout %s; stderr %s", result.Exit, calls.Load(), result.Stdout, result.Stderr)
							}
							if !reflect.DeepEqual(parityLocalTree(t, home), before) {
								t.Fatal("local navigation changed its owned public workspace")
							}
							name := group.id + ".navigation." + mode + "." + spelling
							evidence.Checks[name] = append(evidence.Checks[name], lane.name)
						})
					}
				}
			}
		})
	}
	saveParityLocal(t, "navigation", evidence)
}

func TestParityNavigationOracleRequiresSelectedPathAndChildren(t *testing.T) {
	const valid = "Usage\n\n  jira adf [command]\n\nCommands\n\n  convert  Convert Markdown\n  render   Render ADF\n"
	if !parityNavigationHelp([]byte(valid), "adf", "convert render") {
		t.Fatal("independent navigation fixture refused")
	}
	for _, tc := range []struct{ name, wrong string }{
		{"wrong path", strings.Replace(valid, "jira adf", "jira issue", 1)},
		{"missing child", strings.Replace(valid, "  render   Render ADF\n", "", 1)},
		{"description only", "Usage\n\n  jira adf [command]\nconvert and render are described here\n"},
		{"wrong prefix", strings.Replace(valid, "jira adf", "jira adf-other", 1)},
		{"example only", strings.Replace(valid, "jira adf", "jira issue", 1) + "\nExamples\n\n  jira adf [command]\n"},
		{"child path", strings.Replace(valid, "jira adf", "jira adf convert", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if parityNavigationHelp([]byte(tc.wrong), "adf", "convert render") {
				t.Fatal("navigation oracle accepted another path or missing command rows")
			}
		})
	}
}
