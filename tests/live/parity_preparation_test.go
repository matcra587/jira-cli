//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// Expected identities and wire shapes are authored here independently of the
// production preparation registry. Only the admitted current user is eligible.
func parityAssigneePayload(account string, native bool) (json.RawMessage, error) {
	if account == "" {
		return nil, fmt.Errorf("assignee recipe requires the admitted current user")
	}
	var value any = account
	if native {
		value = map[string]string{"accountId": account}
	}
	return json.Marshal(map[string]any{"fields": map[string]any{"assignee": value}})
}

func (r *liveRun) parityAssigneeInput(t *testing.T, key string, payload json.RawMessage, native bool) string {
	t.Helper()
	want, err := parityAssigneePayload(r.watcherAccountID, native)
	if err != nil || !r.verified || r.ownedLiveIssue(key) == nil || !nativeMetadataMatches(payload, want, nil) {
		t.Fatal("assignee input requires an active owned issue and exactly the admitted account payload")
	}
	path := filepath.Join(r.dir, "parity-assignee-"+output.NewRequestID()+".json")
	if err := xos.AtomicWrite(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if r.issueInputs == nil {
		r.issueInputs = make(map[string]nativeIssueInput)
	}
	r.issueInputs[path] = nativeIssueInput{key: key, digest: sha256.Sum256(payload)}
	return path
}

func parityPreparation(stdout []byte, account string) (json.RawMessage, error) {
	var frame struct {
		Data envelope.IssueInputPreparationOutput `json:"data"`
	}
	want, err := parityAssigneePayload(account, true)
	if err != nil || json.Unmarshal(stdout, &frame) != nil || !nativeMetadataMatches(frame.Data.Payload, want, nil) || frame.Data.Diagnostics == nil || len(frame.Data.Diagnostics) != 0 || len(frame.Data.Fields) != 1 || frame.Data.Fields[0] != (envelope.IssueInputPreparedField{Field: "assignee", Type: "assignee", Handling: "encoded"}) {
		return nil, fmt.Errorf("preparation differs from the independent accountId payload and handling contract")
	}
	return frame.Data.Payload, nil
}

func parityAssigneeReceipt(stdout []byte, lane, key, account string, preview bool) bool {
	var frame struct {
		Data struct {
			Issue   liveIssue       `json:"issue"`
			DryRun  *bool           `json:"dry_run"`
			Fields  json.RawMessage `json:"fields"`
			Payload json.RawMessage `json:"payload"`
			Results []liveResult    `json:"results"`
		} `json:"data"`
	}
	want, err := parityAssigneePayload(account, true)
	if err != nil || json.Unmarshal(stdout, &frame) != nil || frame.Data.DryRun == nil || *frame.Data.DryRun != preview {
		return false
	}
	if lane == "before" {
		var payload struct {
			Fields json.RawMessage `json:"fields"`
		}
		return json.Unmarshal(want, &payload) == nil && frame.Data.Issue.Key == key && nativeMetadataMatches(frame.Data.Fields, payload.Fields, nil)
	}
	return nativeMetadataMatches(frame.Data.Payload, want, nil) && len(frame.Data.Results) == 1 && frame.Data.Results[0].Issue.Key == key && frame.Data.Results[0].OK
}

func (p *parityRun) assigneeNative(t *testing.T, key string) liveIssue {
	t.Helper()
	owned := p.owner.ownedLiveIssue(key)
	if owned == nil {
		t.Fatal("assignee observation requires an active owned issue")
	}
	reply := rawRead(t, p.owner, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=summary,description,labels,project,issuetype,assignee")
	var issue liveIssue
	if json.Unmarshal(reply.Response.JSON, &issue) != nil || issue.Key != key || issue.ID != owned.ID {
		t.Fatal("assignee observation lost the bound native issue identity")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(issue.Fields, &fields) != nil || fields["assignee"] == nil {
		t.Fatal("assignee observation lacks an explicit native preimage")
	}
	return issue
}

func TestLiveParityInputPreparation(t *testing.T) {
	p := newParityRun(t)
	var initial json.RawMessage
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			key := p.create(t, lane)
			before := p.assigneeNative(t, key)
			var original struct {
				Assignee *struct {
					AccountID string `json:"accountId"`
				} `json:"assignee"`
			}
			if json.Unmarshal(before.Fields, &original) != nil || (original.Assignee != nil && original.Assignee.AccountID == "") {
				t.Fatal("assignee preimage is neither explicit null nor an identified native user")
			}
			if p.evidence.Preimages == nil {
				p.evidence.Preimages = make(map[string]json.RawMessage)
			}
			p.evidence.Preimages["N005."+lane.Name] = slices.Clone(before.Fields)
			p.save(t)
			if initial == nil {
				initial = slices.Clone(before.Fields)
			} else if !parityUnchangedFields(before.Fields, initial, "summary") {
				t.Fatal("paired assignee preimages differ; no edit admitted")
			}
			metadata := rawRead(t, p.owner, "api", "get", "/rest/api/3/issue/"+key+"/editmeta")
			var screen struct {
				Fields map[string]struct {
					Schema     struct{ Type, System string } `json:"schema"`
					Operations []string                      `json:"operations"`
				} `json:"fields"`
			}
			if json.Unmarshal(metadata.Response.JSON, &screen) != nil || screen.Fields["assignee"].Schema.Type != "user" || screen.Fields["assignee"].Schema.System != "assignee" || !slices.Contains(screen.Fields["assignee"].Operations, "set") {
				t.Fatal("fresh native edit metadata does not admit the assignee recipe")
			}
			authored, err := parityAssigneePayload(p.owner.watcherAccountID, false)
			if err != nil {
				t.Fatal(err)
			}
			path := p.owner.parityAssigneeInput(t, key, authored, false)
			if lane.Name == "after" {
				metadataPath := filepath.Join(p.owner.dir, "assignee-editmeta.json")
				if err := xos.AtomicWrite(metadataPath, metadata.Response.JSON, 0o600); err != nil {
					t.Fatal(err)
				}
				// Preparation needs neither the admitted keyring nor an API token.
				frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_KEYRING=false", "JIRA_API_TOKEN=", "DBUS_SESSION_BUS_ADDRESS=/dev/null"}, "issue", "input", "prepare", "--input", path, "--metadata", metadataPath)
				prepared, err := parityPreparation(stdout, p.owner.watcherAccountID)
				if exit != 0 || !frame.OK || err != nil {
					t.Fatal("explicit local preparation failed; no edit admitted")
				}
				path = p.owner.parityAssigneeInput(t, key, prepared, true)
			}
			args := []string{"issue", "edit", key, "--json-input", path}
			frame, stdout, exit := p.checkedMutation(t, lane, key, append(slices.Clone(args), "--dry-run")...)
			if exit != 0 || !frame.OK || !parityAssigneeReceipt(stdout, lane.Name, key, p.owner.watcherAccountID, true) {
				t.Fatal("prepared or implicit preview lost its native assignee candidate")
			}
			afterPreview := p.assigneeNative(t, key)
			if !nativeMetadataMatches(afterPreview.Fields, before.Fields, nil) {
				t.Fatal("assignee preview changed native issue state")
			}
			p.check(t, lane, "N005.assignee.preview")
			if err := p.validateMutation(lane, key, args); err != nil {
				t.Fatal(err)
			}
			blocked, _, blockedExit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, args...)
			code := "read_only_blocked"
			if lane.Name == "before" {
				code = "read_only"
			}
			if blockedExit != 3 || blocked.OK || len(blocked.Errors) != 1 || blocked.Errors[0].Code != code {
				t.Fatal("assignee write bypassed its lane-specific read-only refusal")
			}
			afterRefusal := p.assigneeNative(t, key)
			if !nativeMetadataMatches(afterRefusal.Fields, before.Fields, nil) {
				t.Fatal("read-only assignee refusal changed native issue state")
			}
			p.check(t, lane, "N005.assignee.read_only")
			frame, stdout, exit = p.checkedMutation(t, lane, key, args...)
			if exit != 0 || !frame.OK || !parityAssigneeReceipt(stdout, lane.Name, key, p.owner.watcherAccountID, false) {
				t.Fatal("assignee edit lacks its expected acknowledgement; never replay")
			}
			after := p.assigneeNative(t, key)
			var fields struct {
				Assignee struct {
					AccountID string `json:"accountId"`
				} `json:"assignee"`
			}
			if json.Unmarshal(after.Fields, &fields) != nil || fields.Assignee.AccountID != p.owner.watcherAccountID || !parityUnchangedFields(after.Fields, before.Fields, "assignee") {
				t.Fatal("prepared assignee write disagrees with independent native state or changed unrelated fields")
			}
			name := "N005.assignee.native_changed"
			if original.Assignee != nil && original.Assignee.AccountID == p.owner.watcherAccountID {
				name = "N005.assignee.native_idempotent"
			}
			p.check(t, lane, name)
		})
	}
}

func TestParityAssigneeInputGuard(t *testing.T) {
	r := &liveRun{verified: true, dir: t.TempDir(), watcherAccountID: "owned", manifest: liveManifest{Issues: []ownedIssue{{Key: "KEY-1", ID: "1"}, {Key: "KEY-2", ID: "2"}}}}
	for _, native := range []bool{false, true} {
		payload, err := parityAssigneePayload("owned", native)
		if err != nil {
			t.Fatal(err)
		}
		path := r.parityAssigneeInput(t, "KEY-1", payload, native)
		args := []string{"issue", "edit", "KEY-1", "--json-input", path}
		if err := r.checkMutation([]string{"KEY-1"}, args); err != nil {
			t.Fatal(err)
		}
		if err := r.checkMutation([]string{"KEY-2"}, []string{"issue", "edit", "KEY-2", "--json-input", path}); err == nil {
			t.Fatal("assignee input admitted another owned target")
		}
		if err := os.WriteFile(path, []byte(`{"fields":{"assignee":{"accountId":"foreign"}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := r.checkMutation([]string{"KEY-1"}, args); err == nil {
			t.Fatal("assignee input admitted changed account bytes")
		}
	}
}

func TestParityPreparationReceipt(t *testing.T) {
	const valid = `{"data":{"payload":{"fields":{"assignee":{"accountId":"owned"}}},"fields":[{"field":"assignee","type":"assignee","handling":"encoded"}],"diagnostics":[]}}`
	if _, err := parityPreparation([]byte(valid), "owned"); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		`{"data":{"payload":{"fields":{"assignee":{"accountId":"foreign"}}},"fields":[{"field":"assignee","type":"assignee","handling":"encoded"}],"diagnostics":[]}}`,
		`{"data":{"payload":{"fields":{"assignee":{"name":"owned"}}},"fields":[{"field":"assignee","type":"assignee","handling":"encoded"}],"diagnostics":[]}}`,
		`{"data":{"payload":{"fields":{"assignee":{"accountId":"owned"}}},"fields":[],"diagnostics":[]}}`,
		`{"data":{"payload":{"fields":{"assignee":{"accountId":"owned"}}},"fields":[{"field":"assignee","type":"assignee","handling":"unverified"}],"diagnostics":[]}}`,
		`{"data":{"payload":{"fields":{"assignee":{"accountId":"owned"}}},"fields":[{"field":"assignee","type":"assignee","handling":"encoded"}],"diagnostics":null}}`,
	} {
		if _, err := parityPreparation([]byte(invalid), "owned"); err == nil {
			t.Fatal("sparse or changed preparation receipt admitted")
		}
	}
	if _, err := parityPreparation([]byte(valid), ""); err == nil {
		t.Fatal("preparation receipt admitted without a verified identity")
	}
}

func TestParityActualBinaryPreparation(t *testing.T) {
	const metadata = `{"fields":{"assignee":{"required":false,"key":"assignee","name":"Assignee","schema":{"type":"user","system":"assignee"},"operations":["set"]}}}`
	const authored = `{"fields":{"assignee":"owned"}}`
	const native = `{"fields":{"assignee":{"accountId":"owned"}}}`
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			if err := parityFingerprint(lane.binary, lane.digest); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				requests = append(requests, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				if user, token, ok := r.BasicAuth(); !ok || user != "fixture@example.invalid" || token != "synthetic-fixture-token" {
					http.Error(w, "wrong synthetic binding", http.StatusUnauthorized)
					return
				}
				switch r.Method + " " + r.URL.Path {
				case "GET /rest/api/3/issue/PAIR-1/editmeta":
					_, _ = w.Write([]byte(metadata))
				case "PUT /rest/api/3/issue/PAIR-1":
					body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
					if err != nil || len(body) > 4096 || !nativeMetadataMatches(body, json.RawMessage(native), nil) {
						t.Error("actual writer changed the independently authored accountId payload")
					}
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Error("actual preparation/writer contacted an unexpected route")
					http.Error(w, "unexpected fixture route", http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			home := t.TempDir()
			path := filepath.Join(home, "authored.json")
			if err := os.WriteFile(path, []byte(authored), 0o600); err != nil {
				t.Fatal(err)
			}
			if lane.name == "after" {
				metadataPath := filepath.Join(home, "metadata.json")
				if err := os.WriteFile(metadataPath, []byte(metadata), 0o600); err != nil {
					t.Fatal(err)
				}
				frame, stdout, exit := parityFixtureRunAt(t, lane.name, lane.binary, server.URL, home, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE="}, "issue", "input", "prepare", "--input", path, "--metadata", metadataPath)
				prepared, err := parityPreparation(stdout, "owned")
				if exit != 0 || !frame.OK || err != nil || parityValidateFrame(stdout, lane.name, "issue.input.prepare", exit) != nil {
					t.Fatalf("actual local preparation failed: %v exit=%d %s", err, exit, stdout)
				}
				path = filepath.Join(home, "prepared.json")
				if err := os.WriteFile(path, prepared, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"issue", "edit", "PAIR-1", "--json-input", path}
			frame, stdout, exit := parityFixtureRunAt(t, lane.name, lane.binary, server.URL, home, nil, append(slices.Clone(args), "--dry-run")...)
			if exit != 0 || !frame.OK || parityValidateFrame(stdout, lane.name, "issue.edit", exit) != nil || !parityAssigneeReceipt(stdout, lane.name, "PAIR-1", "owned", true) {
				t.Fatalf("actual assignee preview failed: exit=%d %s", exit, stdout)
			}
			blocked, stdout, exit := parityFixtureRunAt(t, lane.name, lane.binary, server.URL, home, []string{"JIRA_READ_ONLY=true"}, args...)
			code := "read_only_blocked"
			if lane.name == "before" {
				code = "read_only"
			}
			if exit != 3 || blocked.OK || parityValidateFrame(stdout, lane.name, "issue.edit", exit) != nil || len(blocked.Errors) != 1 || blocked.Errors[0].Code != code {
				t.Fatal("actual assignee write bypassed read-only refusal")
			}
			mu.Lock()
			count := len(requests)
			mu.Unlock()
			if count != 0 {
				t.Fatal("local preparation, preview or read-only refusal contacted the fixture")
			}
			frame, stdout, exit = parityFixtureRunAt(t, lane.name, lane.binary, server.URL, home, nil, args...)
			if exit != 0 || !frame.OK || parityValidateFrame(stdout, lane.name, "issue.edit", exit) != nil || !parityAssigneeReceipt(stdout, lane.name, "PAIR-1", "owned", false) {
				t.Fatalf("actual assignee writer failed: exit=%d %s", exit, stdout)
			}
			want := []string{"PUT /rest/api/3/issue/PAIR-1"}
			if lane.name == "before" {
				want = append([]string{"GET /rest/api/3/issue/PAIR-1/editmeta"}, want...)
			}
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(requests, want) {
				t.Fatalf("actual preparation/writer route census changed: got=%v want=%v", requests, want)
			}
		})
	}
}
