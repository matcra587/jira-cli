package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

type windowsBrowserReceipt struct {
	PID  uint32   `json:"pid"`
	Args []string `json:"args"`
}

// Browser handoff supplies only the URL, so the owned helper handles its mode
// before the testing flag parser. Ordinary test execution never enters it.
func init() {
	if os.Getenv("JIRA_TEST_WINDOWS_BROWSER_HELPER") != "1" {
		return
	}
	path := os.Getenv("JIRA_TEST_WINDOWS_BROWSER_RECEIPT")
	data, err := json.Marshal(windowsBrowserReceipt{windows.GetCurrentProcessId(), os.Args[1:]})
	if err != nil || !filepath.IsAbs(path) || os.WriteFile(path+".partial", data, 0o600) != nil || os.Rename(path+".partial", path) != nil {
		os.Exit(40)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path + ".release"); err == nil {
			os.Exit(0)
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(41)
}

func TestNativeWindowsBrowserHandoff(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	directory := t.TempDir()
	helper := filepath.Join(directory, "browser café $HOME;literal'quote.exe")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal("cannot read owned native browser", err)
	}
	if err := os.WriteFile(helper, data, 0o700); err != nil {
		t.Fatal("cannot prepare owned native browser", err)
	}
	settings := filepath.Join(directory, "selected settings.toml")
	if err := os.WriteFile(settings, []byte("base_url='"+server.URL+"/jira'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := server.URL + "/jira/browse/KEY-7"
	for _, mode := range []string{"json", "compact", "agent", "human open", "human web"} {
		t.Run(mode, func(t *testing.T) {
			receipt := filepath.Join(t.TempDir(), "browser.json")
			var observed windowsBrowserReceipt
			joined := false
			observedStartup := false
			t.Cleanup(func() {
				if !joined && !observedStartup && observed.PID == 0 {
					deadline := time.Now().Add(5 * time.Second)
					for time.Now().Before(deadline) {
						data, err := os.ReadFile(receipt)
						if err == nil && json.Unmarshal(data, &observed) == nil && observed.PID != 0 {
							break
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
				var handle windows.Handle
				if !joined && observed.PID != 0 {
					var err error
					handle, err = windows.OpenProcess(windows.SYNCHRONIZE, false, observed.PID)
					if err != nil {
						t.Error("cannot join owned native browser after failure", err)
					}
				}
				if err := os.WriteFile(receipt+".release", nil, 0o600); err != nil {
					t.Error(err)
				}
				if handle == 0 {
					return
				}
				defer windows.CloseHandle(handle) //nolint:errcheck // observation handle has no writes
				if state, err := windows.WaitForSingleObject(handle, 5000); err != nil || state != windows.WAIT_OBJECT_0 {
					t.Error("owned native browser did not terminate after failure", state, err)
				}
			})
			env := []string{
				"JIRA_BASE_URL=", "JIRA_EMAIL=", "JIRA_API_TOKEN=", "JIRA_KEYRING=false",
				"BROWSER=" + helper, "JIRA_TEST_WINDOWS_BROWSER_HELPER=1",
				"JIRA_TEST_WINDOWS_BROWSER_RECEIPT=" + receipt,
			}
			args := []string{"open", "KEY-007", "--config", settings, "--no-input"}
			human := strings.HasPrefix(mode, "human")
			switch mode {
			case "agent":
				env = append(env, "AGENT=1")
			case "human web":
				args = []string{"issue", "view", "KEY-7", "--web", "--config", settings, "--no-input", "--output=human"}
			case "human open":
				args = append(args, "--output=human")
			default:
				args = append(args, "--output="+mode)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != 0 || stderr != "" || !strings.Contains(stdout, want) {
				t.Fatal("native browser command failed", exit, stdout, stderr)
			}
			if !human {
				format := mode
				if mode == "agent" {
					format = "compact"
				}
				var out struct {
					Opened *bool  `json:"opened"`
					WebURL string `json:"web_url"`
				}
				if json.Unmarshal(adfData(t, stdout, format, "open", true), &out) != nil || out.Opened == nil || *out.Opened || out.WebURL != want {
					t.Fatal("machine output fabricated browser handoff", stdout)
				}
				// Start is asynchronous. Observe independently of the receipt's
				// opened field, including a launch that falsely reports false.
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					data, err := os.ReadFile(receipt)
					if err == nil {
						if err := json.Unmarshal(data, &observed); err != nil {
							t.Fatal("invalid unexpected browser receipt", err)
						}
						t.Fatal("machine output launched native browser", observed)
					}
					if !os.IsNotExist(err) {
						t.Fatal("cannot observe native browser suppression", err)
					}
					time.Sleep(10 * time.Millisecond)
				}
				observedStartup = true
				return
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				data, err := os.ReadFile(receipt)
				if err == nil {
					if json.Unmarshal(data, &observed) != nil || observed.PID == 0 || !slices.Equal(observed.Args, []string{want}) {
						t.Fatal("native browser received changed or additional argv", string(data))
					}
					break
				}
				if !os.IsNotExist(err) || time.Now().After(deadline) {
					observedStartup = true
					t.Fatal("native browser produced no receipt", err)
				}
				time.Sleep(10 * time.Millisecond)
			}
			handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, observed.PID)
			if err != nil {
				t.Fatal("cannot observe owned native browser", err)
			}
			defer windows.CloseHandle(handle) //nolint:errcheck // observation handle has no writes
			if err := os.WriteFile(receipt+".release", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if state, err := windows.WaitForSingleObject(handle, 5000); err != nil || state != windows.WAIT_OBJECT_0 {
				t.Fatal("owned native browser did not terminate", state, err)
			}
			joined = true
			var code uint32
			if err := windows.GetExitCodeProcess(handle, &code); err != nil || code != 0 {
				t.Fatal("owned native browser exited unsuccessfully", code, err)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatal("native browser handoff contacted Jira", requests.Load())
	}
}
