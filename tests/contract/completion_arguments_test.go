package contract

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gechr/x/shell"
	xslices "github.com/gechr/x/slices"
)

func TestCompletionShellAndEmptyPredictorsUseRealBinary(t *testing.T) {
	want := "bash\nelvish\nfish\nnu\npwsh\nzsh\n"
	for _, shell := range []string{"bash", "elvish", "fish", "nu", "pwsh", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			stdout, stderr, exit := runBinary(t, "--@shell="+shell, "--@complete=shell")
			if exit != 0 || stderr != "" || stdout != want {
				t.Fatalf("shell predictor exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			stdout, stderr, exit = runBinary(t, "--@shell="+shell, "--@complete=none", "--", "KEY-1")
			if exit != 0 || stderr != "" || stdout != "" {
				t.Fatalf("empty predictor exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
		})
	}
}

func completionArgumentReplies(t *testing.T, script string, env, words []string) []string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("Bash is unavailable")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "completion.bash")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	encoded := xslices.Map(words, shell.Quote)
	driver := "source " + shell.Quote(path) + " || exit 40\n__test_quoted=false\ncompopt() { [[ \"$*\" != *noquote* ]] || __test_quoted=true; return 0; }\nCOMP_WORDS=(" + strings.Join(encoded, " ") + ")\nCOMP_CWORD=" + strconv.Itoa(len(words)-1) + "\n_jira jira " + shell.Quote(words[len(words)-1]) + " " + shell.Quote(words[len(words)-2]) + " || exit 41\nprintf '%s\\0' \"$__test_quoted\" \"${COMPREPLY[@]}\"\n"
	env = append(slices.Clone(env), "PATH="+filepath.Dir(jiraBinary(t))+string(os.PathListSeparator)+os.Getenv("PATH"))
	stdout, stderr, exit := runExecutableIOInDirStarted(t, bash, dir, env, nil, nil, nil, "--noprofile", "--norc", "-c", driver)
	if exit != 0 || stderr != "" {
		t.Fatalf("generated Bash completion exit=%d stderr=%q", exit, stderr)
	}
	quoted, stdout, ok := strings.Cut(stdout, "\x00")
	if !ok {
		t.Fatal("completion driver omitted quoting metadata")
	}
	stdout = strings.TrimSuffix(stdout, "\x00")
	if stdout == "" {
		return nil
	}
	values := strings.Split(stdout, "\x00")
	if quoted == "true" {
		// Current-word replies carry shell quoting and explicitly disable
		// Readline's filename quoting. Decode their literal spelling here;
		// actual editor insertion is verified independently by the PTY guard.
		for i, value := range values {
			parts, err := shell.Split(value)
			if err != nil || len(parts) != 1 {
				t.Fatal("current-word reply is not one literal shell word", value, err)
			}
			values[i] = parts[0]
		}
	}
	return values
}

func TestCompletionIssueKeysStopBeforeCommentAndWeblinkIDs(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issue/DEMO-1" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","key":"DEMO-1","fields":{"summary":"Fixture"}}`))
	}))
	t.Cleanup(server.Close)
	env := append(searchEnv(server.URL), "XDG_CACHE_HOME="+t.TempDir(), "JIRA_NO_UPDATE_CHECK=1")
	stdout, stderr, exit := runBinaryEnv(t, env, "issue", "view", "DEMO-1", "--output=compact")
	if exit != 0 {
		t.Fatalf("seed history exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	env = append(env, "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null")
	script, stderr, exit := runBinaryEnv(t, env, "completion", "bash")
	if exit != 0 || stderr != "" {
		t.Fatalf("generate Bash exit=%d stderr=%q", exit, stderr)
	}
	for _, command := range [][]string{{"issue", "comment", "edit"}, {"issue", "comment", "delete"}, {"issue", "weblink", "delete"}} {
		t.Run(strings.Join(command, "_"), func(t *testing.T) {
			words := append([]string{"jira"}, command...)
			values := completionArgumentReplies(t, script, env, append(slices.Clone(words), "DEMO"))
			if !slices.Equal(values, []string{"DEMO-1"}) {
				t.Fatalf("first issue-key slot=%q, want populated history", values)
			}
			values = completionArgumentReplies(t, script, env, append(slices.Clone(words), "DEMO-1", "DEMO"))
			if len(values) != 0 {
				t.Fatalf("ID slot reuses issue-key suggestions: %q", values)
			}
		})
	}
	if requests.Load() != 1 {
		t.Fatalf("offline completion made requests: got %d, want only the history seed", requests.Load())
	}
}

func TestCompletionSortChoicesUseGeneratedBash(t *testing.T) {
	script, stderr, exit := runBinary(t, "completion", "bash")
	if exit != 0 || stderr != "" {
		t.Fatalf("generate Bash exit=%d stderr=%q", exit, stderr)
	}
	for _, tc := range []struct{ words, want []string }{
		{words: []string{"jira", "issue", "comment", "list", "KEY-1", "--order", ""}, want: []string{"newest", "oldest"}},
		{words: []string{"jira", "issue", "list", "--order-by", ""}, want: []string{"created", "updated", "priority", "status", "key", "summary", "assignee"}},
	} {
		t.Run(strings.Join(tc.words, "_"), func(t *testing.T) {
			values := completionArgumentReplies(t, script, nil, tc.words)
			if !slices.Equal(values, tc.want) {
				t.Fatalf("sort candidates=%q, want %q", values, tc.want)
			}
		})
	}
}
