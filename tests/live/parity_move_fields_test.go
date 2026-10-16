//go:build live

package live

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type parityFieldMoveAttempt struct {
	Lane  string `json:"lane"`
	Key   string `json:"key"`
	Input string `json:"input"`
	Bound bool   `json:"bound"`
}

// The historical spelling is translated only for the shared ownership guard.
// The actual subprocess still receives its original lane's command grammar.
func parityFieldMoveAdmission(r *liveRun, lane string, keys, args []string) error {
	if len(args) < 3 || args[0] != "issue" || args[1] != "move" {
		return errors.New("field mapping requires its exact issue move route")
	}
	canonical := slices.Clone(args)
	switch lane {
	case "before":
		if !liveKey.MatchString(args[2]) {
			return errors.New("legacy field mapping requires literal owned issue keys")
		}
		canonical = append([]string{"issue", "move", "fields"}, args[2:]...)
	case "after":
		if args[2] != "fields" {
			return errors.New("current field mapping requires the fields subcommand")
		}
	default:
		return errors.New("field mapping requires an admitted lane")
	}
	for i, arg := range canonical {
		if arg != "--json-input" {
			continue
		}
		if i+1 >= len(canonical) {
			return errors.New("field mapping lacks a registered input")
		}
		path := canonical[i+1]
		if filepath.Dir(path) != r.dir {
			return errors.New("field mapping input must remain an owned direct child")
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("field mapping input must remain a regular file")
		}
		return r.checkMutation(keys, canonical)
	}
	return errors.New("field mapping requires a registered exact input")
}

func parityFieldMoveData(raw []byte, lane string, keys []string, payload []byte, preview bool) bool {
	if !jsontext.Value(raw).IsValid() || !jsontext.Value(payload).IsValid() || len(keys) == 0 || (lane != "before" && lane != "after") {
		return false
	}
	rows := make([]any, 0, len(keys))
	for _, key := range keys {
		if !liveKey.MatchString(key) {
			return false
		}
		if lane == "before" {
			data := map[string]any{"issue": map[string]any{"key": key}, "payload": json.RawMessage(payload), "dry_run": preview}
			if !preview {
				data["result"] = map[string]any{}
			}
			if len(keys) == 1 {
				expected, _ := json.Marshal(data)
				return parityAuthoringJSON(raw, expected)
			}
			rows = append(rows, map[string]any{"key": key, "ok": true, "data": data})
		} else {
			rows = append(rows, map[string]any{"issue": map[string]any{"key": key}, "ok": true, "data": map[string]any{}})
		}
	}
	want := map[string]any{"results": rows, "succeeded": len(keys), "failed": 0}
	if lane == "after" {
		want = map[string]any{"dry_run": preview, "notify": true, "payload": json.RawMessage(payload), "description_route": "adf", "results": rows}
	}
	expected, _ := json.Marshal(want)
	return parityAuthoringJSON(raw, expected)
}

func parityFieldMoveConfirmation(raw []byte, lane string, keys []string) bool {
	if !jsontext.Value(raw).IsValid() || len(keys) < 1 || len(keys) > 2 || (lane != "before" && lane != "after") {
		return false
	}
	for _, key := range keys {
		if !liveKey.MatchString(key) {
			return false
		}
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil || !bytes.Equal(root["data"], []byte("null")) {
		return false
	}
	subject := keys[0]
	if len(keys) == 2 {
		subject = "2 issues (" + keys[0] + " and " + keys[1] + ")"
	}
	want := map[string]any{"code": "flag_required", "message": "applying a move field payload to " + subject + " needs confirmation — --no-input means it cannot be asked for, so pass --force to confirm up front", "hint": "Add the flag the command needs — run it with `--help` to see which.", "field": "force"}
	if lane == "before" {
		message := "issue move requires --force in headless / agent / --no-input mode"
		if len(keys) == 2 {
			message = "issue move with multiple keys requires --force"
		}
		want = map[string]any{"type": "validation", "code": "validation_failed", "message": message, "hint": "Check the values you passed and try again.", "retryable": false}
	}
	expected, _ := json.Marshal([]any{want})
	return parityAuthoringJSON(root["errors"], expected)
}

func TestParityFieldMoveConfirmationRefusesUnrelatedFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		lane  string
		keys  []string
		valid string
	}{
		{"before", []string{"PAIR-1"}, `{"data":null,"errors":[{"type":"validation","code":"validation_failed","message":"issue move requires --force in headless / agent / --no-input mode","hint":"Check the values you passed and try again.","retryable":false}]}`},
		{"before", []string{"PAIR-1", "PAIR-2"}, `{"data":null,"errors":[{"type":"validation","code":"validation_failed","message":"issue move with multiple keys requires --force","hint":"Check the values you passed and try again.","retryable":false}]}`},
		{"after", []string{"PAIR-1"}, "{\"data\":null,\"errors\":[{\"code\":\"flag_required\",\"message\":\"applying a move field payload to PAIR-1 needs confirmation — --no-input means it cannot be asked for, so pass --force to confirm up front\",\"hint\":\"Add the flag the command needs — run it with `--help` to see which.\",\"field\":\"force\"}]}"},
		{"after", []string{"PAIR-1", "PAIR-2"}, "{\"data\":null,\"errors\":[{\"code\":\"flag_required\",\"message\":\"applying a move field payload to 2 issues (PAIR-1 and PAIR-2) needs confirmation — --no-input means it cannot be asked for, so pass --force to confirm up front\",\"hint\":\"Add the flag the command needs — run it with `--help` to see which.\",\"field\":\"force\"}]}"},
	} {
		t.Run(fmt.Sprintf("%s/%d", tc.lane, len(tc.keys)), func(t *testing.T) {
			if !parityFieldMoveConfirmation([]byte(tc.valid), tc.lane, tc.keys) {
				t.Fatal("independent field confirmation fixture refused")
			}
			for name, invalid := range map[string]string{"missing-hint": strings.ReplaceAll(tc.valid, `"hint":`, `"missing_hint":`), "wrong-hint": strings.ReplaceAll(strings.ReplaceAll(tc.valid, "try again", "accept anything"), "run it with", "ignore"), "wrong-field": strings.ReplaceAll(tc.valid, `"field":"force"`, `"field":"json-input"`), "missing-field": strings.ReplaceAll(tc.valid, `,"field":"force"`, ""), "foreign-reason": strings.ReplaceAll(strings.ReplaceAll(tc.valid, "requires --force", "requires --token"), "applying a move field payload", "editing a local configuration"), "false-data": strings.ReplaceAll(tc.valid, `"data":null`, `"data":{}`)} {
				if invalid == tc.valid {
					continue
				}
				t.Run(name, func(t *testing.T) {
					if !jsontext.Value(invalid).IsValid() {
						t.Fatal("negative confirmation fixture must remain complete JSON")
					}
					if parityFieldMoveConfirmation([]byte(invalid), tc.lane, tc.keys) {
						t.Fatal("confirmation oracle admitted an unrelated reason, hint or field")
					}
				})
			}
		})
	}
}

func TestParityFieldMoveAdmissionPreservesBulkBoundary(t *testing.T) {
	t.Parallel()
	r := &liveRun{verified: true, dir: t.TempDir(), moveProjectID: "10", moveTypes: map[string]bool{"1": true, "2": true}, manifest: liveManifest{Issues: []ownedIssue{{Key: "PAIR-1", ID: "101"}, {Key: "PAIR-2", ID: "102"}}}}
	path := filepath.Join(r.dir, "fields.json")
	body := []byte(`{"fields":{"summary":"Owned","issuetype":{"id":"1"}}}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	keys := []string{"PAIR-1", "PAIR-2"}
	r.moveInputs = map[string]nativeMoveInput{path: {keys: slices.Clone(keys), digest: sha256.Sum256(body), fields: true}}
	for _, lane := range []string{"before", "after"} {
		args := []string{"issue", "move"}
		if lane == "after" {
			args = append(args, "fields")
		}
		args = append(args, keys...)
		args = append(args, "--json-input", path, "--force", "--parallelism=1")
		original := slices.Clone(args)
		if err := parityFieldMoveAdmission(r, lane, keys, args); err != nil || !slices.Equal(args, original) {
			t.Fatal("field mapping refused its owned recipe or changed caller argv")
		}
		for _, invalid := range [][]string{append(slices.Clone(args), "--wait"), append(slices.Clone(args), "--parallelism=2"), append(slices.Clone(args), "--parallelism=1"), append(slices.Clone(args), "--jql=project=OTHER")} {
			if parityFieldMoveAdmission(r, lane, keys, invalid) == nil {
				t.Fatal("field mapping broadened its allowed mutation grammar")
			}
		}
		if parityFieldMoveAdmission(r, lane, keys[:1], args) == nil {
			t.Fatal("field mapping omitted a declared target")
		}
		r.manifest.Issues[1].Deleted = true
		if parityFieldMoveAdmission(r, lane, keys, args) == nil {
			t.Fatal("field mapping admitted an inactive target")
		}
		r.manifest.Issues[1].Deleted = false
	}
	args := []string{"issue", "move", "PAIR-1", "PAIR-2", "--json-input", path, "--force", "--parallelism=1"}
	if r.checkMutation(keys, args) == nil {
		t.Fatal("native bulk route accepted a field recipe")
	}
	if parityFieldMoveAdmission(r, "unknown", keys, args) == nil {
		t.Fatal("unadmitted lane accepted")
	}
	if err := os.WriteFile(path, []byte(`{"fields":{"project":{"key":"OTHER"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if parityFieldMoveAdmission(r, "before", keys, args) == nil {
		t.Fatal("field mapping accepted changed registered bytes")
	}
}

func TestParityFieldMoveOperationMapping(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"issue", "move", "PAIR-1"}, "issue.move"},
		{[]string{"issue", "move", "fields", "PAIR-1"}, "issue.move.fields"},
	} {
		if parityOperation(tc.args) != tc.want {
			t.Fatal("historical field mapping changed the native bulk operation")
		}
	}
}

func TestParityFieldMoveParallelismRemainsScoped(t *testing.T) {
	t.Parallel()
	r := &liveRun{verified: true, dir: t.TempDir(), moveProjectID: "10", moveTypes: map[string]bool{"1": true, "2": true}, manifest: liveManifest{Issues: []ownedIssue{{Key: "PAIR-1", ID: "101"}}}}
	keys := []string{"PAIR-1"}
	path := r.moveInput(t, keys, "2")
	args := []string{"issue", "move", "PAIR-1", "--json-input", path, "--force"}
	if err := r.checkMutation(keys, args); err != nil {
		t.Fatal("native bulk control recipe refused")
	}
	if r.checkMutation(keys, append(slices.Clone(args), "--parallelism=1")) == nil {
		t.Fatal("field parallelism leaked into the native bulk route")
	}
	fields := []string{"issue", "move", "fields", "PAIR-1", "--json-input", path, "--force"}
	if parityFieldMoveAdmission(r, "after", keys, fields) == nil {
		t.Fatal("field mapping admitted a native bulk recipe")
	}
}

func TestParityFieldMoveReceiptRejectsInventedMigration(t *testing.T) {
	t.Parallel()
	const payload = `{"fields":{"summary":"Owned","issuetype":{"id":"1"}}}`
	for _, tc := range []struct{ lane, raw string }{
		{"before", `{"issue":{"key":"PAIR-1"},"payload":` + payload + `,"dry_run":false,"result":{}}`},
		{"after", `{"dry_run":false,"notify":true,"payload":` + payload + `,"description_route":"adf","results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{}}]}`},
	} {
		if !parityFieldMoveData([]byte(tc.raw), tc.lane, []string{"PAIR-1"}, []byte(payload), false) {
			t.Fatal("independent field move receipt refused")
		}
		for _, invalid := range []string{`{}`, tc.raw + `{}`, strings.ReplaceAll(tc.raw, `"PAIR-1"`, `"PAIR-2"`), strings.ReplaceAll(tc.raw, `"Owned"`, `"Changed"`), strings.Replace(tc.raw, `"dry_run":false`, `"dry_run":true`, 1), strings.Replace(tc.raw, `"dry_run":false`, `"task":{"id":"unrelated"},"dry_run":false`, 1)} {
			if parityFieldMoveData([]byte(invalid), tc.lane, []string{"PAIR-1"}, []byte(payload), false) {
				t.Fatal("field move receipt admitted a changed target, payload or bulk task claim")
			}
		}
	}
}
