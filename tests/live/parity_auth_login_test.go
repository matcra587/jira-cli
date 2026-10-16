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
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	koanftoml "github.com/knadh/koanf/parsers/toml"
)

func parityAuthLoginData(raw []byte, lane, site, path string, preview, unchanged bool) bool {
	if !jsontext.Value(raw).IsValid() {
		return false
	}
	want := map[string]any{"profile": "fixture", "auth_type": "token", "token_type": "classic", "scoped": false, "secret_backend": "env", "onepassword_account": "", "stored_secret": false, "verified": true, "skip_verify": false, "credential_env": "JIRA_TOKEN_FIXTURE", "account_id": "account-1", "display_name": "Fixture User", "boards_cached": 0}
	if lane == "after" {
		status := "saved"
		if preview {
			status = "prepared"
		} else if unchanged {
			status = "unchanged"
		}
		want = map[string]any{
			"dry_run": preview, "base_url": site, "email": "fixture@example.invalid", "account_id": "account-1", "display_name": "Fixture User", "verified": true,
			"credential": map[string]any{"backend": "env", "status": "external", "attempted": false, "saved": false, "reference": "JIRA_TOKEN_FIXTURE"},
			"settings":   map[string]any{"path": path, "would_change": !unchanged, "changed": !preview && !unchanged, "created": false, "saved": !preview, "status": status},
		}
	} else if lane != "before" || preview {
		return false
	}
	expected, _ := json.Marshal(want)
	return parityAuthoringJSON(raw, expected)
}

func parityAuthLoginSettings(raw []byte, lane, site, home string) bool {
	if bytes.Contains(raw, []byte("synthetic-fixture-token")) {
		return false
	}
	settings, err := koanftoml.Parser().Unmarshal(raw)
	if err != nil {
		return false
	}
	if lane == "after" {
		want := map[string]any{"theme": "dark", "base_url": site, "email": "fixture@example.invalid", "cloud_id": "", "secret_backend": "env", "api_token_env": "JIRA_TOKEN_FIXTURE", "account_id": "account-1", "account_id_site": site, "account_id_email": "fixture@example.invalid", "account_id_cloud_id": ""}
		return reflect.DeepEqual(settings, want)
	}
	if lane != "before" || settings["default_profile"] != "fixture" {
		return false
	}
	// Check the whole selected native profile, including all empty fields and
	// historical defaults. No production configuration loader participates.
	profiles, err := json.Marshal(settings["profiles"])
	// The legacy TOML encoder omits the nil team_account_ids slice.
	want := fmt.Sprintf(`[{"name":"fixture","base_url":%q,"auth_type":"token","email":"fixture@example.invalid","default_project":"","default_issue_type":"","default_board":"","refresh_interval":30,"timeout":30,"workday_seconds":28800,"secret_backend":"env","cloud_id":"","onepassword_account":"","vault":"","item":"","account_id":"account-1","read_only":false,"editor":""}]`, site)
	if err != nil || !parityAuthoringJSON(profiles, []byte(want)) {
		return false
	}
	wantSettings := map[string]any{"default_profile": "fixture", "queries_path": filepath.Join(home, "jira-cli", "queries"), "editor": "", "profiles": json.RawMessage(want), "theme": map[string]any{"name": "", "path": ""}, "aliases": map[string]any{}, "tui": map[string]any{"refresh_interval": 60, "default_tab": "issues", "tabs": []string{"issues", "epics", "search", "activity"}, "preview": "", "preview_size": 0, "default_lens": "", "icons": ""}}
	actual, err := json.Marshal(settings)
	expected, marshalErr := json.Marshal(wantSettings)
	return err == nil && marshalErr == nil && parityAuthoringJSON(actual, expected)
}

func parityAuthLoginBoardCache(raw []byte, started, finished time.Time) bool {
	var data map[string]json.RawMessage
	if !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &data) != nil {
		return false
	}
	var fetched string
	if json.Unmarshal(data["fetched_at"], &fetched) != nil {
		return false
	}
	stamp, err := time.Parse(time.RFC3339, fetched)
	if err != nil || stamp.IsZero() || stamp.Before(started.Truncate(time.Second)) || stamp.After(finished) {
		return false
	}
	delete(data, "fetched_at")
	rest, err := json.Marshal(data)
	return err == nil && parityAuthoringJSON(rest, []byte(`{"ttl_seconds":2419200,"truncated":false,"truncated_reason":"","pages_fetched":1,"items":[]}`))
}

func parityAuthLoginCachePublication(raw []byte, home, site, path string, started, finished time.Time) bool {
	items, published, valid := parityMetadataLegacyWrapper(raw, home, site, "boards", path)
	return valid && !published.Before(started) && !published.After(finished) && parityAuthLoginBoardCache(items, started, finished)
}

func parityAuthLoginInputRefusal(raw []byte, lane string) bool {
	var frame map[string]json.RawMessage
	if !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &frame) != nil || !bytes.Equal(frame["data"], []byte("null")) {
		return false
	}
	want := `[{"code":"flag_value_invalid","message":"the environment backend cannot store a supplied token; configure JIRA_API_TOKEN or api_token_env and run login without --with-token","hint":"That value isn't the type the flag expects — run the command with ` + "`--help`" + ` to see its format.","field":"with-token"}]`
	if lane == "before" {
		want = `[{"type":"validation","code":"validation_failed","message":"validation: the env backend reads the credential from the profile's JIRA_TOKEN_* environment variable at run time; do not supply a token to store","hint":"Check the values you passed and try again.","retryable":false}]`
	} else if lane != "after" {
		return false
	}
	return parityAuthoringJSON(frame["errors"], []byte(want))
}

func parityAuthLoginFiles(t *testing.T, home, lane, site, config, step string, before map[string]parityLocalFile, started, finished time.Time) {
	t.Helper()
	after := parityLocalTree(t, home)
	if step == "preview" {
		if !reflect.DeepEqual(before, after) {
			t.Fatal("login preview changed owned files")
		}
	} else {
		raw, err := os.ReadFile(config)
		if err != nil || !parityAuthLoginSettings(raw, lane, site, home) {
			t.Fatalf("login public settings differ: error%v public%s", err, raw)
		}
		configRelative, err := filepath.Rel(home, config)
		if err != nil {
			t.Fatal(err)
		}
		_, cache := parityMetadataLegacyLocation(home, site, "boards")
		cacheRelative, err := filepath.Rel(home, cache)
		if err != nil {
			t.Fatal(err)
		}
		files := 0
		for name, entry := range after {
			if entry.Mode.IsRegular() {
				files++
				if name != configRelative && (lane != "before" || name != cacheRelative) {
					t.Fatal("login published an unexpected file: ", name)
				}
			} else if !entry.Mode.IsDir() {
				t.Fatal("login published a nonregular member: ", name)
			}
		}
		wantFiles := 1
		if lane == "before" {
			wantFiles = 2
			cacheRaw, err := os.ReadFile(cache)
			if err != nil || !parityAuthLoginCachePublication(cacheRaw, home, site, cache, started, finished) || step == "repeat" && before[cacheRelative] == after[cacheRelative] {
				t.Fatalf("legacy login cache differs: error%v public%s", err, cacheRaw)
			}
		} else if step == "repeat" && !reflect.DeepEqual(before, after) {
			t.Fatal("unchanged current login changed owned files")
		}
		if files != wantFiles {
			t.Fatal("login published an incomplete file set")
		}
	}
}

func TestParityActualBinaryAuthLoginEnvironment(t *testing.T) {
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
			http.Error(w, "unexpected login wire", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/3/myself":
			_, _ = w.Write([]byte(`{"accountId":"account-1","displayName":"Fixture User","active":true}`))
		case "/rest/agile/1.0/board":
			_, _ = w.Write([]byte(`{"isLast":true,"startAt":0,"maxResults":50,"total":0,"values":[]}`))
		default:
			http.Error(w, "unexpected login endpoint", 500)
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
				extra := []string{}
				if lane.Name == "after" {
					// Use a public reference, never a persisted token. Remove the
					// generic override so this actually verifies the selected name.
					public := "theme='dark'\nsecret_backend='env'\napi_token_env='JIRA_TOKEN_FIXTURE'\n"
					if err := os.WriteFile(config, []byte(public), 0o600); err != nil {
						t.Fatal(err)
					}
					extra = []string{"JIRA_API_TOKEN=", "JIRA_BASE_URL=", "JIRA_EMAIL="}
				}
				steps := []string{"stdin_refusal", "save", "repeat"}
				if lane.Name == "after" {
					steps = []string{"stdin_refusal", "preview", "save", "repeat"}
				}
				for _, step := range steps {
					t.Run(step, func(t *testing.T) {
						before := parityLocalTree(t, home)
						args := []string{"auth", "login", "--email=fixture@example.invalid"}
						if lane.Name == "before" {
							args = append(args, "--profile-name=fixture", "--backend=env", "--base-url="+server.URL)
						} else {
							args = append(args, "--site="+server.URL)
						}
						if step == "preview" {
							args = append(args, "--dry-run")
						}
						input := parityFixtureInput{Config: config, Output: mode}
						if step == "stdin_refusal" {
							flag := "--with-token"
							if lane.Name == "before" {
								flag = "--secret-stdin"
							}
							args = append(args, flag)
							input.Stdin = []byte("synthetic-rejected-input\n")
						}
						mu.Lock()
						start := len(requests)
						mu.Unlock()
						started := time.Now().UTC()
						result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, input, extra, args...)
						finished := time.Now().UTC()
						evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
						for _, secret := range []string{"synthetic-fixture-token", "synthetic-rejected-input"} {
							if bytes.Contains(result.Stdout, []byte(secret)) || bytes.Contains(result.Stderr, []byte(secret)) {
								t.Fatal("login leaked synthetic credential bytes")
							}
						}
						if step == "stdin_refusal" {
							mu.Lock()
							count := len(requests) - start
							mu.Unlock()
							if result.Exit != 3 || !parityAuthLoginInputRefusal(result.Stdout, lane.Name) || count != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) || bytes.Contains(result.Stdout, input.Stdin[:len(input.Stdin)-1]) || bytes.Contains(result.Stderr, input.Stdin[:len(input.Stdin)-1]) {
								t.Fatalf("environment input refusal lost its reason or entered work: exit%d public%s", result.Exit, result.Stdout)
							}
							parityAuthoringData(t, result.Stdout, lane.Name, "auth.login", 3)
							check := "C017.auth.login.env.stdin_refusal." + mode
							evidence.Checks[check] = append(evidence.Checks[check], lane.Name)
							return
						}
						if result.Exit != 0 || len(result.Stderr) != 0 {
							t.Fatalf("environment login failed: exit%d stdout%s stderr%s", result.Exit, result.Stdout, result.Stderr)
						}
						data := result.Stdout
						if mode == "json" {
							parityAuthoringData(t, result.Stdout, lane.Name, "auth.login", 0)
							data = parityOriginalFrameData(result.Stdout)
						}
						if !parityAuthLoginData(data, lane.Name, server.URL, config, step == "preview", step == "repeat") || bytes.Contains(result.Stdout, []byte("synthetic-fixture-token")) {
							t.Fatalf("login lost independent public publication facts: %s", data)
						}
						mu.Lock()
						got := append([]string{}, requests[start:]...)
						mu.Unlock()
						want := []string{"GET /rest/api/3/myself authenticated=true"}
						if lane.Name == "before" {
							want = append(want, "GET /rest/agile/1.0/board?maxResults=50 authenticated=true")
						}
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("login wire differs: %v", got)
						}
						parityAuthLoginFiles(t, home, lane.Name, server.URL, config, step, before, started, finished)
						check := "C017.auth.login.env." + step + "." + mode
						evidence.Checks[check] = append(evidence.Checks[check], lane.Name)
					})
					if t.Failed() {
						break
					}
				}
				if !t.Failed() && lane.Name == "after" {
					t.Run("fresh_process", func(t *testing.T) {
						before := parityLocalTree(t, home)
						mu.Lock()
						start := len(requests)
						mu.Unlock()
						result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, extra, "auth", "status")
						evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
						if result.Exit != 0 || len(result.Stderr) != 0 {
							t.Fatal("fresh process could not use saved routing and environment reference")
						}
						data := result.Stdout
						if mode == "json" {
							parityAuthoringData(t, result.Stdout, lane.Name, "auth.status", 0)
							data = parityOriginalFrameData(result.Stdout)
						}
						mu.Lock()
						got := append([]string{}, requests[start:]...)
						mu.Unlock()
						if !parityAuthStatusData(data, lane.Name, server.URL) || !reflect.DeepEqual(got, []string{"GET /rest/api/3/myself authenticated=true"}) || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
							t.Fatal("fresh process lost saved identity/routing or changed files")
						}
						evidence.Checks["C017.auth.login.env.fresh_process."+mode] = []string{lane.Name}
					})
				}
			})
		}
	}
	saveParityLocal(t, "auth-login-fixture", evidence)
}

func TestParityAuthLoginRequiresCompletePublication(t *testing.T) {
	t.Parallel()
	valid := `{"dry_run":false,"base_url":"https://example.invalid","email":"fixture@example.invalid","account_id":"account-1","display_name":"Fixture User","verified":true,"credential":{"backend":"env","status":"external","attempted":false,"saved":false,"reference":"JIRA_TOKEN_FIXTURE"},"settings":{"path":"public.toml","would_change":true,"changed":true,"created":false,"saved":true,"status":"saved"}}`
	if !parityAuthLoginData([]byte(valid), "after", "https://example.invalid", "public.toml", false, false) {
		t.Fatal("complete independent login publication refused")
	}
	for name, invalid := range map[string]string{"foreign_account": strings.ReplaceAll(valid, "account-1", "foreign-2"), "wrong_reference": strings.ReplaceAll(valid, "JIRA_TOKEN_FIXTURE", "JIRA_TOKEN_OTHER"), "false_provider_write": strings.Replace(valid, `"attempted":false`, `"attempted":true`, 1), "false_settings_ack": strings.Replace(valid, `"saved":true`, `"saved":false`, 1), "wrong_path": strings.ReplaceAll(valid, "public.toml", "foreign.toml"), "missing_verified": strings.ReplaceAll(valid, `"verified":true,`, ""), "leaked_token": strings.ReplaceAll(valid, `"verified":true`, `"verified":true,"token":"synthetic-fixture-token"`), "duplicate": strings.ReplaceAll(valid, `"verified":true`, `"verified":true,"verified":true`)} {
		t.Run(name, func(t *testing.T) {
			if parityAuthLoginData([]byte(invalid), "after", "https://example.invalid", "public.toml", false, false) {
				t.Fatal("login admitted incomplete or invented publication facts")
			}
		})
	}
}

func TestParityAuthLoginSettingsRequireBoundPublicMetadata(t *testing.T) {
	t.Parallel()
	valid := "theme='dark'\nbase_url='https://example.invalid'\nemail='fixture@example.invalid'\ncloud_id=''\nsecret_backend='env'\napi_token_env='JIRA_TOKEN_FIXTURE'\naccount_id='account-1'\naccount_id_site='https://example.invalid'\naccount_id_email='fixture@example.invalid'\naccount_id_cloud_id=''\n"
	if !parityAuthLoginSettings([]byte(valid), "after", "https://example.invalid", "") {
		t.Fatal("complete independent public settings refused")
	}
	for name, invalid := range map[string]string{"foreign_id": strings.ReplaceAll(valid, "account-1", "foreign-2"), "foreign_binding": strings.ReplaceAll(valid, "account_id_site='https://example.invalid'", "account_id_site='https://foreign.invalid'"), "wrong_reference": strings.ReplaceAll(valid, "JIRA_TOKEN_FIXTURE", "JIRA_TOKEN_OTHER"), "missing_cloud_binding": strings.ReplaceAll(valid, "account_id_cloud_id=''\n", ""), "wrong_theme": strings.ReplaceAll(valid, "theme='dark'", "theme='light'"), "persisted_secret": valid + "api_token='synthetic-fixture-token'\n", "extra_field": valid + "read_only=true\n", "sparse": "account_id='account-1'\n"} {
		t.Run(name, func(t *testing.T) {
			if parityAuthLoginSettings([]byte(invalid), "after", "https://example.invalid", "") {
				t.Fatal("login settings admitted incomplete binding or collateral data")
			}
		})
	}
}

func TestParityAuthLoginBoardCacheRequiresCompleteNativeData(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, time.January, 1, 0, 0, 0, 500_000_000, time.UTC)
	finished := started.Add(time.Second)
	valid := `{"fetched_at":"2026-01-01T00:00:00Z","ttl_seconds":2419200,"truncated":false,"truncated_reason":"","pages_fetched":1,"items":[]}`
	if !parityAuthLoginBoardCache([]byte(valid), started, finished) {
		t.Fatal("complete independent empty board cache refused")
	}
	for name, invalid := range map[string]string{"missing_items": strings.ReplaceAll(valid, `,"items":[]`, ""), "null_items": strings.ReplaceAll(valid, `"items":[]`, `"items":null`), "wrong_ttl": strings.ReplaceAll(valid, "2419200", "60"), "wrong_pages": strings.ReplaceAll(valid, `"pages_fetched":1`, `"pages_fetched":0`), "false_complete": strings.ReplaceAll(valid, `"truncated":false`, `"truncated":true`), "bad_timestamp": strings.ReplaceAll(valid, "2026-01-01T00:00:00Z", "yesterday"), "stale_timestamp": strings.ReplaceAll(valid, "2026-01-01T00:00:00Z", "2000-01-01T00:00:00Z"), "future_timestamp": strings.ReplaceAll(valid, "2026-01-01T00:00:00Z", "2099-01-01T00:00:00Z"), "duplicate": strings.ReplaceAll(valid, `"items":[]`, `"items":[],"items":[]`)} {
		t.Run(name, func(t *testing.T) {
			if parityAuthLoginBoardCache([]byte(invalid), started, finished) {
				t.Fatal("login admitted incomplete or invented native board state")
			}
		})
	}
}

func TestParityAuthLoginRefusalRequiresExactReason(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ lane, raw string }{
		{"before", `{"data":null,"errors":[{"type":"validation","code":"validation_failed","message":"validation: the env backend reads the credential from the profile's JIRA_TOKEN_* environment variable at run time; do not supply a token to store","hint":"Check the values you passed and try again.","retryable":false}]}`},
		{"after", `{"data":null,"errors":[{"code":"flag_value_invalid","message":"the environment backend cannot store a supplied token; configure JIRA_API_TOKEN or api_token_env and run login without --with-token","hint":"That value isn't the type the flag expects — run the command with ` + "`--help`" + ` to see its format.","field":"with-token"}]}`},
	} {
		t.Run(tc.lane, func(t *testing.T) {
			if !parityAuthLoginInputRefusal([]byte(tc.raw), tc.lane) {
				t.Fatal("complete independent environment refusal refused")
			}
			for name, invalid := range map[string]string{"wrong_reason": strings.ReplaceAll(tc.raw, "cannot store", "cannot log"), "false_data": strings.ReplaceAll(tc.raw, `"data":null`, `"data":{}`), "missing_errors": strings.ReplaceAll(tc.raw, `"errors":`, `"missing_errors":`), "wrong_field": strings.ReplaceAll(tc.raw, `"field":"with-token"`, `"field":"unknown"`), "wrong_retry": strings.ReplaceAll(tc.raw, `"retryable":false`, `"retryable":true`), "wrong_hint": strings.ReplaceAll(strings.ReplaceAll(tc.raw, "Check the values", "Retry every write"), "That value isn't", "Retry every write")} {
				if invalid == tc.raw {
					continue
				}
				t.Run(name, func(t *testing.T) {
					if !jsontext.Value(invalid).IsValid() {
						t.Fatal("negative refusal must remain complete JSON")
					}
					if parityAuthLoginInputRefusal([]byte(invalid), tc.lane) {
						t.Fatal("login admitted an unrelated or incomplete refusal")
					}
				})
			}
		})
	}
}

func TestParityAuthLoginCachePublicationRequiresFreshBoundWrapper(t *testing.T) {
	t.Parallel()
	home, site := t.TempDir(), "https://example.invalid"
	profile, path := parityMetadataLegacyLocation(home, site, "boards")
	started := time.Date(2026, time.January, 1, 0, 0, 0, 500_000_000, time.UTC)
	finished := started.Add(time.Second)
	valid := fmt.Sprintf(`{"profile":%q,"resource":"boards","schema":1,"fetched_at":"2026-01-01T00:00:00.75Z","data":{"fetched_at":"2026-01-01T00:00:00Z","ttl_seconds":2419200,"truncated":false,"truncated_reason":"","pages_fetched":1,"items":[]}}`, profile)
	if !parityAuthLoginCachePublication([]byte(valid), home, site, path, started, finished) {
		t.Fatal("fresh independently bound cache wrapper refused")
	}
	for name, invalid := range map[string]string{"old_wrapper": strings.ReplaceAll(valid, "2026-01-01T00:00:00.75Z", "2000-01-01T00:00:00Z"), "future_wrapper": strings.ReplaceAll(valid, "2026-01-01T00:00:00.75Z", "2099-01-01T00:00:00Z"), "foreign_profile": strings.ReplaceAll(valid, profile, "foreign-2"), "wrong_resource": strings.ReplaceAll(valid, `"resource":"boards"`, `"resource":"fields"`), "wrong_schema": strings.ReplaceAll(valid, `"schema":1`, `"schema":2`), "stale_native": strings.ReplaceAll(valid, `"fetched_at":"2026-01-01T00:00:00Z"`, `"fetched_at":"2000-01-01T00:00:00Z"`)} {
		t.Run(name, func(t *testing.T) {
			if parityAuthLoginCachePublication([]byte(invalid), home, site, path, started, finished) {
				t.Fatal("login admitted stale or unrelated cache publication")
			}
		})
	}
}
