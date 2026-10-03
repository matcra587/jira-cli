//go:build live

package live

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

// TestLiveAliases runs a locally managed chain through the actual domain read
// against an independent native response. Read-only stays on; no Jira writes.
func TestLiveAliases(t *testing.T) {
	r := newLiveRun(t)
	key := requiredEnv(t, "JIRA_LIVE_READ_KEY")
	if !regexp.MustCompile("^" + regexp.QuoteMeta(r.manifest.Project) + "-[0-9]+$").MatchString(key) {
		t.Fatal("alias acceptance needs the designated read fixture")
	}
	frame, stdout, exit := r.invoke(t, t.Context(), nil, "auth", "status")
	var auth struct {
		Data envelope.AuthStatusOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &auth) != nil || auth.Data.Email == "" || auth.Data.BaseURL != r.manifest.BaseURL {
		t.Fatal("cannot establish non-secret alias account identity")
	}
	config := filepath.Join(r.dir, "aliases.toml")
	local := []string{"JIRA_KEYRING=false", "JIRA_EMAIL=", "JIRA_READ_ONLY=true"}
	frame, stdout, exit = r.invoke(t, t.Context(), local, "alias", "set", "inspect", "issue view --fields=summary,status", "--config", config)
	var set struct {
		Data envelope.AliasSetOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &set) != nil || !set.Data.Changed || set.Data.DryRun || set.Data.Name != "inspect" {
		t.Fatal("local alias creation lacks its acknowledged receipt")
	}
	before, err := os.ReadFile(config)
	if err != nil {
		t.Fatal("cannot inspect the authored alias file")
	}
	input := filepath.Join(r.dir, "aliases.yaml")
	if err := os.WriteFile(input, []byte("outer: inner\ninner: inspect\n"), 0o600); err != nil {
		t.Fatal("cannot write the local forward-chain input")
	}
	for _, preview := range []bool{true, false} {
		args := []string{"alias", "import", input, "--config", config}
		if preview {
			args = append(args, "--dry-run")
		}
		frame, stdout, exit = r.invoke(t, t.Context(), local, args...)
		var imported struct {
			Data envelope.AliasImportOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(stdout, &imported) != nil || imported.Data.DryRun != preview || imported.Data.Changed == preview || imported.Data.Imported != 2 || len(imported.Data.Skipped) != 0 || len(imported.Data.Aliases) != 2 || imported.Data.Aliases[0] != "inner" || imported.Data.Aliases[1] != "outer" {
			t.Fatal("local chain import lost preview, ordering or save acknowledgement")
		}
		if preview {
			after, err := os.ReadFile(config)
			if err != nil || string(after) != string(before) {
				t.Fatal("alias import preview changed the local file")
			}
		}
	}
	readonly := []string{"JIRA_READ_ONLY=true", "JIRA_EMAIL=" + auth.Data.Email}
	frame, stdout, exit = r.invoke(t, t.Context(), readonly, "outer", key, "--config", config)
	var operation struct {
		Meta struct {
			Command string `json:"command"`
		} `json:"meta"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &operation) != nil || operation.Meta.Command != "issue.view" || frame.Data.Issue.Key != key {
		t.Fatal("actual alias dispatch lost the requested domain operation or read identity")
	}
	var domainFields struct {
		Summary *string `json:"summary"`
		Status  struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"status"`
	}
	if json.Unmarshal(frame.Data.Issue.Fields, &domainFields) != nil || domainFields.Summary == nil || domainFields.Status.ID == "" {
		t.Fatal("domain alias omitted requested native fields")
	}
	native := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=summary,status")
	var independentlyRead struct {
		Key    string          `json:"key"`
		Fields json.RawMessage `json:"fields"`
		ID     string          `json:"id"`
	}
	if json.Unmarshal(native.Response.JSON, &independentlyRead) != nil || independentlyRead.Key != key || independentlyRead.ID != frame.Data.Issue.ID {
		t.Fatal("independent native read differs from alias issue identity")
	}
	nativeFields := domainFields
	// Reset pointers and values before decoding; omitted data must not inherit
	// the domain observation and make the comparison vacuously pass.
	nativeFields.Summary, nativeFields.Status.ID, nativeFields.Status.Name = nil, "", ""
	if json.Unmarshal(independentlyRead.Fields, &nativeFields) != nil || nativeFields.Summary == nil || *nativeFields.Summary != *domainFields.Summary || nativeFields.Status != domainFields.Status {
		t.Fatal("alias fields differ from the independent native response")
	}
	frame, stdout, exit = r.invoke(t, t.Context(), local, "alias", "delete", "outer", "--force", "--config", config)
	var deleted struct {
		Data envelope.AliasDeleteOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &deleted) != nil || !deleted.Data.Deleted || !deleted.Data.Changed {
		t.Fatal("local alias deletion lacks acknowledgement")
	}
	if len(r.manifest.Issues) != 0 || len(r.manifest.BulkTasks) != 0 {
		t.Fatal("read-only alias acceptance unexpectedly owns Jira resources")
	}
}
