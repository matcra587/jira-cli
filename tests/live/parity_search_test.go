//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// These fixtures are authored independently of both native model decoders.
// In particular, the legacy model omits an explicit null assignee on output.
type paritySearchRow struct {
	ID, Key, Summary, Assignee string
	Labels                     []string
}

func TestParitySearchOperationMapping(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		lane string
		args []string
		want string
	}{
		{"before", []string{"issue", "mine", "--detail", "--all"}, "issue.list"},
		{"after", []string{"issue", "mine", "--detail", "--all"}, "issue.mine"},
		{"before", []string{"issue", "list", "--detail"}, "issue.list"},
		{"before", []string{"search", "jql", "key = PAIR-1"}, "search.jql"},
		{"before", []string{"search", "saved", "owned"}, "search.saved"},
	} {
		if parityExpectedOperation(tc.lane, tc.args, 0) != tc.want {
			t.Fatal("search mapping lost its declared successful operation")
		}
	}
}

func paritySearchSavedMetadata(raw []byte, lane, name, display, query, description, project string) bool {
	if !jsontext.Value(raw).IsValid() {
		return false
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(raw, &data) != nil {
		return false
	}
	want := map[string]any{"key": name, "name": display, "jql": query, "description": description, "project": project}
	var got []byte
	switch lane {
	case "before":
		want["source"] = "saved"
		projection := make(map[string]json.RawMessage)
		for _, key := range []string{"source", "key", "name", "jql", "description", "project"} {
			if value, present := data[key]; present {
				projection[key] = value
			}
		}
		got, _ = json.Marshal(projection)
	case "after":
		got = data["saved_query"]
	default:
		return false
	}
	expected, _ := json.Marshal(want)
	return parityAuthoringJSON(got, expected)
}

func paritySearchMetadata(raw []byte, lane, family, query string) bool {
	if !jsontext.Value(raw).IsValid() || (lane != "before" && lane != "after") {
		return false
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(raw, &data) != nil {
		return false
	}
	projection := make(map[string]json.RawMessage)
	for _, key := range []string{"source", "detail", "jql", "precedence", "board_scope"} {
		if value, present := data[key]; present {
			projection[key] = value
		}
	}
	want := map[string]any{}
	if lane == "before" {
		if family == "inline" {
			want = map[string]any{"source": "inline", "jql": query}
		} else {
			want = map[string]any{"detail": true, "jql": query, "precedence": "none", "board_scope": map[string]any{"applied": false}}
		}
	}
	actual, _ := json.Marshal(projection)
	expected, _ := json.Marshal(want)
	return parityAuthoringJSON(actual, expected)
}

func TestParitySearchMetadataRejectsFalseProvenance(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ lane, family, valid string }{
		{"before", "list", `{"detail":true,"jql":"owned","precedence":"none","board_scope":{"applied":false}}`},
		{"before", "inline", `{"source":"inline","jql":"owned"}`},
		{"after", "list", `{"issues":[]}`},
	} {
		if !paritySearchMetadata([]byte(tc.valid), tc.lane, tc.family, "owned") {
			t.Fatal("independent search provenance refused")
		}
		invalid := strings.Replace(tc.valid, `"owned"`, `"other"`, 1)
		if tc.lane == "after" {
			invalid = `{"jql":"owned"}`
		}
		if paritySearchMetadata([]byte(invalid), tc.lane, tc.family, "owned") {
			t.Fatal("false search provenance admitted")
		}
	}
}

func TestParitySearchSavedMetadataRejectsChangedScope(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ lane, valid string }{
		{"before", `{"source":"saved","key":"plain","name":"plain","jql":"key = PAIR-1","description":"","project":""}`},
		{"after", `{"saved_query":{"key":"plain","name":"plain","jql":"key = PAIR-1","description":"","project":""}}`},
	} {
		if !paritySearchSavedMetadata([]byte(tc.valid), tc.lane, "plain", "plain", "key = PAIR-1", "", "") {
			t.Fatal("independent plain saved-query metadata refused")
		}
		for _, invalid := range []string{`{}`, tc.valid + `{}`, strings.Replace(tc.valid, `"key = PAIR-1"`, `"project = OTHER"`, 1), strings.Replace(tc.valid, `"key":"plain"`, `"key":"other"`, 1), strings.Replace(tc.valid, `"name":"plain"`, `"name":null`, 1)} {
			if paritySearchSavedMetadata([]byte(invalid), tc.lane, "plain", "plain", "key = PAIR-1", "", "") {
				t.Fatal("saved-query metadata oracle admitted a changed query, identity or presence")
			}
		}
	}
}

// Only known missing entries may wait for indexing. Wrong assignments,
// foreign identities, duplicate identities and changed authored fields refuse.
func paritySearchMissingOnly(rows, want []paritySearchRow) bool {
	if rows == nil || len(rows) >= len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, row := range rows {
		index := slices.IndexFunc(want, func(expected paritySearchRow) bool { return expected.Key == row.Key })
		if index < 0 || seen[row.Key] || !reflect.DeepEqual(row, want[index]) {
			return false
		}
		seen[row.Key] = true
	}
	return true
}

func TestParitySearchIndexWaitRejectsWrongRows(t *testing.T) {
	t.Parallel()
	want := []paritySearchRow{{"1", "PAIR-1", "assigned", "self", []string{"run"}}, {"2", "PAIR-2", "control", "", []string{"run"}}}
	if !paritySearchMissingOnly([]paritySearchRow{}, want) || !paritySearchMissingOnly(want[:1], want) {
		t.Fatal("known missing entries refused")
	}
	for _, invalid := range [][]paritySearchRow{nil, want, {want[0], want[0]}, {{"9", "PAIR-9", "foreign", "self", []string{"run"}}}, {{"1", "PAIR-1", "assigned", "other", []string{"run"}}}, {{"1", "PAIR-1", "changed", "self", []string{"run"}}}} {
		if paritySearchMissingOnly(invalid, want) {
			t.Fatal("search wait admitted invalid or complete rows")
		}
	}
}

func paritySearchRows(raw []byte, lane string) ([]paritySearchRow, error) {
	if !jsontext.Value(raw).IsValid() || (lane != "before" && lane != "after" && lane != "native") {
		return nil, fmt.Errorf("invalid search document or lane")
	}
	var body map[string]json.RawMessage
	var issues []map[string]json.RawMessage
	if json.Unmarshal(raw, &body) != nil || json.Unmarshal(body["issues"], &issues) != nil || issues == nil {
		return nil, fmt.Errorf("search issues must be a present array")
	}
	rows := make([]paritySearchRow, 0, len(issues))
	ids, keys := map[string]bool{}, map[string]bool{}
	for _, issue := range issues {
		var row paritySearchRow
		var fields map[string]json.RawMessage
		if json.Unmarshal(issue["id"], &row.ID) != nil || row.ID == "" || ids[row.ID] || json.Unmarshal(issue["key"], &row.Key) != nil || !liveKey.MatchString(row.Key) || keys[row.Key] || json.Unmarshal(issue["fields"], &fields) != nil || fields == nil {
			return nil, fmt.Errorf("missing, malformed or duplicate native identity")
		}
		if bytes.Equal(fields["summary"], []byte("null")) || json.Unmarshal(fields["summary"], &row.Summary) != nil || json.Unmarshal(fields["labels"], &row.Labels) != nil || row.Labels == nil {
			return nil, fmt.Errorf("missing native summary or labels")
		}
		assignee, present := fields["assignee"]
		if !present && lane != "before" {
			return nil, fmt.Errorf("missing native assignee state")
		}
		if present && !bytes.Equal(assignee, []byte("null")) {
			var account map[string]json.RawMessage
			if json.Unmarshal(assignee, &account) != nil || json.Unmarshal(account["accountId"], &row.Assignee) != nil || row.Assignee == "" {
				return nil, fmt.Errorf("invalid native assignee identity")
			}
		}
		// This is a declared historical omission, not a general missing/null
		// equivalence. Current and native documents require explicit presence.
		if lane == "before" && present && bytes.Equal(assignee, []byte("null")) {
			return nil, fmt.Errorf("legacy modeled output unexpectedly retained null")
		}
		ids[row.ID], keys[row.Key] = true, true
		rows = append(rows, row)
	}
	return rows, nil
}

func paritySearchPage(raw []byte, lane, mode string, returned, pageSize int, last bool, cursor string, drained bool) bool {
	if !jsontext.Value(raw).IsValid() || (lane != "before" && lane != "after") || (mode != "json" && mode != "compact") || returned < 0 || pageSize < 1 || (last && cursor != "") || (!last && cursor == "") {
		return false
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return false
	}
	data := root
	if mode == "json" {
		data = nil
		if json.Unmarshal(root["data"], &data) != nil || data == nil {
			return false
		}
	}
	var pagination map[string]json.RawMessage
	if lane == "before" && mode == "json" {
		var meta map[string]json.RawMessage
		if json.Unmarshal(root["meta"], &meta) != nil || json.Unmarshal(meta["pagination"], &pagination) != nil {
			return false
		}
	} else if json.Unmarshal(data["pagination"], &pagination) != nil {
		return false
	}
	if pagination == nil {
		return false
	}
	want := map[string]any{"returned": returned, "is_last": last}
	if lane == "before" {
		maxResults := pageSize
		want = map[string]any{"startAt": 0, "maxResults": maxResults, "isLast": last}
		if drained {
			want["maxResults"], want["total"] = returned, returned
		}
		if cursor != "" {
			want["nextCursor"] = cursor
		}
	} else {
		if cursor != "" {
			want["next_page_token"] = cursor
		}
		var truncated *bool
		if json.Unmarshal(data["truncated"], &truncated) != nil || truncated == nil || *truncated || len(data["truncated_reason"]) != 0 {
			return false
		}
	}
	actual, err := json.Marshal(pagination)
	expected, wantErr := json.Marshal(want)
	return err == nil && wantErr == nil && parityAuthoringJSON(actual, expected)
}

func TestParitySearchRowsRejectFalseIdentityAndPresence(t *testing.T) {
	t.Parallel()
	const native = `{"issues":[{"id":"10001","key":"PAIR-1","fields":{"summary":"Owned café <&>","labels":["run"],"assignee":{"accountId":"self"}}},{"id":"10002","key":"PAIR-2","fields":{"summary":"Control","labels":["run"],"assignee":null}}]}`
	want := []paritySearchRow{{"10001", "PAIR-1", "Owned café <&>", "self", []string{"run"}}, {"10002", "PAIR-2", "Control", "", []string{"run"}}}
	for _, lane := range []string{"native", "after", "before"} {
		t.Run(lane, func(t *testing.T) {
			valid := native
			if lane == "before" {
				valid = strings.Replace(valid, `,"assignee":null`, "", 1)
			}
			rows, err := paritySearchRows([]byte(valid), lane)
			if err != nil || !reflect.DeepEqual(rows, want) {
				t.Fatal("independent complete search fixture refused")
			}
			for _, member := range []struct{ native, changed string }{{`"issues"`, `"Issues"`}, {`"accountId"`, `"AccountId"`}} {
				t.Run("wrong-case-"+member.native, func(t *testing.T) {
					if _, err := paritySearchRows([]byte(strings.ReplaceAll(valid, member.native, member.changed)), lane); err == nil {
						t.Fatal("wrong-case native identity member admitted")
					}
				})
			}
			for _, invalid := range []string{`{}`, `{"issues":null}`, valid + `{}`, strings.Replace(valid, `"10002"`, `"10001"`, 1), strings.Replace(valid, `"PAIR-2"`, `"PAIR-1"`, 1), strings.Replace(valid, `"summary":"Control"`, `"summary":null`, 1), strings.Replace(valid, `"labels":["run"]`, `"labels":null`, 1), strings.Replace(valid, `"accountId":"self"`, `"accountId":""`, 1), strings.Replace(valid, `"id":"10001"`, `"id":"other","id":"10001"`, 1)} {
				if _, err := paritySearchRows([]byte(invalid), lane); err == nil {
					t.Fatal("search oracle admitted malformed or ambiguous native rows")
				}
			}
			if lane != "before" {
				if _, err := paritySearchRows([]byte(strings.Replace(valid, `,"assignee":null`, "", 1)), lane); err == nil {
					t.Fatal("missing current assignee admitted as explicit null")
				}
			}
		})
	}
}

func TestParitySearchPageRejectFalseCompletion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ lane, raw string }{
		{"before", `{"meta":{"pagination":{"startAt":0,"maxResults":1,"isLast":false,"nextCursor":"opaque"}},"data":{"issues":[]}}`},
		{"after", `{"data":{"issues":[],"pagination":{"returned":1,"is_last":false,"next_page_token":"opaque"},"truncated":false}}`},
	} {
		t.Run(tc.lane, func(t *testing.T) {
			if !paritySearchPage([]byte(tc.raw), tc.lane, "json", 1, 1, false, "opaque", false) {
				t.Fatal("independent bounded page fixture refused")
			}
			for _, invalid := range []string{`{}`, tc.raw + `{}`, strings.ReplaceAll(tc.raw, `"opaque"`, `"different"`), strings.ReplaceAll(tc.raw, `false`, `true`), strings.Replace(tc.raw, `"pagination":`, `"pagination":null,"pagination":`, 1)} {
				if paritySearchPage([]byte(invalid), tc.lane, "json", 1, 1, false, "opaque", false) {
					t.Fatal("page oracle admitted false cursor, completion or duplicate members")
				}
			}
		})
	}
}

func TestParitySearchPageRejectsMisplacedCurrentMembers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, raw string }{
		{"pagination", `{"pagination":{"returned":1,"is_last":false,"next_page_token":"opaque"},"data":{"issues":[],"truncated":false}}`},
		{"truncated", `{"truncated":false,"data":{"issues":[],"pagination":{"returned":1,"is_last":false,"next_page_token":"opaque"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if paritySearchPage([]byte(tc.raw), "after", "json", 1, 1, false, "opaque", false) {
				t.Fatal("envelope member satisfied a missing data member")
			}
		})
	}
}
