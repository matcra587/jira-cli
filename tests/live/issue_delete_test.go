//go:build live

package live

import (
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// checkIssueDeleteMutation binds every positional target and permits a cascade
// only after this run independently observed its complete owned child set.
func (r *liveRun) checkIssueDeleteMutation(keys, args []string) error {
	if len(args) < 3 || args[0] != "issue" || args[1] != "delete" {
		return errors.New("unsupported issue deletion grammar")
	}
	var targets []string
	i := 2
	for ; i < len(args) && !strings.HasPrefix(args[i], "--"); i++ {
		if !liveKey.MatchString(args[i]) || !r.owns(args[i]) || slices.Contains(targets, args[i]) {
			return errors.New("issue deletion requires distinct explicit owned targets")
		}
		targets = append(targets, args[i])
	}
	if len(targets) == 0 || !slices.Equal(keys, targets) {
		return errors.New("issue deletion targets differ from declared ownership")
	}
	seen := make(map[string]bool)
	cascade := false
	for ; i < len(args); i++ {
		flag := args[i]
		if flag == "--cascade" {
			flag = "--delete-subtasks"
		}
		if seen[flag] {
			return errors.New("repeated issue deletion option")
		}
		seen[flag] = true
		switch flag {
		case "--force", "--dry-run":
		case "--parallelism=1":
		case "--parallelism":
			i++
			if i >= len(args) || args[i] != "1" {
				return errors.New("unsupported issue deletion parallelism")
			}
		case "--delete-subtasks":
			cascade = true
		default:
			return errors.New("unsupported issue deletion option")
		}
	}
	if cascade {
		for _, key := range targets {
			children, observed := r.deleteChildren[key]
			if !observed || r.ownedLiveIssue(key) == nil {
				return errors.New("issue cascade lacks a complete owned child observation")
			}
			for _, child := range children {
				owned := r.ownedLiveIssue(child)
				if owned == nil || owned.Parent != key {
					return errors.New("issue cascade includes an inactive or unrelated child")
				}
			}
		}
	}
	return nil
}

// subtaskPayload authors a fixed create recipe after current metadata has
// identified the type and the parent is confirmed active and owned.
func (r *liveRun) subtaskPayload(parent, typeID, summary string) ([]byte, error) {
	if !r.verified || r.ownedLiveIssue(parent) == nil || r.manifest.Project == "" || !r.subtaskTypes[typeID] || r.manifest.RunID == "" || !strings.HasPrefix(summary, r.manifest.RunID+" ") {
		return nil, errors.New("subtask creation requires verified ownership and current type metadata")
	}
	return json.Marshal(map[string]any{"fields": map[string]any{
		"project": map[string]string{"key": r.manifest.Project}, "issuetype": map[string]string{"id": typeID},
		"parent": map[string]string{"key": parent}, "summary": summary, "labels": []string{r.manifest.RunID},
	}})
}

func (r *liveRun) createDeleteSubtask(t *testing.T, parent string) string {
	t.Helper()
	metadata := rawRead(t, r, "api", "get", "/rest/api/3/issue/createmeta/"+url.PathEscape(r.manifest.Project)+"/issuetypes", "--query=maxResults=100")
	var types struct {
		Total      int `json:"total"`
		StartAt    int `json:"startAt"`
		IssueTypes []struct {
			ID, Name string
			Subtask  bool
		} `json:"issueTypes"`
	}
	if json.Unmarshal(metadata.Response.JSON, &types) != nil || types.IssueTypes == nil || types.StartAt != 0 || types.Total != len(types.IssueTypes) {
		t.Fatal("subtask fixture metadata is incomplete")
	}
	typeID := ""
	for _, typ := range types.IssueTypes {
		if typ.ID != "" && typ.Subtask {
			if typeID != "" {
				t.Fatal("subtask fixture type is ambiguous")
			}
			typeID = typ.ID
		}
	}
	if typeID == "" {
		t.Fatal("dedicated project has no available subtask fixture type")
	}
	r.subtaskTypes = map[string]bool{typeID: true}
	summary := r.manifest.RunID + " deletion-owned-subtask"
	data, err := r.subtaskPayload(parent, typeID, summary)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.dir, "owned-subtask-"+output.NewRequestID()+".json")
	if err := xos.AtomicWrite(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	r.manifest.Attempts = append(r.manifest.Attempts, summary)
	r.save(t)
	frame := r.success(t, "issue", "create", "--json-input", path, "--verify")
	r.register(t, frame.Data.Issue)
	r.manifest.Issues[len(r.manifest.Issues)-1].Parent = parent
	r.save(t)
	return frame.Data.Issue.Key
}

func (r *liveRun) observeDeleteChildren(t *testing.T, parent string, expected []string) {
	t.Helper()
	if r.ownedLiveIssue(parent) == nil {
		t.Fatal("cascade observation requires an active owned parent")
	}
	stored := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(parent), "--query=fields=subtasks")
	var issue struct {
		ID, Key string
		Fields  struct {
			Subtasks []struct{ ID, Key string } `json:"subtasks"`
		} `json:"fields"`
	}
	if json.Unmarshal(stored.Response.JSON, &issue) != nil || issue.Key != parent || issue.ID != r.ownedLiveIssue(parent).ID || issue.Fields.Subtasks == nil || len(issue.Fields.Subtasks) != len(expected) {
		t.Fatal("cascade observation differs from its complete owned child set")
	}
	var observed []string
	for _, child := range issue.Fields.Subtasks {
		owned := r.ownedLiveIssue(child.Key)
		if owned == nil || owned.ID != child.ID || owned.Parent != parent || !slices.Contains(expected, child.Key) || slices.Contains(observed, child.Key) {
			t.Fatal("cascade observation includes an unowned or unrelated subtask")
		}
		observed = append(observed, child.Key)
	}
	if r.deleteChildren == nil {
		r.deleteChildren = make(map[string][]string)
	}
	r.deleteChildren[parent] = observed
}

func (r *liveRun) confirmDeletedIssue(t *testing.T, key string) {
	t.Helper()
	if !r.owns(key) {
		t.Fatal("deletion verification requires a recorded owned issue")
	}
	frame, _, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "api", "get", "/rest/api/3/issue/"+url.PathEscape(key))
	if exit != 2 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "jira_not_found" {
		t.Fatal("owned issue deletion was not independently confirmed")
	}
	for i := range r.manifest.Issues {
		if r.manifest.Issues[i].Key == key {
			r.manifest.Issues[i].Deleted = true
		}
	}
	r.save(t)
}

func (r *liveRun) issueDeleteReceipt(t *testing.T, keys, extraEnv []string, args ...string) (envelope.IssueDeleteOutput, int) {
	t.Helper()
	if err := r.checkMutation(keys, args); err != nil {
		t.Fatal(err)
	}
	frame, stdout, exit := r.invoke(t, t.Context(), extraEnv, args...)
	var decoded struct {
		Data *envelope.IssueDeleteOutput `json:"data"`
	}
	if json.Unmarshal(stdout, &decoded) != nil || decoded.Data == nil || len(decoded.Data.Results) != len(keys) || frame.OK != (exit == 0) {
		t.Fatal("issue deletion omitted its complete typed receipt")
	}
	for i, row := range decoded.Data.Results {
		if row.Issue.Key != keys[i] {
			t.Fatal("issue deletion receipt changed target ordering")
		}
	}
	return *decoded.Data, exit
}

func TestLiveIssueDeletes(t *testing.T) {
	r := newLiveRun(t)
	parent := r.create(t, "deletion-parent")
	child := r.createDeleteSubtask(t, parent)
	sibling := r.create(t, "deletion-sibling")
	missing := r.create(t, "deletion-missing")
	r.observeDeleteChildren(t, parent, []string{child})
	for _, operation := range []string{"preview", "read-only", "unconfirmed", "refused-parent"} {
		args := []string{"issue", "delete", parent}
		var env []string
		switch operation {
		case "preview":
			args = append(args, "--dry-run", "--delete-subtasks")
		case "read-only":
			args = append(args, "--force", "--delete-subtasks")
			env = []string{"JIRA_READ_ONLY=true"}
		case "unconfirmed":
		case "refused-parent":
			args = append(args, "--force")
		}
		if operation == "unconfirmed" {
			if err := r.checkMutation([]string{parent}, args); err != nil {
				t.Fatal(err)
			}
			frame, _, exit := r.invoke(t, t.Context(), nil, args...)
			if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "flag_required" {
				t.Fatal("headless issue deletion did not require acknowledgement")
			}
		} else {
			out, exit := r.issueDeleteReceipt(t, []string{parent}, env, args...)
			row := out.Results[0]
			switch operation {
			case "preview":
				if exit != 0 || !out.DryRun || !out.DeleteSubtasks || !row.OK || row.Data == nil || row.Err != nil {
					t.Fatal("issue deletion preview changed its request")
				}
			case "read-only":
				if exit != 3 || out.DryRun || !out.DeleteSubtasks || row.OK || row.Data != nil || row.Err == nil || row.Err.Code != "read_only_blocked" {
					t.Fatal("read-only issue deletion did not retain its refusal")
				}
			case "refused-parent":
				if exit != 3 || out.DryRun || out.DeleteSubtasks || row.OK || row.Data != nil || row.Err == nil || row.Err.Code != "jira_bad_request" {
					t.Fatal("parent deletion did not preserve Jira's subtask refusal")
				}
			}
		}
		r.observeDeleteChildren(t, parent, []string{child})
		for _, key := range []string{child, sibling, missing} {
			r.success(t, "issue", "view", key)
		}
	}
	out, exit := r.issueDeleteReceipt(t, []string{missing}, nil, "issue", "delete", missing, "--force")
	if exit != 0 || out.DeleteSubtasks || !out.Results[0].OK || out.Results[0].Data == nil || out.Results[0].Err != nil {
		t.Fatal("single deletion lacks its native204 receipt")
	}
	r.confirmDeletedIssue(t, missing)
	r.observeDeleteChildren(t, parent, []string{child})
	out, exit = r.issueDeleteReceipt(t, []string{parent}, nil, "issue", "delete", parent, "--cascade", "--force")
	if exit != 0 || !out.DeleteSubtasks || !out.Results[0].OK || out.Results[0].Data == nil || out.Results[0].Err != nil {
		t.Fatal("cascade deletion lacks its explicit204 receipt")
	}
	r.confirmDeletedIssue(t, parent)
	r.confirmDeletedIssue(t, child)
	r.success(t, "issue", "view", sibling)
	out, exit = r.issueDeleteReceipt(t, []string{sibling, missing}, nil, "issue", "delete", sibling, missing, "--force", "--parallelism=1")
	if exit != 0 || out.DeleteSubtasks || !out.Results[0].OK || out.Results[0].Data == nil || out.Results[0].Err != nil || out.Results[1].OK || out.Results[1].Data != nil || out.Results[1].Err == nil || out.Results[1].Err.Code != "jira_not_found" {
		t.Fatal("partial issue deletion did not retain its acknowledgement and missing target")
	}
	r.confirmDeletedIssue(t, sibling)
	out, exit = r.issueDeleteReceipt(t, []string{parent, sibling}, nil, "issue", "delete", parent, sibling, "--force", "--parallelism=1")
	if exit != 2 || out.DryRun || out.DeleteSubtasks {
		t.Fatal("total issue deletion failure changed its classification")
	}
	for _, row := range out.Results {
		if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != "jira_not_found" {
			t.Fatal("total issue deletion failure invented an acknowledgement")
		}
	}
}
