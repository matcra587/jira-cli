//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestParityNativeMoveOperationMapping(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"issue", "move", "PAIR-1"}, "issue.move"},
		{[]string{"issue", "move", "status", "owned-task"}, "issue.move.status"},
		{[]string{"issue", "move", "fields", "PAIR-1"}, "issue.move.fields"},
	} {
		if parityOperation(tc.args) != tc.want {
			t.Fatal("native move/status operation lost its exact command boundary")
		}
	}
}

func parityUniqueMoveTask(tasks []ownedBulkTask, index int, id string) bool {
	if id == "" || index < 0 || index >= len(tasks) {
		return false
	}
	for i, task := range tasks {
		if i != index && task.ID == id {
			return false
		}
	}
	return true
}

func TestParityNativeMoveRejectsReusedTask(t *testing.T) {
	t.Parallel()
	tasks := []ownedBulkTask{{ID: "settled-1", Terminal: true}, {Keys: []string{"PAIR-1"}}}
	if !parityUniqueMoveTask(tasks, 1, "new-2") || parityUniqueMoveTask(tasks, 1, "settled-1") || parityUniqueMoveTask(tasks, 1, "") || tasks[1].ID != "" {
		t.Fatal("duplicate or missing task identity must retain the unresolved intent")
	}
}

// Required facts are compared from original JSON, so omitted false values and
// identity members cannot pass through Go's zero-value decoding.
func parityNativeMoveReceipt(raw []byte, step, key, id, destination string, payload []byte, waited bool) bool {
	if !jsontext.Value(raw).IsValid() || !jsontext.Value(payload).IsValid() {
		return false
	}
	preview := step == "local_preview" || step == "remote_preview"
	remote := step != "local_preview"
	native := step == "native"
	if !preview && step != "read_only" && !native {
		return false
	}
	var root, data map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil || json.Unmarshal(root["data"], &data) != nil || data == nil {
		return false
	}
	issue := map[string]any{"key": key}
	if remote {
		issue["id"] = id
	}
	outcome := "planned"
	if native {
		outcome = "accepted"
		if waited {
			outcome = "processed"
		}
	}
	row := map[string]any{"issue": issue, "ok": true, "data": map[string]any{"applied": native && waited, "outcome": outcome, "destination": ""}}
	if remote {
		row["data"].(map[string]any)["destination"] = destination
	} else {
		// An immutable numeric source cannot be bound to a positional key locally.
		delete(row["data"].(map[string]any), "destination")
	}
	if step == "read_only" {
		hint := "Read-only mode is on, so writes are refused before they reach Jira — clear it to allow this command to mutate."
		err := map[string]any{"code": "read_only_blocked", "message": "read-only mode: write blocked: POST /rest/api/3/bulk/issues/move", "hint": hint}
		row = map[string]any{"issue": issue, "ok": false, "error": err}
		top := map[string]any{"code": err["code"], "message": err["message"], "hint": "Inspect retained data and verify mutation outcomes before retrying. " + hint}
		want, _ := json.Marshal([]any{top})
		if !parityAuthoringJSON(root["errors"], want) {
			return false
		}
	}
	want := map[string]any{"dry_run": preview, "polls": 0, "terminal": native && waited, "poll_limit_reached": false, "payload": json.RawMessage(payload), "targets": []any{map[string]any{"source": id, "destination": destination}}, "bindings_validated": remote, "remote_validated": remote, "attempted": !preview, "acknowledged": native, "results": []any{row}}
	if native {
		var task map[string]json.RawMessage
		var taskID string
		if json.Unmarshal(data["task"], &task) != nil || len(task) != 1 || json.Unmarshal(task["id"], &taskID) != nil || taskID == "" {
			return false
		}
		receipt, _ := json.Marshal(map[string]any{"taskId": taskID})
		if !parityAuthoringJSON(data["receipt"], receipt) {
			return false
		}
		delete(data, "task")
		delete(data, "receipt")
		if waited {
			var polls int
			var progress map[string]json.RawMessage
			var processed []int64
			var status, observedTask string
			nativeID, err := strconv.ParseInt(id, 10, 64)
			if err != nil || json.Unmarshal(data["polls"], &polls) != nil || polls < 1 || json.Unmarshal(data["progress"], &progress) != nil || json.Unmarshal(progress["taskId"], &observedTask) != nil || observedTask != taskID || json.Unmarshal(progress["status"], &status) != nil || status != "COMPLETE" || json.Unmarshal(progress["processedAccessibleIssues"], &processed) != nil || !slices.Equal(processed, []int64{nativeID}) {
				return false
			}
			want["polls"] = polls
			delete(data, "progress")
		}
	}
	// Self is presentation metadata; identity is independently bound by key/ID.
	var rows []map[string]json.RawMessage
	if json.Unmarshal(data["results"], &rows) != nil || len(rows) != 1 {
		return false
	}
	var reference map[string]json.RawMessage
	if json.Unmarshal(rows[0]["issue"], &reference) != nil {
		return false
	}
	if self, exists := reference["self"]; exists {
		var value string
		if !remote || step == "read_only" || json.Unmarshal(self, &value) != nil || value == "" {
			return false
		}
		delete(reference, "self")
		rows[0]["issue"], _ = json.Marshal(reference)
		data["results"], _ = json.Marshal(rows)
	}
	got, _ := json.Marshal(data)
	expected, _ := json.Marshal(want)
	return parityAuthoringJSON(got, expected)
}

func TestParityNativeMoveReceiptRequiresCompleteFacts(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"sendBulkNotification":false,"targetToSourcesMapping":{"10,2":{"issueIdsOrKeys":["101"]}}}`)
	preview := `{"data":{"dry_run":true,"polls":0,"terminal":false,"poll_limit_reached":false,"payload":PAYLOAD,"targets":[{"source":"101","destination":"10,2"}],"bindings_validated":false,"remote_validated":false,"attempted":false,"acknowledged":false,"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"applied":false,"outcome":"planned"}}]}}`
	preview = strings.ReplaceAll(preview, "PAYLOAD", string(payload))
	remote := strings.ReplaceAll(preview, `"bindings_validated":false,"remote_validated":false`, `"bindings_validated":true,"remote_validated":true`)
	remote = strings.ReplaceAll(remote, `"key":"PAIR-1"`, `"key":"PAIR-1","id":"101"`)
	remote = strings.ReplaceAll(remote, `"outcome":"planned"`, `"outcome":"planned","destination":"10,2"`)
	accepted := strings.ReplaceAll(remote, `"dry_run":true`, `"dry_run":false`)
	accepted = strings.ReplaceAll(accepted, `"attempted":false,"acknowledged":false`, `"attempted":true,"acknowledged":true,"task":{"id":"task-1"},"receipt":{"taskId":"task-1"}`)
	accepted = strings.ReplaceAll(accepted, `"outcome":"planned"`, `"outcome":"accepted"`)
	processed := strings.ReplaceAll(accepted, `"polls":0,"terminal":false`, `"polls":1,"terminal":true,"progress":{"taskId":"task-1","status":"COMPLETE","processedAccessibleIssues":[101]}`)
	processed = strings.ReplaceAll(processed, `"applied":false,"outcome":"accepted"`, `"applied":true,"outcome":"processed"`)
	refused := `{"data":{"dry_run":false,"polls":0,"terminal":false,"poll_limit_reached":false,"payload":PAYLOAD,"targets":[{"source":"101","destination":"10,2"}],"bindings_validated":true,"remote_validated":true,"attempted":true,"acknowledged":false,"results":[{"issue":{"key":"PAIR-1","id":"101"},"ok":false,"error":{"code":"read_only_blocked","message":"read-only mode: write blocked: POST /rest/api/3/bulk/issues/move","hint":"Read-only mode is on, so writes are refused before they reach Jira — clear it to allow this command to mutate."}}]},"errors":[{"code":"read_only_blocked","message":"read-only mode: write blocked: POST /rest/api/3/bulk/issues/move","hint":"Inspect retained data and verify mutation outcomes before retrying. Read-only mode is on, so writes are refused before they reach Jira — clear it to allow this command to mutate."}]}`
	refused = strings.ReplaceAll(refused, "PAYLOAD", string(payload))
	for _, tc := range []struct {
		name, raw string
		waited    bool
	}{{"local_preview", preview, false}, {"remote_preview", remote, false}, {"native", accepted, false}, {"native", processed, true}, {"read_only", refused, false}} {
		t.Run(fmt.Sprintf("%s/waited_%t", tc.name, tc.waited), func(t *testing.T) {
			if !parityNativeMoveReceipt([]byte(tc.raw), tc.name, "PAIR-1", "101", "10,2", payload, tc.waited) {
				t.Fatal("complete independently authored native receipt refused")
			}
			for name, invalid := range map[string]string{"missing_false": strings.ReplaceAll(tc.raw, `"poll_limit_reached":false,`, ""), "foreign_key": strings.ReplaceAll(tc.raw, "PAIR-1", "FOREIGN-1"), "failed_row": strings.ReplaceAll(tc.raw, `"ok":true`, `"ok":false`), "wrong_outcome": strings.ReplaceAll(tc.raw, `"outcome":`, `"ignored":`), "missing_payload": strings.ReplaceAll(tc.raw, `"payload":`+string(payload)+`,`, ""), "empty_data": `{"data":{}}`, "null_data": `{"data":null}`} {
				if invalid == tc.raw {
					continue
				}
				t.Run(name, func(t *testing.T) {
					if !jsontext.Value(invalid).IsValid() || bytes.Equal([]byte(invalid), []byte(tc.raw)) {
						t.Fatal("negative receipt must be changed complete original JSON")
					}
					if parityNativeMoveReceipt([]byte(invalid), tc.name, "PAIR-1", "101", "10,2", payload, tc.waited) {
						t.Fatal("native receipt admitted incomplete or foreign facts")
					}
				})
			}
		})
	}
}

func parityNativeMoveStatus(raw []byte, taskID string, native []byte) bool {
	if !jsontext.Value(raw).IsValid() || !jsontext.Value(native).IsValid() || taskID == "" {
		return false
	}
	want, _ := json.Marshal(map[string]any{"task": map[string]any{"id": taskID}, "progress": json.RawMessage(native), "polls": 1, "terminal": true, "poll_limit_reached": false})
	return parityAuthoringJSON(raw, want)
}

func TestParityNativeMoveStatusRequiresCompleteFacts(t *testing.T) {
	t.Parallel()
	native := []byte(`{"taskId":"task-1","status":"COMPLETE","processedAccessibleIssues":[101],"future":false}`)
	valid := `{"task":{"id":"task-1"},"progress":{"taskId":"task-1","status":"COMPLETE","processedAccessibleIssues":[101],"future":false},"polls":1,"terminal":true,"poll_limit_reached":false}`
	if !parityNativeMoveStatus([]byte(valid), "task-1", native) {
		t.Fatal("complete native status refused")
	}
	for name, invalid := range map[string]string{"foreign_task": strings.ReplaceAll(valid, "task-1", "foreign-2"), "missing_false": strings.ReplaceAll(valid, `,"poll_limit_reached":false`, ""), "foreign_processed": strings.ReplaceAll(valid, "[101]", "[102]"), "lost_native": strings.ReplaceAll(valid, `,"future":false`, ""), "duplicate": strings.ReplaceAll(valid, `"polls":1`, `"polls":1,"polls":1`)} {
		t.Run(name, func(t *testing.T) {
			if parityNativeMoveStatus([]byte(invalid), "task-1", native) {
				t.Fatal("status admitted incomplete or foreign native facts")
			}
		})
	}
}

// This capability has no historical bulk-job counterpart. Every acceptance
// marker is current-only even though both lanes establish account admission.
func TestLiveParityNativeMove(t *testing.T) {
	p := newParityRun(t)
	r := p.owner
	lane := p.evidence.Lanes[1]
	initialType, destination := p.moveTypeMetadata(t)
	permissions := rawRead(t, r, "api", "get", "/rest/api/3/mypermissions", "--query=projectKey="+r.manifest.Project, "--query=permissions=BULK_CHANGE,MOVE_ISSUES,CREATE_ISSUES")
	var body map[string]json.RawMessage
	var permitted map[string]map[string]json.RawMessage
	if !jsontext.Value(permissions.Response.JSON).IsValid() || json.Unmarshal(permissions.Response.JSON, &body) != nil || json.Unmarshal(body["permissions"], &permitted) != nil {
		t.Fatal("native move permissions are incomplete")
	}
	for _, name := range []string{"BULK_CHANGE", "MOVE_ISSUES", "CREATE_ISSUES"} {
		var allowed *bool
		if json.Unmarshal(permitted[name]["havePermission"], &allowed) != nil || allowed == nil || !*allowed {
			t.Fatal("native move requires an unavailable permission")
		}
	}
	key := p.create(t, lane)
	keys := []string{key}
	before := ownedMoveState(t, r, key)
	baseline := p.fieldMoveNative(t, key)
	if before.Fields.IssueType.ID != initialType {
		t.Fatal("native move fixture does not match verified initial type metadata")
	}
	for i, typeID := range []string{destination, initialType} {
		input := r.moveInput(t, keys, typeID)
		payload, err := os.ReadFile(input)
		if err != nil {
			t.Fatal(err)
		}
		args := []string{"issue", "move", key, "--json-input", input, "--force"}
		for _, step := range []string{"local_preview", "remote_preview", "read_only", "native"} {
			t.Run(fmt.Sprintf("%d/%s", i, step), func(t *testing.T) {
				argv := slices.Clone(args)
				var extra []string
				switch step {
				case "local_preview":
					argv = append(argv, "--dry-run")
				case "remote_preview":
					argv = append(argv, "--dry-run", "--validate-remote")
				case "read_only":
					extra = []string{"JIRA_READ_ONLY=true"}
				case "native":
					if i == 1 {
						argv = append(argv, "--wait", "--max-polls=40", "--poll-interval=1s")
					}
				}
				if err := r.checkMutation(keys, argv); err != nil {
					t.Fatal(err)
				}
				// Even a broken preview/refusal must not leave an untracked job.
				// A known no-write outcome removes only this provisional intent.
				index := len(r.manifest.BulkTasks)
				r.manifest.BulkTasks = append(r.manifest.BulkTasks, ownedBulkTask{Keys: slices.Clone(keys)})
				r.save(t)
				if err := r.checkMutation(keys, argv); err != nil {
					t.Fatal(err)
				}
				frame, stdout, exit := p.invoke(t, lane, extra, argv...)
				var receipt struct {
					Data envelope.IssueMoveOutput `json:"data"`
				}
				if !jsontext.Value(stdout).IsValid() || json.Unmarshal(stdout, &receipt) != nil {
					t.Fatal("native move lacks complete original JSON; retain pending task intent")
				}
				out := receipt.Data
				if out.Task != nil && out.Task.ID != "" {
					if !parityUniqueMoveTask(r.manifest.BulkTasks, index, out.Task.ID) {
						t.Fatal("native receipt reused an earlier task ID; retain unresolved intent and original receipt")
					}
					r.manifest.BulkTasks[index].ID = out.Task.ID
					r.save(t)
				}
				if !parityNativeMoveReceipt(stdout, step, key, before.ID, r.moveProjectID+","+typeID, payload, i == 1) {
					t.Fatal("native move receipt lost required original JSON facts; retain task intent")
				}
				if step != "native" {
					if out.Task != nil || out.Acknowledged {
						t.Fatal("preview/refusal returned a native job; retain its intent for cleanup reconciliation")
					}
					if step == "read_only" {
						if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "read_only_blocked" {
							t.Fatal("native move refusal lacks known local write admission")
						}
					} else if exit != 0 || !frame.OK || !out.DryRun || out.Attempted || out.BindingsValidated != (step == "remote_preview") || out.RemoteValidated != (step == "remote_preview") {
						t.Fatal("native preview fabricated request entry or lost its remote-check facts")
					}
					r.manifest.BulkTasks = slices.Delete(r.manifest.BulkTasks, index, index+1)
					r.save(t)
					p.fieldMoveObserve(t, keys, map[string]parityEpicState{key: baseline}, map[string]string{key: before.Fields.IssueType.ID}, map[string]string{key: before.Fields.Summary})
				} else {
					if exit != 0 || !frame.OK || !out.Acknowledged || out.Task == nil || out.Task.ID == "" || len(out.Results) != 1 || out.Results[0].Issue.ID != before.ID || out.Results[0].Data == nil {
						t.Fatal("native submission failed; retain task receipt and never replay")
					}
					if i == 0 && (out.Polls != 0 || out.Terminal || out.Results[0].Data.Applied) {
						t.Fatal("asynchronous native acknowledgement claimed completion")
					}
					if i == 1 && (!out.Terminal || out.PollLimitReached || out.Polls == 0 || !out.Results[0].OK || !out.Results[0].Data.Applied || out.Results[0].Data.Outcome != "processed") {
						t.Fatal("waited native move lost its observed completion")
					}
					if !r.finishMoveTasks(t) {
						t.Fatal("native move did not settle; cleanup must retain its resources")
					}
					observed := ownedMoveState(t, r, key)
					if observed.ID != before.ID || observed.Key != before.Key || observed.Fields.IssueType.ID != typeID || observed.Fields.Summary != before.Fields.Summary || !parityAuthoringJSON(observed.Fields.Description, before.Fields.Description) {
						t.Fatal("native job's actual stored type/summary/body differs from the owned request")
					}
					before = observed
					p.fieldMoveObserve(t, keys, map[string]parityEpicState{key: baseline}, map[string]string{key: typeID}, map[string]string{key: before.Fields.Summary})
				}
				p.check(t, lane, fmt.Sprintf("N006.move.native.%d.%s", i, step))
			})
			if t.Failed() {
				return
			}
		}
		task := r.manifest.BulkTasks[len(r.manifest.BulkTasks)-1]
		if task.ID == "" || !task.Terminal {
			t.Fatal("native progress requires a known completed owned task")
		}
		args = []string{"issue", "move", "status"}
		if i == 1 {
			args = append(args, "--wait", "--max-polls=40", "--poll-interval=1s")
		}
		args = append(args, "--", task.ID)
		frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, args...)
		var status struct {
			Data envelope.IssueMoveStatusOutput `json:"data"`
		}
		if !jsontext.Value(stdout).IsValid() || exit != 0 || !frame.OK || json.Unmarshal(stdout, &status) != nil || status.Data.Task == nil || status.Data.Task.ID != task.ID || !status.Data.Terminal || status.Data.Polls != 1 || status.Data.Progress == nil || status.Data.Progress.Status.Value != "COMPLETE" {
			t.Fatal("explicit native status lost its task or terminal observation")
		}
		native := rawRead(t, r, "api", "get", "/rest/api/3/bulk/queue/"+url.PathEscape(task.ID))
		var progress map[string]json.RawMessage
		var state string
		var nativeTask string
		var processed []int64
		id, err := strconv.ParseInt(before.ID, 10, 64)
		if err != nil || id <= 0 || !jsontext.Value(native.Response.JSON).IsValid() || json.Unmarshal(native.Response.JSON, &progress) != nil || json.Unmarshal(progress["taskId"], &nativeTask) != nil || nativeTask != task.ID || json.Unmarshal(progress["status"], &state) != nil || state != "COMPLETE" || json.Unmarshal(progress["processedAccessibleIssues"], &processed) != nil || !slices.Equal(processed, []int64{id}) || !slices.Equal(status.Data.Progress.ProcessedAccessibleIssues.Value, processed) {
			t.Fatal("native queue observation differs from the exact owned processed ID")
		}
		if !parityNativeMoveStatus(parityOriginalFrameData(stdout), task.ID, native.Response.JSON) {
			t.Fatal("status receipt differs from complete independent terminal native progress")
		}
		p.check(t, lane, fmt.Sprintf("N007.move.status.native_%d", i))
	}
	if final := ownedMoveState(t, r, key); final.Fields.IssueType.ID != initialType {
		t.Fatal("native move did not restore its original type")
	}
}
