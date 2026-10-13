package contract

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/adapters"
	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
)

func prepareBinaryMetadataCompletion(t *testing.T) ([]string, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	disk, err := adapters.NewDiskCache()
	if err != nil {
		t.Fatal(err)
	}
	store, err := adapters.NewScopedCache(disk, "https://example.com", "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		resource, items string
	}{
		{resource: "projects", items: `[{"id":"1","key":"DEMO","name":"Example project"}]`},
		{resource: "issuetypes", items: `[{"id":"10001","name":"Task"}]`},
		{resource: "fields", items: `[{"id":"customfield_10001","name":"Native field"}]`},
	} {
		var items []json.RawMessage
		if err := json.Unmarshal([]byte(tc.items), &items); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(struct {
			Version int `json:"version"`
			cachecmd.MetadataValue
		}{Version: 1, MetadataValue: cachecmd.MetadataValue{Items: items, Count: len(items), Pages: 1, Complete: true}})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, resource := range cachecmd.MetadataResources() {
			if resource.Name == tc.resource {
				found = true
				if err := store.Set(resource.Key("https://example.com"), raw); err != nil {
					t.Fatal(err)
				}
			}
		}
		if !found {
			t.Fatalf("missing producer %s", tc.resource)
		}
	}
	board := []byte(`{"boards":[{"id":42,"name":"Example board","type":null,"project_keys":[],"projects_complete":true}],"fetched_at":` + strconv.Quote(time.Now().UTC().Format(time.RFC3339Nano)) + `,"pages_fetched":1,"complete":true,"truncated":false,"project_failures":[]}`)
	if err := store.Set(cachecmd.BoardListKey, board); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "selected.toml")
	if err := os.WriteFile(path, []byte("base_url = 'https://example.com'\nemail = 'person@example.com'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{"XDG_CACHE_HOME=" + dir, "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_READ_ONLY=true", "JIRA_NO_UPDATE_CHECK=1"}, path
}

func TestMetadataCompletionBinarySelectsForwardedConfigWithoutCredentials(t *testing.T) {
	env, path := prepareBinaryMetadataCompletion(t)
	for _, tc := range []struct{ kind, want string }{
		{kind: "cacheproject", want: "DEMO\tExample project\n"},
		{kind: "cachefield", want: "customfield_10001\tNative field\n"},
	} {
		for _, flags := range [][]string{{"--config", path}, {"--config=" + path}} {
			args := append([]string{"--@complete=" + tc.kind, "--"}, flags...)
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != 0 || stdout != tc.want || stderr != "" {
				t.Fatalf("callback %v exit=%d stdout=%q stderr=%q want=%q", args, exit, stdout, stderr, tc.want)
			}
		}
		stdout, stderr, exit := runBinaryEnv(t, env, "--@complete="+tc.kind)
		if exit != 0 || stdout != "" || stderr != "" {
			t.Fatalf("unselected scope exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
		}
	}
}

func TestMetadataCompletionBashInsertsIdentitiesWithoutDescriptions(t *testing.T) {
	env, path := prepareBinaryMetadataCompletion(t)
	script, stderr, exit := runBinaryEnv(t, env, "completion", "bash")
	if exit != 0 || stderr != "" || !strings.Contains(script, "_jira()") {
		t.Fatalf("generate Bash exit=%d stderr=%q", exit, stderr)
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "completion.bash")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(jiraBinary(t), filepath.Join(dir, "jira")); err != nil {
		t.Fatal(err)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	// Only completion options need an interactive context. Candidate generation
	// and the real binary callback run unchanged through the generated function.
	driver := `source "$1"
compopt() { :; }
shift
COMP_WORDS=(jira "$@")
COMP_CWORD=$((${#COMP_WORDS[@]} - 1))
cur=${COMP_WORDS[COMP_CWORD]}
prev=${COMP_WORDS[COMP_CWORD-1]}
_jira jira "$cur" "$prev" || exit
printf '%s\0' "${COMPREPLY[@]}"
`
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{name: "default project", args: []string{"config", "set", "default_project", "DEM"}, want: []string{"DEMO"}},
		{name: "default issue type", args: []string{"config", "set", "default_issue_type", "Ta"}, want: []string{"Task"}},
		{name: "default board", args: []string{"config", "set", "default_board", "Exam"}, want: []string{"Example board"}},
		{name: "project CSV", args: []string{"issue", "list", "--project", "DEM"}, want: []string{"DEMO"}},
		{name: "field CSV", args: []string{"issue", "view", "DEMO-1", "--fields", "cust"}, want: []string{"customfield_10001"}},
		{name: "literal project", args: []string{"epic", "list", "--project", "DEM"}, want: []string{"DEMO"}},
		{name: "CSV continuation", args: []string{"issue", "list", "--project", "OTHER,DEM"}, want: []string{"OTHER,DEMO"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--noprofile", "--norc", "-c", driver, "metadata-driver", scriptPath, "--config", path}, tc.args...)
			stdout, stderr, exit := runExecutableIOInDirStarted(t, bash, dir, append(slices.Clone(env), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH")), nil, nil, nil, args...)
			got := strings.Split(strings.TrimSuffix(stdout, "\x00"), "\x00")
			if exit != 0 || stderr != "" || !slices.Equal(got, tc.want) {
				t.Fatalf("generated Bash inserted=%q want=%q exit=%d stderr=%q", got, tc.want, exit, stderr)
			}
		})
	}
}

func TestFishCompletesWorkingDefaultValuesThroughGeneratedScript(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("native Fish is unavailable")
	}
	env, selected := prepareBinaryMetadataCompletion(t)
	script, stderr, exit := runBinaryEnv(t, env, "completion", "fish")
	if exit != 0 || stderr != "" {
		t.Fatal("Fish generation failed", exit, stderr)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "completion.fish")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(jiraBinary(t), filepath.Join(dir, "jira")); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ key, prefix, want string }{
		{"default_project", "DEM", "DEMO"},
		{"default_issue_type", "Ta", "Task"},
		{"default_board", "Exam", "Example board"},
		{"theme", "au", "auto"},
	} {
		t.Run(test.key, func(t *testing.T) {
			line := "jira --config " + shell.Quote(selected) + " config set " + test.key + " " + test.prefix
			stdout, stderr, exit := runExecutableIOInDirStarted(t, fish, dir, append(slices.Clone(env), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH")), nil, nil, nil, "--no-config", "-c", `source "$argv[1]"; or exit
complete -C "$argv[2]"`, "--", path, line)
			if exit != 0 || stderr != "" || strings.Split(strings.TrimSpace(stdout), "\t")[0] != test.want {
				t.Fatalf("generated Fish default identity=%q want=%q exit=%d stderr=%q", stdout, test.want, exit, stderr)
			}
		})
	}
}
