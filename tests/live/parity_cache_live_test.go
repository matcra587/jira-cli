//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func parityCacheViewedIssue(stdout []byte, lane, key, id string) bool {
	if key == "" || id == "" || !jsontext.Value(stdout).IsValid() || parityValidateFrame(stdout, lane, "issue.view", 0) != nil {
		return false
	}
	// Ordinary view is a flat single-issue operation in both lanes. The keyed
	// attachment/watcher read adapter does not describe this producer.
	var frame, data, issue map[string]json.RawMessage
	var observedID, observedKey string
	return json.Unmarshal(stdout, &frame) == nil &&
		json.Unmarshal(frame["data"], &data) == nil &&
		json.Unmarshal(data["issue"], &issue) == nil &&
		json.Unmarshal(issue["id"], &observedID) == nil &&
		json.Unmarshal(issue["key"], &observedKey) == nil &&
		observedID == id && observedKey == key
}

func parityLiveHistoryPath(p *parityRun, lane parityLane) (path, identity string) {
	if lane.Name == "before" {
		profile := strings.TrimSpace(lane.Profile)
		seed := lane.Config + "\x00" + strings.TrimRight(strings.ToLower(strings.TrimSpace(p.owner.manifest.BaseURL)), "/") + "\x00" + profile
		digest := sha256.Sum256([]byte(seed))
		identity = profile + fmt.Sprintf("-%x", digest[:8])
		component := strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(identity)
		return filepath.Join(p.owner.dir, "cache-before", "jira-cli", component, "issuekeys.json"), identity
	}
	seed := fmt.Sprintf("%d:%s%d:%s", len(p.owner.manifest.BaseURL), p.owner.manifest.BaseURL, len(p.owner.account), p.owner.account)
	namespace := fmt.Sprintf("%x", sha256.Sum256([]byte(seed)))
	identity = "scope-v2:" + namespace + ":v1:issuekeys"
	return filepath.Join(p.owner.cacheDir, "jira", "scope-v2-"+namespace, fmt.Sprintf("%x.json", sha256.Sum256([]byte(identity)))), identity
}

func parityLiveHistoryStored(t *testing.T, p *parityRun, lane parityLane, want []string) time.Time {
	t.Helper()
	path, identity := parityLiveHistoryPath(p, lane)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("owned native issue history is absent or nonregular")
	}
	raw, err := os.ReadFile(path)
	if err != nil || !jsontext.Value(raw).IsValid() {
		t.Fatal("owned native issue history wrapper is malformed")
	}
	var wrapper struct {
		Profile, Resource, Key string
		Schema                 int
		FetchedAt              time.Time `json:"fetched_at"`
		Written                time.Time
		Data                   json.RawMessage
		Value                  []byte
	}
	if json.Unmarshal(raw, &wrapper) != nil {
		t.Fatal("owned native issue history wrapper cannot be decoded")
	}
	payload, written := wrapper.Value, wrapper.Written
	if lane.Name == "before" {
		if wrapper.Profile != identity || wrapper.Resource != "issuekeys" || wrapper.Schema != 1 {
			t.Fatal("legacy issue history changed its selected namespace/schema")
		}
		payload, written = wrapper.Data, wrapper.FetchedAt
	} else if wrapper.Key != identity {
		t.Fatal("current issue history changed its selected site/account namespace")
	}
	expected, err := json.Marshal(want)
	if err != nil || !parityAuthoringJSON(payload, expected) || written.IsZero() {
		t.Fatal("native persisted issue history differs from independently read Jira identity")
	}
	return written
}

func parityLiveHistoryRead(t *testing.T, p *parityRun, lane parityLane, extra, keys []string, written time.Time) {
	t.Helper()
	frame, stdout, exit := p.invoke(t, lane, extra, "cache", "issuekeys")
	if exit != 0 || !frame.OK {
		t.Fatal("native-backed local issue history failed; inspect retained receipts")
	}
	got, err := json.Marshal(parityAuthoringCleanData(t, stdout, lane.Name, "cache.issuekeys"))
	want := map[string]any{"issue_keys": keys, "count": len(keys)}
	if lane.Name == "before" {
		want["profile"] = lane.Profile
	} else {
		want["cache_state"] = "missing"
		if !written.IsZero() {
			want["cache_state"], want["written_at"] = "present", written.UTC().Format(time.RFC3339Nano)
		}
	}
	expected, wantErr := json.Marshal(want)
	if err != nil || wantErr != nil || !parityAuthoringJSON(got, expected) {
		t.Fatal("history command differs from its independently observed native cache")
	}
}

func TestLiveParityCacheHistoryAndClear(t *testing.T) {
	p := newParityRun(t)
	key := requiredEnv(t, "JIRA_LIVE_READ_KEY")
	native := rawRead(t, p.owner, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=summary")
	var issue struct{ ID, Key string }
	if json.Unmarshal(native.Response.JSON, &issue) != nil || issue.ID == "" || issue.Key != key {
		t.Fatal("independent native read did not establish the selected issue identity")
	}
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			live := []string{"JIRA_READ_ONLY=true", "JIRA_EMAIL=" + p.owner.account}
			began := time.Now()
			frame, stdout, exit := p.invoke(t, lane, live, "issue", "view", key)
			if exit != 0 || !frame.OK || !parityCacheViewedIssue(stdout, lane.Name, key, issue.ID) {
				t.Fatal("native read did not populate its selected owned issue history")
			}
			written := parityLiveHistoryStored(t, p, lane, []string{key})
			if written.Before(began) || written.After(time.Now()) {
				t.Fatal("native history has an impossible publication time")
			}
			local := []string{"JIRA_READ_ONLY=true", "JIRA_EMAIL=" + p.owner.account, "JIRA_API_TOKEN=", "JIRA_KEYRING=false"}
			root := p.owner.cacheDir
			if lane.Name == "before" {
				root = filepath.Join(p.owner.dir, "cache-before")
			}
			before := parityLocalTree(t, root)
			parityLiveHistoryRead(t, p, lane, local, []string{key}, written)
			if !reflect.DeepEqual(before, parityLocalTree(t, root)) {
				t.Fatal("native-backed local history read changed its owned cache")
			}
			p.check(t, lane, "C032.history.native-read")
			path, _ := parityLiveHistoryPath(p, lane)
			for _, phase := range []string{"preview", "clear", "noop"} {
				args := []string{"cache", "clear", "issuekeys"}
				if phase == "preview" {
					args = append(args, "--dry-run")
				} else {
					args = append(args, "--force")
				}
				frame, stdout, exit := p.invoke(t, lane, local, args...)
				if exit != 0 || !frame.OK {
					t.Fatal("native-backed local history clear failed; inspect retained receipts")
				}
				got, err := json.Marshal(parityAuthoringCleanData(t, stdout, lane.Name, "cache.clear"))
				want := map[string]any{"resource": "issuekeys", "dry_run": phase == "preview"}
				if lane.Name == "before" {
					want["profile"], want["removed"] = lane.Profile, phase != "noop"
				} else {
					count, removed := 1, 0
					entries := []map[string]any{}
					if phase == "noop" {
						count = 0
					} else {
						entries = append(entries, map[string]any{"id": filepath.Base(path), "resource": "issuekeys", "attempted": phase == "clear", "removed": phase == "clear"})
					}
					if phase == "clear" {
						removed = 1
					}
					want["would_remove"], want["removed"], want["unclassified"], want["entries"] = count, removed, 0, entries
				}
				expected, wantErr := json.Marshal(want)
				if err != nil || wantErr != nil || !parityAuthoringJSON(got, expected) {
					t.Fatal("history clear receipt contradicts its exact owned effect")
				}
				if phase == "clear" {
					relative, err := filepath.Rel(root, path)
					if err != nil {
						t.Fatal(err)
					}
					delete(before, relative)
				}
				if !reflect.DeepEqual(before, parityLocalTree(t, root)) {
					t.Fatal("history clear changed another owned cache entry or missed its acknowledgement")
				}
				p.check(t, lane, "C029.clear.native-history."+phase)
			}
			parityLiveHistoryRead(t, p, lane, local, []string{}, time.Time{})
			if !reflect.DeepEqual(before, parityLocalTree(t, root)) {
				t.Fatal("empty history read recreated cleared storage")
			}
			p.check(t, lane, "C032.history.native-cleared")
		})
	}
}

func TestParityCacheViewedIssueRejectsWrongIdentityOrLayout(t *testing.T) {
	for _, lane := range []string{"before", "after"} {
		t.Run(lane, func(t *testing.T) {
			contract := ""
			if lane == "after" {
				contract = `"contract_version":"1",`
			}
			valid := `{"ok":true,"meta":{` + contract + `"command":"issue.view","timestamp":"2026-01-01T00:00:00Z","request_id":"owned-fixture"},"data":{"issue":{"id":"10001","key":"PAIR-1"}},"errors":[],"warnings":[]}`
			if !parityCacheViewedIssue([]byte(valid), lane, "PAIR-1", "10001") {
				t.Fatal("independent flat issue view fixture refused")
			}
			for _, tc := range []struct{ name, original, replacement string }{
				{"wrong-id", `"id":"10001"`, `"id":"10002"`},
				{"wrong-key", `"key":"PAIR-1"`, `"key":"PAIR-2"`},
				{"wrong-issue-case", `"issue":`, `"Issue":`},
				{"wrong-id-case", `"id":`, `"ID":`},
				{"wrong-key-case", `"key":`, `"KEY":`},
				{"wrong-layout", `"data":{"issue":{"id":"10001","key":"PAIR-1"}}`, `"data":{"results":[{"issue":{"id":"10001","key":"PAIR-1"}}]}`},
				{"duplicate", `"id":"10001"`, `"id":"10002","id":"10001"`},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if parityCacheViewedIssue([]byte(strings.Replace(valid, tc.original, tc.replacement, 1)), lane, "PAIR-1", "10001") {
						t.Fatal("wrong native identity or borrowed keyed layout earned producer credit")
					}
				})
			}
		})
	}
}
