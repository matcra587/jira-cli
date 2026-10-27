package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestIssueReadOptionalMetadata(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, verb, endpoint, body, member, want, human, forbidden string
		array                                                      string
	}{
		{name: "watchers absent", verb: "watchers", endpoint: "/watchers", body: `{}`, member: "watchers", human: "Watchers were not returned.", forbidden: "No watchers."},
		{name: "watchers null", verb: "watchers", endpoint: "/watchers", body: `{"watchers":null,"watchCount":null,"isWatching":null}`, member: "watchers", want: "null", human: "Watchers unavailable.", forbidden: "No watchers."},
		{name: "watchers empty", verb: "watchers", endpoint: "/watchers", body: `{"watchers":[],"watchCount":0,"isWatching":false}`, member: "watchers", want: "[]", human: "No watchers."},
		{name: "watchers hidden", verb: "watchers", endpoint: "/watchers", body: `{"watchCount":3}`, member: "watchers", human: "3 watching, but this account cannot see who.", forbidden: "No watchers."},
		{name: "activity absent", verb: "watchers", endpoint: "/watchers", body: `{"watchers":[{"accountId":"fixture","displayName":"Riley"}]}`, array: "watchers", member: "active", human: "not returned", forbidden: "No watchers."},
		{name: "activity null", verb: "watchers", endpoint: "/watchers", body: `{"watchers":[{"accountId":"fixture","displayName":"Riley","active":null}]}`, array: "watchers", member: "active", want: "null", human: "Riley", forbidden: "no\n"},
		{name: "activity false", verb: "watchers", endpoint: "/watchers", body: `{"watchers":[{"accountId":"fixture","displayName":"Riley","active":false}]}`, array: "watchers", member: "active", want: "false", human: "no\n", forbidden: "not returned"},
		{name: "links absent", verb: "link", body: `{"id":"1","key":"ABC-1","fields":{}}`, member: "links", human: "Links were not returned.", forbidden: "No links."},
		{name: "links null", verb: "link", body: `{"id":"1","key":"ABC-1","fields":{"issuelinks":null}}`, member: "links", want: "null", human: "Links unavailable.", forbidden: "No links."},
		{name: "links empty", verb: "link", body: `{"id":"1","key":"ABC-1","fields":{"issuelinks":[]}}`, member: "links", want: "[]", human: "No links."},
		{name: "size absent", verb: "attachment", body: `{"id":"1","key":"ABC-1","fields":{"attachment":[{"id":"42","filename":"fixture.txt"}]}}`, array: "attachments", member: "size", human: "not returned", forbidden: "0 B"},
		{name: "size null", verb: "attachment", body: `{"id":"1","key":"ABC-1","fields":{"attachment":[{"id":"42","filename":"fixture.txt","size":null}]}}`, array: "attachments", member: "size", want: "null", human: "fixture.txt", forbidden: "0 B"},
		{name: "size zero", verb: "attachment", body: `{"id":"1","key":"ABC-1","fields":{"attachment":[{"id":"42","filename":"fixture.txt","size":0}]}}`, array: "attachments", member: "size", want: "0", human: "0 B", forbidden: "not returned"},
	} {
		for _, mode := range []string{"human", "json", "compact"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issue/ABC-1"+tc.endpoint {
						t.Errorf("read = %s %s", r.Method, r.URL)
					}
					_, _ = w.Write([]byte(tc.body))
				}))
				t.Cleanup(server.Close)
				stdout, stderr, exit := runBinaryEnv(t, searchEnv(server.URL), "issue", tc.verb, "list", "ABC-1", "--output="+mode, "--color=never")
				if exit != 0 || stderr != "" || calls.Load() != 1 {
					t.Fatalf("read: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
				}
				if mode == "human" {
					if !strings.Contains(stdout, tc.human) || (tc.forbidden != "" && strings.Contains(stdout, tc.forbidden)) {
						t.Fatalf("human = %q; want %q without %q", stdout, tc.human, tc.forbidden)
					}
					if tc.want == "null" && tc.array != "" {
						lines := strings.Split(strings.TrimSpace(stdout), "\n")
						cells := strings.Fields(lines[len(lines)-1])
						index := len(cells) - 1
						if tc.member == "size" {
							index = 2
						}
						if index < 0 || index >= len(cells) || cells[index] != "-" {
							t.Fatalf("null %s cell must be a dash: %q", tc.member, stdout)
						}
					}
					return
				}
				var out struct {
					Results []struct {
						Issue struct{ Key string }
						OK    bool
						Data  map[string]json.RawMessage
					}
				}
				if err := json.Unmarshal(adfData(t, stdout, mode, "issue."+tc.verb+".list", true), &out); err != nil {
					t.Fatal(err)
				}
				if len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Issue.Key != "ABC-1" {
					t.Fatalf("read identity/outcome = %+v", out)
				}
				data := out.Results[0].Data
				if tc.verb == "watchers" {
					var native map[string]json.RawMessage
					if err := json.Unmarshal([]byte(tc.body), &native); err != nil {
						t.Fatal(err)
					}
					for wire, name := range map[string]string{"watchCount": "watch_count", "isWatching": "is_watching"} {
						if string(data[name]) != string(native[wire]) {
							t.Errorf("%s = %s; want native %s", name, data[name], native[wire])
						}
					}
				}
				if tc.array != "" {
					var rows []map[string]json.RawMessage
					if err := json.Unmarshal(data[tc.array], &rows); err != nil {
						t.Fatal(err)
					}
					if len(rows) != 1 {
						t.Fatalf("%s = %s; want one identified row", tc.array, data[tc.array])
					}
					data = rows[0]
					id, wantID := "id", `"42"`
					if tc.array == "watchers" {
						id, wantID = "account_id", `"fixture"`
					}
					if string(data[id]) != wantID {
						t.Fatalf("%s = %s; want %s", id, data[id], wantID)
					}
				}
				got, present := data[tc.member]
				if tc.want == "" {
					if present {
						t.Fatalf("absent %s fabricated as %s", tc.member, got)
					}
					return
				}
				var actual, expected any
				if err := json.Unmarshal(got, &actual); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(tc.want), &expected); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(actual, expected) {
					t.Fatalf("%s = %s; want %s", tc.member, got, tc.want)
				}
			})
		}
	}
}
