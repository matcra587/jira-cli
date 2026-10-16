//go:build live

package live

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

type nativeMoveInput struct {
	keys   []string
	digest [sha256.Size]byte
	fields bool
}

type ownedBulkTask struct {
	ID       string   `json:"id,omitempty"`
	Keys     []string `json:"keys"`
	Terminal bool     `json:"terminal"`
}

func (r *liveRun) checkMoveMutation(keys, args []string) error {
	if !r.verified || r.moveProjectID == "" || len(r.moveTypes) != 2 || len(args) < 5 || args[0] != "issue" || args[1] != "move" {
		return errors.New("move requires verified credentials and same-project type metadata")
	}
	positionals := []string{}
	i := 2
	fieldMove := args[i] == "fields"
	if fieldMove {
		i++
	}
	for i < len(args) && !strings.HasPrefix(args[i], "--") {
		key := args[i]
		if !liveKey.MatchString(key) || r.ownedLiveIssue(key) == nil || slices.Contains(positionals, key) {
			return errors.New("move requires distinct active owned positional keys")
		}
		positionals = append(positionals, key)
		i++
	}
	if len(positionals) == 0 || !slices.Equal(positionals, keys) {
		return errors.New("move targets differ from their complete ownership declaration")
	}
	path := ""
	seen := map[string]bool{}
	for ; i < len(args); i++ {
		arg := args[i]
		if seen[arg] {
			return errors.New("move repeats an option")
		}
		seen[arg] = true
		switch arg {
		case "--json-input":
			if path != "" || i+1 >= len(args) {
				return errors.New("move requires one registered native input")
			}
			i++
			path = args[i]
		case "--force", "--dry-run":
		case "--parallelism=1":
			if !fieldMove {
				return errors.New("native bulk move has no per-key parallelism option")
			}
		case "--validate-remote", "--wait", "--max-polls=40", "--poll-interval=1s":
			if fieldMove {
				return errors.New("field move has an unauthored option")
			}
		default:
			return errors.New("move has an unauthored option")
		}
	}
	registered, ok := r.moveInputs[path]
	if !ok || registered.fields != fieldMove || !slices.Equal(registered.keys, positionals) {
		return errors.New("move input is not registered for these targets")
	}
	data, err := os.ReadFile(path)
	if err != nil || sha256.Sum256(data) != registered.digest {
		return errors.New("move input changed after registration")
	}
	return nil
}

// Authored input has one fixed same-project destination and no parent, status
// or mandatory-field overrides. Source IDs are exclusively active owned issues.
func (r *liveRun) moveInput(t *testing.T, keys []string, typeID string) string {
	t.Helper()
	if !r.verified || r.moveProjectID == "" || !r.moveTypes[typeID] || len(keys) == 0 {
		t.Fatal("move input requires verified same-project type metadata")
	}
	ids := []string{}
	for _, key := range keys {
		owned := r.ownedLiveIssue(key)
		if owned == nil || owned.ID == "" || slices.Contains(ids, owned.ID) {
			t.Fatal("move input includes an inactive, foreign or repeated identity")
		}
		ids = append(ids, owned.ID)
	}
	group := map[string]any{"issueIdsOrKeys": ids, "inferClassificationDefaults": true, "inferFieldDefaults": true, "inferStatusDefaults": true, "inferSubtaskTypeDefault": true}
	data, err := jira.MarshalNoEscape(map[string]any{"sendBulkNotification": false, "targetToSourcesMapping": map[string]any{r.moveProjectID + "," + typeID: group}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.dir, "native-move-"+output.NewRequestID()+".json")
	if err := xos.AtomicWrite(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if r.moveInputs == nil {
		r.moveInputs = make(map[string]nativeMoveInput)
	}
	r.moveInputs[path] = nativeMoveInput{keys: slices.Clone(keys), digest: sha256.Sum256(data)}
	return path
}

// Cleanup cannot race an accepted background move. An unknown job receipt is
// kept in the manifest and stops deletion rather than guessing that it settled.
func (r *liveRun) finishMoveTasks(t *testing.T) bool {
	t.Helper()
	for i := range r.manifest.BulkTasks {
		task := &r.manifest.BulkTasks[i]
		if task.Terminal {
			continue
		}
		if task.ID == "" {
			t.Error("owned move has no usable task receipt; cleanup requires reconciliation from private receipts")
			return false
		}
		frame, raw, exit := r.invoke(t, context.Background(), []string{"JIRA_READ_ONLY=true"}, "issue", "move", "status", "--wait", "--max-polls=40", "--poll-interval=1s", "--", task.ID)
		var status struct {
			Data envelope.IssueMoveStatusOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(raw, &status) != nil || status.Data.Task == nil || status.Data.Task.ID != task.ID || !status.Data.Terminal {
			t.Error("owned move has not settled; cleanup did not delete its resources")
			return false
		}
		task.Terminal = true
		r.save(t)
	}
	return true
}

func TestLiveMoveMutationOwnershipGrammar(t *testing.T) {
	t.Parallel()
	r := &liveRun{verified: true, dir: t.TempDir(), moveProjectID: "10000", moveTypes: map[string]bool{"10001": true, "10002": true}, manifest: liveManifest{Issues: []ownedIssue{{Key: "KEY-1", ID: "101"}, {Key: "KEY-2", ID: "102"}, {Key: "KEY-3", ID: "103", Deleted: true}}}}
	keys := []string{"KEY-1", "KEY-2"}
	path := r.moveInput(t, keys, "10002")
	valid := []string{"issue", "move", "KEY-1", "KEY-2", "--json-input", path, "--force"}
	if err := r.checkMutation(keys, valid); err != nil {
		t.Fatalf("authored owned move refused: %v", err)
	}
	for _, tc := range []struct{ keys, args []string }{
		{keys, append(slices.Clone(valid), "--dry-run", "--validate-remote")},
		{keys, append(slices.Clone(valid), "--wait", "--max-polls=40", "--poll-interval=1s")},
	} {
		if err := r.checkMutation(tc.keys, tc.args); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ keys, args []string }{
		{[]string{"KEY-1"}, valid},
		{[]string{"KEY-2", "KEY-1"}, valid},
		{[]string{"KEY-1", "FOREIGN-1"}, []string{"issue", "move", "KEY-1", "FOREIGN-1", "--json-input", path}},
		{[]string{"KEY-1", "KEY-3"}, []string{"issue", "move", "KEY-1", "KEY-3", "--json-input", path}},
		{[]string{"KEY-1", "KEY-1"}, []string{"issue", "move", "KEY-1", "KEY-1", "--json-input", path}},
		{keys, append(slices.Clone(valid), "--jql=project=KEY")},
		{keys, append(slices.Clone(valid), "--json-input", path)},
		{keys, append(slices.Clone(valid), "--max-polls=1000")},
		{keys, []string{"issue", "move", "KEY-1:2", "--json-input", path}},
		{keys, []string{"issue", "move", "KEY-1,KEY-2", "--json-input", path}},
		{keys, []string{"issue", "move", "KEY-1", "--force", "KEY-2", "--json-input", path}},
		{keys, []string{"issue", "move", "KEY-1", "KEY-2", "--json-input=unregistered.json"}},
		{keys, []string{"issue", "move", "status", "100", "--json-input", path}},
	} {
		if err := r.checkMutation(tc.keys, tc.args); err == nil {
			t.Fatalf("unsafe move grammar admitted: %v", tc.args)
		}
	}
	r.manifest.Issues[1].Deleted = true
	if err := r.checkMutation(keys, valid); err == nil {
		t.Fatal("move admitted a target deleted after input registration")
	}
	r.manifest.Issues[1].Deleted = false
	if err := os.WriteFile(path, []byte(`{"targetToSourcesMapping":{"FOREIGN,10002":{"issueIdsOrKeys":["999"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.checkMutation(keys, valid); err == nil {
		t.Fatal("move admitted changed native input")
	}
}

func TestLiveMoveFieldsMutationOwnershipGrammar(t *testing.T) {
	t.Parallel()
	r := &liveRun{verified: true, dir: t.TempDir(), moveProjectID: "10000", moveTypes: map[string]bool{"10001": true, "10002": true}, manifest: liveManifest{Issues: []ownedIssue{{Key: "KEY-1", ID: "101"}}}}
	keys := []string{"KEY-1"}
	path := filepath.Join(r.dir, "fields.json")
	data := []byte(`{"fields":{"issuetype":{"id":"10001"},"summary":"Owned field change"}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	r.moveInputs = map[string]nativeMoveInput{path: {keys: keys, digest: sha256.Sum256(data), fields: true}}
	valid := []string{"issue", "move", "fields", "KEY-1", "--json-input", path, "--force"}
	if err := r.checkMutation(keys, valid); err != nil {
		t.Fatal(err)
	}
	if err := r.checkMutation(keys, append(slices.Clone(valid), "--dry-run")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"issue", "move", "KEY-1", "--json-input", path, "--force"},
		{"issue", "move", "fields", "FOREIGN-1", "--json-input", path, "--force"},
		{"issue", "move", "fields", "KEY-1", "KEY-2", "--json-input", path, "--force"},
		append(slices.Clone(valid), "--wait"),
		append(slices.Clone(valid), "--validate-remote"),
	} {
		if err := r.checkMutation(keys, args); err == nil {
			t.Fatalf("unsafe field move admitted: %v", args)
		}
	}
	if err := r.checkMutation([]string{"KEY-1", "KEY-2"}, valid); err == nil {
		t.Fatal("incomplete positional ownership declaration admitted")
	}
	r.manifest.Issues[0].Deleted = true
	if err := r.checkMutation(keys, valid); err == nil {
		t.Fatal("deleted target admitted")
	}
	r.manifest.Issues[0].Deleted = false
	if err := os.WriteFile(path, []byte(`{"fields":{"project":{"key":"FOREIGN"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.checkMutation(keys, valid); err == nil {
		t.Fatal("changed field move payload admitted")
	}
}

type moveStoredIssue struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Fields struct {
		Project     struct{ ID string } `json:"project"`
		IssueType   struct{ ID string } `json:"issuetype"`
		Summary     string              `json:"summary"`
		Description json.RawMessage     `json:"description"`
		Subtasks    []json.RawMessage   `json:"subtasks"`
		Parent      json.RawMessage     `json:"parent"`
	} `json:"fields"`
}

func ownedMoveState(t *testing.T, r *liveRun, key string) moveStoredIssue {
	t.Helper()
	owned := r.ownedLiveIssue(key)
	if owned == nil {
		t.Fatal("move verification requires an active owned fixture")
	}
	read := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(owned.ID), "--query=fields=project,issuetype,summary,description,subtasks,parent")
	var stored moveStoredIssue
	if json.Unmarshal(read.Response.JSON, &stored) != nil || stored.ID != owned.ID || stored.Key != key || stored.Fields.Project.ID != r.moveProjectID || stored.Fields.Subtasks == nil || len(stored.Fields.Subtasks) != 0 || (len(stored.Fields.Parent) != 0 && string(stored.Fields.Parent) != "null") {
		t.Fatal("move fixture identity, project or hierarchy is unsafe")
	}
	return stored
}

func TestLiveIssueMove(t *testing.T) {
	r := newLiveRun(t)
	metadata := rawRead(t, r, "api", "get", "/rest/api/3/project/"+url.PathEscape(r.manifest.Project))
	var project struct {
		ID         string `json:"id"`
		IssueTypes []struct {
			ID, Name string
			Subtask  *bool
		} `json:"issueTypes"`
	}
	if json.Unmarshal(metadata.Response.JSON, &project) != nil || project.ID == "" || len(project.IssueTypes) == 0 {
		t.Fatal("move project metadata is incomplete")
	}
	r.moveProjectID = project.ID
	r.moveTypes = make(map[string]bool)
	taskID, epicID := "", ""
	for _, typ := range project.IssueTypes {
		if typ.Subtask == nil || *typ.Subtask || typ.ID == "" {
			continue
		}
		if typ.Name == r.issueType {
			taskID = typ.ID
		}
		if typ.Name == "Epic" {
			epicID = typ.ID
		}
	}
	if taskID == "" || epicID == "" || taskID == epicID {
		t.Fatal("move requires two available non-subtask fixture types")
	}
	r.moveTypes[taskID], r.moveTypes[epicID] = true, true
	permissions := rawRead(t, r, "api", "get", "/rest/api/3/mypermissions", "--query=projectKey="+r.manifest.Project, "--query=permissions=BULK_CHANGE,MOVE_ISSUES,CREATE_ISSUES")
	var permitted struct {
		Permissions map[string]struct {
			HavePermission bool `json:"havePermission"`
		} `json:"permissions"`
	}
	if json.Unmarshal(permissions.Response.JSON, &permitted) != nil {
		t.Fatal("move permission metadata is incomplete")
	}
	for _, permission := range []string{"BULK_CHANGE", "MOVE_ISSUES", "CREATE_ISSUES"} {
		if !permitted.Permissions[permission].HavePermission {
			t.Fatal("move requires an unavailable permission")
		}
	}
	key := r.create(t, "native-move")
	before := ownedMoveState(t, r, key)
	if before.Fields.IssueType.ID != taskID {
		t.Fatal("created move fixture type differs from current metadata")
	}
	keys := []string{key}
	for i, destination := range []string{epicID, taskID} {
		path := r.moveInput(t, keys, destination)
		args := []string{"issue", "move", key, "--json-input", path, "--force"}
		if err := r.checkMutation(keys, args); err != nil {
			t.Fatal(err)
		}
		for _, extra := range [][]string{{"--dry-run"}, {"--dry-run", "--validate-remote"}} {
			frame, raw, exit := r.invoke(t, t.Context(), nil, append(slices.Clone(args), extra...)...)
			var preview struct{ Data envelope.IssueMoveOutput }
			if exit != 0 || !frame.OK || json.Unmarshal(raw, &preview) != nil || !preview.Data.DryRun || preview.Data.Attempted || preview.Data.Acknowledged || preview.Data.RemoteValidated != (len(extra) == 2) || preview.Data.BindingsValidated != (len(extra) == 2) {
				t.Fatal("move preview invented remote binding or submission")
			}
		}
		refused, _, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, args...)
		if exit != 3 || refused.OK || len(refused.Errors) != 1 || refused.Errors[0].Code != "read_only_blocked" {
			t.Fatal("read-only move was not refused")
		}
		current := ownedMoveState(t, r, key)
		if !reflect.DeepEqual(current, before) {
			t.Fatal("preview or read-only refusal changed the owned fixture")
		}
		// Persist a pending record before invoking the write. Failure before a
		// receipt cannot accidentally authorize deletion while a job is active.
		r.manifest.BulkTasks = append(r.manifest.BulkTasks, ownedBulkTask{Keys: slices.Clone(keys)})
		job := &r.manifest.BulkTasks[len(r.manifest.BulkTasks)-1]
		r.save(t)
		if i == 1 {
			args = append(args, "--wait", "--max-polls=40", "--poll-interval=1s")
		}
		if err := r.checkMutation(keys, args); err != nil {
			t.Fatal(err)
		}
		frame, raw, exit := r.invoke(t, t.Context(), nil, args...)
		var receipt struct{ Data envelope.IssueMoveOutput }
		if json.Unmarshal(raw, &receipt) != nil {
			t.Fatal("move has no decodable receipt; inspect private evidence")
		}
		out := receipt.Data
		if out.Task != nil {
			job.ID = out.Task.ID
		}
		job.Terminal = out.Terminal
		r.save(t)
		if exit != 0 || !frame.OK || !out.Acknowledged || out.Task == nil || len(out.Results) != 1 || out.Results[0].Issue.ID != before.ID {
			t.Fatal("owned move submission failed; inspect private receipts")
		}
		if i == 0 && (out.Polls != 0 || out.Terminal || out.Results[0].Data.Applied) {
			t.Fatal("asynchronous acknowledgement claimed completion")
		}
		if i == 1 && (!out.Terminal || out.PollLimitReached || out.Polls == 0 || !out.Results[0].OK || out.Results[0].Data == nil || !out.Results[0].Data.Applied || out.Results[0].Data.Outcome != "processed") {
			t.Fatal("waited move did not retain explicit per-issue completion")
		}
		if !r.finishMoveTasks(t) {
			t.Fatal("owned move did not settle within its bounded observation")
		}
		statusFrame, statusRaw, statusExit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "issue", "move", "status", "--", job.ID)
		var status struct {
			Data envelope.IssueMoveStatusOutput
		}
		if statusExit != 0 || !statusFrame.OK || json.Unmarshal(statusRaw, &status) != nil || !status.Data.Terminal || status.Data.Progress == nil || status.Data.Progress.Status.Value != "COMPLETE" || status.Data.Polls != 1 {
			t.Fatal("settled task could not be resumed through a single read")
		}
		rawProgress := rawRead(t, r, "api", "get", "/rest/api/3/bulk/queue/"+url.PathEscape(job.ID))
		var native jira.BulkOperationProgress
		if json.Unmarshal(rawProgress.Response.JSON, &native) != nil || native.ValidateTask(job.ID) != nil || native.Status.Value != status.Data.Progress.Status.Value || !slices.Equal(native.ProcessedAccessibleIssues.Value, status.Data.Progress.ProcessedAccessibleIssues.Value) || len(native.ProcessedAccessibleIssues.Value) != 1 {
			t.Fatal("typed task progress differs from an independent raw observation")
		}
		current = ownedMoveState(t, r, key)
		var wantBody, gotBody any
		if json.Unmarshal(before.Fields.Description, &wantBody) != nil || json.Unmarshal(current.Fields.Description, &gotBody) != nil || current.Fields.IssueType.ID != destination || current.Fields.Summary != before.Fields.Summary || !reflect.DeepEqual(wantBody, gotBody) {
			t.Fatal("independent move read differs in destination, summary or native body")
		}
		before = current
	}

	// Exercise the historical field PUT separately after both native jobs
	// settle. Its stored result is verified by stable ID, without search.
	summary := before.Fields.Summary + " field move verified"
	fieldData, err := jira.MarshalNoEscape(map[string]any{"fields": map[string]any{
		"issuetype": map[string]string{"id": epicID}, "summary": summary, "description": before.Fields.Description,
	}})
	if err != nil {
		t.Fatal(err)
	}
	fieldPath := filepath.Join(r.dir, "field-move-"+output.NewRequestID()+".json")
	if err := xos.AtomicWrite(fieldPath, fieldData, 0o600); err != nil {
		t.Fatal(err)
	}
	r.moveInputs[fieldPath] = nativeMoveInput{keys: slices.Clone(keys), digest: sha256.Sum256(fieldData), fields: true}
	fieldArgs := []string{"issue", "move", "fields", key, "--json-input", fieldPath}
	if err := r.checkMutation(keys, fieldArgs); err != nil {
		t.Fatal(err)
	}
	frame, raw, exit := r.invoke(t, t.Context(), nil, append(slices.Clone(fieldArgs), "--dry-run")...)
	var fieldReceipt struct{ Data envelope.IssueEditOutput }
	if exit != 0 || !frame.OK || json.Unmarshal(raw, &fieldReceipt) != nil || !fieldReceipt.Data.DryRun || len(fieldReceipt.Data.Results) != 1 || !fieldReceipt.Data.Results[0].OK {
		t.Fatal("field move local preview lost its prepared request")
	}
	frame, _, exit = r.invoke(t, t.Context(), nil, fieldArgs...)
	if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "flag_required" {
		t.Fatal("field move allowed a missing acknowledgement")
	}
	fieldArgs = append(fieldArgs, "--force")
	frame, _, exit = r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, fieldArgs...)
	if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "read_only_blocked" {
		t.Fatal("read-only field move was not refused")
	}
	if !reflect.DeepEqual(ownedMoveState(t, r, key), before) {
		t.Fatal("field preview or refusal changed the fixture")
	}
	if err := r.checkMutation(keys, fieldArgs); err != nil {
		t.Fatal(err)
	}
	frame, raw, exit = r.invoke(t, t.Context(), nil, fieldArgs...)
	if exit != 0 || !frame.OK || json.Unmarshal(raw, &fieldReceipt) != nil || fieldReceipt.Data.DryRun || len(fieldReceipt.Data.Results) != 1 || !fieldReceipt.Data.Results[0].OK || fieldReceipt.Data.Results[0].Issue.Key != key {
		t.Fatal("owned field move was not acknowledged")
	}
	current := ownedMoveState(t, r, key)
	if current.Fields.Summary != summary || current.Fields.IssueType.ID != epicID || !reflect.DeepEqual(current.Fields.Description, before.Fields.Description) {
		t.Fatal("independent field move verification differs from the authored request")
	}
	returnData, err := jira.MarshalNoEscape(map[string]any{"fields": map[string]any{"issuetype": map[string]string{"id": taskID}}})
	if err != nil {
		t.Fatal(err)
	}
	returnPath := filepath.Join(r.dir, "field-return-"+output.NewRequestID()+".json")
	if err := xos.AtomicWrite(returnPath, returnData, 0o600); err != nil {
		t.Fatal(err)
	}
	r.moveInputs[returnPath] = nativeMoveInput{keys: slices.Clone(keys), digest: sha256.Sum256(returnData), fields: true}
	returnArgs := []string{"issue", "move", "fields", key, "--json-input", returnPath, "--force"}
	if err := r.checkMutation(keys, returnArgs); err != nil {
		t.Fatal(err)
	}
	frame, raw, exit = r.invoke(t, t.Context(), nil, returnArgs...)
	if exit != 0 || !frame.OK || json.Unmarshal(raw, &fieldReceipt) != nil || len(fieldReceipt.Data.Results) != 1 || !fieldReceipt.Data.Results[0].OK {
		t.Fatal("owned field move back to original type was not acknowledged")
	}
	returned := ownedMoveState(t, r, key)
	if returned.Fields.IssueType.ID != taskID || returned.Fields.Summary != summary || !reflect.DeepEqual(returned.Fields.Description, before.Fields.Description) {
		t.Fatal("independent return field move verification differs from the request")
	}
}
