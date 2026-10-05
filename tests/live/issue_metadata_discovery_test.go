//go:build live

package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// liveMetadataReader exercises the shared metadata consumer using the approved
// binary/keyring harness. One-row HTTP pages force real server continuations,
// even when the site's inventory fits the consumer's normal fifty-row request.
// This proves the walker and server protocol; adapter methods have their own
// hermetic transport tests. It does not prove an exposed discovery command.
type liveMetadataReader struct {
	t *testing.T
	r *liveRun
}

func (c liveMetadataReader) read(ctx context.Context, endpoint string, offset *int) ([]byte, error) {
	args := []string{"api", "get", endpoint}
	if offset != nil {
		args = append(args, fmt.Sprintf("--query=startAt=%d", *offset), "--query=maxResults=1")
	}
	frame, stdout, exit := c.r.invoke(c.t, ctx, []string{"JIRA_READ_ONLY=true"}, args...)
	var decoded struct {
		Data envelope.APIOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &decoded) != nil || decoded.Data.Response == nil || decoded.Data.Response.StatusCode != 200 || decoded.Data.Response.Truncated {
		return nil, errors.New("live metadata read lacks a complete HTTP 200 receipt; inspect local evidence")
	}
	return decoded.Data.Response.JSON, nil
}

func (c liveMetadataReader) GetCreateIssueTypes(ctx context.Context, project string, start, size int) (core.IssueMetadataRead, error) {
	if project != c.r.manifest.Project {
		return core.IssueMetadataRead{}, errors.New("metadata project is outside the dedicated read scope")
	}
	data, err := c.read(ctx, "/rest/api/3/issue/createmeta/"+project+"/issuetypes", &start)
	if err != nil {
		return core.IssueMetadataRead{}, err
	}
	page, err := core.DecodeCreateIssueTypes(data, start, size)
	return core.IssueMetadataRead{Page: page}, err
}

func (c liveMetadataReader) GetCreateIssueFields(ctx context.Context, project, typeID string, start, size int) (core.IssueMetadataRead, error) {
	if project != c.r.manifest.Project {
		return core.IssueMetadataRead{}, errors.New("metadata project is outside the dedicated read scope")
	}
	data, err := c.read(ctx, "/rest/api/3/issue/createmeta/"+project+"/issuetypes/"+typeID, &start)
	if err != nil {
		return core.IssueMetadataRead{}, err
	}
	page, err := core.DecodeCreateIssueFields(data, start, size)
	return core.IssueMetadataRead{Page: page}, err
}

func (c liveMetadataReader) GetEditIssueFields(ctx context.Context, key string) (core.IssueEditMetadataRead, error) {
	if key != requiredEnv(c.t, "JIRA_LIVE_READ_KEY") || !strings.HasPrefix(key, c.r.manifest.Project+"-") {
		return core.IssueEditMetadataRead{}, errors.New("edit metadata is outside the dedicated read fixture")
	}
	data, err := c.read(ctx, "/rest/api/3/issue/"+key+"/editmeta", nil)
	if err != nil {
		return core.IssueEditMetadataRead{}, err
	}
	return core.DecodeEditIssueFields(data)
}

func TestLiveNativeFieldMetadataDiscovery(t *testing.T) {
	r := newLiveRun(t)
	key := requiredEnv(t, "JIRA_LIVE_READ_KEY")
	if !strings.HasPrefix(key, r.manifest.Project+"-") {
		t.Fatal("metadata fixture is outside the dedicated project")
	}
	fixture := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=project,issuetype")
	var issue struct {
		Fields struct {
			Project struct {
				Key string `json:"key"`
			} `json:"project"`
			Type struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"issuetype"`
		} `json:"fields"`
	}
	if json.Unmarshal(fixture.Response.JSON, &issue) != nil || issue.Fields.Project.Key != r.manifest.Project || issue.Fields.Type.ID == "" || issue.Fields.Type.Name == "" {
		t.Fatal("cannot establish current metadata context")
	}
	reader := liveMetadataReader{t: t, r: r}
	byName, err := core.DiscoverCreateIssueMetadata(t.Context(), reader, r.manifest.Project, issue.Fields.Type.Name)
	if err != nil || byName == nil || byName.IssueTypeID != issue.Fields.Type.ID || len(byName.Fields) == 0 {
		t.Fatalf("native type-name discovery failed: %v", err)
	}
	byID, err := core.DiscoverCreateIssueMetadata(t.Context(), reader, r.manifest.Project, issue.Fields.Type.ID)
	if err != nil || byID == nil || byID.IssueTypeID != byName.IssueTypeID || len(byID.Fields) != len(byName.Fields) {
		t.Fatalf("explicit ID discovery differs from name resolution: %v", err)
	}
	// Independently decode larger HTTP pages without the service's decoder or
	// collector, then compare the complete field-ID inventory.
	identities := make(map[string]bool)
	start, total := 0, -1
	for {
		result := rawRead(t, r, "api", "get", "/rest/api/3/issue/createmeta/"+r.manifest.Project+"/issuetypes/"+issue.Fields.Type.ID,
			fmt.Sprintf("--query=startAt=%d", start), "--query=maxResults=200")
		var page struct {
			Start  *int `json:"startAt"`
			Total  *int `json:"total"`
			Fields []struct {
				ID string `json:"fieldId"`
			} `json:"fields"`
		}
		if json.Unmarshal(result.Response.JSON, &page) != nil || page.Start == nil || *page.Start != start || page.Total == nil || *page.Total < start || page.Fields == nil {
			t.Fatal("independent metadata inventory has incomplete page facts")
		}
		if total >= 0 && total != *page.Total {
			t.Fatal("metadata changed during independent inventory")
		}
		total = *page.Total
		for _, field := range page.Fields {
			if field.ID == "" || identities[field.ID] {
				t.Fatal("independent metadata repeats or lacks an identity")
			}
			identities[field.ID] = true
		}
		start += len(page.Fields)
		if start == total {
			break
		}
		if len(page.Fields) == 0 || start > total {
			t.Fatal("independent metadata walk failed to advance")
		}
	}
	if len(identities) != len(byName.Fields) || len(byID.FieldPages) != total || len(byName.FieldPages) != total {
		t.Fatal("one-row native discovery did not collect every field page")
	}
	for id := range identities {
		if byName.Fields[id] == nil || byID.Fields[id] == nil {
			t.Fatal("discovery omitted an independently listed field")
		}
	}
	edit, err := reader.GetEditIssueFields(t.Context(), key)
	if err != nil || edit.Fields == nil || len(edit.Native) == 0 {
		t.Fatalf("native edit metadata failed: %v", err)
	}
	t.Logf("complete metadata: %d type pages, %d field pages, %d independently listed fields; all HTTP requests read-only", len(byName.TypePages), len(byName.FieldPages), len(identities))
}
