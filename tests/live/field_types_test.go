//go:build live

package live

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

// TestLiveFieldMetadataDiscovery verifies the published metadata workflow using
// only read-only requests. It does not submit illustrative custom-field values.
func TestLiveFieldMetadataDiscovery(t *testing.T) {
	r := newLiveRun(t)
	frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "agent", "fieldtypes")
	var discovery struct {
		Data envelope.FieldTypesOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &discovery) != nil || len(discovery.Data.Metadata) != 3 || len(discovery.Data.Types) != 20 || discovery.Data.Handling.TypeValidation || discovery.Data.Handling.ScalarConversion {
		t.Fatal("local field discovery lost its native handling or metadata routes")
	}
	key := requiredEnv(t, "JIRA_LIVE_READ_KEY")
	if !strings.HasPrefix(key, r.manifest.Project+"-") {
		t.Fatal("metadata read fixture must belong to the dedicated project")
	}
	issue := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(key), "--query=fields=issuetype,project")
	var stored struct {
		Fields struct {
			IssueType struct {
				ID string `json:"id"`
			} `json:"issuetype"`
			Project struct {
				Key string `json:"key"`
			} `json:"project"`
		} `json:"fields"`
	}
	if json.Unmarshal(issue.Response.JSON, &stored) != nil || stored.Fields.IssueType.ID == "" || stored.Fields.Project.Key != r.manifest.Project {
		t.Fatal("cannot establish the current project and issue type for metadata reads")
	}
	for _, route := range discovery.Data.Metadata {
		path := strings.NewReplacer("KEY-123", url.PathEscape(key), "PROJECT", url.PathEscape(r.manifest.Project), "TYPE_ID", url.PathEscape(stored.Fields.IssueType.ID)).Replace(route.Path)
		result := rawRead(t, r, "api", "get", path)
		switch route.Path {
		case "/rest/api/3/field":
			var fields []struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(result.Response.JSON, &fields) != nil || len(fields) == 0 {
				t.Fatal("field metadata has no discoverable field IDs")
			}
			found := false
			for _, field := range fields {
				found = found || field.ID == "summary"
			}
			if !found {
				t.Fatal("field metadata omitted the standard summary field")
			}
		case "/rest/api/3/issue/KEY-123/editmeta":
			var metadata struct {
				Fields map[string]json.RawMessage `json:"fields"`
			}
			if json.Unmarshal(result.Response.JSON, &metadata) != nil || metadata.Fields == nil {
				t.Fatal("edit metadata lost its native field map")
			}
		case "/rest/api/3/issue/createmeta/PROJECT/issuetypes/TYPE_ID":
			var page struct {
				Fields []struct {
					FieldID string          `json:"fieldId"`
					Schema  json.RawMessage `json:"schema"`
				} `json:"fields"`
			}
			if json.Unmarshal(result.Response.JSON, &page) != nil || len(page.Fields) == 0 {
				t.Fatal("create metadata has no field definitions")
			}
			for _, field := range page.Fields {
				if field.FieldID == "" || len(field.Schema) == 0 || string(field.Schema) == "null" {
					t.Fatal("create metadata lost a field's ID or schema")
				}
			}
		default:
			t.Fatal("field discovery published an unverified metadata route")
		}
	}
}
