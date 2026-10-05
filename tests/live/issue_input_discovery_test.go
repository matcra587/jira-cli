//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

// TestLiveIssueInputDiscoveryCommands exercises the exposed read variants with
// read-only policy enabled. It owns no remote resources and never submits the
// discovered skeletons. Independent raw reads establish identities and complete
// inventories without using the production metadata decoder or collector.
func TestLiveIssueInputDiscoveryCommands(t *testing.T) {
	r := newLiveRun(t)
	identityFrame, identityRaw, identityExit := r.invoke(t, t.Context(), nil, "auth", "status")
	var identity struct {
		Data envelope.AuthStatusOutput `json:"data"`
	}
	if identityExit != 0 || !identityFrame.OK || json.Unmarshal(identityRaw, &identity) != nil || strings.TrimSpace(identity.Data.Email) == "" {
		t.Fatal("verified CLI identity cannot select the owned discovery history cache")
	}
	key := requiredEnv(t, "JIRA_LIVE_READ_KEY")
	if !strings.HasPrefix(key, r.manifest.Project+"-") {
		t.Fatal("input-discovery fixture is outside the dedicated project")
	}
	endpoint := "/rest/api/3/issue/" + key
	before := rawRead(t, r, "api", "get", endpoint, "--query=fields=project,issuetype,summary,description,status,updated")
	var fixture struct {
		Fields struct {
			Project struct {
				ID  string `json:"id"`
				Key string `json:"key"`
			} `json:"project"`
			Type struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"issuetype"`
		} `json:"fields"`
	}
	if json.Unmarshal(before.Response.JSON, &fixture) != nil || fixture.Fields.Project.Key != r.manifest.Project || fixture.Fields.Project.ID == "" || fixture.Fields.Type.ID == "" || fixture.Fields.Type.Name == "" {
		t.Fatal("independent issue read cannot establish the discovery context")
	}
	projectID, typeID := fixture.Fields.Project.ID, fixture.Fields.Type.ID
	project := rawRead(t, r, "api", "get", "/rest/api/3/project/"+projectID)
	typeEndpoint := "/rest/api/3/issue/createmeta/" + projectID + "/issuetypes"
	types, typePages := independentInputInventory(t, r, typeEndpoint, "issueTypes", "id")
	fields, fieldPages := independentInputInventory(t, r, typeEndpoint+"/"+typeID, "fields", "fieldId")
	if types[typeID] == nil || len(fields) == 0 {
		t.Fatal("independent create inventory lacks the fixture type or fields")
	}
	for _, projectSelector := range []string{r.manifest.Project, projectID} {
		for _, typeSelector := range []string{fixture.Fields.Type.Name, typeID} {
			frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "issue", "create", "--generate-json", "--project="+projectSelector, "--type="+typeSelector)
			var result struct {
				Meta struct {
					Command string `json:"command"`
				} `json:"meta"`
				Data envelope.IssueCreateInputOutput `json:"data"`
			}
			if exit != 0 || !frame.OK || json.Unmarshal(stdout, &result) != nil || result.Meta.Command != "issue.create.input" || !result.Data.Complete || result.Data.Fields == nil || len(result.Data.TypePages) == 0 || len(result.Data.FieldPages) == 0 {
				t.Fatal("exposed create-input read lacks a complete typed receipt")
			}
			assertInputNativeEqual(t, result.Data.Project, project.Response.JSON)
			assertInputNativeEqual(t, result.Data.IssueType, types[typeID])
			assertInputFieldsEqual(t, *result.Data.Fields, fields)
			assertInputSkeleton(t, result.Data.Payload, fields, projectID, typeID)
		}
	}
	// A locally unknown type retains the complete independently inventoried
	// native candidate set, rather than masquerading as Jira's HTTP 404.
	frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "issue", "create", "--generate-json", "--project="+projectID, "--type=__input_discovery_unknown_type__")
	var unresolved struct {
		Data envelope.IssueCreateInputOutput `json:"data"`
	}
	if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "issue_type_unknown" || json.Unmarshal(stdout, &unresolved) != nil || unresolved.Data.Complete || unresolved.Data.Fields != nil || unresolved.Data.Payload != nil || unresolved.Data.IssueTypes == nil || len(*unresolved.Data.IssueTypes) != len(types) {
		t.Fatal("live unknown-type refusal lost native candidates or fabricated input")
	}
	seenCandidates := make(map[string]bool)
	for _, candidate := range *unresolved.Data.IssueTypes {
		var identity struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(candidate, &identity) != nil || types[identity.ID] == nil || seenCandidates[identity.ID] {
			t.Fatal("live refusal returned a candidate outside the independent inventory")
		}
		seenCandidates[identity.ID] = true
		assertInputNativeEqual(t, candidate, types[identity.ID])
	}
	edit := rawRead(t, r, "api", "get", endpoint+"/editmeta")
	var editable struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if json.Unmarshal(edit.Response.JSON, &editable) != nil || editable.Fields == nil {
		t.Fatal("independent edit read lacks an explicit field map")
	}
	path := filepath.Join(r.dir, "discovery-targets.txt")
	if err := os.WriteFile(path, []byte(key+"\n"), 0o600); err != nil {
		t.Fatal("cannot save the local discovery target list")
	}
	for i, selector := range [][]string{{key}, {"--jql=key = " + key}, {"--from-file=" + path}} {
		cacheDir := filepath.Join(r.dir, fmt.Sprintf("discovery-history-%d", i))
		liveEnv := []string{"JIRA_READ_ONLY=true", "XDG_CACHE_HOME=" + cacheDir}
		localEnv := append(slices.Clone(liveEnv), "JIRA_EMAIL="+identity.Data.Email, "JIRA_API_TOKEN=", "JIRA_KEYRING=false", "DBUS_SESSION_BUS_ADDRESS=/dev/null")
		readHistory := func(want []string) {
			t.Helper()
			frame, raw, exit := r.invoke(t, t.Context(), localEnv, "cache", "issuekeys")
			var history struct {
				Data envelope.CacheIssueKeysOutput `json:"data"`
			}
			if exit != 0 || !frame.OK || json.Unmarshal(raw, &history) != nil || !slices.Equal(history.Data.IssueKeys, want) || history.Data.Count != len(want) {
				t.Fatal("native input discovery did not persist its independently read identity")
			}
		}
		readHistory([]string{})
		if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
			t.Fatal("local empty discovery history read created storage")
		}
		args := append([]string{"issue", "edit", "--generate-json"}, selector...)
		frame, stdout, exit := r.invoke(t, t.Context(), liveEnv, args...)
		var result struct {
			Meta struct {
				Command string `json:"command"`
			} `json:"meta"`
			Data envelope.IssueEditInputOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(stdout, &result) != nil || result.Meta.Command != "issue.edit.input" || len(result.Data.Results) != 1 || !result.Data.Results[0].OK || result.Data.Results[0].Issue.Key != key || result.Data.Results[0].Data == nil {
			t.Fatal("exposed edit-input selector lacks its requested read context")
		}
		data := result.Data.Results[0].Data
		assertInputNativeEqual(t, data.Metadata, edit.Response.JSON)
		assertInputFieldsEqual(t, data.Fields, editable.Fields)
		assertInputSkeleton(t, data.Payload, editable.Fields, "", "")
		readHistory([]string{key})
	}
	after := rawRead(t, r, "api", "get", endpoint, "--query=fields=project,issuetype,summary,description,status,updated")
	assertInputNativeEqual(t, after.Response.JSON, before.Response.JSON)
	if len(r.manifest.Issues) != 0 || len(r.manifest.Attempts) != 0 {
		t.Fatal("read discovery created an owned mutation fixture")
	}
	t.Logf("exposed read routes match %d type pages, %d field pages, %d create fields and %d edit fields; fixture unchanged", typePages, fieldPages, len(fields), len(editable.Fields))
}

func independentInputInventory(t *testing.T, r *liveRun, endpoint, member, identity string) (map[string]json.RawMessage, int) {
	t.Helper()
	entries := make(map[string]json.RawMessage)
	start, total, pages := 0, -1, 0
	for {
		result := rawRead(t, r, "api", "get", endpoint, fmt.Sprintf("--query=startAt=%d", start), "--query=maxResults=1")
		var facts struct {
			Start *int `json:"startAt"`
			Total *int `json:"total"`
		}
		var native map[string]json.RawMessage
		var rows []json.RawMessage
		if json.Unmarshal(result.Response.JSON, &facts) != nil || facts.Start == nil || *facts.Start != start || facts.Total == nil || *facts.Total < start || json.Unmarshal(result.Response.JSON, &native) != nil || json.Unmarshal(native[member], &rows) != nil || rows == nil || total >= 0 && total != *facts.Total {
			t.Fatal("independent input inventory has missing or contradictory page facts")
		}
		total = *facts.Total
		pages++
		for _, row := range rows {
			var object map[string]json.RawMessage
			var id string
			if json.Unmarshal(row, &object) != nil || json.Unmarshal(object[identity], &id) != nil || id == "" || entries[id] != nil {
				t.Fatal("independent input inventory repeats or lacks an identity")
			}
			entries[id] = row
		}
		start += len(rows)
		if start == total {
			return entries, pages
		}
		if len(rows) == 0 || start > total {
			t.Fatal("independent input inventory cannot advance")
		}
	}
}

func assertInputFieldsEqual(t *testing.T, got, want map[string]json.RawMessage) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatal("exposed discovery field inventory differs from independent HTTP reads")
	}
	for id, native := range want {
		assertInputNativeEqual(t, got[id], native)
	}
}

// Number-aware comparison retains integer precision and every unknown member.
func assertInputNativeEqual(t *testing.T, got, want json.RawMessage) {
	t.Helper()
	values := make([]any, 2)
	for i, raw := range []json.RawMessage{got, want} {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&values[i]) != nil {
			t.Fatal("native discovery comparison lacks a complete JSON value")
		}
	}
	if !reflect.DeepEqual(values[0], values[1]) {
		t.Fatal("exposed discovery changed native metadata or fixture state; inspect local receipts")
	}
}

func assertInputSkeleton(t *testing.T, raw json.RawMessage, definitions map[string]json.RawMessage, projectID, typeID string) {
	t.Helper()
	var payload struct {
		Fields map[string]json.RawMessage              `json:"fields"`
		Update map[string][]map[string]json.RawMessage `json:"update"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Fields == nil {
		t.Fatal("live discovery lacks a native fields/update skeleton")
	}
	for id, native := range definitions {
		if projectID != "" && id == "project" || typeID != "" && id == "issuetype" {
			continue
		}
		var facts map[string]json.RawMessage
		var operations []string
		if json.Unmarshal(native, &facts) != nil || json.Unmarshal(facts["operations"], &operations) != nil || operations == nil {
			t.Fatal("independent definition lacks advertised operations")
		}
		if slices.Contains(operations, "set") {
			if string(payload.Fields[id]) != "null" || len(payload.Update[id]) != 0 {
				t.Fatal("settable native field lacks an unchosen null placeholder")
			}
		} else {
			if payload.Fields[id] != nil || len(payload.Update[id]) != len(operations) {
				t.Fatal("skeleton invented or omitted an advertised update operation")
			}
			for i, operation := range operations {
				if len(payload.Update[id][i]) != 1 || string(payload.Update[id][i][operation]) != "null" {
					t.Fatal("update skeleton chose a value or changed native operation order")
				}
			}
		}
		delete(payload.Fields, id)
		delete(payload.Update, id)
	}
	for id, value := range map[string]string{"project": projectID, "issuetype": typeID} {
		if value == "" {
			continue
		}
		expected, err := json.Marshal(map[string]string{"id": value})
		if err != nil {
			t.Fatal(err)
		}
		assertInputNativeEqual(t, payload.Fields[id], expected)
		delete(payload.Fields, id)
	}
	if len(payload.Fields) != 0 || len(payload.Update) != 0 {
		t.Fatal("skeleton contains values outside its native field context")
	}
}
