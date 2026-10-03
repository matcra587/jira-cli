package contract

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestAliasExpansionResolvesFinalConfigAndCallerFlagPrecedence(t *testing.T) {
	t.Parallel()
	first := aliasConfig(t, "timeout='2m'\n")
	second := aliasConfig(t, "timeout='3m'\n")
	aliasFrame(t, first, "", "alias", "set", "inspect", "--", "--config "+shell.Quote(second)+" --timeout=4s config list")
	for _, tail := range [][]string{nil, {"--timeout=5s"}, {"--config", first}} {
		args := append([]string{"--config", first, "--output=json", "inspect"}, tail...)
		stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL="}, args...)
		var f apiFrame
		if json.Unmarshal([]byte(stdout), &f) != nil || !f.OK || exit != 0 || f.Meta.Command != "config.list" {
			t.Fatalf("expanded globals broke shared dispatch: %d %s %s", exit, stdout, stderr)
		}
		var out envelope.ConfigListOutput
		if err := json.Unmarshal(f.Data, &out); err != nil {
			t.Fatal(err)
		}
		wantPath, wantTimeout := second, "4s"
		if len(tail) > 0 && tail[0] == "--timeout=5s" {
			wantTimeout = "5s"
		}
		if len(tail) > 0 && tail[0] == "--config" {
			wantPath = first
		}
		if out.Path != wantPath {
			t.Fatalf("expanded config path ignored: %q want %q", out.Path, wantPath)
		}
		found := false
		for _, setting := range out.Settings {
			if setting.Key == "timeout" {
				found = true
				if setting.Value != wantTimeout || setting.Source != "flag" {
					t.Fatalf("expanded timeout or caller override ignored: %+v", setting)
				}
			}
		}
		if !found {
			t.Fatal("settings inspection omitted timeout")
		}
	}
}

func TestAliasCyclesRefuseWithoutHTTPAndBuiltinsStayLazy(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	t.Cleanup(server.Close)
	path := aliasConfig(t, "[aliases]\na='b'\nb='a'\nversion='api post /rest/api/3/fixture --force'\n")
	env := []string{"JIRA_BASE_URL=" + server.URL, "JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL="}
	for _, mode := range []string{"json", "compact"} {
		stdout, stderr, exit := runBinaryEnv(t, env, "a", "--config", path, "--output="+mode)
		var f apiFrame
		if json.Unmarshal([]byte(stdout), &f) != nil || f.OK || exit != cli.ExitValidation || len(f.Errors) != 1 || f.Errors[0].Code != cli.CodeAliasInvalid || f.Errors[0].Hint == "" || calls.Load() != 0 {
			t.Fatalf("cyclic alias reached dependencies or lost shared error frame: %d %s %s", exit, stdout, stderr)
		}
	}
	for _, args := range [][]string{{"version", "--output=json"}, {"alias", "set", "good", "version", "--output=json"}, {"alias", "delete", "a", "--force", "--output=json"}, {"version", "--help"}} {
		args = append(args, "--config", path)
		stdout, stderr, exit := runBinaryEnv(t, env, args...)
		if exit != 0 || stdout == "" || calls.Load() != 0 {
			t.Fatalf("unrelated invalid alias disabled a built-in or local repair: %d %s", exit, stderr)
		}
	}
	// A syntactically broken config cannot disable credential-free help/version.
	if err := os.WriteFile(path, []byte("[aliases]\ninvalid = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit := runBinaryEnv(t, env, "version", "--config", path, "--output=json")
	if exit != 0 || stdout == "" || calls.Load() != 0 {
		t.Fatalf("version tried to load malformed alias settings: %d %s", exit, stderr)
	}
}

func TestAliasCompletionUsesSelectedFileAndKeepsSafeIdentities(t *testing.T) {
	t.Parallel()
	path := aliasConfig(t, "[aliases]\n'z.name'='version'\n'日本語'='version'\n\"bad\\u0085name\"='version'\n\"bad\\u001bname\"='version'\nsafe=\"version\\u001b[2J\\u0007\"\n")
	for _, args := range [][]string{
		{"--config", path, "--@complete=alias", "--@shell=fish"},
		{"--@complete=alias", "--@shell=fish", "--", "alias", "delete", "--config", path},
	} {
		stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL="}, args...)
		if exit != 0 || stderr != "" || !strings.Contains(stdout, "z.name\tversion\n") || !strings.Contains(stdout, "日本語\tversion\n") || !strings.Contains(stdout, "safe\t") || strings.ContainsAny(stdout, "\x1b\a\u0085") || strings.Contains(stdout, "bad\x1bname") {
			t.Fatalf("alias completion lost selected local names or leaked terminal controls: %d %q %q", exit, stdout, stderr)
		}
	}
}

func TestAliasFailedClobberRestoresOldDefinitionAndSkipsProposedDependants(t *testing.T) {
	t.Parallel()
	path := aliasConfig(t, "[aliases]\nkept='version'\n")
	f := aliasFrame(t, path, "kept: missing\ndependant: kept\nvalid: version\n", "alias", "import", "--clobber")
	var out envelope.AliasImportOutput
	if json.Unmarshal(f.Data, &out) != nil || out.Imported != 1 || len(out.Skipped) != 2 || out.Skipped["kept"] == "" || out.Skipped["dependant"] == "" {
		t.Fatal("failed clobber or its proposed dependant was accepted")
	}
	f = aliasFrame(t, path, "", "alias", "list")
	var list envelope.AliasListOutput
	if json.Unmarshal(f.Data, &list) != nil || !maps.Equal(list.Aliases, map[string]string{"kept": "version", "valid": "version"}) {
		t.Fatal("failed clobber discarded the old alias or persisted a rejected dependant")
	}
}

func TestAliasMutationsRetainForceReadonlyPreviewAndNoReplay(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/fixture" {
			t.Error("alias changed the explicit raw mutation")
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"errorMessages":["fixture unavailable"]}`))
	}))
	t.Cleanup(server.Close)
	path := aliasConfig(t, "[aliases]\nwrite='api post /rest/api/3/fixture'\n")
	for _, tc := range []struct {
		name, code string
		args, env  []string
		wantCalls  int32
		wantOK     bool
	}{
		{name: "force", code: cli.CodeFlagRequired},
		{name: "preview", args: []string{"--dry-run"}, wantOK: true},
		{name: "readonly", args: []string{"--force"}, env: []string{"JIRA_READ_ONLY=true"}, code: cli.CodeReadOnlyBlocked},
		{name: "failed write", args: []string{"--force"}, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--config", path, "write", "--output=json", "--no-input"}, tc.args...)
			env := append(editReceiptEnv(server.URL), tc.env...)
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			var f apiFrame
			if json.Unmarshal([]byte(stdout), &f) != nil || f.OK != tc.wantOK || calls.Load() != tc.wantCalls || f.Meta.Command != "api.post" {
				t.Fatalf("alias bypassed shared mutation policy: %d %s %s calls=%d", exit, stdout, stderr, calls.Load())
			}
			if tc.wantOK {
				if exit != 0 || !strings.Contains(string(f.Data), `"dry_run":true`) {
					t.Fatal("alias preview lost the shared local receipt")
				}
			} else if exit == 0 || len(f.Errors) == 0 || (tc.code != "" && f.Errors[0].Code != tc.code) {
				t.Fatal("alias mutation lost the original refusal or failure")
			}
		})
	}
}
