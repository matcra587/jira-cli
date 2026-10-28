//go:build linux || darwin

package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gechr/x/shell"
	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/tests/internal/nativepty"
)

// Exercise the public executable and real publication/reload owners for every
// writable key. Explicit keys make additions to the schema fail this inventory.
func TestDashboardNativeEverySettingSavesReloadsAndReopens(t *testing.T) {
	var mu sync.Mutex
	var gatewayAttempts int
	cloudExercised := false
	type observedSearch struct{ host, email, query string }
	searches := make(chan observedSearch, 256)
	var slowSearch bool
	deadlineObserved := make(chan time.Duration, 1)
	serve := func(w http.ResponseWriter, r *http.Request) {
		read := r.Method == http.MethodGet || r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search/jql"
		if !read {
			t.Errorf("settings audit attempted a Jira write: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/3/search/jql":
			var body struct {
				JQL string `json:"jql"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			email, _, _ := r.BasicAuth()
			searches <- observedSearch{host: r.Host, email: email, query: body.JQL}
			mu.Lock()
			slow := slowSearch
			slowSearch = false
			mu.Unlock()
			if slow {
				started := time.Now()
				<-r.Context().Done()
				deadlineObserved <- time.Since(started)
				return
			}
			_, _ = fmt.Fprint(w, `{"isLast":true,"issues":[{"id":"10001","key":"DEMO-1","fields":{"summary":"Settings audit issue","status":{"name":"In progress"},"assignee":{"displayName":"Audit person","accountId":"audit-person"},"updated":"2026-01-01T12:00:00Z"}}]}`)
		case "/rest/api/3/project/search":
			_, _ = fmt.Fprint(w, `{"startAt":0,"maxResults":100,"total":1,"isLast":true,"values":[{"id":"10402","key":"DEMO","name":"Audit project"}]}`)
		case "/rest/api/3/project/10402":
			_, _ = fmt.Fprint(w, `{"id":"10402","key":"DEMO","name":"Audit project"}`)
		case "/rest/api/3/issue/createmeta/10402/issuetypes":
			_, _ = fmt.Fprint(w, `{"startAt":0,"maxResults":50,"total":2,"issueTypes":[{"id":"10001","name":"Bug"},{"id":"10002","name":"Task"}]}`)
		case "/rest/api/3/issue/DEMO-1":
			_, _ = fmt.Fprint(w, `{"id":"10001","key":"DEMO-1","fields":{"summary":"Settings audit issue","labels":["audit-label"],"priority":{"name":"Audit priority"},"issuetype":{"name":"Audit type"},"description":null}}`)
		default:
			t.Errorf("unexpected fixture request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(serve))
	defer server.Close()
	other := httptest.NewServer(http.HandlerFunc(serve))
	defer other.Close()
	// Scoped routing must not contact a public gateway during this test. Observe
	// its CONNECT target and refuse the tunnel; gateway path semantics have their
	// own transport tests using an injected client.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "api.atlassian.com:443" {
			t.Errorf("unexpected proxy request: %s %s", r.Method, r.Host)
		}
		mu.Lock()
		gatewayAttempts++
		mu.Unlock()
		http.Error(w, "synthetic gateway refusal", http.StatusBadGateway)
	}))
	defer proxy.Close()
	dir := t.TempDir()
	themePath := filepath.Join(dir, "audit-theme.toml")
	if err := os.WriteFile(themePath, []byte("base='nord'\n[syntax]\naccent='#123456'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"base_url": other.URL, "cloud_id": "audit-cloud", "email": "changed@example.com",
		"credential_id": "12345678-1234-4234-8234-123456789abc",
		"account_id":    "audit-person", "account_id_site": "https://other.invalid",
		"account_id_email": "cached@example.com", "account_id_cloud_id": "cached-cloud",
		"api_token_env": "JIRA_TOKEN_SETTINGS_AUDIT", "default_project": "DEMO",
		"default_issue_type": "Task", "default_board": "42", "refresh_interval": "17",
		"team_account_ids": `["person-a","person-b"]`, "keyring": "false",
		"secret_backend": "1password", "onepassword_account": "audit-account",
		"onepassword_vault": "audit-vault", "onepassword_item": "audit-item",
		"read_only": "true", "timeout": "2s", "glyphs": "nerd", "theme": "nord",
		"theme_path": themePath, "workday_seconds": "21600",
		"queries_path": filepath.Join(dir, "queries with spaces"),
		"editor":       shell.Quote(jiraBinary(t)) + " --version 'literal ; $(not-run)'",
		"tui_preview":  "false", "tui_status": "false", "tui_assignee": "false",
		"tui_labels": "false", "tui_priority": "false", "tui_issue_type": "false",
		"tui_compact": "false", "tui_updated": "false", "tui_auto_page": "false",
	}
	if err := os.MkdirAll(values["queries_path"], 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(values["queries_path"], "Audit saved view.jql"), []byte("project = DEMO"), 0o600); err != nil {
		t.Fatal(err)
	}
	keys := slices.Sorted(maps.Keys(values))
	writable := config.WritableSettingKeys()
	slices.Sort(writable)
	if !slices.Equal(keys, writable) {
		t.Fatalf("audit inventory differs from writable schema: inventory=%v schema=%v", keys, config.WritableSettingKeys())
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			if key == "cloud_id" {
				cloudExercised = true
			}
			fixture := t.TempDir()
			path := filepath.Join(fixture, "settings.toml")
			initial := fmt.Sprintf("format_version=1\ndefault_profile='audit'\ntheme='dark'\n[profiles.audit]\nbase_url=%q\nemail='person@example.com'\nsecret_backend='env'\ntimeout='800ms'\n[profiles.untouched]\nbase_url='https://untouched.invalid'\n", server.URL)
			if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
				t.Fatal(err)
			}
			load := func() *config.Resolved {
				t.Helper()
				resolved, err := config.Load(config.Options{Path: path, Environ: func() []string { return nil }})
				if err != nil {
					t.Fatal(err)
				}
				return resolved
			}
			before := load().Config.Values()
			terminal := startSettingsAuditTerminal(t, fixture, path, proxy.URL)
			terminal.wait("Settings audit issue")
			terminal.sendWait(",", "Side-by-side preview")
			terminal.selectSetting(key)
			if key == "theme" {
				terminal.sendWait("\r", "Built-in")
				// Primer's stable fuzzy order also matches monochrome-dark.
				terminal.sendWait("\x1b[200~nord\x1b[201~", "/ nord")
				terminal.sendWait("\x1b[B\r", "unsaved")
			} else if slices.Equal(config.SettingValueChoices(key), []string{"true", "false"}) {
				terminal.sendWait("\r", "unsaved")
			} else if len(config.SettingValueChoices(key)) > 0 {
				terminal.sendWait("\r", "keep in draft")
				terminal.sendWait("\x1b[200~"+values[key]+"\x1b[201~\r", "unsaved")
			} else {
				terminal.sendWait("\r", "cancel edit")
				terminal.sendWait("\x15\x1b[200~"+values[key]+"\x1b[201~\r", "unsaved")
			}
			if after := load().Config.Values(); !maps.Equal(before, after) {
				t.Fatal("editing the draft published settings before Save")
			}
			terminal.sendWait("\x13", "Runtime reloaded.")
			after := load()
			expected := maps.Clone(before)
			expected[key] = values[key]
			if !maps.Equal(after.Config.Values(), expected) {
				t.Fatalf("save changed unrelated values or lost the selected value: key=%s wanted=%q got=%q", key, values[key], after.Config.Values()[key])
			}
			if after.Source(key) != config.LayerFile {
				t.Fatalf("saved setting has source %s", after.Source(key))
			}
			untouched, err := config.Load(config.Options{Path: path, Profile: "untouched", Environ: func() []string { return nil }})
			if err != nil || untouched.Config.BaseURL != "https://untouched.invalid" {
				t.Fatal("save damaged the other profile", err)
			}
			terminal.sendWait("\x13", "No unsaved changes.")
			if slices.Contains([]string{"base_url", "email", "default_project", "default_issue_type", "queries_path", "read_only", "timeout"}, key) {
				terminal.sendWait("\x1b", "JQL")
				switch key {
				case "default_issue_type":
					terminal.sendWait("n", "DEMO · 10402")
					terminal.sendWait("DEMO\r", "› Task · 10002")
					terminal.sendWait("\r", "DEMO · Task")
				case "queries_path":
					terminal.sendWait("v", "Audit saved view")
				case "read_only":
					terminal.sendWait("n", "unavailable in read-only mode")
				case "default_project", "base_url", "email":
					if key == "default_project" {
						terminal.sendWait("2", `project in ("DEMO")`)
					}
					wanted := server.URL
					if key == "base_url" {
						wanted = other.URL
					}
					wantedEmail := "person@example.com"
					if key == "email" {
						wantedEmail = values[key]
					}
					observed := false
					deadline := time.NewTimer(3 * time.Second)
					defer deadline.Stop()
					for !observed {
						select {
						case search := <-searches:
							observed = "http://"+search.host == wanted && search.email == wantedEmail && (key != "default_project" || strings.Contains(search.query, `project in ("DEMO")`))
						case <-deadline.C:
							t.Fatal("new runtime did not use the saved connection or project")
						}
					}
				case "timeout":
					mu.Lock()
					slowSearch = true
					mu.Unlock()
					terminal.sendWait("r", "Loading")
					select {
					case duration := <-deadlineObserved:
						if duration < 1500*time.Millisecond || duration > 3500*time.Millisecond {
							t.Fatalf("saved 2s deadline not applied: %s", duration)
						}
					case <-time.After(4 * time.Second):
						t.Fatal("saved request timeout did not cancel the read")
					}
				}
			}
			terminal.stop()
			if key == "workday_seconds" || key == "editor" {
				args := []string{"worklog", "add", "DEMO-1", "--time-spent=1d", "--started=2026-01-02", "--dry-run"}
				if key == "editor" {
					args = []string{"config", "edit", "--dry-run"}
				}
				args = append(args, "--config="+path, "--output=json")
				stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false", "JIRA_EDITOR=", "VISUAL=", "EDITOR="}, args...)
				var frame struct {
					OK   bool `json:"ok"`
					Data struct {
						Editor         []string        `json:"editor"`
						WorkdaySeconds int             `json:"workday_seconds"`
						Payload        json.RawMessage `json:"payload"`
					} `json:"data"`
				}
				if exit != 0 || stderr != "" || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK {
					t.Fatalf("saved setting was not usable by its consumer: exit=%d stderr=%q output=%s", exit, stderr, stdout)
				}
				if key == "editor" && !slices.Equal(frame.Data.Editor, []string{jiraBinary(t), "--version", "literal ; $(not-run)"}) {
					t.Fatal("saved editor did not preserve executable and literal argv")
				}
				if key == "workday_seconds" && (frame.Data.WorkdaySeconds != 21600 || !bytes.Contains(frame.Data.Payload, []byte(`"timeSpentSeconds":21600`))) {
					t.Fatal("saved workday did not change the actual 1d worklog payload")
				}
			}
			// A new executable must resolve the same declaration, then default and
			// cancellation must leave the published bytes intact.
			published, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			reopened := startSettingsAuditTerminal(t, fixture, path, proxy.URL)
			reopened.wait("JIRA")
			reopened.sendWait(",", "Side-by-side preview")
			reopened.selectSetting(key)
			reopened.sendWait("d", "unsaved")
			reopened.sendWait("\x1b", "JQL")
			retained, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(retained, published) {
				t.Fatal("reopening or canceling a default reset changed published bytes", err)
			}
			if slices.Equal(config.SettingValueChoices(key), []string{"true", "false"}) {
				reopened.sendWait(",", "Side-by-side preview")
				reopened.selectSetting(key)
				reopened.sendWait("d", "unsaved")
				reopened.sendWait("\x13", "Runtime reloaded.")
				if load().Config.Values()[key] != fmt.Sprint(config.Defaults()[key]) {
					t.Fatal("saving the reverse boolean change did not apply its default")
				}
				if key == "read_only" {
					reopened.sendWait("\x1b", "JQL")
					reopened.sendWait("n", "DEMO · 10402")
				}
			}
			reopened.stop()
		})
	}
	for _, owner := range []struct{ key, guidance string }{{"api_token", "jira auth login"}, {"aliases", "jira alias set"}} {
		t.Run(owner.key, func(t *testing.T) {
			fixture := t.TempDir()
			path := filepath.Join(fixture, "settings.toml")
			original := []byte(fmt.Sprintf("base_url=%q\nemail='person@example.com'\nsecret_backend='env'\n", server.URL))
			if err := os.WriteFile(path, original, 0o600); err != nil {
				t.Fatal(err)
			}
			terminal := startSettingsAuditTerminal(t, fixture, path, proxy.URL)
			terminal.wait("Settings audit issue")
			terminal.sendWait(",", "Side-by-side preview")
			terminal.selectSetting(owner.key)
			terminal.sendWait("\r", owner.guidance)
			terminal.sendWait("\x13", "No unsaved changes.")
			terminal.stop()
			retained, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(original, retained) {
				t.Fatal("dedicated owner row modified configuration", err)
			}
		})
	}
	mu.Lock()
	defer mu.Unlock()
	if cloudExercised && gatewayAttempts == 0 {
		t.Fatal("changing cloud_id did not adopt scoped gateway routing")
	}
}

type settingsAuditTerminal struct {
	t          *testing.T
	master     *os.File
	slave      *os.File
	before     *term.State
	done       <-chan struct{}
	finished   chan error
	joined     bool
	transcript bytes.Buffer
	bytesRead  int
}

func startSettingsAuditTerminal(t *testing.T, dir, path, proxy string) *settingsAuditTerminal {
	t.Helper()
	master, slave, err := nativepty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 30, Col: 120}); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	process := exec.CommandContext(ctx, jiraBinary(t), "tui", "--config="+path, "--output=human", "--color=never")
	process.Dir = dir
	process.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "XDG_CONFIG_HOME=" + filepath.Join(dir, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(dir, "cache"), "TERM=xterm-256color", "HTTPS_PROXY=" + proxy,
		"JIRA_API_TOKEN=synthetic-settings-audit", "JIRA_TOKEN_SETTINGS_AUDIT=synthetic-settings-reference",
	}
	process.Stdin, process.Stdout, process.Stderr = slave, slave, slave
	if err := process.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	terminal := &settingsAuditTerminal{t: t, master: master, slave: slave, before: before, done: ctx.Done(), finished: make(chan error, 1)}
	go func() { terminal.finished <- process.Wait() }()
	t.Cleanup(func() {
		cancel()
		if !terminal.joined {
			if err := <-terminal.finished; err != nil {
				t.Logf("settings audit joined failed invocation during cleanup: %v", err)
			}
		}
	})
	return terminal
}

func (p *settingsAuditTerminal) sendWait(input, expected string) {
	p.t.Helper()
	p.transcript.Reset()
	if _, err := unix.Write(int(p.master.Fd()), []byte(input)); err != nil {
		p.t.Fatal(err)
	}
	p.wait(expected)
}

func (p *settingsAuditTerminal) selectSetting(key string) {
	p.t.Helper()
	// Prefix the complete key and its following space, avoiding description and
	// substring matches (account_id versus team_account_ids, for example).
	p.sendWait("/^"+key+" \r", "^"+key)
}

func (p *settingsAuditTerminal) wait(expected string) {
	p.t.Helper()
	for {
		buffer := make([]byte, 8192)
		n, err := unix.Read(int(p.master.Fd()), buffer)
		if n > 0 {
			p.transcript.Write(buffer[:n])
			p.bytesRead += n
		}
		if p.bytesRead > 1<<20 {
			p.t.Fatal("settings audit exceeded its transcript bound")
		}
		if err != nil && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EIO) {
			p.t.Fatal(err)
		}
		if strings.Contains(p.transcript.String(), expected) {
			return
		}
		select {
		case err := <-p.finished:
			p.joined = true
			p.t.Fatalf("TUI exited while waiting for %q: %v\n%s", expected, err, &p.transcript)
		case <-p.done:
			p.t.Fatalf("timed out waiting for %q\n%s", expected, &p.transcript)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (p *settingsAuditTerminal) stop() {
	p.t.Helper()
	if _, err := unix.Write(int(p.master.Fd()), []byte{3}); err != nil {
		p.t.Fatal(err)
	}
	select {
	case err := <-p.finished:
		p.joined = true
		if err != nil {
			p.t.Fatal("TUI did not exit successfully", err)
		}
	case <-p.done:
		p.t.Fatal("TUI failed to exit")
	}
	after, err := term.GetState(int(p.slave.Fd()))
	if err != nil || *after != *p.before {
		p.t.Fatal("TUI failed to restore its borrowed terminal", err)
	}
}
