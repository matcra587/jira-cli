package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

func TestIssueSearchPageMetadataSurvivesLaterFailure(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"list", "mine"} {
		for _, empty := range []bool{false, true} {
			t.Run(verb+"/empty="+strconv.FormatBool(empty), func(t *testing.T) {
				t.Parallel()
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if requests.Add(1) == 1 {
						issues := issueDetailsFixture
						if empty {
							issues = ""
						}
						_, _ = w.Write([]byte(`{"isLast":false,"nextPageToken":"resume","issues":[` + issues + `],"names":{"summary":"Summary"},"schema":null,"warnings":["native warning"],"futureCamelPage":{"rawText":"\u2028","zeroValue":0}}`))
						return
					}
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"errorMessages":["later read refused"]}`))
				}))
				t.Cleanup(server.Close)
				stdout, _, exit := runBinaryEnv(t, searchEnv(server.URL), "issue", verb, "--project=ABC", "--all", "--expand=names,schema", "--output=json", "--jq=.unavailable")
				var frame struct {
					OK   bool `json:"ok"`
					Data struct {
						Pages []map[string]json.RawMessage `json:"search_pages"`
					} `json:"data"`
				}
				if exit != cli.ExitValidation || requests.Load() != 2 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Data.Pages) != 1 {
					t.Fatalf("validated native page metadata lost on failure: exit=%d requests=%d stdout=%s", exit, requests.Load(), stdout)
				}
				page := frame.Data.Pages[0]
				if _, repeated := page["issues"]; repeated || string(page["schema"]) != "null" || string(page["names"]) != `{"summary":"Summary"}` || string(page["futureCamelPage"]) != `{"rawText":"\u2028","zeroValue":0}` || string(page["warnings"]) != `["native warning"]` || string(page["nextPageToken"]) != `"resume"` || string(page["isLast"]) != "false" {
					t.Fatalf("native page values changed or issues duplicated: %v", page)
				}
			})
		}
	}
}

// This fixture pins native spellings and explicit zero/null observations.
const issueDetailsFixture = `{"id":"10001","key":"ABC-1","fields":{"summary":"detail fixture","customfield_10011":null},"renderedFields":{"summary":"<b>detail & fixture</b>"},"names":{"customfield_10011":"Custom field"},"schema":null,"transitions":[],"operations":{"linkGroups":[]},"editmeta":{"fields":{}},"changelog":{"total":0,"histories":[]},"versionedRepresentations":{"summary":{"1":false}},"futureCamelMeta":{"zeroValue":0,"explicitNull":null,"nativeText":"<>&\u2028"}}`

func TestIssueReadExpansionsThroughTheBinary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, op, expand string
		args             []string
		search           bool
	}{
		{"view default", "issue.view", "", []string{"issue", "view", "ABC-1", "--comments=0"}, false},
		{"view narrowed", "issue.view", "", []string{"issue", "view", "ABC-1", "--fields=summary", "--comments=0"}, false},
		{"view explicit", "issue.view", "changelog,names", []string{"issue", "view", "ABC-1", "--expand=changelog,names", "--comments=0"}, false},
		{"view disabled", "issue.view", "", []string{"issue", "view", "ABC-1", "--expand=", "--comments=0"}, false},
		{"list detail", "issue.list", "renderedFields,names,schema,transitions,operations,changelog", []string{"issue", "list", "--project=ABC", "--detail"}, true},
		{"mine detail", "issue.mine", "renderedFields,names,schema,transitions,operations,changelog", []string{"issue", "mine", "--project=ABC", "--detail"}, true},
		{"list detail disabled", "issue.list", "", []string{"issue", "list", "--project=ABC", "--detail", "--expand="}, true},
		{"mine detail disabled", "issue.mine", "", []string{"issue", "mine", "--project=ABC", "--detail", "--expand="}, true},
		{"list detail replacement", "issue.list", "names", []string{"issue", "list", "--project=ABC", "--detail", "--expand=names"}, true},
		{"mine detail replacement", "issue.mine", "names", []string{"issue", "mine", "--project=ABC", "--detail", "--expand=names"}, true},
		{"list explicit", "issue.list", "schema,names", []string{"issue", "list", "--project=ABC", "--fields=summary", "--expand=schema,names"}, true},
		{"mine explicit", "issue.mine", "schema,names", []string{"issue", "mine", "--project=ABC", "--fields=summary", "--expand=schema,names"}, true},
	} {
		for _, mode := range []string{"json", "compact", "agent", "human"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					w.Header().Set("Content-Type", "application/json")
					if tc.search {
						var body struct {
							Expand string   `json:"expand"`
							Fields []string `json:"fields"`
						}
						if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" || json.NewDecoder(r.Body).Decode(&body) != nil || body.Expand != tc.expand {
							t.Errorf("search did not send the requested native expansion: method=%s path=%s expand=%q", r.Method, r.URL.Path, body.Expand)
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						if strings.Contains(tc.name, "detail") && (len(body.Fields) != 1 || body.Fields[0] != "*all") {
							t.Errorf("detail did not request all native fields: %v", body.Fields)
						}
						_, _ = w.Write([]byte(`{"isLast":true,"issues":[` + issueDetailsFixture + `]}`))
						return
					}
					if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issue/ABC-1" || r.URL.Query().Get("expand") != tc.expand {
						t.Errorf("issue read did not send native expansion: method=%s path=%s expand=%q", r.Method, r.URL.Path, r.URL.Query().Get("expand"))
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					_, _ = w.Write([]byte(issueDetailsFixture))
				}))
				t.Cleanup(server.Close)
				args := append([]string{}, tc.args...)
				if mode != "agent" {
					args = append(args, "--output="+mode)
				}
				env := searchEnv(server.URL)
				if mode == "agent" {
					env = append(env, "AGENT=1")
				}
				stdout, stderr, exit := runBinaryEnv(t, env, args...)
				if exit != 0 || requests.Load() != 1 || stderr != "" {
					t.Fatalf("expanded read: exit=%d requests=%d stdout=%s stderr=%s", exit, requests.Load(), stdout, stderr)
				}
				if mode == "human" {
					if !strings.Contains(stdout, "detail fixture") {
						t.Fatalf("expanded read lost the shared human view: %s", stdout)
					}
					return
				}
				format := mode
				if mode == "agent" {
					format = "compact"
				}
				var data struct {
					Issue  map[string]json.RawMessage   `json:"issue"`
					Issues []map[string]json.RawMessage `json:"issues"`
				}
				if err := json.Unmarshal(adfData(t, stdout, format, tc.op, true), &data); err != nil {
					t.Fatal(err)
				}
				issue := data.Issue
				if tc.search {
					if len(data.Issues) != 1 {
						t.Fatal("expanded search lost its issue")
					}
					issue = data.Issues[0]
				}
				var details map[string]json.RawMessage
				if json.Unmarshal(issue["details"], &details) != nil {
					t.Fatalf("native metadata missing: %s", issue["details"])
				}
				var native map[string]json.RawMessage
				if err := json.Unmarshal([]byte(issueDetailsFixture), &native); err != nil {
					t.Fatal(err)
				}
				delete(native, "id")
				delete(native, "key")
				delete(native, "fields")
				if len(details) != len(native) {
					t.Fatalf("metadata members dropped or duplicated identity/fields: got=%v want=%v", details, native)
				}
				for key, want := range native {
					if string(details[key]) != string(want) {
						t.Errorf("native %s changed: got=%s want=%s", key, details[key], want)
					}
				}
			})
		}
	}
}

func TestIssueExpansionRefusalsBeforeRequests(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"issue", "view", "ABC-1", "--expand=unknown"},
		{"issue", "list", "--project=ABC", "--expand=unknown"},
		{"issue", "mine", "--project=ABC", "--expand=unknown"},
		{"issue", "list", "--project=ABC", "--detail", "--fields=summary"},
		{"issue", "mine", "--project=ABC", "--detail", "--fields=summary"},
		{"issue", "list", "--project=ABC", "--detail", "--count"},
		{"issue", "mine", "--project=ABC", "--expand=names", "--count"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(http.StatusBadRequest) }))
			t.Cleanup(server.Close)
			stdout, _, exit := runBinaryEnv(t, searchEnv(server.URL), append(args, "--output=json")...)
			var frame apiFrame
			if exit != cli.ExitValidation || requests.Load() != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || (frame.Errors[0].Code != "flag_value_invalid" && frame.Errors[0].Code != "flag_conflict" && frame.Errors[0].Code != "flag_not_applicable") {
				t.Fatalf("expansion input was not locally validated: exit=%d requests=%d stdout=%s", exit, requests.Load(), stdout)
			}
		})
	}
}
