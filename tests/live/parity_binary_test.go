//go:build live

package live

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

func parityFixtureRun(t *testing.T, lane, binary, site string, extra []string, args ...string) (liveFrame, []byte, int) {
	t.Helper()
	return parityFixtureRunAt(t, lane, binary, site, t.TempDir(), extra, args...)
}

func parityFixtureRunAt(t *testing.T, lane, binary, site, home string, extra []string, args ...string) (liveFrame, []byte, int) {
	t.Helper()
	return parityFixtureRunConfigAt(t, lane, binary, site, home, "", extra, args...)
}

func parityFixtureRunConfigAt(t *testing.T, lane, binary, site, home, publicConfig string, extra []string, args ...string) (liveFrame, []byte, int) {
	t.Helper()
	result := parityFixtureProcessAt(t, lane, binary, site, home, parityFixtureInput{Config: publicConfig}, extra, args...)
	var frame liveFrame
	if json.Unmarshal(result.Stdout, &frame) != nil {
		t.Fatalf("selected fixture binary returned no frame: exit %d, stdout %s, stderr %s", result.Exit, result.Stdout, result.Stderr)
	}
	return frame, result.Stdout, result.Exit
}

type parityFixtureInput struct {
	Config           string
	Stdin            []byte
	Output           string
	WorkingDirectory string
}

type parityFixtureResult struct {
	Argv   []string
	Stdout []byte
	Stderr []byte
	Exit   int
}

func parityFixtureProcessAt(t *testing.T, lane, binary, site, home string, input parityFixtureInput, extra []string, args ...string) parityFixtureResult {
	t.Helper()
	argv := append([]string(nil), args...)
	if lane == "before" {
		config := input.Config
		if config == "" {
			config = filepath.Join(home, "legacy.toml")
			public := fmt.Sprintf("default_profile=\"fixture\"\n[[profiles]]\nname=\"fixture\"\nbase_url=%q\nemail=\"fixture@example.invalid\"\nsecret_backend=\"env\"\n", site)
			if err := os.WriteFile(config, []byte(public), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		argv = append([]string{"--config", config, "--profile", "fixture"}, argv...)
	} else if input.Config != "" {
		argv = append([]string{"--config", input.Config}, argv...)
	}
	mode := input.Output
	if mode == "" {
		mode = "json"
	}
	// Runtime controls must remain flags when the caller supplies -- before a
	// literal positional value. Prefixing also avoids mistaking a flag's value
	// for the command's positional terminator.
	argv = append([]string{"--output=" + mode, "--no-input", "--timeout=5s"}, argv...)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, argv...)
	// Synthetic local commands, including project-scoped exports, must never
	// select the repository or the user's current directory as their workspace.
	cmd.Dir = home
	if input.WorkingDirectory != "" {
		relative, err := filepath.Rel(home, input.WorkingDirectory)
		if err != nil || !filepath.IsLocal(relative) {
			t.Fatal("synthetic working directory must remain inside its owned home")
		}
		cmd.Dir = input.WorkingDirectory
	}
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "APPDATA=" + home, "LOCALAPPDATA=" + home, "XDG_CONFIG_HOME=" + home, "XDG_CACHE_HOME=" + home, "JIRA_BASE_URL=" + site, "JIRA_EMAIL=fixture@example.invalid", "JIRA_API_TOKEN=synthetic-fixture-token", "JIRA_TOKEN_FIXTURE=synthetic-fixture-token", "JIRA_KEYRING=false", "JIRA_SECRET_BACKEND=env", "JIRA_NO_UPDATE_CHECK=1", "NO_COLOR=1", "TERM=dumb"}
	if value := os.Getenv("PATHEXT"); value != "" {
		cmd.Env = append(cmd.Env, "PATHEXT="+value)
	}
	cmd.Env = append(cmd.Env, extra...)
	cmd.Stdin = bytes.NewReader(input.Stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	exit := 0
	if err := cmd.Run(); err != nil {
		var exited *exec.ExitError
		if errors.As(err, &exited) {
			exit = exited.ExitCode()
		} else {
			t.Fatal("selected fixture binary did not start: ", err)
		}
	}
	if ctx.Err() != nil {
		t.Fatalf("selected fixture binary exceeded its owned deadline: %v; stderr %s", ctx.Err(), stderr.String())
	}
	return parityFixtureResult{Argv: argv, Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Exit: exit}
}

func TestParityActualBinaryReads(t *testing.T) {
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			if err := parityFingerprint(lane.binary, lane.digest); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var requests []string
			name := "Relates"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				requests = append(requests, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				if user, password, ok := r.BasicAuth(); !ok || user != "fixture@example.invalid" || password != "synthetic-fixture-token" {
					http.Error(w, "wrong synthetic binding", http.StatusUnauthorized)
					return
				}
				switch r.Method + " " + r.URL.Path {
				case "GET /rest/api/3/issue/PAIR-1":
					_, _ = w.Write([]byte(`{"id":"10001","key":"PAIR-1","fields":{"attachment":[],"issuelinks":[]}}`))
				case "GET /rest/api/3/issue/PAIR-1/comment":
					_, _ = w.Write([]byte(`{"startAt":0,"maxResults":50,"total":0,"comments":[]}`))
				case "GET /rest/api/3/issue/PAIR-1/transitions":
					_, _ = w.Write([]byte(parityWorkflowFixture))
				case "GET /rest/api/3/issue/PAIR-1/watchers":
					_, _ = w.Write([]byte(`{"watchCount":1,"isWatching":true,"watchers":[{"accountId":"owned","displayName":"Fixture","active":true,"accountType":"atlassian"}]}`))
				case "GET /rest/api/3/issueLinkType":
					_, _ = fmt.Fprintf(w, `{"issueLinkTypes":[{"id":"10000","name":%q,"inward":"relates to","outward":"relates to"}]}`, name)
				default:
					http.Error(w, "unexpected fixture read", http.StatusBadRequest)
				}
			}))
			defer server.Close()
			home := t.TempDir()
			invoke := func(expected string, args ...string) []byte {
				mu.Lock()
				start := len(requests)
				mu.Unlock()
				frame, stdout, exit := parityFixtureRunAt(t, lane.name, lane.binary, server.URL, home, nil, args...)
				if err := parityValidateFrame(stdout, lane.name, parityExpectedOperation(lane.name, args, exit), exit); err != nil || exit != 0 || !frame.OK {
					t.Fatalf("actual read failed: %v, exit %d, %s", err, exit, stdout)
				}
				mu.Lock()
				observed := append([]string(nil), requests[start:]...)
				mu.Unlock()
				if expected == "" {
					if len(observed) != 0 {
						t.Fatal("warm cache read contacted server")
					}
				} else if len(observed) != 1 || observed[0] != expected {
					t.Fatalf("actual read did not enter expected native route: %v", observed)
				}
				return stdout
			}
			for _, tc := range []struct{ family, member, route string }{{"attachment", "attachments", "GET /rest/api/3/issue/PAIR-1"}, {"comment", "comments", "GET /rest/api/3/issue/PAIR-1/comment"}, {"link", "links", "GET /rest/api/3/issue/PAIR-1"}} {
				stdout := invoke(tc.route, "issue", tc.family, "list", "PAIR-1")
				data, err := parityReadData(stdout, lane.name, tc.family, "PAIR-1")
				if err != nil || !parityEmptyCollection(data, tc.member) {
					t.Fatalf("actual %s single-key layout differs from declared projection: %v", tc.family, err)
				}
			}
			transitionArgs := []string{"issue", "transition", "PAIR-1"}
			if lane.name == "after" {
				transitionArgs = []string{"issue", "transition", "list", "PAIR-1"}
			}
			transitionReply := invoke("GET /rest/api/3/issue/PAIR-1/transitions", transitionArgs...)
			transitionData, err := parityReadData(transitionReply, lane.name, "transition", "PAIR-1")
			if err != nil || !parityTransitionRead(transitionData, json.RawMessage(parityWorkflowFixture), lane.name) {
				t.Fatal("actual workflow read changed its explicit native/legacy projection")
			}
			stdout := invoke("GET /rest/api/3/issue/PAIR-1/watchers", "issue", "watchers", "list", "PAIR-1")
			data, err := parityReadData(stdout, lane.name, "watchers", "PAIR-1")
			want := envelope.IssueWatchersListData{Watchers: jira.NullableOf([]envelope.WatcherView{{AccountID: "owned", DisplayName: "Fixture", Active: jira.NullableOf(true), AccountType: "atlassian"}}), WatchCount: jira.NullableOf(int64(1)), IsWatching: jira.NullableOf(true)}
			if err != nil || !parityWatchersMatch(data, lane.name, want) {
				t.Fatal("actual watcher projection differs from independent visible user")
			}
			stdout = invoke("GET /rest/api/3/issueLinkType", "issue", "link", "types")
			initial, err := parityFreshLinkTypes(stdout, lane.name)
			wantType := storedLinkType{ID: "10000", Name: "Relates", Inward: "relates to", Outward: "relates to"}
			if err != nil || len(initial) != 1 || initial[0] != wantType {
				t.Fatal("initial native inventory or fresh provenance changed")
			}
			stdout = invoke("", "issue", "link", "types")
			warm, err := parityLinkTypes(stdout, lane.name, true)
			if err != nil || len(warm) != 1 || warm[0] != wantType {
				t.Fatal("actual warm cache receipt lost its cached provenance or native inventory")
			}
			mu.Lock()
			name = "Updated relates"
			mu.Unlock()
			stdout = invoke("GET /rest/api/3/issueLinkType", "issue", "link", "types", "--refresh")
			types, err := parityFreshLinkTypes(stdout, lane.name)
			if err != nil || len(types) != 1 || types[0] != (storedLinkType{ID: "10000", Name: "Updated relates", Inward: "relates to", Outward: "relates to"}) {
				t.Fatal("actual refresh retained stale inventory or lost native provenance")
			}
		})
	}
}

func TestParityActualBinaryLifecycle(t *testing.T) {
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires explicitly selected actual binary")
			}
			if err := parityFingerprint(lane.binary, lane.digest); err != nil {
				t.Fatal(err)
			}
			const initial = `{"project":{"id":"10"},"issuetype":{"id":"20"},"summary":"authored","labels":["owned"],"description":` + parityDescription + `}`
			var mu sync.Mutex
			var requests []string
			var stored json.RawMessage
			var deleted bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				requests = append(requests, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				if user, password, ok := r.BasicAuth(); !ok || user != "fixture@example.invalid" || password != "synthetic-fixture-token" {
					http.Error(w, "wrong synthetic binding", http.StatusUnauthorized)
					return
				}
				switch r.Method + " " + r.URL.Path {
				case "GET /rest/api/3/issue/createmeta/10/issuetypes/20":
					_, _ = w.Write([]byte(`{"startAt":0,"maxResults":50,"total":3,"fields":[{"fieldId":"summary","name":"Summary","schema":{"type":"string"}},{"fieldId":"description","name":"Description","schema":{"type":"string"}},{"fieldId":"labels","name":"Labels","schema":{"type":"array","items":"string"}}]}`))
				case "GET /rest/api/3/issue/PAIR-1/editmeta":
					_, _ = w.Write([]byte(`{"fields":{"summary":{"name":"Summary","schema":{"type":"string"}}}}`))
				case "POST /rest/api/3/issue":
					body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024+1))
					if err != nil || len(body) > 64*1024 || !nativeMetadataMatches(body, json.RawMessage(`{"fields":`+initial+`}`), nil) || stored != nil {
						http.Error(w, "unexpected create payload or replay", 400)
						return
					}
					stored = json.RawMessage(initial)
					w.WriteHeader(201)
					_, _ = w.Write([]byte(`{"id":"10001","key":"PAIR-1"}`))
				case "GET /rest/api/3/issue/PAIR-1":
					if stored == nil || deleted {
						w.WriteHeader(404)
						_, _ = w.Write([]byte(`{"errorMessages":["not found"],"errors":{}}`))
						return
					}
					_, _ = fmt.Fprintf(w, `{"id":"10001","key":"PAIR-1","fields":%s}`, stored)
				case "PUT /rest/api/3/issue/PAIR-1":
					request, err := io.ReadAll(io.LimitReader(r.Body, 64*1024+1))
					if err != nil || len(request) > 64*1024 || !nativeMetadataMatches(request, json.RawMessage(`{"fields":{"summary":"edited"}}`), nil) || stored == nil || deleted {
						http.Error(w, "unexpected edit payload", 400)
						return
					}
					stored = json.RawMessage(strings.Replace(string(stored), `"authored"`, `"edited"`, 1))
					w.WriteHeader(204)
				case "DELETE /rest/api/3/issue/PAIR-1":
					if stored == nil || deleted {
						http.Error(w, "unexpected delete replay", 400)
						return
					}
					deleted = true
					w.WriteHeader(204)
				default:
					http.Error(w, "unexpected fixture request "+r.Method+" "+r.URL.RequestURI(), 400)
				}
			}))
			defer server.Close()
			input := filepath.Join(t.TempDir(), "create.json")
			if err := os.WriteFile(input, []byte(`{"fields":`+initial+`}`), 0o600); err != nil {
				t.Fatal(err)
			}
			invoke := func(extra []string, args ...string) liveFrame {
				frame, stdout, exit := parityFixtureRun(t, lane.name, lane.binary, server.URL, extra, args...)
				if err := parityValidateFrame(stdout, lane.name, strings.Join(args[:2], "."), exit); err != nil {
					t.Fatalf("invalid actual fixture envelope: %v; %s", err, stdout)
				}
				if exit != 0 || !frame.OK {
					t.Fatalf("actual %s fixture operation failed: exit %d; %s", lane.name, exit, stdout)
				}
				if (args[1] == "edit" || args[1] == "delete") && !parityWriteTarget(frame, lane.name, "PAIR-1") {
					t.Fatal("actual write lost its lane-specific target receipt")
				}
				return frame
			}
			invoke(nil, "issue", "create", "--json-input", input, "--dry-run")
			mu.Lock()
			count := len(requests)
			mu.Unlock()
			if count != 0 {
				t.Fatal("create preview contacted fixture")
			}
			created := invoke(nil, "issue", "create", "--json-input", input, "--verify")
			if created.Data.Issue.ID != "10001" || created.Data.Issue.Key != "PAIR-1" {
				t.Fatal("actual create lost fixture acknowledgement")
			}
			if lane.name == "after" && !parityCurrentCreateAcknowledged(created, created.Data.Issue) {
				t.Fatal("actual current create lost its stronger acknowledgement contract")
			}
			mu.Lock()
			preEdit := append(json.RawMessage(nil), stored...)
			count = len(requests)
			mu.Unlock()
			invoke(nil, "issue", "edit", "PAIR-1", "--summary", "preview", "--dry-run")
			blocked, stdout, exit := parityFixtureRun(t, lane.name, lane.binary, server.URL, []string{"JIRA_READ_ONLY=true"}, "issue", "edit", "PAIR-1", "--summary", "blocked")
			code := "read_only_blocked"
			if lane.name == "before" {
				code = "read_only"
			}
			if parityValidateFrame(stdout, lane.name, "issue.edit", exit) != nil || exit != 3 || blocked.OK || len(blocked.Errors) != 1 || blocked.Errors[0].Code != code {
				t.Fatalf("actual read-only refusal changed: %s", stdout)
			}
			mu.Lock()
			unchanged := len(requests) == count && bytes.Equal(stored, preEdit)
			mu.Unlock()
			if !unchanged {
				t.Fatal("preview or refusal contacted fixture or changed state")
			}
			invoke(nil, "issue", "edit", "PAIR-1", "--summary", "edited", "--verify")
			mu.Lock()
			postEdit := append(json.RawMessage(nil), stored...)
			mu.Unlock()
			if !parityUnchangedFields(postEdit, preEdit, "summary") {
				t.Fatal("actual edit changed unrelated fields")
			}
			invoke(nil, "issue", "delete", "PAIR-1", "--force")
			mu.Lock()
			defer mu.Unlock()
			if !deleted || slicesCount(requests, "POST /rest/api/3/issue") != 1 || slicesCount(requests, "PUT /rest/api/3/issue/PAIR-1") != 1 || slicesCount(requests, "DELETE /rest/api/3/issue/PAIR-1") != 1 {
				t.Fatal("actual commands did not perform exactly one create, edit and delete")
			}
		})
	}
}

func slicesCount(values []string, want string) int {
	count := 0
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
}

// Both actual binaries must consume the controlled native response. In
// particular, the legacy no-client fallback can return exit zero and a key
// without making any request; that is not successful view parity.
func TestParityActualBinaryView(t *testing.T) {
	before := os.Getenv("JIRA_PARITY_BEFORE_BINARY")
	after := os.Getenv("JIRA_LIVE_BINARY")
	if before == "" || after == "" {
		t.Skip("actual paired binary fixtures require explicit selected binaries")
	}
	const native = `{"id":"10001","key":"PAIR-1","fields":{"summary":"native <&> summary","description":{"type":"doc","version":1,"content":[]},"status":{"id":"1","name":"Open"}}}`
	var mu sync.Mutex
	var requests []string
	var competingRequests atomic.Int32
	competing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		competingRequests.Add(1)
		http.Error(w, "foreign site", 400)
	}))
	defer competing.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issue/PAIR-1" {
			http.Error(w, "unexpected fixture request", http.StatusBadRequest)
			return
		}
		if user, password, ok := r.BasicAuth(); !ok || user != "fixture@example.invalid" || password != "synthetic-fixture-token" {
			http.Error(w, "wrong synthetic binding", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(native))
	}))
	defer server.Close()
	for _, lane := range []struct{ name, binary, digest string }{
		{"before", before, os.Getenv("JIRA_PARITY_BEFORE_SHA256")},
		{"after", after, os.Getenv("JIRA_PARITY_AFTER_SHA256")},
	} {
		t.Run(lane.name, func(t *testing.T) {
			if err := parityFingerprint(lane.binary, lane.digest); err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			argv := []string{"issue", "view", "PAIR-1", "--fields=summary,description,status", "--output=json", "--no-input", "--timeout=5s"}
			if lane.name == "before" {
				config := filepath.Join(home, "legacy.toml")
				public := fmt.Sprintf("default_profile=\"fixture\"\n[[profiles]]\nname=\"fixture\"\nbase_url=%q\nemail=\"fixture@example.invalid\"\nsecret_backend=\"env\"\n", server.URL)
				if err := os.WriteFile(config, []byte(public), 0o600); err != nil {
					t.Fatal(err)
				}
				argv = append([]string{"--config", config}, argv...)
			}
			mu.Lock()
			start := len(requests)
			mu.Unlock()
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, lane.binary, argv...)
			cmd.Env = []string{
				"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home,
				"APPDATA=" + home, "LOCALAPPDATA=" + home, "XDG_CONFIG_HOME=" + home, "XDG_CACHE_HOME=" + home,
				"JIRA_BASE_URL=" + server.URL, "JIRA_EMAIL=fixture@example.invalid", "JIRA_API_TOKEN=synthetic-fixture-token",
				"JIRA_TOKEN_FIXTURE=synthetic-fixture-token",
				"JIRA_KEYRING=false", "JIRA_SECRET_BACKEND=env", "JIRA_NO_UPDATE_CHECK=1", "NO_COLOR=1", "TERM=dumb",
			}
			if value := os.Getenv("PATHEXT"); value != "" {
				cmd.Env = append(cmd.Env, "PATHEXT="+value)
			}
			if lane.name == "before" {
				cmd.Env = append(cmd.Env, "JIRA_DEFAULT_PROFILE=foreign", "JIRA_PROFILE_FIXTURE_BASE_URL="+competing.URL, "JIRA_PROFILE_FOREIGN_BASE_URL="+competing.URL)
				cmd.Env = append(parityLegacyEnvironment(cmd.Env), "JIRA_TOKEN_FIXTURE=synthetic-fixture-token")
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("selected %s binary did not execute view: %v; stdout %s; stderr %s", lane.name, err, stdout.String(), stderr.String())
			}
			if err := parityValidateFrame(stdout.Bytes(), lane.name, "issue.view", 0); err != nil {
				t.Fatal("actual view envelope failed: ", err)
			}
			mu.Lock()
			observed := append([]string(nil), requests[start:]...)
			mu.Unlock()
			if len(observed) != 1 || observed[0] != "GET /rest/api/3/issue/PAIR-1" {
				t.Fatalf("selected binary did not perform the exact native read: %v", observed)
			}
			if competingRequests.Load() != 0 {
				t.Fatal("inherited legacy profile redirected the fixture request")
			}
			var frame liveFrame
			if json.Unmarshal(stdout.Bytes(), &frame) != nil || !frame.OK || frame.Data.Issue.ID != "10001" || frame.Data.Issue.Key != "PAIR-1" {
				t.Fatal("selected binary lost the controlled native identity")
			}
			var fields struct {
				Summary     string
				Description map[string]json.RawMessage
				Status      map[string]json.RawMessage
			}
			if json.Unmarshal(frame.Data.Issue.Fields, &fields) != nil || fields.Summary != "native <&> summary" || string(fields.Status["name"]) != `"Open"` || string(fields.Description["type"]) != `"doc"` || string(fields.Description["version"]) != "1" {
				t.Fatal("selected binary changed independent common field values")
			}
			if lane.name == "after" {
				if string(fields.Status["id"]) != `"1"` || string(fields.Description["content"]) != "[]" {
					t.Fatal("current view lost native status identity or explicit empty ADF content")
				}
			} else if _, found := fields.Status["id"]; found || strings.Contains(string(frame.Data.Issue.Fields), `"content"`) {
				t.Fatal("legacy fixture behavior changed; reassess the declared native-preservation improvement")
			}
		})
	}
}
