//go:build linux

package root

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/1password/onepassword-sdk-go"
	"golang.org/x/sys/unix"

	adapteronepassword "github.com/matcra587/jira-cli/internal/adapters/onepassword"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

type loginSelectionClient struct{ core.JiraClient }

func (loginSelectionClient) CurrentUser(context.Context) (core.UserRead, error) {
	return core.UserRead{User: &jira.UserDetails{AccountID: jira.NullableOf("interactive-account"), EmailAddress: jira.NullableNull[string]()}}, nil
}

func TestInteractiveLoginPreservesSDKSelection(t *testing.T) {
	const token = "synthetic-interactive-sdk-token"
	if os.Getenv("JIRA_TEST_LOGIN_SELECTION_CHILD") == "1" {
		path := os.Getenv("JIRA_TEST_LOGIN_SELECTION_CONFIG")
		config.SetFlagLayer(config.FlagLayer{Path: path})
		fixture := &sdkAuthFixture{item: sdk.Item{ID: "item-id", VaultID: "vault-id", Title: "Fixture Item", Fields: []sdk.ItemField{{ID: "unmanaged", Value: "retain native field"}}}}
		store := adapteronepassword.New(adapteronepassword.Config{Account: "desktop.example.invalid", Vault: "vault-id", Item: "item-id", LookupEnv: func(string) (string, bool) { return "synthetic-sdk-service-token", true }, ClientFactory: func(context.Context, adapteronepassword.ClientOptions) (adapteronepassword.Client, error) {
			return fixture, nil
		}})
		deps := defaultDeps()
		calls := 0
		deps.IdentitySaveClientFor = func(site, email, candidate, cloud string) (core.JiraClient, error) {
			if site != "https://example.invalid" || email != "public@example.invalid" || candidate != token || cloud != "" {
				t.Fatal("interactive verification changed candidate")
			}
			return loginSelectionClient{}, nil
		}
		deps.LoginCredentials = func(ctx context.Context, selected core.CredentialLocation) (core.CredentialStore, error) {
			calls++
			if ctx.Err() != nil || selected.Backend != core.CredentialBackendOnePassword || selected.SDKAccount != "desktop.example.invalid" || selected.Vault != "vault-id" || selected.Item != "item-id" || selected.Account != "default" {
				t.Fatal("interactive form discarded SDK selection")
			}
			return store, nil
		}
		program, sink := newProgram(deps, os.Stdout, os.Stderr)
		program.Root.SetIn(os.Stdin)
		if exit := execute(t.Context(), program, sink, []string{"--config", path, "--output=json", "auth", "login", "--site=https://example.invalid", "--email=public@example.invalid"}); exit != 0 {
			t.Fatalf("interactive SDK login exit=%d", exit)
		}
		if calls != 1 || fixture.puts != 1 || fixture.creates != 0 {
			t.Fatal("interactive SDK publication skipped or replayed")
		}
		file, err := config.ReadFile(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		id, err := file.Setting("account_id")
		if err != nil || id.Value != "interactive-account" {
			t.Fatal("interactive SDK login did not publish actual verified ID")
		}
		return
	}
	master, slave := openPromptTerminal(t)
	before, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "selected.toml")
	if err := os.WriteFile(path, []byte("base_url='https://example.invalid'\nemail='public@example.invalid'\nsecret_backend='1password'\nonepassword_account='desktop.example.invalid'\nonepassword_vault='vault-id'\nonepassword_item='item-id'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, self, "-test.run=^TestInteractiveLoginPreservesSDKSelection$")
	child.Env = []string{"PATH=" + os.Getenv("PATH"), "TERM=dumb", "NO_COLOR=1", "JIRA_NO_UPDATE_CHECK=1", "JIRA_TEST_LOGIN_SELECTION_CHILD=1", "JIRA_TEST_LOGIN_SELECTION_CONFIG=" + path}
	child.Stdin, child.Stderr = slave, slave
	var stdout, screen bytes.Buffer
	child.Stdout = &stdout
	var mu sync.Mutex
	changed, drained := make(chan struct{}, 1), make(chan struct{})
	go func() {
		defer close(drained)
		buffer := make([]byte, 4096)
		for {
			n, err := master.Read(buffer)
			if n > 0 {
				mu.Lock()
				_, _ = screen.Write(buffer[:n])
				mu.Unlock()
				select {
				case changed <- struct{}{}:
				default:
				}
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() { _ = slave.Close(); _ = master.Close(); <-drained }()
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	exited := make(chan struct{})
	go func() { finished <- child.Wait(); close(exited) }()
	waited := false
	defer func() {
		if !waited {
			cancel()
			<-finished
		}
	}()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	observation := promptObservation{marker: "API token", snapshot: func() string { mu.Lock(); defer mu.Unlock(); return screen.String() }, changed: changed, expired: timer.C, exited: exited, drained: drained}
	if err := observation.wait(); err != nil {
		t.Fatal(err)
	}
	for {
		mode, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
		if err != nil {
			t.Fatal(err)
		}
		if mode.Lflag&unix.ECHO == 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native password masking did not start")
		case <-time.After(time.Millisecond):
		}
	}
	if _, err := io.WriteString(master, token+"\r"); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		waited = true
		t.Fatalf("actual interactive SDK child failed: %v stdout=%s", err, &stdout)
	}
	waited = true
	after, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil || *after != *before {
		t.Fatal("interactive SDK form failed to restore native terminal modes")
	}
	mu.Lock()
	visible := screen.String()
	mu.Unlock()
	if strings.Contains(stdout.String()+visible, token) {
		t.Fatal("interactive SDK login exposed candidate token")
	}
	line, _, ok := strings.Cut(stdout.String(), "\n")
	if !ok {
		t.Fatal("interactive SDK child omitted machine receipt")
	}
	var frame struct {
		OK   bool                     `json:"ok"`
		Data envelope.AuthLoginOutput `json:"data"`
	}
	if json.Unmarshal([]byte(line), &frame) != nil || !frame.OK || !frame.Data.Verified || !frame.Data.Credential.Saved || frame.Data.Credential.Backend != core.CredentialBackendOnePassword || !frame.Data.Settings.Saved {
		t.Fatal("actual interactive SDK login has false publication receipt")
	}
}
