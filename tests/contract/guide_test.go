package contract

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	xos "github.com/gechr/x/os"
	"github.com/gechr/x/shell"
	docentexport "github.com/matcra587/docent/export"

	"github.com/matcra587/jira-cli/internal/agentguides"
	agentcmd "github.com/matcra587/jira-cli/internal/cli/agent"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func readGuideOutput(t *testing.T, stdout, mode, operation string) envelope.GuideOutput {
	t.Helper()
	payload := []byte(stdout)
	if mode == "json" {
		var f frame
		if err := json.Unmarshal(payload, &f); err != nil || !f.OK || f.Meta.Command != operation || f.Meta.ContractVersion != "1" {
			t.Fatal("guide retrieval lost the shared success frame")
		}
		payload = f.Data
	}
	var out envelope.GuideOutput
	if err := json.Unmarshal(payload, &out); err != nil || out.ContractVersion != "1" || out.Guides == nil || out.Document == "" {
		t.Fatal("guide retrieval lost its typed routing metadata or document")
	}
	var raw any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatal(err)
	}
	assertSnakeCaseKeys(t, "$", raw)
	return out
}

func TestGuideRetrievalModesAndDoors(t *testing.T) {
	t.Parallel()
	guides, err := agentguides.Load()
	if err != nil {
		t.Fatal(err)
	}
	index, err := docentexport.Index(guides.Guides(), "1")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := guides.Get("raw-api")
	save, _ := raw.Section("Save")
	cases := []struct {
		name, kind, slug, section, document string
		args                                []string
	}{
		{name: "index", kind: "index", document: index},
		{name: "runbook", kind: "runbook", slug: "raw-api", document: docentexport.Runbook{}.Render(raw), args: []string{"raw-api"}},
		{name: "section", kind: "section", slug: "raw-api", section: "Save", document: save.Body + "\n", args: []string{"raw-api", "--section=save"}},
		{name: "all", kind: "all", document: docentexport.Concat(guides.Guides()), args: []string{"--all"}},
		{name: "empty section", kind: "section", slug: "discover-cli", section: "Preconditions", document: "\n", args: []string{"discover-cli", "--section=Preconditions"}},
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	env := []string{"JIRA_BASE_URL=" + server.URL, "JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL="}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var first *envelope.GuideOutput
			for _, door := range [][]string{{"agent", "guide"}, {"guide"}} {
				operation := strings.Join(door, ".")
				for _, mode := range []string{"json", "compact", "human"} {
					stdout, stderr, exit := runBinaryEnv(t, env, slices.Concat(door, tc.args, []string{"--output=" + mode})...)
					if exit != 0 || stderr != "" {
						t.Fatalf("%s %s failed: exit=%d stderr=%q", operation, mode, exit, stderr)
					}
					if mode == "human" {
						want := strings.ReplaceAll(tc.document, docentexport.GuideSeparator, "\n")
						if stdout != want {
							t.Fatal("human rendering changed Markdown layout or leaked machine separators")
						}
						continue
					}
					out := readGuideOutput(t, stdout, mode, operation)
					if out.Kind != tc.kind || out.Slug != tc.slug || out.Section != tc.section || out.Document != tc.document {
						t.Fatal("guide selection changed the requested canonical document")
					}
					if first == nil {
						first = &out
					} else if !reflect.DeepEqual(*first, out) {
						t.Fatal("guide doors or machine modes returned different data")
					}
					if tc.kind == "index" || tc.kind == "all" {
						var slugs []string
						for _, g := range out.Guides {
							slugs = append(slugs, g.Slug)
							if g.Title == "" || g.Description == "" || g.WhenToUse == "" || g.Commands == nil {
								t.Fatal("index omitted required routing metadata")
							}
						}
						if !slices.Equal(slugs, []string{"discover-cli", "machine-output", "raw-api", "jql-discovery", "safe-mutation", "cache-metadata", "export-guides", "configure-theme", "update-cli"}) {
							t.Fatal("index lost canonical guide order or membership")
						}
					}
					if tc.kind == "runbook" {
						if strings.Contains(out.Document, "slug:") || strings.Contains(out.Document, "when_to_use:") {
							t.Fatal("single runbook leaked frontmatter")
						}
						for _, heading := range []string{"Decide", "Run", "Save", "Preconditions", "Recover", "Next"} {
							if strings.Count(out.Document, "## "+heading+"\n") != 1 {
								t.Fatalf("runbook lost section %s", heading)
							}
						}
					}
				}
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatal("local guide retrieval contacted Jira")
	}
}

func TestGuideNamesRecoveryAndHeadlessOutput(t *testing.T) {
	t.Parallel()
	for _, door := range [][]string{{"agent", "guide"}, {"guide"}} {
		operation := strings.Join(door, ".")
		for _, name := range []string{"machine-output", "output-contract", "MACHINE_OUTPUT", "machine"} {
			stdout, stderr, exit := runBinaryEnv(t, []string{"AGENT=1", "JIRA_KEYRING=false"}, slices.Concat(door, []string{name})...)
			if exit != 0 || stderr != "" {
				t.Fatalf("guide name %q did not resolve in headless mode", name)
			}
			out := readGuideOutput(t, stdout, "compact", operation)
			if out.Slug != "machine-output" || out.Kind != "runbook" {
				t.Fatal("name resolution did not retain the canonical guide identity")
			}
		}
		for _, mode := range []string{"json", "compact"} {
			for _, tc := range []struct {
				args        []string
				code, field string
			}{
				{[]string{"missing"}, "arg_value_invalid", "slug"},
				{[]string{"a"}, "arg_value_invalid", "slug"},
				{[]string{"raw-api", "--section=missing"}, "flag_value_invalid", "section"},
				{[]string{"--section=Run"}, "flag_not_applicable", "section"},
				{[]string{"raw-api", "--all"}, "flag_conflict", "all"},
			} {
				stdout, _, exit := runBinary(t, slices.Concat(door, tc.args, []string{"--output=" + mode})...)
				var failed struct {
					OK     bool `json:"ok"`
					Errors []struct {
						Code, Field string
						Suggestions []string
					} `json:"errors"`
				}
				if exit != 3 || json.Unmarshal([]byte(stdout), &failed) != nil || failed.OK || len(failed.Errors) != 1 || failed.Errors[0].Code != tc.code || failed.Errors[0].Field != tc.field {
					t.Fatalf("%v lost its typed failure: exit=%d stdout=%q", tc.args, exit, stdout)
				}
				if tc.code != "flag_conflict" && len(failed.Errors[0].Suggestions) == 0 {
					t.Fatal("guide selection failure omitted executable recovery")
				}
				for _, suggestion := range failed.Errors[0].Suggestions {
					args, err := shell.Split(strings.TrimPrefix(suggestion, "jira "))
					if err != nil {
						t.Fatal(err)
					}
					stdout, stderr, exit := runBinary(t, args...)
					if exit != 0 || stderr != "" {
						t.Fatalf("literal recovery failed: %q", suggestion)
					}
					readGuideOutput(t, stdout, "json", operation)
				}
			}
			_, _, exit := runBinary(t, slices.Concat(door, []string{"raw-api", "--section=Run", "--all", "--output=" + mode})...)
			if exit != 3 {
				t.Fatal("mutually exclusive guide selectors were accepted")
			}
		}
	}
}

func TestGuideHelpExamplesExecute(t *testing.T) {
	t.Parallel()
	guides, err := agentguides.Load()
	if err != nil {
		t.Fatal(err)
	}
	nested, _, err := agentcmd.New(agentcmd.Deps{Guides: guides}).Find([]string{"guide"})
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []struct{ example, operation string }{
		{nested.Example, "agent.guide"}, {agentcmd.NewGuide(guides).Example, "guide"},
	} {
		count := 0
		for line := range strings.SplitSeq(cmd.example, "\n") {
			if !strings.HasPrefix(line, "$ jira ") {
				continue
			}
			args, err := shell.Split(strings.TrimPrefix(line, "$ jira "))
			if err != nil {
				t.Fatal(err)
			}
			for _, agent := range []string{"0", "1"} {
				stdout, stderr, exit := runBinaryEnv(t, []string{"AGENT=" + agent, "JIRA_KEYRING=false"}, args...)
				if exit != 0 || stderr != "" {
					t.Fatalf("published guide example failed: %q", line)
				}
				switch count {
				case 0:
					readGuideOutput(t, stdout, "json", cmd.operation)
				case 1:
					g, _ := guides.Get("raw-api")
					run, _ := g.Section("Run")
					if strings.TrimSuffix(stdout, "\n") != run.Body+"\n" {
						t.Fatal("published jq extraction changed canonical section bytes")
					}
				case 2:
					out := readGuideOutput(t, stdout, "compact", cmd.operation)
					if out.Kind != "all" || strings.Count(out.Document, "\f\n") != 8 {
						t.Fatal("complete guide example lost unambiguous canonical boundaries")
					}
				}
			}
			count++
		}
		if count != 3 {
			t.Fatal("published guide examples changed without execution coverage")
		}
	}
}

func TestGuideCompletionIsLocalAndCanonical(t *testing.T) {
	t.Parallel()
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL="}, "--@complete=guide", "--@shell=fish")
	want := "discover-cli\tDiscover commands before acting\nmachine-output\tSelect and inspect machine output\nraw-api\tSend an explicit scoped Jira API request\njql-discovery\tDiscover JQL metadata and validate explicit queries\nsafe-mutation\tPreview and verify a Jira change\ncache-metadata\tPrime and inspect scoped metadata\nexport-guides\tInstall the CLI's guides as agent skills\nconfigure-theme\tSelect built-in and custom terminal themes\nupdate-cli\tInspect and update the installed CLI\n"
	if exit != 0 || stderr != "" || stdout != want {
		t.Fatalf("guide completion failed or lost routing metadata: exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
	}
}

func TestEmbeddedGuideRunRecipesExecute(t *testing.T) {
	t.Parallel()
	guides, err := agentguides.Load()
	if err != nil {
		t.Fatal(err)
	}
	searchBody := []byte(`{"jql":"project = KEY","fields":["summary"],"maxResults":1}`)
	searchFile := apiInput(t, searchBody)
	issueFile := apiInput(t, []byte(`{"fields":{"project":{"key":"KEY"},"issuetype":{"name":"Task"},"summary":"Preview"}}`))
	moveFile := apiInput(t, []byte(`{"sendBulkNotification":false,"targetToSourcesMapping":{"10000,10001":{"issueIdsOrKeys":["KEY-123"],"inferClassificationDefaults":true,"inferFieldDefaults":true,"inferStatusDefaults":true,"inferSubtaskTypeDefault":true}}}`))
	markdownFile := apiInput(t, []byte("**Review**"))
	fieldsFile := apiInput(t, []byte(`{"fields":{"issuetype":{"id":"10001"}}}`))
	transitionFile := apiInput(t, []byte(`{"transition":{"name":"Done"},"fields":{},"update":{},"historyMetadata":{"type":"guide"}}`))
	adfFile := apiInput(t, []byte(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Review"}]}]}`))
	aliasContent := "[aliases]\nmine='version'\n"
	aliasFile := aliasConfig(t, aliasContent)
	aliasImport := apiInput(t, []byte("newName: version\n"))
	newSettings := filepath.Join(t.TempDir(), "missing", "settings.toml")
	paletteFile := apiInput(t, []byte("base='light'\n[colors]\nhelp_command='#123456'\n"))
	issue := `{"id":"10123","key":"KEY-123","fields":{"summary":"Fixture","status":{"id":"1","name":"To Do"},"project":{"key":"KEY"},"issuetype":{"id":"10044","name":"Task"}}}`
	var reads, searches, userSearches, boardReads, projectReads, worklogReads, taskReads atomic.Int32
	var jqlParses, jqlReferences atomic.Int32
	var transitionReads atomic.Int32
	var inputMetadataReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /rest/api/3/project/KEY":
			inputMetadataReads.Add(1)
			_, _ = io.WriteString(w, `{"id":"10402","key":"KEY"}`)
		case "GET /rest/api/3/issue/createmeta/10402/issuetypes":
			inputMetadataReads.Add(1)
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"total":1,"issueTypes":[{"id":"10044","name":"Task"}]}`)
		case "GET /rest/api/3/issue/createmeta/10402/issuetypes/10044":
			inputMetadataReads.Add(1)
			_, _ = fmt.Fprintf(w, `{"startAt":0,"maxResults":50,"total":1,"fields":[%s]}`, inputDiscoveryField)
		case "GET /rest/api/3/issue/KEY-123/editmeta":
			inputMetadataReads.Add(1)
			_, _ = fmt.Fprintf(w, `{"fields":{"customfield_14002":%s}}`, inputDiscoveryField)
		case "POST /rest/api/3/jql/parse":
			jqlParses.Add(1)
			var request struct {
				Queries []string `json:"queries"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Queries) == 0 {
				t.Error("published parser recipe lost queries")
				w.WriteHeader(400)
				return
			}
			rows := make([]map[string]any, len(request.Queries))
			for i, query := range request.Queries {
				rows[i] = map[string]any{"query": query, "errors": []string{}}
				if query == "project = (" {
					rows[i]["errors"] = []string{"syntax error"}
				}
			}
			body, err := json.Marshal(map[string]any{"queries": rows})
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			_, _ = w.Write(body)
		case "GET /rest/api/3/jql/autocompletedata":
			jqlReferences.Add(1)
			_, _ = io.WriteString(w, jqlReferenceFixture)
		case "GET /rest/agile/1.0/board":
			boardReads.Add(1)
			_, _ = io.WriteString(w, `{"isLast":true,"startAt":0,"maxResults":50,"values":[{"id":42,"name":"Sprint","type":"scrum"}]}`)
		case "GET /rest/agile/1.0/board/42/project":
			projectReads.Add(1)
			_, _ = io.WriteString(w, `{"isLast":true,"startAt":0,"maxResults":50,"values":[{"id":"10042","key":"KEY"}]}`)
		case "GET /rest/api/3/user/search":
			userSearches.Add(1)
			if r.URL.Query().Get("query") != "sam" || r.URL.Query().Get("startAt") != "0" || r.URL.Query().Get("maxResults") != "50" {
				t.Error("published user search recipe changed query or paging")
			}
			_, _ = io.WriteString(w, `[{"accountId":"fixture-id","displayName":"Sam","active":true}]`)
		case "GET /rest/api/3/issue/KEY-123":
			reads.Add(1)
			_, _ = io.WriteString(w, issue)
		case "GET /rest/api/3/issue/KEY-123/transitions":
			transitionReads.Add(1)
			if r.URL.Query().Get("expand") != "transitions.fields" {
				t.Error("published transition recipe did not request screen metadata")
			}
			_, _ = io.WriteString(w, `{"transitions":[{"id":"31","name":"Finish","to":{"name":"Done"},"hasScreen":false,"fields":{}}]}`)
		case "GET /rest/api/3/bulk/queue/10000":
			taskReads.Add(1)
			_, _ = io.WriteString(w, `{"taskId":"10000","status":"RUNNING","progressPercent":100}`)
		case "GET /rest/api/3/issue/KEY-123/worklog":
			worklogReads.Add(1)
			if r.URL.Query().Get("startAt") != "0" || r.URL.Query().Get("maxResults") != "100" {
				t.Error("published worklog drain recipe ignored page policy")
			}
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":100,"total":1,"worklogs":[{"id":"100","timeSpentSeconds":60}]}`)
		case "POST /rest/api/3/search/jql":
			searches.Add(1)
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != string(searchBody) {
				t.Error("published search recipe changed the native request body")
			}
			_, _ = io.WriteString(w, `{"issues":[`+issue+`],"isLast":true}`)
		default:
			t.Errorf("published guide recipe sent an unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	env := append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true")
	count := 0
	managedWork := t.TempDir()
	managedBinary := filepath.Join(managedWork, "mise", "installs", "github-jira", "owned", "bin", "jira")
	if err := os.MkdirAll(filepath.Dir(managedBinary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := xos.CopyFile(jiraBinary(t), managedBinary); err != nil {
		t.Fatal(err)
	}
	for g := range guides.All() {
		run, _ := g.Section("Run")
		for line := range strings.SplitSeq(run.Body, "\n") {
			if !strings.HasPrefix(line, "jira ") {
				continue
			}
			args, err := shell.Split(strings.TrimPrefix(line, "jira "))
			if err != nil {
				t.Fatal(err)
			}
			for i, arg := range args {
				switch arg {
				case "new-settings.toml":
					args[i] = newSettings
				case "./palette.toml":
					args[i] = paletteFile
				case "search.json":
					args[i] = searchFile
				case "issue.json":
					args[i] = issueFile
				case "move.json":
					args[i] = moveFile
				case "fields.json":
					args[i] = fieldsFile
				case "transition.json":
					args[i] = transitionFile
				case "notes.md":
					args[i] = markdownFile
				case "document.json":
					args[i] = adfFile
				case "aliases.toml":
					args[i] = aliasFile
				case "aliases.yaml":
					args[i] = aliasImport
				}
			}
			var stdout, stderr string
			var exit int
			if g.Slug == "update-cli" {
				stdout, stderr, exit = runExecutableIOInDirStarted(t, managedBinary, managedWork, env, nil, nil, nil, args...)
			} else {
				stdout, stderr, exit = runBinaryEnv(t, env, args...)
			}
			if exit != 0 || stderr != "" || stdout == "" {
				t.Fatalf("guide %s recipe failed: %q exit=%d stderr=%q", g.Slug, line, exit, stderr)
			}
			if slices.Contains(args, "--dry-run") {
				var f frame
				if json.Unmarshal([]byte(stdout), &f) != nil || !f.OK || !strings.Contains(string(f.Data), `"dry_run":true`) {
					t.Fatal("published mutation preview did not retain its dry-run receipt")
				}
			}
			if slices.Contains(args, "--generate-json") {
				var f frame
				if json.Unmarshal([]byte(stdout), &f) != nil || !f.OK || f.Meta.Command != "issue."+args[1]+".input" {
					t.Fatal("published live input recipe did not execute the typed read operation")
				}
			}
			if strings.HasPrefix(line, "jira issue transition ") {
				var f nativeTransitionFrame
				var payload map[string]json.RawMessage
				if json.Unmarshal([]byte(stdout), &f) != nil || !f.OK || json.Unmarshal(f.Data.Payload, &payload) != nil ||
					string(payload["transition"]) != `{"name":"Done"}` || string(payload["fields"]) != "{}" || string(payload["update"]) != "{}" || string(payload["historyMetadata"]) != `{"type":"guide"}` || len(f.Data.Results) != 1 ||
					!f.Data.Results[0].OK || f.Data.Results[0].Data.Attempted || f.Data.Results[0].Data.Applied {
					t.Fatal("published native transition preview lost input or claimed a write")
				}
				if slices.Contains(args, "--validate-remote") {
					if json.Unmarshal(f.Data.Results[0].Data.Payload, &payload) != nil || string(payload["transition"]) != `{"id":"31"}` {
						t.Fatal("published remote transition preview lost resolved target")
					}
				}
			}
			count++
		}
	}
	stored, err := os.ReadFile(aliasFile)
	if err != nil || string(stored) != aliasContent {
		t.Fatal("published local alias previews changed settings")
	}
	if _, err := os.Stat(filepath.Dir(newSettings)); !os.IsNotExist(err) {
		t.Fatal("published initialization preview created settings or its parent")
	}
	if count != 80 || inputMetadataReads.Load() != 4 || reads.Load() != 2 || searches.Load() != 1 || userSearches.Load() != 3 || boardReads.Load() != 3 || projectReads.Load() != 3 || worklogReads.Load() != 1 || taskReads.Load() != 1 || jqlParses.Load() != 3 || jqlReferences.Load() != 1 || transitionReads.Load() != 1 {
		t.Fatalf("guide recipe coverage changed: recipes=%d GET=%d search=%d user-search=%d boards=%d projects=%d worklogs=%d", count, reads.Load(), searches.Load(), userSearches.Load(), boardReads.Load(), projectReads.Load(), worklogReads.Load())
	}
}
