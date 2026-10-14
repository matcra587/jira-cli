//go:build windows && nativeconsole

package contract

import (
	"context"
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
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestNativeWindowsElvishInsertion(t *testing.T) {
	elvish := nativeWindowsElvish(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"profile", "profile-inline", "positional", "csv", "inline", "reload", "fields-described", "fields-second", "fields-described-inline", "fields-second-inline", "download-directory", "download-nested-directory"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			process := exec.CommandContext(ctx, self, "-test.v", "-test.run=^TestWindowsElvishInsertionConsoleProcess$")
			process.Env = append(os.Environ(), "JIRA_TEST_ELVISH_BINARY="+elvish, "JIRA_TEST_ELVISH_INSERTION="+mode)
			process.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
			process.WaitDelay = 2 * time.Second
			output, err := process.CombinedOutput()
			if err != nil || !strings.Contains(string(output), "elvish-insertion-verified") || !strings.Contains(string(output), "--- PASS: TestWindowsElvishInsertionConsoleProcess") {
				t.Fatal("native Elvish insertion failed", err, string(output))
			}
		})
	}
}

// This process owns its console. The generated external callbacks execute the
// real CLI; the accepted command records argv without uploading or migrating.
func TestWindowsElvishInsertionConsoleProcess(t *testing.T) {
	mode := os.Getenv("JIRA_TEST_ELVISH_INSERTION")
	if mode == "" {
		return
	}
	if !slices.Contains([]string{"profile", "profile-inline", "positional", "csv", "inline", "reload", "fields-described", "fields-second", "fields-described-inline", "fields-second-inline", "download-directory", "download-nested-directory"}, mode) {
		t.Fatal("invalid owned Elvish insertion role")
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
		"XDG_CONFIG_HOME=" + filepath.Join(dir, "config"), "XDG_CACHE_HOME=" + filepath.Join(dir, "cache"),
		"TEMP=" + dir, "TMP=" + dir, "JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL=", "JIRA_BASE_URL=",
		"JIRA_READ_ONLY=true", "JIRA_NO_UPDATE_CHECK=1", "HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1",
	}
	script, stderr, exit := runExecutableIOInDirStarted(t, cli, dir, env, nil, nil, nil, "completion", "elvish")
	if exit != 0 || stderr != "" {
		t.Fatal("native Elvish generation failed", exit, stderr)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	line := "jira --config " + quote(settings) + " issue attachment add KEY-1 candidate"
	filename := "candidate café $HOME;literal'quote,comma.txt"
	want := []string{"--config", settings, "issue", "attachment", "add", "KEY-1", filename}
	if strings.HasPrefix(mode, "fields") {
		metadataEnv, selected := prepareBinaryElvishFieldCompletion(t)
		env = append(env, metadataEnv...)
		env = append(env, "JIRA_KEYRING=false")
		value, prior := "customfield_10001", "customfield_10002"
		if strings.Contains(mode, "second") {
			value, prior = prior, value
		}
		line = "jira --config " + quote(selected) + " issue view DEMO-1 --fields " + prior + ",customfield_1000"
		want = []string{"--config", selected, "issue", "view", "DEMO-1", "--fields", prior + "," + value}
		if strings.HasSuffix(mode, "inline") {
			line = strings.Replace(line, "--fields ", "--fields=", 1)
			want = append(want[:len(want)-2], "--fields="+prior+","+value)
		}
	} else if strings.HasPrefix(mode, "profile") {
		legacy := filepath.Join(dir, "legacy source.toml")
		if err := os.WriteFile(legacy, []byte("default_profile='work'\n[[profiles]]\nname='work'\nbase_url='https://example.invalid'\nemail='fixture@example.invalid'\n[[profiles]]\nname='other'\nbase_url='https://other.invalid'\nemail='other@example.invalid'\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		line = "jira --config " + quote(settings) + " config migrate --source " + quote(legacy) + " --source-profile wo"
		want = []string{"--config", settings, "config", "migrate", "--source", legacy, "--source-profile", "work"}
		if mode == "profile-inline" {
			line = strings.TrimSuffix(line, "--source-profile wo") + "--source-profile=wo"
			want = append(want[:len(want)-2], "--source-profile=work")
		}
	} else if strings.HasPrefix(mode, "download-") {
		const directory = "candidate café $HOME;literal'quote,comma"
		if err := os.MkdirAll(filepath.Join(dir, directory, "nested directory"), 0o700); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"candidate file.txt", filepath.Join(directory, "nested file.txt")} {
			if err := os.WriteFile(filepath.Join(dir, path), []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		line = "jira --config " + quote(settings) + " issue attachment download 10042 --to candidate"
		want = []string{"--config", settings, "issue", "attachment", "download", "10042", "--to", directory}
		if mode == "download-nested-directory" {
			line = "jira --config " + quote(settings) + " issue attachment download 10042 --to " + quote(filepath.Join(directory, "ne"))
			want[7] = filepath.Join(directory, "nested directory")
		}
	} else {
		if err := os.WriteFile(filepath.Join(dir, filename), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if mode != "positional" {
			line = strings.TrimSuffix(line, "candidate") + "--file candidate"
			want = append(want[:len(want)-1], "--file", `"`+filename+`"`)
			if mode == "inline" {
				line = strings.TrimSuffix(line, "--file candidate") + "--file=candidate"
				want = append(want[:len(want)-2], `--file="`+filename+`"`)
			}
		}
	}
	if mode == "reload" {
		script += "\n" + script
	}
	ready, completed, receipt := filepath.Join(dir, "ready"), filepath.Join(dir, "completed"), filepath.Join(dir, "argv.json")
	script += "\nfn jira {|@args| put $args | to-json >" + quote(receipt) + "; exit 0 }\n" +
		"set edit:prompt = { echo ready >" + quote(ready) + "; put 'OWNED_READY> ' }\n" +
		"set edit:rprompt = (constantly '')\n" +
		"set edit:global-binding[F12] = { echo completed >" + quote(completed) + " }\n"
	rc := filepath.Join(dir, "owned rc.elv")
	if err := os.WriteFile(rc, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	input, output := publicWindowsConsole(t, "CONIN$"), publicWindowsConsole(t, "CONOUT$")
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("owned Elvish console:", windowsElvishConsoleText(output))
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, nativeWindowsElvish(t), "-rc", rc, "-sock", filepath.Join(dir, "socket"), "-db", filepath.Join(dir, "store.db"))
	process.Dir, process.Env = dir, env
	process.Stdin, process.Stdout, process.Stderr = input, output, output
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if process.ProcessState == nil {
			_ = process.Process.Kill()
			_ = process.Wait()
		}
	})
	waitWindowsElvishMarker(t, ctx, ready)
	var keys []windowsElvishKeyRecord
	for _, value := range windows.StringToUTF16(line) {
		if value != 0 {
			keys = append(keys, windowsElvishKeyRecord{EventType: 1, Down: 1, Repeat: 1, Char: value})
		}
	}
	keys = append(keys,
		windowsElvishKeyRecord{EventType: 1, Down: 1, Repeat: 1, Virtual: 9, Char: 9},
		windowsElvishKeyRecord{EventType: 1, Down: 1, Repeat: 1, Virtual: 123})
	writeWindowsElvishKeys(t, input, keys)
	// The bound key runs after Tab handling, providing a real editor boundary.
	waitWindowsElvishMarker(t, ctx, completed)
	writeWindowsElvishKeys(t, input, []windowsElvishKeyRecord{
		{EventType: 1, Down: 1, Repeat: 1, Virtual: 13, Char: 13},
		{EventType: 1, Down: 1, Repeat: 1, Virtual: 13, Char: 13},
	})
	waitWindowsElvishMarker(t, ctx, receipt)
	if err := process.Wait(); err != nil {
		t.Fatal("native Elvish editor did not exit successfully", err)
	}
	data, err = os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal("native Elvish observer did not produce argv", err)
	}
	if strings.HasPrefix(mode, "download-") {
		// Native directory hints may supply a relative prefix or separator.
		// Keep every selected component literal, including its metacharacters.
		if len(got) != len(want) || !slices.Equal(got[:7], want[:7]) || strings.TrimSuffix(strings.TrimPrefix(filepath.ToSlash(got[7]), "./"), "/") != filepath.ToSlash(want[7]) {
			t.Fatal("native Elvish directory hint changed literal argv", got, want)
		}
	} else if !slices.Equal(got, want) {
		t.Fatal("native Elvish Tab changed literal argv", string(data), want)
	}
	fmt.Println("elvish-insertion-verified")
}

// windowsElvishKeyRecord is the native INPUT_RECORD containing KEY_EVENT_RECORD.
type windowsElvishKeyRecord struct {
	EventType uint16
	Padding   uint16
	Down      uint32
	Repeat    uint16
	Virtual   uint16
	Scan      uint16
	Char      uint16
	State     uint32
}

func writeWindowsElvishKeys(t *testing.T, input *os.File, records []windowsElvishKeyRecord) {
	t.Helper()
	if len(records) == 0 || unsafe.Sizeof(windowsElvishKeyRecord{}) != 20 {
		t.Fatal("invalid native Elvish key records")
	}
	var written uint32
	call := windows.NewLazySystemDLL("kernel32.dll").NewProc("WriteConsoleInputW")
	// The native API receives only our validated record slice and output count.
	result, _, err := call.Call(input.Fd(), uintptr(unsafe.Pointer(&records[0])), uintptr(len(records)), uintptr(unsafe.Pointer(&written))) //nolint:gosec // G103: audited native console marshaling of owned buffers.
	if result == 0 || written != uint32(len(records)) {
		t.Fatal("native Elvish console input failed", written, len(records), err)
	}
}

func waitWindowsElvishMarker(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatal("native Elvish marker unreadable", path, err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("native Elvish editor marker timed out", path, ctx.Err())
		case <-ticker.C:
		}
	}
}

func windowsElvishConsoleText(output *os.File) string {
	buffer := make([]uint16, 8000)
	var count uint32
	call := windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleOutputCharacterW")
	result, _, _ := call.Call(output.Fd(), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0, uintptr(unsafe.Pointer(&count))) //nolint:gosec // G103: fixed-size owned buffer for native console diagnostics.
	if result == 0 || count > uint32(len(buffer)) {
		return "owned console diagnostics unavailable"
	}
	return windows.UTF16ToString(buffer[:count])
}
