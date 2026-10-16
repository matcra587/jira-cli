//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"strings"
	"testing"
)

type parityEpicAttempt struct {
	Lane   string `json:"lane"`
	Key    string `json:"key"`
	Parent string `json:"parent,omitempty"`
	Bound  bool   `json:"bound"`
}

type parityEpicState struct {
	ID, Key, ParentState, ParentID, ParentKey string
	Fields                                    map[string]json.RawMessage
}

func parityEpicRestoredParent(actual, want parityEpicState) bool {
	return want.ID != "" && liveKey.MatchString(want.Key) && want.ParentKey == "" && want.ParentID == "" && (want.ParentState == "absent" || want.ParentState == "null") && actual.ID == want.ID && actual.Key == want.Key && actual.ParentState == want.ParentState && actual.ParentID == "" && actual.ParentKey == ""
}

func TestParityEpicRestorationRetainsParentPresence(t *testing.T) {
	t.Parallel()
	baseline := parityEpicState{ID: "101", Key: "PAIR-1", ParentState: "absent"}
	if !parityEpicRestoredParent(baseline, baseline) {
		t.Fatal("exact native unparented preimage refused")
	}
	for _, changed := range []parityEpicState{{ID: "101", Key: "PAIR-1", ParentState: "null"}, {ID: "102", Key: "PAIR-1", ParentState: "absent"}, {ID: "101", Key: "PAIR-1", ParentState: "value", ParentID: "109", ParentKey: "PAIR-9"}, {}} {
		if parityEpicRestoredParent(changed, baseline) {
			t.Fatal("epic baseline collapsed parent presence or identity")
		}
	}
}

func parityEpicNative(raw []byte, id, key string) (parityEpicState, error) {
	var state parityEpicState
	var issue map[string]json.RawMessage
	if !jsontext.Value(raw).IsValid() || id == "" || !liveKey.MatchString(key) || json.Unmarshal(raw, &issue) != nil || json.Unmarshal(issue["id"], &state.ID) != nil || state.ID != id || json.Unmarshal(issue["key"], &state.Key) != nil || state.Key != key || json.Unmarshal(issue["fields"], &state.Fields) != nil || state.Fields == nil {
		return state, fmt.Errorf("native epic observation lost its bound immutable identity")
	}
	for _, name := range []string{"summary", "description", "labels", "project", "issuetype", "status"} {
		if _, present := state.Fields[name]; !present {
			return state, fmt.Errorf("native epic observation lacks requested unrelated field %s", name)
		}
	}
	parent, present := state.Fields["parent"]
	state.ParentState = "absent"
	if present {
		state.ParentState = "null"
		if !bytes.Equal(parent, []byte("null")) {
			state.ParentState = "value"
			var ref map[string]json.RawMessage
			if json.Unmarshal(parent, &ref) != nil || json.Unmarshal(ref["id"], &state.ParentID) != nil || state.ParentID == "" || json.Unmarshal(ref["key"], &state.ParentKey) != nil || !liveKey.MatchString(state.ParentKey) {
				return state, fmt.Errorf("native parent lacks exact identity")
			}
		}
	}
	delete(state.Fields, "parent")
	return state, nil
}

func parityEpicReceipt(raw []byte, lane, verb string, keys []string, parent string, preview bool) bool {
	if !jsontext.Value(raw).IsValid() || (lane != "before" && lane != "after") || (verb != "add" && verb != "remove") || len(keys) == 0 || (verb == "add" && !liveKey.MatchString(parent)) || (verb == "remove" && parent != "") {
		return false
	}
	rows := make([]any, 0, len(keys))
	for _, key := range keys {
		if !liveKey.MatchString(key) {
			return false
		}
		data := map[string]any{"issue": map[string]any{"key": key}, "dry_run": preview}
		if verb == "add" {
			data["epic"], data["added"] = parent, !preview
		} else {
			data["removed"] = !preview
		}
		if lane == "before" {
			if len(keys) == 1 {
				expected, _ := json.Marshal(data)
				return parityAuthoringJSON(raw, expected)
			}
			rows = append(rows, map[string]any{"key": key, "ok": true, "data": data})
		} else {
			rows = append(rows, map[string]any{"issue": map[string]any{"key": key}, "ok": true, "data": map[string]any{"applied": !preview, "remote_validated": !preview, "already_unparented": false, "verify_failed": false}})
		}
	}
	want := map[string]any{"results": rows, "succeeded": len(keys), "failed": 0}
	if lane == "after" {
		var operand any
		if verb == "add" {
			operand = map[string]any{"key": parent}
		}
		want = map[string]any{"dry_run": preview, "notify": true, "payload": map[string]any{"fields": map[string]any{"parent": operand}}, "results": rows}
		if verb == "add" {
			want["epic"] = parent
		}
	}
	expected, _ := json.Marshal(want)
	return parityAuthoringJSON(raw, expected)
}

func parityEpicReadOnlyError(lane, key string, top bool) map[string]any {
	if lane == "before" {
		return map[string]any{"type": "validation", "code": "read_only", "message": "read-only mode is active (JIRA_READ_ONLY env or profile read_only=true); refusing PUT /rest/api/3/issue/" + key, "hint": "Unset `JIRA_READ_ONLY`, set the profile's `read_only=false`, or switch to a profile that allows writes.", "retryable": false}
	}
	hint := "Read-only mode is on, so writes are refused before they reach Jira — clear it to allow this command to mutate."
	if top {
		hint = "Inspect retained data and verify mutation outcomes before retrying. " + hint
	}
	return map[string]any{"code": "read_only_blocked", "message": "read-only mode: write blocked: PUT /rest/api/3/issue/" + key, "hint": hint}
}

func parityEpicRefusal(raw []byte, lane, verb string, keys []string, parent string) bool {
	if !jsontext.Value(raw).IsValid() || len(keys) == 0 || (lane != "before" && lane != "after") || (verb != "add" && verb != "remove") {
		return false
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return false
	}
	errors := []any{parityEpicReadOnlyError(lane, keys[0], true)}
	if lane == "before" {
		errors = make([]any, 0, len(keys))
		for _, key := range keys {
			errors = append(errors, parityEpicReadOnlyError(lane, key, true))
		}
	}
	expectedErrors, _ := json.Marshal(errors)
	if !parityAuthoringJSON(root["errors"], expectedErrors) {
		return false
	}
	if lane == "before" && len(keys) == 1 {
		return bytes.Equal(root["data"], []byte("null"))
	}
	rows := make([]any, 0, len(keys))
	for _, key := range keys {
		row := map[string]any{"issue": map[string]any{"key": key}, "ok": false, "error": parityEpicReadOnlyError(lane, key, false)}
		if lane == "before" {
			delete(row, "issue")
			row["key"] = key
		}
		rows = append(rows, row)
	}
	want := map[string]any{"results": rows, "succeeded": 0, "failed": len(keys)}
	if lane == "after" {
		var operand any
		if verb == "add" {
			operand = map[string]any{"key": parent}
		}
		want = map[string]any{"dry_run": false, "notify": true, "payload": map[string]any{"fields": map[string]any{"parent": operand}}, "results": rows}
		if verb == "add" {
			want["epic"] = parent
		}
	}
	expected, _ := json.Marshal(want)
	return parityAuthoringJSON(root["data"], expected)
}

func TestParityEpicRefusalRejectsInventedSuccess(t *testing.T) {
	t.Parallel()
	const valid = `{"errors":[{"code":"read_only_blocked","message":"read-only mode: write blocked: PUT /rest/api/3/issue/PAIR-1","hint":"Inspect retained data and verify mutation outcomes before retrying. Read-only mode is on, so writes are refused before they reach Jira — clear it to allow this command to mutate."}],"data":{"dry_run":false,"notify":true,"payload":{"fields":{"parent":null}},"results":[{"issue":{"key":"PAIR-1"},"ok":false,"error":{"code":"read_only_blocked","message":"read-only mode: write blocked: PUT /rest/api/3/issue/PAIR-1","hint":"Read-only mode is on, so writes are refused before they reach Jira — clear it to allow this command to mutate."}}]}}`
	if !parityEpicRefusal([]byte(valid), "after", "remove", []string{"PAIR-1"}, "") {
		t.Fatal("independent epic refusal fixture refused")
	}
	for name, invalid := range map[string]string{"foreign": strings.Replace(valid, `"PAIR-1"`, `"PAIR-2"`, 1), "success": strings.Replace(valid, `"ok":false`, `"ok":true`, 1), "applied": strings.Replace(valid, `"ok":false`, `"ok":false,"data":{"applied":true}`, 1), "sparse": `{"errors":[],"data":{}}`, "missing-hint": strings.ReplaceAll(valid, `"hint":`, `"missing_hint":`), "wrong-hint": strings.ReplaceAll(valid, "Read-only mode is on", "Other policy is on"), "foreign-route": strings.ReplaceAll(valid, "/rest/api/3/issue/PAIR-1", "/rest/api/3/issue/PAIR-2"), "duplicate": strings.Replace(valid, `"notify":true`, `"notify":false,"notify":true`, 1)} {
		t.Run(name, func(t *testing.T) {
			if parityEpicRefusal([]byte(invalid), "after", "remove", []string{"PAIR-1"}, "") {
				t.Fatal("refusal oracle admitted invented success, identity or sparse errors")
			}
		})
	}
}

func TestParityEpicNativeRejectsWrongIdentityAndParent(t *testing.T) {
	t.Parallel()
	const valid = `{"id":"101","key":"PAIR-1","fields":{"parent":{"id":"109","key":"PAIR-9"},"summary":"Owned","description":null,"labels":["run"],"project":{"id":"10"},"issuetype":{"id":"1","subtask":false},"status":{"id":"1"}}}`
	state, err := parityEpicNative([]byte(valid), "101", "PAIR-1")
	if err != nil || state.ParentState != "value" || state.ParentID != "109" || state.ParentKey != "PAIR-9" {
		t.Fatal("independent native parent fixture refused")
	}
	for _, invalid := range []string{`{}`, valid + `{}`, strings.ReplaceAll(valid, `"101"`, `"102"`), strings.ReplaceAll(valid, `"PAIR-1"`, `"PAIR-2"`), strings.ReplaceAll(valid, `"id":"109",`, ``), strings.ReplaceAll(valid, `"key":"PAIR-9"`, `"Key":"PAIR-9"`), strings.ReplaceAll(valid, `"parent":{"id":"109","key":"PAIR-9"}`, `"parent":false`), strings.ReplaceAll(valid, `"labels":["run"],`, ``)} {
		if _, err := parityEpicNative([]byte(invalid), "101", "PAIR-1"); err == nil {
			t.Fatal("native parent oracle admitted false identity or missing fields")
		}
	}
}

func TestParityEpicReceiptsRejectFalseClaims(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		lane, verb, raw, parent string
		preview                 bool
	}{
		{"before", "add", `{"issue":{"key":"PAIR-1"},"epic":"PAIR-9","dry_run":true,"added":false}`, "PAIR-9", true},
		{"before", "remove", `{"issue":{"key":"PAIR-1"},"dry_run":false,"removed":true}`, "", false},
		{"after", "add", `{"dry_run":false,"epic":"PAIR-9","notify":true,"payload":{"fields":{"parent":{"key":"PAIR-9"}}},"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"applied":true,"remote_validated":true,"already_unparented":false,"verify_failed":false}}]}`, "PAIR-9", false},
	} {
		if !parityEpicReceipt([]byte(tc.raw), tc.lane, tc.verb, []string{"PAIR-1"}, tc.parent, tc.preview) {
			t.Fatal("independent epic receipt refused")
		}
		for _, invalid := range []string{`{}`, tc.raw + `{}`, strings.ReplaceAll(tc.raw, `"PAIR-1"`, `"PAIR-2"`), strings.ReplaceAll(tc.raw, `"dry_run":false`, `"dry_run":true`), strings.ReplaceAll(tc.raw, `"added":false`, `"added":true`)} {
			if invalid == tc.raw {
				continue
			}
			if parityEpicReceipt([]byte(invalid), tc.lane, tc.verb, []string{"PAIR-1"}, tc.parent, tc.preview) {
				t.Fatal("epic receipt oracle admitted a false target or write claim")
			}
		}
	}
}
