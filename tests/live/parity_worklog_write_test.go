//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type parityWorklogAttempt struct {
	Lane         string `json:"lane"`
	Key          string `json:"key"`
	Digest       string `json:"input_sha256"`
	ID           string `json:"returned_id"`
	NativeID     string `json:"observed_id,omitempty"`
	Acknowledged bool   `json:"acknowledged"`
	Bound        bool   `json:"bound"`
}

func (p *parityRun) validateWorklogMutation(lane parityLane, key string, args []string) error {
	if !slices.Contains(p.evidence.Lanes, lane) || p.owner.ownedLiveIssue(key) == nil {
		return fmt.Errorf("worklog recipe requires an admitted lane and its active owned issue")
	}
	if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
		return err
	}
	return p.owner.checkWorklogMutation([]string{key}, args)
}

func (r *liveRun) parityWorklogInput(t *testing.T, key, lane, started string) string {
	t.Helper()
	if !r.verified || r.ownedLiveIssue(key) == nil || (lane != "before" && lane != "after") {
		t.Fatal("paired worklog input requires its verified active owned target and lane")
	}
	// This exact compatibility input has equivalent minute-based effort in
	// both lanes. No profile workday default or wall-clock input is inferred.
	body := []byte(`{"time_spent":"2m","started":"` + started + `","comment":` + liveWorklogComment + `}`)
	path := filepath.Join(r.dir, "paired-worklog-"+lane+".json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if r.worklogInputs == nil {
		r.worklogInputs = make(map[string]nativeWorklogInput)
	}
	r.worklogInputs[path] = nativeWorklogInput{keys: []string{key}, digest: sha256.Sum256(body)}
	return path
}

func parityWorklogAddData(data []byte, lane, key, started string, preview bool, native json.RawMessage) bool {
	if !jsontext.Value(data).IsValid() {
		return false
	}
	instant, err := time.Parse(time.RFC3339, started)
	if err != nil {
		return false
	}
	payload := map[string]any{"timeSpentSeconds": 120, "started": instant.Format("2006-01-02T15:04:05.000-0700"), "comment": json.RawMessage(liveWorklogComment)}
	var want map[string]any
	switch lane {
	case "before":
		worklog := any(payload)
		if !preview {
			projected, ok := parityWorklogLegacyItems([]json.RawMessage{native})
			if !ok || len(projected) != 1 {
				return false
			}
			worklog = projected[0]
		}
		want = map[string]any{"issue": map[string]any{"key": key}, "worklog": worklog, "dry_run": preview}
	case "after":
		entry := map[string]any{"applied": !preview}
		if !preview {
			var record map[string]json.RawMessage
			var id string
			if !jsontext.Value(native).IsValid() || json.Unmarshal(native, &record) != nil || json.Unmarshal(record["id"], &id) != nil || id == "" {
				return false
			}
			entry["worklog"] = map[string]any{"id": id, "fields": native}
		}
		want = map[string]any{"dry_run": preview, "payload": payload, "notify": false, "body_route": "adf", "workday_seconds": 28800, "results": []any{map[string]any{"issue": map[string]any{"key": key}, "ok": true, "data": entry}}}
	default:
		return false
	}
	expected, err := json.Marshal(want)
	return err == nil && parityAuthoringJSON(data, expected)
}

func (p *parityRun) nativeWorklogPage(t *testing.T, key string) (parityWorklogExpected, json.RawMessage) {
	t.Helper()
	if p.owner.ownedLiveIssue(key) == nil {
		t.Fatal("paired native worklog observation requires its active owned issue")
	}
	reply := rawRead(t, p.owner, "api", "get", "/rest/api/3/issue/"+key+"/worklog", "--query=startAt=0", "--query=maxResults=50")
	page, ok := parityNativeWorklogExpected(reply.Response.JSON, 0, 50)
	if !ok || !page.Complete || page.Total != len(page.Native) {
		t.Fatal("owned native worklog read is incomplete or malformed")
	}
	return page, reply.Response.JSON
}

func TestLiveParityWorklogCreation(t *testing.T) {
	p := newParityRun(t)
	started := time.Now().UTC().Add(-time.Hour).Truncate(time.Second).Format(time.RFC3339)
	for _, lane := range p.evidence.Lanes {
		if !t.Run(lane.Name, func(t *testing.T) {
			key := p.create(t, lane)
			original := p.native(t, key)
			page, _ := p.nativeWorklogPage(t, key)
			if len(page.Native) != 0 {
				t.Fatal("fresh paired issue unexpectedly contains worklogs")
			}
			path := p.owner.parityWorklogInput(t, key, lane.Name, started)
			args := []string{"worklog", "add", key, "--json-input", path, "--parallelism=1"}
			if lane.Name == "after" {
				args = append(args, "--notify=false")
			}
			for _, phase := range []string{"preview", "read-only", "live"} {
				if !t.Run(phase, func(t *testing.T) {
					invoke := slices.Clone(args)
					if phase == "preview" {
						invoke = append(invoke, "--dry-run")
					}
					if err := p.validateWorklogMutation(lane, key, invoke); err != nil {
						t.Fatal(err)
					}
					extra := []string{"JIRA_WORKDAY_SECONDS=28800"}
					if phase == "read-only" {
						extra = append(extra, "JIRA_READ_ONLY=true")
					}
					index := -1
					if phase == "live" {
						input := p.owner.worklogInputs[path]
						index = len(p.evidence.Worklogs)
						p.evidence.Worklogs = append(p.evidence.Worklogs, parityWorklogAttempt{Lane: lane.Name, Key: key, Digest: fmt.Sprintf("%x", input.digest)})
						p.save(t)
					}
					frame, stdout, exit := p.invoke(t, lane, extra, invoke...)
					// Retain every understood receipt before a later observation can
					// fail. Subprocess entry never supplies an acknowledgement.
					if phase == "live" {
						id, ack := parityWorklogAcknowledgement(stdout, lane.Name, key, exit == 0 && frame.OK)
						p.evidence.Worklogs[index].ID, p.evidence.Worklogs[index].Acknowledged = id, ack
						p.save(t)
					}
					stored, _ := p.nativeWorklogPage(t, key)
					if phase == "live" {
						id, ack := p.evidence.Worklogs[index].ID, p.evidence.Worklogs[index].Acknowledged
						if len(stored.Native) == 1 {
							nativeID, valid := parityWorklogStoredEffect(stored.Native[0], started)
							p.evidence.Worklogs[index].NativeID = nativeID
							p.evidence.Worklogs[index].Bound = valid && (id == "" || id == nativeID)
							p.save(t)
						}
						if !ack || !p.evidence.Worklogs[index].Bound || len(stored.Native) != 1 {
							t.Fatal("worklog creation lacks a bound single native effect and valid acknowledgement; never replay")
						}
					} else if len(stored.Native) != 0 {
						t.Fatal("worklog preview or read-only refusal changed Jira")
					}
					if phase == "read-only" {
						if !parityWorklogBlocked(stdout, lane.Name, key, started, exit) {
							t.Fatal("worklog read-only refusal lost its actual classification")
						}
					} else {
						if exit != 0 || !frame.OK {
							t.Fatal("worklog creation/preview failed; inspect retained receipts, never replay")
						}
						var native json.RawMessage
						if phase == "live" {
							// The POST record and later GET can differ in optional native
							// metadata. Bind common requested effects independently here;
							// complete 201 preservation has its controlled-server oracle.
							var ok bool
							native, ok = parityWorklogReceiptRecord(stdout, lane.Name, key)
							receiptID, matches := parityWorklogStoredEffect(native, started)
							if !ok || !matches || receiptID != p.evidence.Worklogs[index].ID {
								t.Fatal("worklog POST record differs from independently bound request/GET effects")
							}
						}
						data, err := json.Marshal(parityAuthoringCleanData(t, stdout, lane.Name, "worklog.add"))
						if err != nil || !parityWorklogAddData(data, lane.Name, key, started, phase == "preview", native) {
							t.Fatal("worklog creation/preview differs from independently observed native values and requested policy")
						}
					}
					current := p.native(t, key)
					if current.ID != original.ID || current.Key != original.Key || !parityAuthoringJSON(current.Fields, original.Fields) {
						t.Fatal("worklog recipe changed unrelated owned issue identity/fields")
					}
					p.check(t, lane, "C072.worklogs."+phase)
				}) {
					return
				}
			}
			keys, pages := []string{key}, map[string]parityWorklogExpected{}
			stored, _ := p.nativeWorklogPage(t, key)
			pages[key] = stored
			frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, "worklog", "list", key)
			if exit != 0 || !frame.OK || !parityWorklogProjection(parityWorklogData(t, stdout, lane.Name, "json", keys, pages), lane.Name, keys, pages) {
				t.Fatal("nonempty native worklog list differs from independently observed creation")
			}
			p.check(t, lane, "C073.worklogs.native-created")
			if lane.Name == "after" {
				for _, tc := range []struct {
					name        string
					start, size int
					flags       []string
				}{
					{"one-record", 0, 1, []string{"--limit=1"}},
					{"after-record", 1, 1, []string{"--start=1", "--limit=1"}},
					{"all-records", 0, 100, []string{"--all"}},
				} {
					if !t.Run("paging/"+tc.name, func(t *testing.T) {
						// The preceding acknowledged write supplied one independently
						// bound record. Compare a separately requested native page;
						// an empty pre-existing issue cannot prove offset selection.
						read := rawRead(t, p.owner, "api", "get", "/rest/api/3/issue/"+key+"/worklog", "--query=startAt="+fmt.Sprint(tc.start), "--query=maxResults="+fmt.Sprint(tc.size))
						expected, ok := parityNativeWorklogExpected(read.Response.JSON, tc.start, tc.size)
						if !ok || !expected.Complete || expected.Total != 1 || len(expected.Native) != 1-tc.start {
							t.Fatal("owned native paging fixture lost its acknowledged single record or exact offset")
						}
						if tc.start == 0 && !parityAuthoringJSON(expected.Native[0], stored.Native[0]) {
							t.Fatal("independent native worklog changed before paging entry")
						}
						args := append([]string{"worklog", "list", key}, tc.flags...)
						frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, args...)
						pages := map[string]parityWorklogExpected{key: expected}
						if exit != 0 || !frame.OK || !parityWorklogProjection(parityWorklogData(t, stdout, lane.Name, "json", keys, pages), lane.Name, keys, pages) {
							t.Fatal("explicit worklog paging differs from its independent native record, offset or complete receipt")
						}
						after, _ := p.nativeWorklogPage(t, key)
						if len(after.Native) != 1 || !parityAuthoringJSON(after.Native[0], stored.Native[0]) {
							t.Fatal("worklog paging changed its owned stored record")
						}
						p.check(t, lane, "C073.worklogs.paging."+tc.name+".native")
					}) {
						return
					}
				}
			}
		}) {
			return
		}
	}
}

func TestParityWorklogMutationOwnership(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(home, "admitted-fixture")
	if err := os.WriteFile(binary, []byte("independent admission bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte("independent admission bytes")))
	lane := parityLane{Name: "before", Binary: binary, Digest: digest}
	r := &liveRun{verified: true, dir: home, manifest: liveManifest{Issues: []ownedIssue{{Key: "PAIR-1"}, {Key: "PAIR-2"}, {Key: "PAIR-3", Deleted: true}}}}
	p := &parityRun{owner: r, evidence: parityEvidence{Lanes: []parityLane{lane}}}
	path := r.parityWorklogInput(t, "PAIR-1", "before", "2026-01-02T12:30:00Z")
	valid := []string{"worklog", "add", "PAIR-1", "--json-input", path, "--parallelism=1"}
	if err := p.validateWorklogMutation(lane, "PAIR-1", valid); err != nil {
		t.Fatal("valid fixed owned worklog recipe refused: ", err)
	}
	for _, tc := range []struct {
		name string
		lane parityLane
		key  string
		args []string
	}{
		{"foreign-lane", parityLane{Name: "other", Binary: binary, Digest: digest}, "PAIR-1", valid},
		{"wrong-target", lane, "PAIR-2", valid},
		{"inactive-target", lane, "PAIR-3", valid},
		{"foreign-target", lane, "PAIR-9", valid},
		{"extra-target", lane, "PAIR-1", append(slices.Clone(valid), "PAIR-2")},
		{"selector", lane, "PAIR-1", append(slices.Clone(valid), "--jql=project=PAIR")},
		{"wrong-input", lane, "PAIR-1", []string{"worklog", "add", "PAIR-1", "--json-input", filepath.Join(home, "unregistered.json"), "--parallelism=1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if p.validateWorklogMutation(tc.lane, tc.key, tc.args) == nil {
				t.Fatal("unsupported worklog recipe entered its paired mutation boundary")
			}
		})
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("changed", 3)), 0o600); err != nil {
		t.Fatal(err)
	}
	if p.validateWorklogMutation(lane, "PAIR-1", valid) == nil {
		t.Fatal("changed registered worklog input entered mutation")
	}
}

func parityWorklogAcknowledgement(stdout []byte, lane, key string, success bool) (string, bool) {
	if !jsontext.Value(stdout).IsValid() {
		return "", false
	}
	var frame, data, worklog map[string]json.RawMessage
	if json.Unmarshal(stdout, &frame) != nil || json.Unmarshal(frame["data"], &data) != nil || data == nil {
		return "", false
	}
	if key == "" {
		return "", false
	}
	var dry *bool
	if json.Unmarshal(data["dry_run"], &dry) != nil || dry == nil || *dry {
		return "", false
	}
	ack := false
	switch lane {
	case "before":
		if !parityWorklogReceiptTarget(data["issue"], key) {
			return "", false
		}
		if json.Unmarshal(data["worklog"], &worklog) != nil || worklog == nil {
			return "", false
		}
	case "after":
		var rows []map[string]json.RawMessage
		var entry map[string]json.RawMessage
		var ok, applied *bool
		if json.Unmarshal(data["results"], &rows) != nil || len(rows) != 1 || json.Unmarshal(rows[0]["ok"], &ok) != nil || ok == nil || json.Unmarshal(rows[0]["data"], &entry) != nil || entry == nil || json.Unmarshal(entry["applied"], &applied) != nil || applied == nil {
			return "", false
		}
		if !parityWorklogReceiptTarget(rows[0]["issue"], key) {
			return "", false
		}
		// Applied is the current acknowledgement contract even if a later
		// receipt/output error makes the command fail. Do not erase it.
		ack = *ok && *applied
		if raw, present := entry["worklog"]; present && json.Unmarshal(raw, &worklog) != nil {
			return "", ack
		}
	default:
		return "", false
	}
	var id string
	if json.Unmarshal(worklog["id"], &id) != nil || id == "" || strings.TrimSpace(id) != id {
		return "", ack
	}
	if lane == "before" {
		ack = success
	}
	return id, ack
}

func TestParityWorklogAcknowledgementRetainsKnownEffects(t *testing.T) {
	for _, tc := range []struct {
		name, lane, frame, id string
		success, ack          bool
	}{
		{"legacy-success", "before", `{"data":{"issue":{"key":"PAIR-1"},"dry_run":false,"worklog":{"id":"501"}}}`, "501", true, true},
		{"legacy-failure-retains-id", "before", `{"data":{"issue":{"key":"PAIR-1"},"dry_run":false,"worklog":{"id":"501"}}}`, "501", false, false},
		{"current-success", "after", `{"data":{"dry_run":false,"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"applied":true,"worklog":{"id":"501"}}}]}}`, "501", true, true},
		{"current-late-error", "after", `{"data":{"dry_run":false,"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"applied":true,"worklog":{"id":"501"}}}]}}`, "501", false, true},
		{"current-missing-record", "after", `{"data":{"dry_run":false,"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"applied":true,"receipt_error":{"code":"worklog_response_invalid"}}}]}}`, "", true, true},
		{"current-refusal", "after", `{"data":{"dry_run":false,"results":[{"issue":{"key":"PAIR-1"},"ok":false,"error":{"code":"read_only_blocked"}}]}}`, "", false, false},
		{"missing-application", "after", `{"data":{"dry_run":false,"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"worklog":{"id":"501"}}}]}}`, "", true, false},
		{"null-application", "after", `{"data":{"dry_run":false,"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"applied":null,"worklog":{"id":"501"}}}]}}`, "", true, false},
		{"preview-cannot-acknowledge", "before", `{"data":{"dry_run":true,"worklog":{"id":"501"}}}`, "", true, false},
		{"duplicate-id", "before", `{"data":{"issue":{"key":"PAIR-1"},"dry_run":false,"worklog":{"id":"502","id":"501"}}}`, "", true, false},
		{"legacy-missing-target", "before", `{"data":{"dry_run":false,"worklog":{"id":"501"}}}`, "", true, false},
		{"legacy-null-target", "before", `{"data":{"dry_run":false,"issue":null,"worklog":{"id":"501"}}}`, "", true, false},
		{"legacy-foreign-target", "before", `{"data":{"dry_run":false,"issue":{"key":"PAIR-2"},"worklog":{"id":"501"}}}`, "", true, false},
		{"current-missing-target", "after", `{"data":{"dry_run":false,"results":[{"ok":true,"data":{"applied":true,"worklog":{"id":"501"}}}]}}`, "", true, false},
		{"current-null-target", "after", `{"data":{"dry_run":false,"results":[{"issue":null,"ok":true,"data":{"applied":true,"worklog":{"id":"501"}}}]}}`, "", true, false},
		{"current-foreign-target", "after", `{"data":{"dry_run":false,"results":[{"issue":{"key":"PAIR-2"},"ok":true,"data":{"applied":true,"worklog":{"id":"501"}}}]}}`, "", true, false},
		{"unknown-lane", "other", `{"data":{"issue":{"key":"PAIR-1"},"dry_run":false,"worklog":{"id":"501"}}}`, "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, ack := parityWorklogAcknowledgement([]byte(tc.frame), tc.lane, "PAIR-1", tc.success)
			if id != tc.id || ack != tc.ack {
				t.Fatal("understood worklog effects or unresolved receipts changed during admission")
			}
		})
	}
}

func TestParityWorklogAddPreviewUsesIndependentWireTimestamp(t *testing.T) {
	const before = `{"issue":{"key":"PAIR-1"},"worklog":{"timeSpentSeconds":120,"started":"2026-01-02T12:30:00.000+0000","comment":` + liveWorklogComment + `},"dry_run":true}`
	const after = `{"dry_run":true,"payload":{"timeSpentSeconds":120,"started":"2026-01-02T12:30:00.000+0000","comment":` + liveWorklogComment + `},"notify":false,"body_route":"adf","workday_seconds":28800,"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"applied":false}}]}`
	for _, lane := range []string{"before", "after"} {
		t.Run(lane, func(t *testing.T) {
			valid := before
			if lane == "after" {
				valid = after
			}
			if !parityWorklogAddData([]byte(valid), lane, "PAIR-1", "2026-01-02T12:30:00Z", true, nil) {
				t.Fatal("independent worklog preview refused its exact native timestamp and effort")
			}
			for _, mutation := range []struct{ name, original, replacement string }{
				{"wrong-effort", `"timeSpentSeconds":120`, `"timeSpentSeconds":60`},
				{"wrong-key", `"PAIR-1"`, `"PAIR-2"`},
				{"wrong-instant", "12:30:00.000+0000", "13:30:00.000+0000"},
				{"false-live", `"dry_run":true`, `"dry_run":false`},
			} {
				t.Run(mutation.name, func(t *testing.T) {
					changed := strings.Replace(valid, mutation.original, mutation.replacement, 1)
					if changed == valid || parityWorklogAddData([]byte(changed), lane, "PAIR-1", "2026-01-02T12:30:00Z", true, nil) {
						t.Fatal("changed worklog preview earned native input or application credit")
					}
				})
			}
		})
	}
}

func parityWorklogReceiptTarget(raw []byte, key string) bool {
	var issue map[string]json.RawMessage
	var observed string
	return json.Unmarshal(raw, &issue) == nil && json.Unmarshal(issue["key"], &observed) == nil && observed == key
}

func parityWorklogReceiptRecord(stdout []byte, lane, key string) (json.RawMessage, bool) {
	if _, ack := parityWorklogAcknowledgement(stdout, lane, key, true); !ack {
		return nil, false
	}
	var frame, data map[string]json.RawMessage
	if json.Unmarshal(stdout, &frame) != nil || json.Unmarshal(frame["data"], &data) != nil {
		return nil, false
	}
	if lane == "before" {
		return data["worklog"], true
	}
	var rows []map[string]json.RawMessage
	var entry, worklog map[string]json.RawMessage
	if json.Unmarshal(data["results"], &rows) != nil || len(rows) != 1 || json.Unmarshal(rows[0]["data"], &entry) != nil || json.Unmarshal(entry["worklog"], &worklog) != nil || worklog == nil {
		return nil, false
	}
	return worklog["fields"], true
}

func parityWorklogStoredEffect(raw []byte, started string) (string, bool) {
	var fields map[string]json.RawMessage
	var id, observedStart string
	var seconds *int
	if !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &fields) != nil || json.Unmarshal(fields["id"], &id) != nil || id == "" || strings.TrimSpace(id) != id || json.Unmarshal(fields["timeSpentSeconds"], &seconds) != nil || seconds == nil || *seconds != 120 || json.Unmarshal(fields["started"], &observedStart) != nil || !parityAuthoringJSON(fields["comment"], []byte(liveWorklogComment)) {
		return "", false
	}
	observed, err := time.Parse("2006-01-02T15:04:05.999-0700", observedStart)
	expected, wantErr := time.Parse(time.RFC3339, started)
	return id, err == nil && wantErr == nil && observed.Equal(expected)
}

func parityWorklogBlocked(stdout []byte, lane, key, started string, exit int) bool {
	if !jsontext.Value(stdout).IsValid() || exit != 3 || parityValidateFrame(stdout, lane, "worklog.add", exit) != nil {
		return false
	}
	var frame struct {
		Data     json.RawMessage
		Errors   []struct{ Code string }
		Warnings json.RawMessage
	}
	if json.Unmarshal(stdout, &frame) != nil || len(frame.Errors) != 1 || frame.Errors[0].Code != parityCommentReadOnlyCode(lane) || !parityAuthoringJSON(frame.Warnings, []byte(`[]`)) {
		return false
	}
	if lane == "before" {
		return string(frame.Data) == "null"
	}
	if lane != "after" {
		return false
	}
	instant, err := time.Parse(time.RFC3339, started)
	if err != nil {
		return false
	}
	want := map[string]any{
		"dry_run": false, "notify": false, "body_route": "adf", "workday_seconds": 28800,
		"payload": map[string]any{"timeSpentSeconds": 120, "started": instant.Format("2006-01-02T15:04:05.000-0700"), "comment": json.RawMessage(liveWorklogComment)},
		"results": []any{map[string]any{"issue": map[string]any{"key": key}, "ok": false, "error": map[string]any{
			"code": "read_only_blocked", "message": "read-only mode: write blocked: POST /rest/api/3/issue/" + key + "/worklog",
			"hint": "Read-only mode is on, so writes are refused before they reach Jira — clear it to allow this command to mutate.",
		}}},
	}
	expected, err := json.Marshal(want)
	return err == nil && parityAuthoringJSON(frame.Data, expected)
}

func TestParityActualBinaryWorklogCreation(t *testing.T) {
	const started = "2026-01-02T12:30:00Z"
	const response = `{"id":"501","timeSpentSeconds":120,"started":"2026-01-02T12:30:00.000+0000","comment":` + liveWorklogComment + `,"author":{"accountId":"native-author","emailAddress":null},"post_only":true,"future":900719925474099312345}`
	const expectedBody = `{"timeSpentSeconds":120,"started":"2026-01-02T12:30:00.000+0000","comment":` + liveWorklogComment + `}`
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		for _, mode := range []string{"json", "compact"} {
			for _, phase := range []string{"preview", "read-only", "live", "missing-id"} {
				t.Run(lane.name+"/"+mode+"/"+phase, func(t *testing.T) {
					if lane.binary == "" {
						t.Skip("requires selected actual binary")
					}
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						body, err := io.ReadAll(io.LimitReader(r.Body, 8193))
						query, queryErr := url.ParseQuery(r.URL.RawQuery)
						wantQuery := url.Values{}
						if lane.name == "after" {
							wantQuery.Set("notifyUsers", "false")
						}
						user, password, authenticated := r.BasicAuth()
						if (phase != "live" && phase != "missing-id") || err != nil || len(body) > 8192 || !parityAuthoringJSON(body, []byte(expectedBody)) || queryErr != nil || !reflect.DeepEqual(query, wantQuery) || r.Method != "POST" || r.URL.Path != "/rest/api/3/issue/PAIR-1/worklog" || r.Header.Get("Content-Type") != "application/json" || !authenticated || user != "fixture@example.invalid" || password != "synthetic-fixture-token" {
							t.Error("worklog recipe changed its exact authenticated POST/body/notification policy or entered HTTP during refusal/preview")
							http.Error(w, "wrong synthetic request", http.StatusBadRequest)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusCreated)
						reply := response
						if phase == "missing-id" {
							reply = strings.Replace(response, `"id":"501",`, "", 1)
						}
						_, _ = w.Write([]byte(reply))
					}))
					defer server.Close()
					home := t.TempDir()
					config := parityCachePublicConfig(t, home, lane.name, server.URL)
					path := filepath.Join(home, "owned-worklog.json")
					input := []byte(`{"time_spent":"2m","started":"` + started + `","comment":` + liveWorklogComment + `}`)
					if err := os.WriteFile(path, input, 0o600); err != nil {
						t.Fatal(err)
					}
					args := []string{"worklog", "add", "PAIR-1", "--json-input", path, "--parallelism=1"}
					extra := []string{"JIRA_WORKDAY_SECONDS=28800"}
					if lane.name == "after" {
						args = append(args, "--notify=false")
					}
					switch phase {
					case "preview":
						args = append(args, "--dry-run")
						extra = append(extra, "JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=")
					case "read-only":
						extra = append(extra, "JIRA_READ_ONLY=true")
					}
					if err := parityFingerprint(lane.binary, lane.digest); err != nil {
						t.Fatal(err)
					}
					result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, extra, args...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					if phase == "read-only" {
						if calls.Load() != 0 || !parityWorklogBlocked(result.Stdout, lane.name, "PAIR-1", started, result.Exit) {
							t.Fatalf("worklog read-only refusal changed its native policy: exit %d; HTTP %d; stdout %s; stderr %s", result.Exit, calls.Load(), result.Stdout, result.Stderr)
						}
						for _, mutation := range []struct{ name, original, replacement string }{
							{"wrong-code", `"` + parityCommentReadOnlyCode(lane.name) + `"`, `"unrelated"`},
							{"false-success", `"ok":false`, `"ok":true`},
						} {
							t.Run(mutation.name, func(t *testing.T) {
								changed := strings.Replace(string(result.Stdout), mutation.original, mutation.replacement, 1)
								if changed == string(result.Stdout) || parityWorklogBlocked([]byte(changed), lane.name, "PAIR-1", started, result.Exit) {
									t.Fatal("changed worklog refusal earned policy acceptance")
								}
							})
						}
					} else {
						wantCalls := int32(0)
						if phase == "live" || phase == "missing-id" {
							wantCalls = 1
						}
						if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load() != wantCalls {
							t.Fatalf("worklog creation/preview failed: exit %d; HTTP %d want %d; stdout %s; stderr %s", result.Exit, calls.Load(), wantCalls, result.Stdout, result.Stderr)
						}
						if phase == "missing-id" {
							if !parityWorklogMissingReceipt(result.Stdout, lane.name, mode, started) {
								t.Fatalf("acknowledged unusable worklog receipt lost its actual outcome: %s", result.Stdout)
							}
							for _, mutation := range []struct{ name, original, replacement string }{
								{"wrong-target", `"PAIR-1"`, `"PAIR-2"`},
								{"wrong-effort", `"timeSpentSeconds":120`, `"timeSpentSeconds":60`},
							} {
								t.Run(mutation.name, func(t *testing.T) {
									changed := strings.Replace(string(result.Stdout), mutation.original, mutation.replacement, 1)
									if changed == string(result.Stdout) || parityWorklogMissingReceipt([]byte(changed), lane.name, mode, started) {
										t.Fatal("changed incomplete worklog receipt earned acknowledgement acceptance")
									}
								})
							}
						} else {
							data := result.Stdout
							if mode == "json" {
								var err error
								data, err = json.Marshal(parityAuthoringCleanData(t, data, lane.name, "worklog.add"))
								if err != nil {
									t.Fatal(err)
								}
								if phase == "live" {
									id, ack := parityWorklogAcknowledgement(result.Stdout, lane.name, "PAIR-1", true)
									if id != "501" || !ack {
										t.Fatal("actual successful worklog creation lost bound receipt identity or acknowledgement")
									}
								}
							}
							if !parityWorklogAddData(data, lane.name, "PAIR-1", started, phase == "preview", json.RawMessage(response)) {
								t.Fatalf("worklog recipe lost exact requested fields or lane-specific 201 preservation: %s", result.Stdout)
							}
						}
					}
					id := "C072.worklogs.fixture." + phase + "." + mode
					evidence.Checks[id] = append(evidence.Checks[id], lane.name)
				})
			}
		}
	}
	saveParityLocal(t, "worklog-write-fixture", evidence)
}

func parityWorklogMissingReceipt(stdout []byte, lane, mode, started string) bool {
	if !jsontext.Value(stdout).IsValid() {
		return false
	}
	data := stdout
	if mode == "json" {
		if parityValidateFrame(stdout, lane, "worklog.add", 0) != nil {
			return false
		}
		var frame map[string]json.RawMessage
		if json.Unmarshal(stdout, &frame) != nil || !parityAuthoringJSON(frame["errors"], []byte(`[]`)) {
			return false
		}
		warnings := `[]`
		if lane == "after" {
			warnings = `[{"type":"worklog_receipt_incomplete","message":"A worklog was created but its receipt is incomplete. Inspect worklog list before retrying.","lossy":false}]`
		}
		if !parityAuthoringJSON(frame["warnings"], []byte(warnings)) {
			return false
		}
		data = frame["data"]
		id, ack := parityWorklogAcknowledgement(stdout, lane, "PAIR-1", true)
		if id != "" || ack != (lane == "after") {
			return false
		}
	}
	const receipt = `{"timeSpentSeconds":120,"started":"2026-01-02T12:30:00.000+0000","comment":` + liveWorklogComment + `}`
	if lane == "before" {
		return parityWorklogAddData(data, lane, "PAIR-1", started, false, []byte(receipt))
	}
	if lane != "after" {
		return false
	}
	const expected = `{"dry_run":false,"payload":` + receipt + `,"notify":false,"body_route":"adf","workday_seconds":28800,"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"applied":true,"receipt_error":{"code":"worklog_response_invalid","message":"worklog was created but its receipt has no valid identity: worklog response invalid: worklog identity is missing, null or blank","hint":"Jira returned a malformed worklog response. Inspect the issue's worklogs before retrying a write; a worklog may already have been created."}}}]}`
	return parityAuthoringJSON(data, []byte(expected))
}
