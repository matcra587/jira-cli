//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type parityPagingReply struct {
	query url.Values
	body  string
}

type parityPagingCase struct {
	name, operation, path, expected string
	args                            []string
	pages                           []parityPagingReply
}

const parityPagingValueHint = "That value isn't the type the flag expects — run the command with `--help` to see its format."

const parityPagingConflictHint = "Those two flags set the same thing — keep the one you meant and drop the other."

func parityPagingRefusal(stdout []byte, operation string, exit int, code string, expectedErrors []byte) bool {
	if !parityLocalRefusal(stdout, "after", operation, exit, code) {
		return false
	}
	var frame map[string]json.RawMessage
	return json.Unmarshal(stdout, &frame) == nil && parityAuthoringJSON(frame["errors"], expectedErrors)
}

func TestParityPagingRefusalRequiresExactAttribution(t *testing.T) {
	const failures = `[{"code":"flag_value_invalid","message":"--start must be between 0 and 999","hint":"That value isn't the type the flag expects — run the command with ` + "`--help`" + ` to see its format.","field":"start"}]`
	const frame = `{"ok":false,"meta":{"command":"user.search","contract_version":"1","timestamp":"2026-01-01T00:00:00Z","request_id":"independent"},"data":null,"errors":` + failures + `,"warnings":[]}`
	check := func(raw string) bool {
		return parityPagingRefusal([]byte(raw), "user.search", 3, "flag_value_invalid", []byte(failures))
	}
	if !check(frame) {
		t.Fatal("complete independent refusal was rejected")
	}
	for _, tc := range []struct{ name, original, replacement string }{
		{"wrong-field", `"field":"start"`, `"field":"limit"`},
		{"wrong-message", "between 0 and 999", "positive"},
		{"wrong-hint", "That value isn't", "Retry until accepted; that value isn't"},
		{"wrong-operation", "user.search", "worklog.list"},
		{"missing-errors", `"errors":`, `"missing_errors":`},
		{"null-errors", failures, `null`},
		{"empty-errors", failures, `[]`},
		{"object-errors", failures, `{}`},
		{"duplicate-field", `"field":"start"`, `"field":"limit","field":"start"`},
		{"case-alias-errors", `"errors":` + failures, `"errors":` + strings.Replace(failures, `"field":"start"`, `"field":"limit"`, 1) + `,"Errors":` + failures},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(frame, tc.original, tc.replacement, 1)
			if changed == frame || check(changed) {
				t.Fatal("an unrelated or malformed paging refusal earned acceptance")
			}
		})
	}
}

// These are authored page plans, not calls to the production walkers or models.
// Sparse user pages require another probe; worklog totals supply a different
// completion contract. The two operations must not share a completion oracle.
func parityPagingCases(t *testing.T) []parityPagingCase {
	t.Helper()
	fixture := parityWorklogFixtures(t)
	worklogPage := func(start, size, total int, records ...json.RawMessage) parityPagingReply {
		if records == nil {
			records = []json.RawMessage{}
		}
		raw, err := json.Marshal(map[string]any{"startAt": start, "maxResults": size, "total": total, "worklogs": records})
		if err != nil {
			t.Fatal(err)
		}
		return parityPagingReply{url.Values{"startAt": {strconv.Itoa(start)}, "maxResults": {strconv.Itoa(size)}}, string(raw)}
	}
	worklogData := func(start, size, total int, complete bool, records ...json.RawMessage) string {
		rows := make([]map[string]any, 0, len(records))
		for _, record := range records {
			var native struct{ ID string }
			if json.Unmarshal(record, &native) != nil || native.ID == "" {
				t.Fatal("independent worklog record lacks identity")
			}
			rows = append(rows, map[string]any{"id": native.ID, "fields": record})
		}
		raw, err := json.Marshal(map[string]any{"results": []any{map[string]any{"issue": map[string]any{"key": "PAIR-2"}, "ok": true, "data": map[string]any{"worklogs": rows, "pagination": map[string]any{"start_at": start, "max_results": size, "total": total, "returned": len(rows)}, "complete": complete, "truncated": false}}}})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	const active = `{"accountId":"owned-active","displayName":"Owned <&> café","active":true,"emailAddress":null}`
	const hidden = `{"accountId":"owned-hidden","displayName":"Hidden email","active":true}`
	const inactive = `{"accountId":"owned-inactive","displayName":"Inactive","active":false}`
	const user = "/rest/api/3/user/search"
	const worklog = "/rest/api/3/issue/PAIR-2/worklog"
	userPage := func(start, size int, body string) parityPagingReply {
		return parityPagingReply{url.Values{"query": {"owned <&> café"}, "startAt": {strconv.Itoa(start)}, "maxResults": {strconv.Itoa(size)}}, body}
	}
	return []parityPagingCase{
		{"worklog-offset", "worklog.list", worklog, worklogData(1, 1, 2, true, fixture.Native[1]), []string{"worklog", "list", "PAIR-2", "--start=1", "--limit=1"}, []parityPagingReply{worklogPage(1, 1, 2, fixture.Native[1])}},
		{"worklog-single-prefix", "worklog.list", worklog, worklogData(0, 1, 2, false, fixture.Native[0]), []string{"worklog", "list", "PAIR-2", "--limit=1"}, []parityPagingReply{worklogPage(0, 1, 2, fixture.Native[0])}},
		{"worklog-size-boundary", "worklog.list", worklog, worklogData(0, 100, 2, true, fixture.Native...), []string{"worklog", "list", "PAIR-2", "--limit=100"}, []parityPagingReply{worklogPage(0, 100, 2, fixture.Native...)}},
		{"worklog-beyond-end", "worklog.list", worklog, worklogData(3, 1, 2, true), []string{"worklog", "list", "PAIR-2", "--start=3", "--limit=1"}, []parityPagingReply{worklogPage(3, 1, 2)}},
		{"worklog-sparse-walk", "worklog.list", worklog, worklogData(0, 100, 2, true, fixture.Native...), []string{"worklog", "list", "PAIR-2", "--all"}, []parityPagingReply{worklogPage(0, 100, 2, fixture.Native[0]), worklogPage(1, 100, 2, fixture.Native[1])}},
		{"worklog-explicit-false", "worklog.list", worklog, worklogData(0, 1, 2, false, fixture.Native[0]), []string{"worklog", "list", "PAIR-2", "--all=false", "--start=0", "--limit=1"}, []parityPagingReply{worklogPage(0, 1, 2, fixture.Native[0])}},
		{"user-offset", "user.search", user, `{"query":"owned <&> café","users":[{"account_id":"owned-active","display_name":"Owned <&> café","email_address":null}],"count":1,"skipped":1,"pagination":{"start_at":7,"max_results":2,"fetched":2,"next_start":9,"complete":false,"search_limit":1000},"truncated":false}`, []string{"user", "search", "owned <&> café", "--start=7", "--limit=2"}, []parityPagingReply{userPage(7, 2, "["+active+","+inactive+"]")}},
		{"user-empty-offset", "user.search", user, `{"query":"owned <&> café","users":[],"count":0,"skipped":0,"pagination":{"start_at":7,"max_results":2,"fetched":0,"complete":true,"search_limit":1000},"truncated":false}`, []string{"user", "search", "owned <&> café", "--start=7", "--limit=2"}, []parityPagingReply{userPage(7, 2, "[]")}},
		{"user-ceiling", "user.search", user, `{"query":"owned <&> café","users":[{"account_id":"owned-active","display_name":"Owned <&> café","email_address":null}],"count":1,"skipped":0,"pagination":{"start_at":999,"max_results":1,"fetched":1,"complete":false,"search_limit":1000},"truncated":true,"truncated_reason":"server_limit"}`, []string{"user", "search", "owned <&> café", "--start=999", "--limit=1000"}, []parityPagingReply{userPage(999, 1, "["+active+"]")}},
		{"user-sparse-walk", "user.search", user, `{"query":"owned <&> café","users":[{"account_id":"owned-active","display_name":"Owned <&> café","email_address":null},{"account_id":"owned-hidden","display_name":"Hidden email"}],"count":2,"skipped":1,"pagination":{"start_at":0,"max_results":50,"fetched":3,"complete":true,"search_limit":1000},"truncated":false}`, []string{"user", "search", "owned <&> café", "--all"}, []parityPagingReply{userPage(0, 50, "["+active+","+inactive+"]"), userPage(2, 50, "["+hidden+"]"), userPage(3, 50, "[]")}},
		{"user-explicit-false", "user.search", user, `{"query":"owned <&> café","users":[{"account_id":"owned-active","display_name":"Owned <&> café","email_address":null}],"count":1,"skipped":0,"pagination":{"start_at":0,"max_results":1,"fetched":1,"next_start":1,"complete":false,"search_limit":1000},"truncated":false}`, []string{"user", "search", "owned <&> café", "--all=false", "--start=0", "--limit=1"}, []parityPagingReply{userPage(0, 1, "["+active+"]")}},
	}
}

func TestParityActualBinaryPagingFlags(t *testing.T) {
	binary, digest := os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")
	if binary == "" {
		t.Skip("requires selected actual current binary")
	}
	if err := parityFingerprint(binary, digest); err != nil {
		t.Fatal(err)
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, tc := range parityPagingCases(t) {
		for _, mode := range []string{"json", "compact"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					i := int(calls.Add(1)) - 1
					body, err := io.ReadAll(io.LimitReader(r.Body, 1))
					query, queryErr := url.ParseQuery(r.URL.RawQuery)
					user, password, authenticated := r.BasicAuth()
					if i >= len(tc.pages) || err != nil || len(body) != 0 || queryErr != nil || !reflect.DeepEqual(query, tc.pages[i].query) || r.Method != "GET" || r.URL.Path != tc.path || !authenticated || user != "fixture@example.invalid" || password != "synthetic-fixture-token" {
						t.Error("paging changed its exact authenticated GET, body or ordered cursor plan")
						http.Error(w, "wrong synthetic request", http.StatusBadRequest)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, tc.pages[i].body)
				}))
				defer server.Close()
				home := t.TempDir()
				config := parityCachePublicConfig(t, home, "after", server.URL)
				preimage := parityLocalTree(t, home)
				began := time.Now()
				result := parityFixtureProcessAt(t, "after", binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_READ_ONLY=true"}, tc.args...)
				evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: "after", Digest: digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
				if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load() != int32(len(tc.pages)) {
					t.Fatalf("paging execution failed: exit %d; HTTP %d want %d; stdout %s; stderr %s", result.Exit, calls.Load(), len(tc.pages), result.Stdout, result.Stderr)
				}
				data := result.Stdout
				if mode == "json" {
					parityAuthoringCleanData(t, result.Stdout, "after", tc.operation)
					data = parityOriginalFrameData(result.Stdout)
				}
				if !parityAuthoringJSON(data, []byte(tc.expected)) {
					t.Fatalf("paging lost independent native rows, privacy or completion: %s", result.Stdout)
				}
				if tc.operation == "worklog.list" {
					history := parityOpenHistory(t, home, "after", server.URL, "PAIR-2", began)
					parityOpenOnlyHistory(t, home, history, preimage)
				} else if !reflect.DeepEqual(preimage, parityLocalTree(t, home)) {
					t.Fatal("user paging changed its owned public tree")
				}
				prefix := "C073"
				if tc.operation == "user.search" {
					prefix = "C069"
				}
				evidence.Checks[fmt.Sprintf("%s.paging.%s.%s", prefix, tc.name, mode)] = []string{"after"}
			})
		}
	}
	saveParityLocal(t, "paging-flags-fixture", evidence)
}

func TestParityActualBinaryPagingFlagRefusals(t *testing.T) {
	binary, digest := os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")
	if binary == "" {
		t.Skip("requires selected actual current binary")
	}
	if err := parityFingerprint(binary, digest); err != nil {
		t.Fatal(err)
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, operation := range []string{"worklog.list", "user.search"} {
		startMessage, limitMessage, largeLimit, allField := "--start: offset must be nonnegative", "--limit: page size must be from 1 to 100", "--limit=101", ""
		if operation == "user.search" {
			startMessage, limitMessage, largeLimit, allField = "--start must be between 0 and 999", "--limit must be between 1 and 1000", "--limit=1001", "all"
		}
		type refusal struct{ name, flag, code, message, field, hint string }
		cases := []refusal{
			{"negative-start", "--start=-1", "flag_value_invalid", startMessage, "start", parityPagingValueHint},
			{"zero-limit", "--limit=0", "flag_value_invalid", limitMessage, "limit", parityPagingValueHint},
			{"large-limit", largeLimit, "flag_value_invalid", limitMessage, "limit", parityPagingValueHint},
			{"all-authored-start", "--all --start=0", "flag_conflict", "--all conflicts with explicit --start or --limit", allField, parityPagingConflictHint},
			{"all-authored-limit", "--all --limit=50", "flag_conflict", "--all conflicts with explicit --start or --limit", allField, parityPagingConflictHint},
		}
		if operation == "user.search" {
			cases = append(cases, refusal{"large-start", "--start=1000", "flag_value_invalid", startMessage, "start", parityPagingValueHint})
		}
		for _, tc := range cases {
			t.Run(operation+"/"+tc.name, func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusForbidden) }))
				defer server.Close()
				home := t.TempDir()
				config := parityCachePublicConfig(t, home, "after", server.URL)
				preimage := parityLocalTree(t, home)
				args := []string{"worklog", "list", "PAIR-2"}
				prefix := "C073"
				if operation == "user.search" {
					args, prefix = []string{"user", "search", "owned"}, "C069"
				}
				args = append(args, strings.Fields(tc.flag)...)
				result := parityFixtureProcessAt(t, "after", binary, server.URL, home, parityFixtureInput{Config: config}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE="}, args...)
				evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: "after", Digest: digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
				failure := map[string]any{"code": tc.code, "message": tc.message, "hint": tc.hint}
				if tc.field != "" {
					failure["field"] = tc.field
				}
				expected, err := json.Marshal([]any{failure})
				if err != nil {
					t.Fatal(err)
				}
				stderr := "❌ " + tc.message + "\n💡 " + tc.hint + "\n"
				if !parityPagingRefusal(result.Stdout, operation, result.Exit, tc.code, expected) || string(result.Stderr) != stderr || calls.Load() != 0 || !reflect.DeepEqual(preimage, parityLocalTree(t, home)) {
					t.Fatalf("invalid paging did not refuse before credentials, HTTP and storage: exit %d; HTTP %d; stdout %s; stderr %s", result.Exit, calls.Load(), result.Stdout, result.Stderr)
				}
				evidence.Checks[prefix+".paging-refusal."+tc.name] = []string{"after"}
			})
		}
	}
	saveParityLocal(t, "paging-flag-refusals-fixture", evidence)
}
