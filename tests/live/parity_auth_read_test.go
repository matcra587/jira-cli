//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

var parityAuthPermissionKeys = []string{"BROWSE_PROJECTS", "CREATE_ISSUES", "EDIT_ISSUES", "DELETE_ISSUES", "ADD_COMMENTS", "WORK_ON_ISSUES", "TRANSITION_ISSUES", "LINK_ISSUES"}

func parityAuthStatusData(raw []byte, lane, site string) bool {
	if !jsontext.Value(raw).IsValid() {
		return false
	}
	want := map[string]any{"base_url": site, "email": "fixture@example.invalid", "account_id": "account-1", "display_name": "Fixture User", "token_source": "env"}
	if lane == "before" {
		grants := make(map[string]bool)
		for i, key := range parityAuthPermissionKeys {
			grants[key] = i%2 == 0
		}
		want = map[string]any{"active_profile": "fixture", "profiles": []any{map[string]any{"profile": "fixture", "token_type": "classic", "valid": true, "source": "env", "redacted": "****oken", "remote": map[string]any{"site": site, "myself": map[string]any{"ok": true, "account_id": "account-1", "email": ""}, "permissions": map[string]any{"ok": true, "grants": grants}}}}}
	} else if lane != "after" {
		return false
	}
	expected, _ := json.Marshal(want)
	return parityAuthoringJSON(raw, expected)
}

func TestParityAuthStatusRequiresCompleteFacts(t *testing.T) {
	t.Parallel()
	valid := `{"base_url":"https://example.invalid","email":"fixture@example.invalid","account_id":"account-1","display_name":"Fixture User","token_source":"env"}`
	if !parityAuthStatusData([]byte(valid), "after", "https://example.invalid") {
		t.Fatal("independently authored identity refused")
	}
	for name, invalid := range map[string]string{"foreign_id": strings.ReplaceAll(valid, "account-1", "foreign-2"), "wrong_scope": strings.ReplaceAll(valid, "https://example.invalid", "https://foreign.invalid"), "wrong_source": strings.ReplaceAll(valid, `"token_source":"env"`, `"token_source":"keyring"`), "missing_source": strings.ReplaceAll(valid, `,"token_source":"env"`, ""), "leaked_token": strings.ReplaceAll(valid, `"token_source":"env"`, `"token_source":"env","token":"synthetic-fixture-token"`), "wrong_case": strings.ReplaceAll(valid, "account_id", "Account_ID"), "sparse": `{"account_id":"account-1"}`, "duplicate": strings.ReplaceAll(valid, `"account_id":"account-1"`, `"account_id":"account-1","account_id":"account-1"`)} {
		t.Run(name, func(t *testing.T) {
			if parityAuthStatusData([]byte(invalid), "after", "https://example.invalid") {
				t.Fatal("auth status admitted incomplete, foreign or secret-bearing facts")
			}
		})
	}
}

func TestParityActualBinaryAuthStatus(t *testing.T) {
	lanes := []parityLane{{Name: "before", Binary: os.Getenv("JIRA_PARITY_BEFORE_BINARY"), Digest: os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")}}
	if lanes[0].Binary == "" || lanes[1].Binary == "" {
		t.Skip("requires both admitted binaries")
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		user, token, ok := r.BasicAuth()
		body, err := io.ReadAll(io.LimitReader(r.Body, 65_537))
		valid := err == nil && ok && user == "fixture@example.invalid" && token == "synthetic-fixture-token" && r.Method == "GET" && len(body) == 0
		requests = append(requests, fmt.Sprintf("%s %s authenticated=%t", r.Method, r.URL.RequestURI(), valid))
		if !valid {
			http.Error(w, "unexpected auth wire", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/3/myself":
			_, _ = w.Write([]byte(`{"accountId":"account-1","displayName":"Fixture User","active":true}`))
		case "/rest/api/3/mypermissions":
			permissions := make(map[string]any)
			for i, key := range parityAuthPermissionKeys {
				permissions[key] = map[string]any{"key": key, "havePermission": i%2 == 0}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"permissions": permissions})
		default:
			http.Error(w, "unexpected auth endpoint", 500)
		}
	}))
	defer server.Close()
	for _, mode := range []string{"json", "compact"} {
		for _, lane := range lanes {
			t.Run(mode+"/"+lane.Name, func(t *testing.T) {
				if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
					t.Fatal(err)
				}
				home := t.TempDir()
				config := parityCachePublicConfig(t, home, lane.Name, server.URL)
				before := parityLocalTree(t, home)
				args := []string{"auth", "status"}
				if lane.Name == "before" {
					args = append(args, "--project=PAIR")
				}
				mu.Lock()
				start := len(requests)
				mu.Unlock()
				result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, nil, args...)
				evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
				if result.Exit != 0 || len(result.Stderr) != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
					t.Fatal("status failed or changed owned public files")
				}
				data := result.Stdout
				if mode == "json" {
					parityAuthoringData(t, result.Stdout, lane.Name, "auth.status", 0)
					data = parityOriginalFrameData(result.Stdout)
				}
				if !parityAuthStatusData(data, lane.Name, server.URL) || bytes.Contains(result.Stdout, []byte("synthetic-fixture-token")) {
					t.Fatal("status lost complete lane identity/permission facts or leaked the synthetic token")
				}
				mu.Lock()
				got := append([]string{}, requests[start:]...)
				mu.Unlock()
				want := []string{"GET /rest/api/3/myself authenticated=true"}
				if lane.Name == "before" {
					want = append(want, "GET /rest/api/3/mypermissions?permissions=BROWSE_PROJECTS%2CCREATE_ISSUES%2CEDIT_ISSUES%2CDELETE_ISSUES%2CADD_COMMENTS%2CWORK_ON_ISSUES%2CTRANSITION_ISSUES%2CLINK_ISSUES&projectKey=PAIR authenticated=true")
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("auth wire differs: %v", got)
				}
				evidence.Checks["C021.auth.status.identity."+mode] = append(evidence.Checks["C021.auth.status.identity."+mode], lane.Name)
			})
		}
	}
	saveParityLocal(t, "auth-status-fixture", evidence)
}

func TestParityActualBinaryAuthTokenContracts(t *testing.T) {
	lanes := []parityLane{{Name: "before", Binary: os.Getenv("JIRA_PARITY_BEFORE_BINARY"), Digest: os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")}}
	if lanes[0].Binary == "" || lanes[1].Binary == "" {
		t.Skip("requires both admitted binaries")
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	var mu sync.Mutex
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		http.Error(w, "local token entered HTTP", 500)
	}))
	defer server.Close()
	for _, lane := range lanes {
		modes := []string{"json", "compact"}
		if lane.Name == "after" {
			modes = append(modes, "human")
		}
		for _, mode := range modes {
			t.Run(lane.Name+"/"+mode, func(t *testing.T) {
				if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
					t.Fatal(err)
				}
				home := t.TempDir()
				config := parityCachePublicConfig(t, home, lane.Name, server.URL)
				before := parityLocalTree(t, home)
				result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, nil, "auth", "token")
				evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
				mu.Lock()
				observedCalls := calls
				mu.Unlock()
				if result.Exit != 0 || len(result.Stderr) != 0 || observedCalls != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
					t.Fatal("local token contract entered HTTP, files or failed")
				}
				if lane.Name == "before" {
					data := result.Stdout
					if mode == "json" {
						parityAuthoringData(t, result.Stdout, lane.Name, "auth.token", 0)
						data = parityOriginalFrameData(result.Stdout)
					}
					want := []byte(`{"profile":"fixture","source":"env","backend":"env","valid":true,"redacted":"****oken","expiry":null,"error":"","credential_env":"JIRA_TOKEN_FIXTURE"}`)
					if mode == "compact" {
						want = []byte(`{"profile":"fixture","source":"env","backend":"env","valid":true,"redacted":"****oken","error":"","credential_env":"JIRA_TOKEN_FIXTURE"}`)
					}
					if !parityAuthoringJSON(data, want) || bytes.Contains(result.Stdout, []byte("synthetic-fixture-token")) {
						t.Fatal("legacy diagnostics lost redaction, native null omission or complete local facts")
					}
				} else {
					if string(result.Stdout) != "synthetic-fixture-token\n" || strings.Contains(string(result.Stdout), "\"data\"") {
						t.Fatal("current raw-token contract lost exact bytes or gained an envelope")
					}
					evidence.Checks["C023.auth.token.raw."+mode] = []string{"after"}
				}
			})
		}
	}
	// Legacy diagnostics and current raw secrets are distinct contracts. Both
	// receipts are retained, but no equivalent-behavior pair is credited.
	saveParityLocal(t, "auth-token-local", evidence)
}
