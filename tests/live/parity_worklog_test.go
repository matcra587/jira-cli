//go:build live

package live

import (
	"encoding/json"
	"encoding/json/jsontext"
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
)

type parityWorklogFixture struct{ Native, Before []json.RawMessage }

func parityWorklogFixtures(t *testing.T) parityWorklogFixture {
	t.Helper()
	var fixture parityWorklogFixture
	raw, err := os.ReadFile("testdata/parity-worklog-oracles.json")
	if err != nil || !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &fixture) != nil || len(fixture.Native) != 2 || len(fixture.Before) != 2 {
		t.Fatal("independent native worklog fixtures are unavailable or malformed")
	}
	return fixture
}

type parityWorklogExpected struct {
	Native, Before     []json.RawMessage
	Start, Size, Total int
	Complete           bool
}

func parityWorklogProjection(data []byte, lane string, keys []string, pages map[string]parityWorklogExpected) bool {
	if !jsontext.Value(data).IsValid() || len(keys) == 0 {
		return false
	}
	rows := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		page, found := pages[key]
		if !found || page.Native == nil || page.Before == nil {
			return false
		}
		var content map[string]any
		switch lane {
		case "before":
			content = map[string]any{"issue": map[string]any{"key": key}, "worklogs": page.Before}
			if len(keys) == 1 {
				expected, err := json.Marshal(content)
				return err == nil && parityAuthoringJSON(data, expected)
			}
			rows = append(rows, map[string]any{"key": key, "ok": true, "data": content})
		case "after":
			worklogs := make([]map[string]any, 0, len(page.Native))
			seen := make(map[string]bool)
			for _, native := range page.Native {
				var fields map[string]json.RawMessage
				var id string
				if !jsontext.Value(native).IsValid() || json.Unmarshal(native, &fields) != nil || json.Unmarshal(fields["id"], &id) != nil || id == "" || seen[id] {
					return false
				}
				seen[id] = true
				worklogs = append(worklogs, map[string]any{"id": id, "fields": native})
			}
			content = map[string]any{"worklogs": worklogs, "pagination": map[string]any{"start_at": page.Start, "max_results": page.Size, "total": page.Total, "returned": len(page.Native)}, "complete": page.Complete, "truncated": false}
			rows = append(rows, map[string]any{"issue": map[string]any{"key": key}, "ok": true, "data": content})
		default:
			return false
		}
	}
	want := map[string]any{"results": rows}
	if lane == "before" {
		want["succeeded"], want["failed"] = len(keys), 0
	}
	expected, err := json.Marshal(want)
	return err == nil && parityAuthoringJSON(data, expected)
}

func parityWorklogData(t *testing.T, stdout []byte, lane, mode string, keys []string, pages map[string]parityWorklogExpected) []byte {
	t.Helper()
	var data map[string]json.RawMessage
	var pagination json.RawMessage
	if mode == "json" {
		data = parityAuthoringCleanData(t, stdout, lane, "worklog.list")
		var frame struct{ Meta map[string]json.RawMessage }
		if json.Unmarshal(stdout, &frame) != nil {
			t.Fatal("worklog envelope lost its metadata")
		}
		pagination = frame.Meta["pagination"]
	} else if !jsontext.Value(stdout).IsValid() || json.Unmarshal(stdout, &data) != nil || data == nil {
		t.Fatal("worklog compact result is not a complete object")
	}
	if mode == "json" && (lane == "after" || len(keys) > 1) && len(pagination) != 0 {
		t.Fatal("worklog read invented global pagination outside its declared lane layout")
	}
	if lane == "before" && len(keys) == 1 {
		if mode == "compact" {
			pagination = data["pagination"]
			delete(data, "pagination")
		}
		page := pages[keys[0]]
		want := map[string]any{"startAt": page.Start, "maxResults": page.Size, "total": page.Total, "isLast": page.Complete}
		if !page.Complete {
			want["nextCursor"] = strconv.Itoa(page.Start + page.Size)
		}
		expected, err := json.Marshal(want)
		if err != nil || !parityAuthoringJSON(pagination, expected) {
			t.Fatalf("legacy single-target worklog read changed its native page metadata: got %s; want %s", pagination, expected)
		}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParityActualBinaryWorklogReads(t *testing.T) {
	fixture := parityWorklogFixtures(t)
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		for _, sample := range []string{"empty", "full-page", "sparse-page"} {
			for _, multiple := range []bool{false, true} {
				for _, mode := range []string{"json", "compact"} {
					t.Run(fmt.Sprintf("%s/%s/multiple=%t/%s", lane.name, sample, multiple, mode), func(t *testing.T) {
						if lane.binary == "" {
							t.Skip("requires selected actual binary")
						}
						keys := []string{"PAIR-2"}
						if multiple {
							keys = append(keys, "PAIR-1")
						}
						pages := map[string]parityWorklogExpected{}
						for _, key := range keys {
							page := parityWorklogExpected{Native: fixture.Native, Before: fixture.Before, Size: 50, Total: 2, Complete: true}
							switch {
							case key == "PAIR-1" || sample == "empty":
								page.Native, page.Before, page.Total = []json.RawMessage{}, []json.RawMessage{}, 0
							case sample == "sparse-page":
								page.Native, page.Before, page.Size, page.Complete = fixture.Native[:1], fixture.Before[:1], 1, false
							}
							pages[key] = page
						}
						var calls atomic.Int32
						counts := make(map[string]*atomic.Int32)
						for _, key := range keys {
							counts[key] = new(atomic.Int32)
						}
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							calls.Add(1)
							body, err := io.ReadAll(io.LimitReader(r.Body, 1))
							query, queryErr := url.ParseQuery(r.URL.RawQuery)
							wantQuery := url.Values{"maxResults": {"50"}, "startAt": {"0"}}
							if lane.name == "before" {
								wantQuery.Del("startAt")
							}
							user, password, authenticated := r.BasicAuth()
							key := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/"), "/worklog")
							page, found := pages[key]
							if err != nil || len(body) != 0 || queryErr != nil || !reflect.DeepEqual(query, wantQuery) || r.Method != "GET" || !found || r.URL.Path != "/rest/api/3/issue/"+key+"/worklog" || !authenticated || user != "fixture@example.invalid" || password != "synthetic-fixture-token" {
								t.Error("worklog read changed its exact authenticated GET/query/body")
								http.Error(w, "wrong synthetic request", http.StatusBadRequest)
								return
							}
							counts[key].Add(1)
							w.Header().Set("Content-Type", "application/json")
							raw, err := json.Marshal(map[string]any{"startAt": page.Start, "maxResults": page.Size, "total": page.Total, "worklogs": page.Native})
							if err != nil {
								t.Error(err)
								return
							}
							_, _ = w.Write(raw)
						}))
						defer server.Close()
						home := t.TempDir()
						config := parityCachePublicConfig(t, home, lane.name, server.URL)
						if err := parityFingerprint(lane.binary, lane.digest); err != nil {
							t.Fatal(err)
						}
						args := append([]string{"worklog", "list"}, keys...)
						args = append(args, "--parallelism=2")
						result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_READ_ONLY=true"}, args...)
						evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
						if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load() != int32(len(keys)) {
							t.Fatalf("worklog read failed: exit %d; HTTP %d want %d; stdout %s; stderr %s", result.Exit, calls.Load(), len(keys), result.Stdout, result.Stderr)
						}
						for _, key := range keys {
							if counts[key].Load() != 1 {
								t.Fatal("worklog read did not enter each exact requested issue once")
							}
						}
						data := parityWorklogData(t, result.Stdout, lane.name, mode, keys, pages)
						if !parityWorklogProjection(data, lane.name, keys, pages) {
							t.Fatalf("worklog read differs from independent native records/order/pagination: %s", result.Stdout)
						}
						for _, mutation := range []struct{ name, original, replacement string }{
							{"wrong-issue", `"PAIR-2"`, `"PAIR-3"`},
							{"null-records", `"worklogs":[]`, `"worklogs":null`},
							{"changed-comment", "Owned", "Changed"},
							{"changed-native-number", "900719925474099312345", "900719925474099312346"},
							{"false-success", `"ok":true`, `"ok":false`},
						} {
							changed := strings.Replace(string(data), mutation.original, mutation.replacement, 1)
							if changed == string(data) {
								continue
							}
							t.Run("guard/"+mutation.name, func(t *testing.T) {
								if parityWorklogProjection([]byte(changed), lane.name, keys, pages) {
									t.Fatal("changed worklog identity/native values or receipt earned read acceptance")
								}
							})
						}
						id := fmt.Sprintf("C073.worklogs.%s.multiple=%t.%s", sample, multiple, mode)
						evidence.Checks[id] = append(evidence.Checks[id], lane.name)
					})
				}
			}
		}
	}
	saveParityLocal(t, "worklog-read-fixture", evidence)
}

func parityWorklogLegacyItems(items []json.RawMessage) ([]json.RawMessage, bool) {
	if items == nil {
		return nil, false
	}
	out := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		var native map[string]json.RawMessage
		if !jsontext.Value(item).IsValid() || json.Unmarshal(item, &native) != nil || native == nil {
			return nil, false
		}
		value := make(map[string]json.RawMessage)
		for _, key := range []string{"id", "timeSpentSeconds", "started", "comment"} {
			if raw, present := native[key]; present && string(raw) != "null" {
				value[key] = raw
			}
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, false
		}
		out = append(out, raw)
	}
	return out, true
}

func TestParityWorklogLegacyProjectionMatchesIndependentFixture(t *testing.T) {
	fixture := parityWorklogFixtures(t)
	items, ok := parityWorklogLegacyItems(fixture.Native)
	actual, err := json.Marshal(items)
	expected, wantErr := json.Marshal(fixture.Before)
	if !ok || err != nil || wantErr != nil || !parityAuthoringJSON(actual, expected) {
		t.Fatal("legacy worklog projection differs from independently authored native fixtures")
	}
}

func TestLiveParityWorklogReads(t *testing.T) {
	p := newParityRun(t)
	key := requiredEnv(t, "JIRA_LIVE_READ_KEY")
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			read := rawRead(t, p.owner, "api", "get", "/rest/api/3/issue/"+key+"/worklog", "--query=startAt=0", "--query=maxResults=50")
			page, ok := parityNativeWorklogExpected(read.Response.JSON, 0, 50)
			if !ok {
				t.Fatal("independent native worklog read lacks supported bounded page facts and unique identities")
			}
			frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, "worklog", "list", key)
			if exit != 0 || !frame.OK {
				t.Fatal("native worklog list failed; inspect retained receipts")
			}
			keys, pages := []string{key}, map[string]parityWorklogExpected{key: page}
			if !parityWorklogProjection(parityWorklogData(t, stdout, lane.Name, "json", keys, pages), lane.Name, keys, pages) {
				t.Fatal("worklog list differs from independently read native records and paging facts")
			}
			p.check(t, lane, "C073.worklogs.native")
		})
	}
}

func parityNativeWorklogExpected(raw []byte, start, size int) (parityWorklogExpected, bool) {
	var page parityWorklogExpected
	var native map[string]json.RawMessage
	var offset, declared, total *int
	if start < 0 || size < 1 || !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &native) != nil || json.Unmarshal(native["startAt"], &offset) != nil || offset == nil || *offset != start || json.Unmarshal(native["maxResults"], &declared) != nil || declared == nil || *declared < 0 || json.Unmarshal(native["total"], &total) != nil || total == nil || *total < 0 || json.Unmarshal(native["worklogs"], &page.Native) != nil || page.Native == nil || len(page.Native) > size || len(page.Native) > *declared || len(page.Native) > 0 && (start > *total || len(page.Native) > *total-start) {
		return parityWorklogExpected{}, false
	}
	seen := make(map[string]bool)
	for _, item := range page.Native {
		var fields map[string]json.RawMessage
		var id string
		if json.Unmarshal(item, &fields) != nil || json.Unmarshal(fields["id"], &id) != nil || id == "" || strings.TrimSpace(id) != id || seen[id] {
			return parityWorklogExpected{}, false
		}
		seen[id] = true
	}
	var ok bool
	page.Before, ok = parityWorklogLegacyItems(page.Native)
	page.Start, page.Size, page.Total = *offset, *declared, *total
	page.Complete = start >= page.Total || len(page.Native) >= page.Total-start
	// A non-progressing page needs its own truncation/warning scenario. It
	// cannot establish an ordinary complete or bounded page comparison here.
	return page, ok && (page.Complete || len(page.Native) > 0)
}

func TestParityWorklogNativePageOracle(t *testing.T) {
	const valid = `{"startAt":0,"maxResults":1,"total":2,"worklogs":[{"id":"501","timeSpentSeconds":90}]}`
	page, ok := parityNativeWorklogExpected([]byte(valid), 0, 50)
	if !ok || page.Start != 0 || page.Size != 1 || page.Total != 2 || page.Complete || len(page.Native) != 1 {
		t.Fatal("independent sparse native page fixture refused or lost its facts")
	}
	t.Run("repeated-record", func(t *testing.T) {
		raw := []byte(`{"startAt":0,"maxResults":2,"total":2,"worklogs":[{"id":"501"},{"id":"501"}]}`)
		if _, ok := parityNativeWorklogExpected(raw, 0, 50); ok {
			t.Fatal("native page oracle admitted a repeated record identity")
		}
	})
	for _, mutation := range []struct{ name, original, replacement string }{
		{"null-start", `"startAt":0`, `"startAt":null`},
		{"null-total", `"total":2`, `"total":null`},
		{"null-size", `"maxResults":1`, `"maxResults":null`},
		{"wrong-start", `"startAt":0`, `"startAt":1`},
		{"negative-total", `"total":2`, `"total":-1`},
		{"null-records", `[{"id":"501","timeSpentSeconds":90}]`, `null`},
		{"missing-records", `"worklogs":`, `"other":`},
		{"wrong-total-case", `"total":`, `"Total":`},
		{"null-id", `"id":"501"`, `"id":null`},
		{"padded-id", `"id":"501"`, `"id":" 501 "`},
		{"duplicate-id", `"id":"501"`, `"id":"502","id":"501"`},
		{"non-progress", `[{"id":"501","timeSpentSeconds":90}]`, `[]`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			changed := strings.Replace(valid, mutation.original, mutation.replacement, 1)
			if changed == valid {
				t.Fatal("native page mutation did not apply")
			}
			if _, ok := parityNativeWorklogExpected([]byte(changed), 0, 50); ok {
				t.Fatal("malformed or unsupported native page earned bounded-read acceptance")
			}
		})
	}
}
