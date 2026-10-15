//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParityMutationAdmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binary")
	const fixture = "independent binary admission fixture"
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	lane := parityLane{Name: "before", Binary: path, Digest: fmt.Sprintf("%x", sha256.Sum256([]byte(fixture)))}
	p := &parityRun{owner: &liveRun{verified: true, manifest: liveManifest{Issues: []ownedIssue{{Key: "PAIR-1", ID: "10001"}}}}, evidence: parityEvidence{Lanes: []parityLane{lane}}}
	args := []string{"issue", "delete", "PAIR-1", "--force"}
	if err := p.validateMutation(lane, "PAIR-1", args); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key  string
		args []string
	}{{"PAIR-2", args}, {"PAIR-1", []string{"issue", "delete", "PAIR-2", "--force"}}, {"PAIR-1", []string{"issue", "clone", "PAIR-1"}}} {
		if p.validateMutation(lane, tc.key, tc.args) == nil {
			t.Fatal("foreign target or unsupported recipe admitted")
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if p.validateMutation(lane, "PAIR-1", args) == nil || len(p.owner.manifest.PendingDeletes) != 0 {
		t.Fatal("local executable refusal acquired a pending deletion")
	}
}

func TestParityWriteTarget(t *testing.T) {
	for _, lane := range []string{"before", "after"} {
		var frame liveFrame
		if parityWriteTarget(frame, lane, "PAIR-1") {
			t.Fatal("empty write target receipt admitted")
		}
		if lane == "before" {
			frame.Data.Issue.Key = "PAIR-1"
		} else {
			frame.Data.Results = []liveResult{{OK: true, Issue: liveIssue{Key: "PAIR-1"}}}
		}
		if !parityWriteTarget(frame, lane, "PAIR-1") || parityWriteTarget(frame, lane, "PAIR-2") {
			t.Fatal("write target binding changed")
		}
	}
}

func TestParityLostDeleteAcknowledgement(t *testing.T) {
	r := &liveRun{manifest: liveManifest{Issues: []ownedIssue{{Key: "PAIR-1", ID: "10001"}}, PendingDeletes: []string{"PAIR-1"}}}
	if r.confirmParityDeletion("PAIR-1", liveFrame{OK: true}, 0) || r.manifest.Issues[0].Deleted || len(r.manifest.PendingDeletes) != 1 {
		t.Fatal("present or unresolved issue lost deletion intent")
	}
	var missing liveFrame
	if err := json.Unmarshal([]byte(`{"ok":false,"errors":[{"code":"jira_not_found"}]}`), &missing); err != nil {
		t.Fatal(err)
	}
	if !r.confirmParityDeletion("PAIR-1", missing, 2) || !r.manifest.Issues[0].Deleted || len(r.manifest.PendingDeletes) != 0 {
		t.Fatal("independent absence after lost delete acknowledgement did not stop cleanup replay")
	}
}

func TestParityLegacyEnvironment(t *testing.T) {
	input := []string{
		"PATH=/fixture/bin", "DBUS_SESSION_BUS_ADDRESS=fixture", "JIRA_KEYRING_SERVICE=owned",
		"JIRA_DEFAULT_PROFILE=foreign", "JIRA_PROFILE_OWNED_BASE_URL=https://foreign.invalid",
		"jira_profile_owned_email=other@example.invalid", "JIRA_TOKEN_OWNED=synthetic", "JIRA_TEST_CREDENTIAL_STORE_DIR=/foreign",
	}
	got := parityLegacyEnvironment(input)
	if !slices.Equal(got, input[:3]) {
		t.Fatal("legacy overlays survived, or native keyring transport was removed")
	}
}

func TestParityCreateAcknowledgement(t *testing.T) {
	issue := liveIssue{Key: "PAIR-1", ID: "10001"}
	var frame liveFrame
	frame.Data.Attempted, frame.Data.Applied = true, true
	frame.Data.Receipt = json.RawMessage(`{"key":"PAIR-1","id":"10001"}`)
	if !parityCurrentCreateAcknowledged(frame, issue) {
		t.Fatal("valid independent acknowledgement refused")
	}
	for _, name := range []string{"attempted", "applied", "receipt", "identity"} {
		t.Run(name, func(t *testing.T) {
			broken := frame
			switch name {
			case "attempted":
				broken.Data.Attempted = false
			case "applied":
				broken.Data.Applied = false
			case "receipt":
				broken.Data.Receipt = nil
			case "identity":
				broken.Data.Receipt = json.RawMessage(`{"key":"PAIR-2","id":"10002"}`)
			}
			if parityCurrentCreateAcknowledged(broken, issue) {
				t.Fatal("missing or mismatched acknowledgement admitted")
			}
		})
	}
}

func TestParityNativeOracles(t *testing.T) {
	fields := json.RawMessage(`{"summary":"authored","description":` + parityDescription + `}`)
	issue := liveIssue{Key: "PAIR-1", ID: "10001", Fields: fields}
	if !parityViewMatches(issue, "10001", "PAIR-1", "authored") {
		t.Fatal("valid independent view refused")
	}
	for _, missing := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`null`), json.RawMessage(`{"summary":"authored"}`)} {
		issue.Fields = missing
		if parityViewMatches(issue, "10001", "PAIR-1", "authored") {
			t.Fatal("sparse view passed with stale native fields")
		}
	}
	issue.Fields = json.RawMessage(strings.Replace(string(fields), `,"marks":[{"type":"strong"}]`, "", 1))
	if parityViewMatches(issue, "10001", "PAIR-1", "authored") {
		t.Fatal("lost rich mark passed the view oracle")
	}
	original := json.RawMessage(`{"summary":"before","labels":["owned"],"project":{"id":"10"}}`)
	for _, changed := range []json.RawMessage{json.RawMessage(`{"summary":"after","labels":[],"project":{"id":"10"}}`), json.RawMessage(`{"summary":"after","labels":["owned"]}`)} {
		if parityUnchangedFields(changed, original, "summary") {
			t.Fatal("unrelated native field loss passed")
		}
	}
	if !parityUnchangedFields(json.RawMessage(`{"summary":"after","labels":["owned"],"project":{"id":"10"}}`), original, "summary") {
		t.Fatal("authored summary-only change refused")
	}
}

func TestParityEnvelope(t *testing.T) {
	valid := `{"ok":true,"meta":{"command":"issue.view","contract_version":"1","timestamp":"2026-10-15T22:00:00Z","request_id":"independent"},"data":{},"errors":[],"warnings":[]}`
	if err := parityValidateFrame([]byte(valid), "after", "issue.view", 0); err != nil {
		t.Fatal(err)
	}
	for _, broken := range []string{strings.Replace(valid, `"issue.view"`, `"issue.edit"`, 1), strings.Replace(valid, `"contract_version":"1"`, `"contract_version":"2"`, 1), strings.Replace(valid, `"ok":true`, `"ok":false`, 1), strings.Replace(valid, `"errors":[]`, `"errors":[{"code":"unexpected"}]`, 1), `{}`, valid + `{}`} {
		if parityValidateFrame([]byte(broken), "after", "issue.view", 0) == nil {
			t.Fatal("malformed or mismatched envelope admitted")
		}
	}
	for _, member := range []string{"data", "errors", "warnings"} {
		var original map[string]json.RawMessage
		if err := json.Unmarshal([]byte(valid), &original); err != nil {
			t.Fatal(err)
		}
		original[member] = json.RawMessage(`null`)
		broken, err := json.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		if parityValidateFrame(broken, "after", "issue.view", 0) == nil {
			t.Fatal("null required envelope member admitted")
		}
		delete(original, member)
		broken, err = json.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		if parityValidateFrame(broken, "after", "issue.view", 0) == nil {
			t.Fatal("missing required envelope member admitted")
		}
	}
}
