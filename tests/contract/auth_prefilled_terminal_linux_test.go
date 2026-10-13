//go:build linux && nativekeyring

package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/credentials"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// testNativeKeyringPrefilledLogin runs inside the private native session. Each
// case starts with the same synthetic record and observes actual form input,
// verification requests, machine output and the resulting native record.
func testNativeKeyringPrefilledLogin(t *testing.T, invoke func(*testing.T, []string, []terminalAuthStep, int, string, ...string) (frame, string), site, email, token string, originalCalls *atomic.Int32) {
	t.Helper()
	const editedEmail = "edited-fixture@example.invalid"
	editedToken := token + "-edited"
	var editedCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		editedCalls.Add(1)
		account, candidate, ok := r.BasicAuth()
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/myself" || !ok || account != editedEmail || candidate != editedToken {
			t.Error("edited login did not verify the authored credential tuple")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"accountId":"edited:fixture","displayName":"Edited fixture","active":true}`)
	}))
	t.Cleanup(server.Close)
	original := credentials.Record{Version: credentials.Version, Site: site, Email: email, Token: token}
	for _, tc := range []struct {
		name, term  string
		edit, abort bool
	}{
		{name: "rich defaults", term: "xterm-256color"},
		{name: "accessible defaults", term: "dumb"},
		{name: "rich edited identity", term: "xterm-256color", edit: true},
		{name: "accessible edited identity", term: "dumb", edit: true},
		{name: "rich canceled edited identity", term: "xterm-256color", edit: true, abort: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seed, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			nativeKeyringTool(t, string(seed), 0, "store", "--label=CLI editable fixture", "service", "jira-cli", "username", "default")
			// Run before testing cancels t.Context during cleanup; the native
			// tool itself still bounds each operation with its own timeout.
			defer nativeKeyringTool(t, "", 0, "clear", "service", "jira-cli", "username", "default")
			stored := func() string {
				return nativeKeyringTool(t, "", 0, "lookup", "service", "jira-cli", "username", "default")
			}
			before := stored()
			oldCount, editedCount := originalCalls.Load(), editedCalls.Load()
			want := original
			accountID := "terminal:fixture"
			siteInput, emailInput := "\r", "\r"
			if tc.edit {
				want.Site, want.Email, want.Token = server.URL, editedEmail, editedToken
				accountID = "edited:fixture"
				// Rich inputs contain editable text; accessible inputs offer a
				// default and accept a replacement as a fresh line.
				clear := ""
				if tc.term != "dumb" {
					clear = "\x01\x0b" // Home, then delete the existing line.
				}
				siteInput, emailInput = clear+want.Site+"\r", clear+want.Email+"\r"
			}
			tokenInput, exit, code := want.Token+"\r", cli.ExitOK, ""
			if tc.edit {
				exit, code = cli.ExitValidation, cli.CodeConfigInvalid
			}
			if tc.abort {
				tokenInput, exit, code = "\x03", cli.ExitCanceled, "canceled"
			}
			result, _ := invoke(t, []string{"TERM=" + tc.term}, []terminalAuthStep{
				{wait: "Jira site", beforeInput: []string{site}, focused: tc.term != "dumb", focusedValue: site, input: siteInput},
				{wait: "Email", beforeInput: []string{email}, focused: tc.term != "dumb", focusedValue: email, input: emailInput},
				{wait: "API token", passwordReady: true, focused: tc.term != "dumb", input: tokenInput},
			}, exit, code, "auth", "login")
			if tc.abort {
				if stored() != before || originalCalls.Load() != oldCount || editedCalls.Load() != editedCount {
					t.Fatal("canceled identity edits changed native credentials or contacted Jira")
				}
				return
			}
			if tc.edit {
				var output envelope.AuthLoginOutput
				if json.Unmarshal(result.Data, &output) != nil || !output.Verified || output.BaseURL != want.Site || output.Email != want.Email || output.AccountID != accountID || output.Credential.Saved || output.Credential.Attempted || output.Settings.Saved || output.Replaced != "" {
					t.Fatal("conflicting interactive scope lost verification or invented publication")
				}
				if stored() != before || originalCalls.Load() != oldCount || editedCalls.Load() != editedCount+1 {
					t.Fatal("conflicting interactive identity changed credentials or verified the wrong scope")
				}
				return
			}
			var record credentials.Record
			if json.Unmarshal([]byte(stored()), &record) != nil || record != want {
				t.Fatal("native storage did not preserve the verified identity and token")
			}
			var output envelope.AuthLoginOutput
			if json.Unmarshal(result.Data, &output) != nil || output.BaseURL != want.Site || output.Email != want.Email || output.AccountID != accountID {
				t.Fatal("machine result disagrees with the verified and stored identity")
			}
			if originalCalls.Load() != oldCount+1 || editedCalls.Load() != editedCount || output.Replaced != "" {
				t.Fatal("default login changed identity or did not verify exactly once")
			}
		})
	}
}
