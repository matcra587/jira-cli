package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

func TestIssueViewHeaderFieldPresence(t *testing.T) {
	t.Parallel()
	type viewCase struct {
		name, selection, fields, human string
	}
	cases := []viewCase{
		{
			name: "no header fields", selection: "customfield_10011",
			fields: `{"customfield_10011":null}`, human: "ABC-1\n",
		},
		{
			name: "summary only", selection: "summary",
			fields: `{"summary":"Header fixture"}`, human: "ABC-1  Header fixture\n",
		},
		{
			name: "status only", selection: "status",
			fields: `{"status":{"name":"Open"}}`, human: "ABC-1  Open\n",
		},
		{
			name: "all null", selection: "summary,status,issuetype,priority,assignee,reporter,labels",
			fields: `{"summary":null,"status":null,"issuetype":null,"priority":null,` +
				`"assignee":null,"reporter":null,"labels":null}`,
			human: "ABC-1  -  -\nType:      -\nPriority:  -\nAssignee:  -\nReporter:  -\nLabels:    -\n",
		},
	}
	for _, field := range []struct {
		name, label, empty, value, human string
	}{
		{name: "summary", empty: `""`, value: `"Distinct summary"`, human: "Distinct summary"},
		{name: "status", empty: `{"name":""}`, value: `{"name":"In progress"}`, human: "In progress"},
		{name: "issuetype", label: "Type", empty: `{}`, value: `{"name":"Task"}`, human: "Task"},
		{name: "priority", label: "Priority", empty: `{"name":""}`, value: `{"name":"Routine"}`, human: "Routine"},
		{name: "assignee", label: "Assignee", empty: `{}`, value: `{"displayName":"Riley"}`, human: "Riley"},
		{
			name: "reporter", label: "Reporter", empty: `{"displayName":""}`,
			value: `{"displayName":"Morgan"}`, human: "Morgan",
		},
		{name: "labels", label: "Labels", empty: `[]`, value: `["alpha","beta"]`, human: "alpha, beta"},
	} {
		for _, state := range []struct{ name, value, human string }{
			{name: "null", value: "null", human: "-"},
			{name: "empty", value: field.empty, human: "-"},
			{name: "value", value: field.value, human: field.human},
		} {
			selection := "summary," + field.name
			fields := `{"summary":"Header fixture","` + field.name + `":` + state.value + `}`
			human := "ABC-1  Header fixture\n" + field.label + ":  " + state.human + "\n"
			switch field.name {
			case "summary":
				selection = "summary"
				fields = `{"summary":` + state.value + `}`
				human = "ABC-1  " + state.human + "\n"
			case "status":
				human = "ABC-1  Header fixture  " + state.human + "\n"
			}
			cases = append(cases, viewCase{
				name: field.name + "/" + state.name, selection: selection, fields: fields, human: human,
			})
		}
	}
	for _, tc := range cases {
		for _, mode := range []string{"human", "json", "compact"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					isIssueRead := r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/ABC-1"
					if !isIssueRead || r.URL.Query().Get("fields") != tc.selection {
						t.Errorf(
							"issue read = %s %s; want GET issue with fields %q",
							r.Method, r.URL, tc.selection,
						)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"10001","key":"ABC-1","fields":` + tc.fields + `}`))
				}))
				t.Cleanup(server.Close)
				args := []string{
					"issue", "view", "ABC-1", "--fields=" + tc.selection,
					"--comments=0", "--color=never", "--output=" + mode,
				}
				stdout, stderr, exit := runBinaryEnv(t, searchEnv(server.URL), args...)
				if exit != 0 || stderr != "" {
					t.Fatalf("issue view: exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
				}
				if got := requests.Load(); got != 1 {
					t.Fatalf("issue reads = %d; want exactly one", got)
				}
				if mode == "human" {
					if stdout != tc.human {
						t.Fatalf("human header = %q; want %q", stdout, tc.human)
					}
					return
				}
				var got struct {
					Issue struct {
						ID, Key string
						Fields  map[string]any
					}
				}
				if err := json.Unmarshal(adfData(t, stdout, mode, "issue.view", true), &got); err != nil {
					t.Fatal(err)
				}
				var want map[string]any
				if err := json.Unmarshal([]byte(tc.fields), &want); err != nil {
					t.Fatal(err)
				}
				if got.Issue.ID != "10001" || got.Issue.Key != "ABC-1" {
					t.Fatalf("native issue identity = %+v; want id=10001 key=ABC-1", got.Issue)
				}
				if !reflect.DeepEqual(got.Issue.Fields, want) {
					t.Fatalf("native fields = %+v; want %s", got.Issue.Fields, tc.fields)
				}
			})
		}
	}
}

func TestIssueListFieldPresence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, selection, columns, issues string
		want                             [][]string
	}{
		{
			name: "default columns", selection: "summary", issues: `{"id":"1","key":"ABC-1","fields":{"summary":"First"}}`,
			want: [][]string{{"KEY", "SUMMARY"}, {"ABC-1", "First"}},
		},
		{
			name: "mixed presence", selection: "summary,assignee", issues: `{"id":"1","key":"ABC-1","fields":{"summary":"First"}},` +
				`{"id":"2","key":"ABC-2","fields":{"summary":"Second","assignee":null}},` +
				`{"id":"3","key":"ABC-3","fields":{"summary":"Third","assignee":{"displayName":"Riley"}}}`,
			want: [][]string{{"KEY", "SUMMARY", "ASSIGNEE"}, {"ABC-1", "First", "not", "returned"}, {"ABC-2", "Second", "-"}, {"ABC-3", "Third", "Riley"}},
		},
		{
			name: "explicit unreturned column", selection: "summary,assignee", columns: "assignee,key", issues: `{"id":"1","key":"ABC-1","fields":{"summary":"First"}}`,
			want: [][]string{{"ASSIGNEE", "KEY"}, {"not", "returned", "ABC-1"}},
		},
	} {
		for _, mode := range []string{"human", "json", "compact"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					var req struct {
						Fields []string `json:"fields"`
					}
					if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" || json.NewDecoder(r.Body).Decode(&req) != nil || strings.Join(req.Fields, ",") != tc.selection {
						t.Errorf("search request = %s %s fields=%v", r.Method, r.URL, req.Fields)
					}
					_, _ = w.Write([]byte(`{"isLast":true,"issues":[` + tc.issues + `]}`))
				}))
				t.Cleanup(server.Close)
				args := []string{"issue", "list", "--jql=project = ABC", "--fields=" + tc.selection, "--output=" + mode, "--color=never"}
				if tc.columns != "" {
					args = append(args, "--columns="+tc.columns)
				}
				stdout, stderr, exit := runBinaryEnv(t, searchEnv(server.URL), args...)
				if exit != 0 || stderr != "" || calls.Load() != 1 {
					t.Fatalf("list: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
				}
				if mode == "human" {
					lines := strings.Split(stdout, "\n")
					if len(lines) < len(tc.want) {
						t.Fatalf("missing table rows: %q", stdout)
					}
					for i, want := range tc.want {
						if got := strings.Fields(lines[i]); !reflect.DeepEqual(got, want) {
							t.Errorf("row %d = %v; want %v", i, got, want)
						}
					}
					return
				}
				var got struct {
					Issues []any `json:"issues"`
				}
				if err := json.Unmarshal(adfData(t, stdout, mode, "issue.list", true), &got); err != nil {
					t.Fatal(err)
				}
				var want []any
				if err := json.Unmarshal([]byte("["+tc.issues+"]"), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got.Issues, want) {
					t.Fatalf("native issues = %v; want %v", got.Issues, want)
				}
			})
		}
	}
}

func TestIssueViewCommentPresence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, member, want string }{
		{name: "absent", want: "Comment preview unavailable."},
		{name: "null", member: `,"comment":null`, want: "Comment preview unavailable."},
		{name: "missing total", member: `,"comment":{"comments":[]}`, want: "Comment preview unavailable."},
		{name: "zero", member: `,"comment":{"total":0,"comments":[]}`, want: "No comments."},
	} {
		for _, mode := range []string{"human", "json", "compact"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				body := `{"id":"1","key":"ABC-1","fields":{"summary":"First"` + tc.member + `}}`
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issue/ABC-1" || r.URL.Query().Get("fields") != "summary,comment" {
						t.Errorf("comment preview read = %s %s", r.Method, r.URL)
					}
					_, _ = w.Write([]byte(body))
				}))
				t.Cleanup(server.Close)
				stdout, stderr, exit := runBinaryEnv(t, searchEnv(server.URL), "issue", "view", "ABC-1", "--fields=summary,comment", "--output="+mode, "--color=never")
				if exit != 0 || stderr != "" || calls.Load() != 1 {
					t.Fatalf("preview: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
				}
				if mode == "human" {
					if !strings.Contains(stdout, tc.want) || (tc.name != "zero" && strings.Contains(stdout, "No comments.")) {
						t.Fatalf("preview = %q; want %q", stdout, tc.want)
					}
					return
				}
				var got struct {
					Issue any `json:"issue"`
				}
				var want any
				if err := json.Unmarshal(adfData(t, stdout, mode, "issue.view", true), &got); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(body), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got.Issue, want) {
					t.Fatalf("native issue = %v; want %v", got.Issue, want)
				}
			})
		}
	}
}

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
