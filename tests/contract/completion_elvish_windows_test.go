//go:build windows && nativeconsole

package contract

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func nativeWindowsElvish(t *testing.T) string {
	t.Helper()
	name := os.Getenv("JIRA_TEST_ELVISH_BINARY")
	if name == "" {
		name = "elvish"
	}
	elvish, err := exec.LookPath(name)
	if err != nil {
		t.Fatal("native Elvish executable required", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, elvish, "-version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(version)) != "0.21.0" {
		t.Fatal("native Elvish 0.21.0 did not execute", err, string(version))
	}
	return elvish
}

func TestNativeWindowsElvishCallbacks(t *testing.T) {
	elvish := nativeWindowsElvish(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"positional", "csv", "inline", "profile", "profile-inline", "reload", "fields-described", "fields-second", "fields-described-inline", "fields-second-inline"} {
		files := []string{"candidate space.txt", "candidate,comma.txt", "candidate café $HOME;literal'quote.txt"}
		if strings.HasPrefix(mode, "profile") || strings.HasPrefix(mode, "fields") || mode == "reload" {
			files = files[1:2]
		}
		for _, filename := range files {
			t.Run(mode+"/"+filename, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
				defer cancel()
				process := exec.CommandContext(ctx, self, "-test.v", "-test.run=^TestWindowsElvishConsoleProcess$")
				process.Env = append(os.Environ(), "JIRA_TEST_ELVISH_BINARY="+elvish, "JIRA_TEST_ELVISH_CASE="+mode, "JIRA_TEST_ELVISH_FILENAME="+filename)
				process.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
				process.WaitDelay = 2 * time.Second
				output, err := process.CombinedOutput()
				if err != nil || !strings.Contains(string(output), "elvish-callback-verified") || !strings.Contains(string(output), "--- PASS: TestWindowsElvishConsoleProcess") {
					t.Fatal("native Elvish console callback failed", err, string(output))
				}
			})
		}
	}
}

// The real interactive runtime creates its edit namespace from CONIN$. Calling
// the registered completer here proves callbacks, not Tab insertion or rendering.
func TestWindowsElvishConsoleProcess(t *testing.T) {
	mode := os.Getenv("JIRA_TEST_ELVISH_CASE")
	if mode == "" {
		return
	}
	filename := os.Getenv("JIRA_TEST_ELVISH_FILENAME")
	if filepath.Base(filename) != filename || !strings.HasPrefix(filename, "candidate") {
		t.Fatal("invalid owned Elvish filename")
	}
	dir := t.TempDir()
	cli := filepath.Join(dir, "jira.exe")
	data, err := os.ReadFile(jiraBinary(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cli, data, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(dir, "selected settings.toml")
	if err := os.WriteFile(settings, []byte("read_only=true\nbase_url='https://example.invalid'\nemail='fixture@example.invalid'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"), "PATHEXT=" + os.Getenv("PATHEXT"),
		"HOME=" + dir, "USERPROFILE=" + dir, "APPDATA=" + filepath.Join(dir, "AppData"), "LOCALAPPDATA=" + filepath.Join(dir, "Local"),
		"TEMP=" + dir, "TMP=" + dir, "JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL=", "JIRA_BASE_URL=",
		"JIRA_NO_UPDATE_CHECK=1", "HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1",
	}
	script, stderr, exit := runExecutableIOInDirStarted(t, cli, dir, env, nil, nil, nil, "completion", "elvish")
	if exit != 0 || stderr != "" {
		t.Fatal("native Elvish generation failed", exit, stderr)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	words := []string{"jira", "--config", settings, "issue", "attachment", "add", "KEY-1", "candidate"}
	fieldValue, fieldPrefix := "", ""
	switch mode {
	case "csv", "reload":
		words = append(words[:len(words)-1], "--file", "candidate")
	case "inline":
		words[len(words)-1] = "--file=candidate"
	case "profile", "profile-inline":
		legacy := filepath.Join(dir, "legacy source.toml")
		if err := os.WriteFile(legacy, []byte("default_profile='work'\n[[profiles]]\nname='work'\nbase_url='https://example.invalid'\nemail='fixture@example.invalid'\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		words = []string{"jira", "--config", settings, "config", "migrate", "--source", legacy, "--source-profile", "wo"}
		if mode == "profile-inline" {
			words = append(words[:len(words)-2], "--source-profile=wo")
		}
	case "fields-described", "fields-second", "fields-described-inline", "fields-second-inline":
		metadataEnv, selected := prepareBinaryElvishFieldCompletion(t)
		env = append(env, metadataEnv...)
		fieldValue, fieldPrefix = "customfield_10001", "customfield_10002,"
		if strings.Contains(mode, "second") {
			fieldValue, fieldPrefix = "customfield_10002", "customfield_10001,"
		}
		words = []string{"jira", "--config", selected, "issue", "view", "DEMO-1", "--fields", fieldPrefix + "customfield_1000"}
		if strings.HasSuffix(mode, "inline") {
			words = append(words[:len(words)-2], "--fields="+words[len(words)-1])
		}
	case "positional":
	default:
		t.Fatal("invalid owned Elvish callback role")
	}
	for i, word := range words {
		words[i] = quote(word)
	}
	if mode == "reload" {
		script += "\n" + script
	}
	receipt := filepath.Join(dir, "candidates.json")
	rc := script + "\nvar replies = [($edit:completion:arg-completer[jira] " + strings.Join(words, " ") + ")]\n" +
		"var stems = []\nfor candidate $replies { if (eq (kind-of $candidate) string) { set stems = [$@stems $candidate] } else { set stems = [$@stems $candidate[stem]] } }\n" +
		"var matches = [(put $@stems | $edit:completion:matcher[argument] " + words[len(words)-1] + ")]\n" +
		"var matched = []\nfor i [(range (count $stems))] { if $matches[$i] { set matched = [$@matched $stems[$i]] } }\n" +
		"put $matched | to-json >" + quote(receipt) + "\nexit 0\n"
	path := filepath.Join(dir, "owned rc.elv")
	if err := os.WriteFile(path, []byte(rc), 0o600); err != nil {
		t.Fatal(err)
	}
	input := publicWindowsConsole(t, "CONIN$")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, os.Getenv("JIRA_TEST_ELVISH_BINARY"), "-rc", path, "-sock", filepath.Join(dir, "socket"), "-db", filepath.Join(dir, "store.db"))
	process.Dir, process.Env, process.Stdin = dir, env, input
	output, err := process.CombinedOutput()
	if err != nil || len(output) != 0 {
		t.Fatal("native Elvish callback runtime failed", err, string(output))
	}
	data, err = os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var values []string
	if json.Unmarshal(data, &values) != nil || len(values) != 1 {
		t.Fatal("native Elvish callback lost candidates", string(data))
	}
	want := filename
	value := values[0]
	if strings.HasPrefix(mode, "fields") {
		want = fieldPrefix + fieldValue
		if strings.HasSuffix(mode, "inline") {
			want = "--fields=" + want
		}
	} else if strings.HasPrefix(mode, "profile") {
		want = "work"
		if mode == "profile-inline" {
			want = "--source-profile=work"
		}
	} else if mode != "positional" {
		if mode == "inline" {
			if !strings.HasPrefix(value, "--file=") {
				t.Fatal("native Elvish callback lost inline flag", value)
			}
			value = strings.TrimPrefix(value, "--file=")
		}
		fields, err := csv.NewReader(strings.NewReader(value)).Read()
		if err != nil || !slices.Equal(fields, []string{filename}) {
			t.Fatal("native Elvish callback changed CSV identity", value, fields, err)
		}
		value = fields[0]
	}
	if value != want {
		t.Fatal("native Elvish callback changed identity", value, want)
	}
	fmt.Println("elvish-callback-verified")
}
