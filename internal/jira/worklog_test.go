package jira

import (
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestWorklogDurationWorkdaysAndComponents(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		input     string
		day, want int
	}{
		{"2h30m", 28800, 9000},
		{"1d", 28800, 28800},
		{"45m", 28800, 2700},
		{" 1d 4h 30m ", 28800, 45000},
		{"1d2h", 21600, 28800},
		{"1d", 0, 28800},
		{"1d", -1, 28800},
		{"0h1m", 28800, 60},
		{"1m1h1m", 28800, 3720},
		{"1d", math.MaxInt, math.MaxInt},
	} {
		t.Run(tc.input+"/"+strconv.Itoa(tc.day), func(t *testing.T) {
			t.Parallel()
			got, err := ParseWorklogDuration(tc.input, tc.day)
			if err != nil || got != tc.want {
				t.Fatalf("duration = %d, %v; want %d", got, err, tc.want)
			}
		})
	}
}

func TestWorklogDurationRejectsMalformedAndOverflow(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		"", " ", "0m", "0d0h", "-1h", "+1h", "3w", "3s", "2", "1.5h",
		"1 h", "1H", "١h", "１h", "1hgarbage", "1m 2", "1m\x003m",
		strconv.Itoa(math.MaxInt) + "h", strconv.Itoa(math.MaxInt) + "0m",
		strconv.Itoa(math.MaxInt/60) + "m1m",
		strconv.Itoa(math.MaxInt/60*2+2) + "m",
	} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			if got, err := ParseWorklogDuration(input, DefaultWorkdaySeconds); err == nil || got != 0 {
				t.Fatalf("invalid duration accepted: %d %v", got, err)
			}
		})
	}
	if got, err := ParseWorklogDuration("1d1m", math.MaxInt); err == nil || got != 0 {
		t.Fatalf("overflowing sum accepted: %d %v", got, err)
	}
	if got, err := ParseWorklogDuration("2d", math.MaxInt); err == nil || got != 0 {
		t.Fatalf("overflowing product accepted: %d %v", got, err)
	}
	if got, err := ParseWorklogDuration("3d", math.MaxInt); err == nil || got != 0 {
		t.Fatalf("overflowing product wrapped positive: %d %v", got, err)
	}
}

func TestWorklogWirePreservesNullableAndRawData(t *testing.T) {
	t.Parallel()
	const body = "{\"id\":\"100\",\"author\":{\"accountId\":\"u\",\"displayName\":null,\"future\":9007199254740993},\"comment\":{\"type\":\"doc\",\"version\":1,\"content\":[],\"future\":\"<>&\u2028\u2029\"},\"timeSpentSeconds\":0,\"started\":null,\"future\":[false,0,null,[],{}]}"
	var worklog Worklog
	if err := json.Unmarshal([]byte(body), &worklog); err != nil {
		t.Fatal(err)
	}
	if !worklog.Started.Set || !worklog.Started.Null || worklog.Created.Set || !worklog.TimeSpentSeconds.Set || worklog.TimeSpentSeconds.Value != 0 {
		t.Fatalf("nullable facts lost: %+v", worklog)
	}
	if worklog.Author.Value.AccountID.Value != "u" || !worklog.Author.Value.DisplayName.Null || string(worklog.Author.Value.Extra["future"]) != "9007199254740993" {
		t.Fatalf("author facts lost: %+v", worklog.Author)
	}
	encoded, err := worklog.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var original, roundTrip map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &original); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"comment", "future"} {
		if string(roundTrip[key]) != string(original[key]) {
			t.Fatalf("%s changed: %s", key, roundTrip[key])
		}
	}
	if len(original) != len(roundTrip) {
		t.Fatal("wire members invented or lost")
	}
	for _, tc := range []struct {
		typ  any
		keys []string
	}{{Worklog{}, worklogKeys}, {PageOfWorklogs{}, pageOfWorklogsKeys}} {
		got := tagsOf(tc.typ)
		if len(got) != len(tc.keys) {
			t.Fatal("generated keys differ from tags")
		}
		for _, key := range got {
			if !slices.Contains(tc.keys, key) {
				t.Fatalf("missing declared key %q", key)
			}
		}
	}
}

func TestWorklogDecodeReplacesReceiverAtomically(t *testing.T) {
	t.Parallel()
	var worklog Worklog
	if err := json.Unmarshal([]byte(`{"id":"100","comment":null,"timeSpentSeconds":10,"future":false}`), &worklog); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"id":"101","comment":{"type":"doc","version":1,"content":[]}}`), &worklog); err != nil {
		t.Fatal(err)
	}
	if worklog.TimeSpentSeconds.Set || worklog.Comment.Null || !worklog.Comment.Set || len(worklog.Extra) != 0 {
		t.Fatalf("old worklog facts survived reuse: %+v", worklog)
	}
	before, err := worklog.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"id":"102","timeSpentSeconds":"invalid"}`), &worklog); err == nil {
		t.Fatal("invalid worklog decoded")
	}
	after, err := worklog.MarshalJSON()
	if err != nil || string(before) != string(after) {
		t.Fatalf("failed decode changed receiver: %s %s %v", before, after, err)
	}
	var page PageOfWorklogs
	if err := json.Unmarshal([]byte(`{"startAt":0,"maxResults":2,"total":1,"worklogs":[{"id":"100"}],"future":false}`), &page); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"startAt":0,"maxResults":2,"total":0}`), &page); err != nil {
		t.Fatal(err)
	}
	if page.Worklogs.Set || len(page.Extra) != 0 {
		t.Fatalf("old page facts survived reuse: %+v", page)
	}
}

func TestAddWorklogRequestPreservesSubmissionBytes(t *testing.T) {
	t.Parallel()
	const comment = "{\"type\":\"doc\",\"version\":1,\"content\":[],\"unknown\":\"<>&\u2028\u2029\\\\u2028\"}"
	req := AddWorklogRequest{TimeSpentSeconds: 90, Started: "2026-01-02T12:30:00.000+0000", Comment: json.RawMessage(comment)}
	encoded, err := req.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 3 || string(payload["comment"]) != comment || string(payload["timeSpentSeconds"]) != "90" {
		t.Fatalf("request changed: %s", encoded)
	}
	minimal, err := (AddWorklogRequest{TimeSpentSeconds: 60}).MarshalJSON()
	if err != nil || string(minimal) != `{"timeSpentSeconds":60}` {
		t.Fatalf("optional data invented: %s %v", minimal, err)
	}
	if strings.Contains(string(encoded), `\u003c`) {
		t.Fatal("ADF escaped")
	}
}
