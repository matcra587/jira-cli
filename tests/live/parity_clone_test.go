//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func parityClonePreview(data []byte, lane, source string, fields map[string]any) bool {
	var expected map[string]any
	switch lane {
	case "before":
		expected = map[string]any{"issue": map[string]any{"key": source}, "payload": map[string]any{"fields": fields}, "dry_run": true}
	case "after":
		expected = map[string]any{"dry_run": true, "results": []any{map[string]any{"issue": map[string]any{"key": source}, "ok": true, "data": map[string]any{"applied": false}}}}
	default:
		return false
	}
	raw, err := json.Marshal(expected)
	return err == nil && parityAuthoringJSON(data, raw)
}

// A local clone preview validates overrides without reading copied fields or
// publishing history. Both layouts leave their owned public files unchanged.
func TestParityActualBinaryCloneLocal(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	lanes := []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}}
	if lanes[0].binary == "" || lanes[1].binary == "" {
		t.Skip("requires both selected actual binaries")
	}
	for _, lane := range lanes {
		if err := parityFingerprint(lane.binary, lane.digest); err != nil {
			t.Fatal(err)
		}
	}
	for _, lane := range lanes {
		for _, mode := range []string{"json", "compact"} {
			for _, input := range []string{"bare", "native-overrides"} {
				t.Run(lane.name+"/"+mode+"/"+input, func(t *testing.T) {
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusForbidden) }))
					defer server.Close()
					home := t.TempDir()
					config := parityCachePublicConfig(t, home, lane.name, server.URL)
					args := []string{"issue", "clone", "PAIR-1", "--dry-run"}
					fields := map[string]any{}
					if input == "native-overrides" {
						fields = map[string]any{"summary": "Independent clone override", "labels": []string{"clone-fixture"}}
						path := filepath.Join(home, "clone overrides.json")
						if err := os.WriteFile(path, []byte(`{"fields":{"summary":"Independent clone override","labels":["clone-fixture"]}}`), 0o600); err != nil {
							t.Fatal(err)
						}
						args = append(args, "--json-input", path)
					}
					before := parityLocalTree(t, home)
					if err := parityFingerprint(lane.binary, lane.digest); err != nil {
						t.Fatal(err)
					}
					result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_KEYRING=false"}, args...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load() != 0 {
						t.Fatalf("clone preview entered remote work or failed: exit%d HTTP%d stdout%s stderr%s", result.Exit, calls.Load(), result.Stdout, result.Stderr)
					}
					raw := result.Stdout
					if mode == "json" {
						parityAuthoringData(t, raw, lane.name, "issue.clone", 0)
						raw = parityOriginalFrameData(raw)
					}
					if !parityClonePreview(raw, lane.name, "PAIR-1", fields) {
						t.Fatal("clone preview changed authored overrides or claimed unread copied fields")
					}
					if !reflect.DeepEqual(before, parityLocalTree(t, home)) {
						t.Fatal("clone preview changed owned input or public files")
					}
					if err := parityFingerprint(lane.binary, lane.digest); err != nil {
						t.Fatal(err)
					}
					check := fmt.Sprintf("I005.clone.local_%s.%s", input, mode)
					evidence.Checks[check] = append(evidence.Checks[check], lane.name)
				})
			}
		}
	}
	saveParityLocal(t, "clone", evidence)
}

func TestParityClonePreviewRejectsInventedSourceFacts(t *testing.T) {
	t.Parallel()
	for _, sample := range []struct{ lane, valid string }{
		{"before", `{"issue":{"key":"PAIR-1"},"payload":{"fields":{}},"dry_run":true}`},
		{"after", `{"dry_run":true,"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"applied":false}}]}`},
	} {
		t.Run(sample.lane, func(t *testing.T) {
			if !parityClonePreview([]byte(sample.valid), sample.lane, "PAIR-1", map[string]any{}) {
				t.Fatal("independent valid clone preview fixture refused")
			}
			for _, invalid := range [][]byte{
				bytes.ReplaceAll([]byte(sample.valid), []byte("PAIR-1"), []byte("PAIR-2")),
				bytes.ReplaceAll([]byte(sample.valid), []byte(`"dry_run":true`), []byte(`"dry_run":false`)),
				bytes.ReplaceAll([]byte(sample.valid), []byte(`"dry_run":true`), []byte(`"dry_run":false,"dry_run":true`)),
			} {
				if parityClonePreview(invalid, sample.lane, "PAIR-1", map[string]any{}) {
					t.Fatal("clone preview oracle accepted a changed identity, preview state or duplicate member")
				}
			}
			for _, invalid := range []string{
				`{"issue":{"key":"PAIR-1"},"payload":{"fields":{}},"dry_run":true,"copied":true}`,
				`{"dry_run":true,"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"applied":true}}]}`,
				`{"dry_run":true,"results":[{"issue":{"key":"PAIR-2"},"ok":true,"data":{"applied":false}}]}`,
				`{"dry_run":false,"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"applied":false}}]}`,
			} {
				if parityClonePreview([]byte(invalid), sample.lane, "PAIR-1", map[string]any{}) {
					t.Fatal("clone preview oracle accepted an invented source or write fact")
				}
			}
		})
	}
}

type parityCloneRecipe struct {
	source, sourceID, path string
	payload                []byte
}

func (r *liveRun) checkCloneRecipe(recipe parityCloneRecipe) error {
	owned := r.ownedLiveIssue(recipe.source)
	if !r.verified || !liveKey.MatchString(recipe.source) || owned == nil || owned.ID == "" || recipe.sourceID != owned.ID {
		return errors.New("clone requires a verified active owned source")
	}
	// Input files are direct children of the owned evidence directory. Refuse
	// an indirect parent or a final symlink before reading any file contents.
	if filepath.Dir(recipe.path) != r.dir {
		return errors.New("clone overrides must be an owned direct-child file")
	}
	info, err := os.Lstat(recipe.path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("clone overrides must remain an owned regular file")
	}
	raw, err := os.ReadFile(recipe.path)
	if err != nil || !bytes.Equal(raw, recipe.payload) {
		return errors.New("clone overrides changed after registration")
	}
	return nil
}

func (p *parityRun) invokeClone(t *testing.T, lane parityLane, recipe parityCloneRecipe, step string) (liveFrame, []byte, int) {
	t.Helper()
	if err := p.owner.checkCloneRecipe(recipe); err != nil {
		t.Fatal(err)
	}
	if err := p.owner.checkCloneSource(recipe, p.native(t, recipe.source)); err != nil {
		t.Fatal(err)
	}
	args := []string{"issue", "clone", recipe.source, "--json-input", recipe.path}
	var extra []string
	switch step {
	case "preview":
		args = append(args, "--dry-run")
	case "read_only":
		extra = []string{"JIRA_READ_ONLY=true"}
	case "native":
	case "confirmation":
		if lane.Name != "before" {
			t.Fatal("legacy clone confirmation observation requires the legacy lane")
		}
	default:
		t.Fatal("clone invocation requires one authored acceptance step")
	}
	if lane.Name == "before" && step != "preview" && step != "confirmation" {
		args = append(args, "--force")
	}
	if err := p.owner.checkCloneRecipe(recipe); err != nil {
		t.Fatal(err)
	}
	return p.invoke(t, lane, extra, args...)
}

// Read only IDs/keys from the dedicated project, without relying on labels
// a faulty clone might omit. Observed baseline identities never grant ownership.
func cloneProjectInventory(t *testing.T, p *parityRun) map[string]string {
	t.Helper()
	rows := rawEpicSearch(t, p.owner, "project = "+strconv.Quote(p.owner.manifest.Project)+" ORDER BY key ASC", "key")
	result := map[string]string{}
	for _, row := range rows {
		if row.Bean.ID.Value == "" {
			t.Fatal("project census lacks an immutable native identity")
		}
		result[row.Key] = row.Bean.ID.Value
	}
	return result
}

// Repeat only reads while indexing converges. Unexpected project identities
// refuse the comparison and never become cleanup targets.
func waitCloneInventory(t *testing.T, p *parityRun, want map[string]string) {
	t.Helper()
	stable := 0
	for range 20 {
		seen := cloneProjectInventory(t, p)
		for key, id := range seen {
			if want[key] != id {
				t.Fatal("clone project census changed unexpectedly; never infer cleanup ownership")
			}
		}
		if reflect.DeepEqual(seen, want) {
			stable++
			if stable == 2 {
				return
			}
		} else {
			stable = 0
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-t.Context().Done():
			timer.Stop()
			t.Fatal("clone inventory verification canceled")
		case <-timer.C:
		}
	}
	t.Fatal("clone census never established the complete expected project set")
}

func (r *liveRun) checkCloneSource(recipe parityCloneRecipe, observed liveIssue) error {
	owned := r.ownedLiveIssue(recipe.source)
	if owned == nil || owned.ID == "" || recipe.sourceID != owned.ID || observed.Key != recipe.source || observed.ID != owned.ID {
		return errors.New("clone source changed its bound native identity")
	}
	return nil
}

func parityCloneLabels(actual, expected []string) bool {
	actual, expected = slices.Clone(actual), slices.Clone(expected)
	slices.Sort(actual)
	slices.Sort(expected)
	return slices.Equal(actual, expected)
}

func parityCloneRefusal(data []byte, lane, source string) bool {
	if lane == "before" {
		return parityAuthoringJSON(data, []byte(`null`))
	}
	if lane != "after" {
		return false
	}
	expected := map[string]any{"dry_run": false, "results": []any{map[string]any{"issue": map[string]any{"key": source}, "ok": false, "error": map[string]any{"code": "read_only_blocked", "message": "read-only mode: write blocked: POST /rest/api/3/issue", "hint": "Read-only mode is on, so writes are refused before they reach Jira — clear it to allow this command to mutate."}}}}
	raw, err := json.Marshal(expected)
	return err == nil && parityAuthoringJSON(data, raw)
}

type parityCloneObservedFields struct {
	Summary     string                    `json:"summary"`
	Description json.RawMessage           `json:"description"`
	Labels      []string                  `json:"labels"`
	Project     struct{ ID, Key string }  `json:"project"`
	IssueType   struct{ ID, Name string } `json:"issuetype"`
}

// This fixture requests no native create followups. Its independently read
// minimal identity must agree with the entire corresponding public receipt.
func parityCloneAcknowledged(data []byte, lane, source string, clone liveIssue, fields map[string]any) bool {
	if clone.ID == "" || clone.Key == "" || clone.Self == "" {
		return false
	}
	identity := map[string]any{"id": clone.ID, "key": clone.Key, "self": clone.Self}
	var expected map[string]any
	switch lane {
	case "before":
		expected = map[string]any{"issue": map[string]any{"key": source}, "payload": map[string]any{"fields": fields}, "result": identity, "dry_run": false}
	case "after":
		expected = map[string]any{"dry_run": false, "results": []any{map[string]any{"issue": map[string]any{"key": source}, "ok": true, "data": map[string]any{"applied": true, "clone": identity, "receipt": identity}}}}
	default:
		return false
	}
	raw, err := json.Marshal(expected)
	return err == nil && parityAuthoringJSON(data, raw)
}

func parityCloneIdentity(stdout []byte, lane string) liveIssue {
	var reply struct {
		Data struct {
			Result  liveIssue
			Results []struct {
				Data *struct{ Clone liveIssue }
			}
		}
	}
	if json.Unmarshal(stdout, &reply) != nil {
		return liveIssue{}
	}
	if lane == "before" {
		return reply.Data.Result
	}
	if lane == "after" && len(reply.Data.Results) == 1 && reply.Data.Results[0].Data != nil {
		return reply.Data.Results[0].Data.Clone
	}
	return liveIssue{}
}

func TestLiveParityClone(t *testing.T) {
	p := newParityRun(t)
	inventory := cloneProjectInventory(t, p)
	sources := []string{p.create(t, p.evidence.Lanes[0]), p.create(t, p.evidence.Lanes[1])}
	for _, key := range sources {
		owned := p.owner.ownedLiveIssue(key)
		inventory[key] = owned.ID
	}
	waitCloneInventory(t, p, inventory)
	for i, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			source := p.native(t, sources[i])
			owned := p.owner.ownedLiveIssue(source.Key)
			if owned == nil || source.ID != owned.ID {
				t.Fatal("fresh clone source changed its owned native identity")
			}
			var sourceFields parityCloneObservedFields
			if json.Unmarshal(source.Fields, &sourceFields) != nil || sourceFields.Project.ID != p.evidence.ProjectID || sourceFields.Project.Key != p.owner.manifest.Project || sourceFields.IssueType.ID != p.evidence.IssueTypeID || sourceFields.IssueType.Name != p.owner.issueType || !slices.Contains(sourceFields.Labels, p.owner.manifest.RunID) || !nativeMetadataMatches(sourceFields.Description, json.RawMessage(parityDescription), nil) {
				t.Fatal("clone source lacks complete observed fields")
			}
			summary := p.owner.manifest.RunID + " clone " + lane.Name
			labels := []string{p.owner.manifest.RunID, p.owner.manifest.RunID + "-clone-" + lane.Name}
			fields := map[string]any{"summary": summary, "labels": labels}
			payload, err := json.Marshal(map[string]any{"fields": fields})
			if err != nil {
				t.Fatal(err)
			}
			recipe := parityCloneRecipe{source: source.Key, sourceID: source.ID, path: filepath.Join(p.owner.dir, "clone-"+lane.Name+".json"), payload: slices.Clone(payload)}
			if err := os.WriteFile(recipe.path, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			for _, step := range []string{"preview", "confirmation", "read_only", "native"} {
				if step == "confirmation" && lane.Name != "before" {
					continue
				}
				attempt := -1
				if step == "native" {
					if err := p.owner.checkCloneRecipe(recipe); err != nil {
						t.Fatal(err)
					}
					p.owner.manifest.Attempts = append(p.owner.manifest.Attempts, summary)
					p.owner.save(t)
					attempt = len(p.evidence.Creations)
					p.evidence.Creations = append(p.evidence.Creations, parityCreation{Lane: lane.Name, Summary: summary, Intent: true})
					p.save(t)
				}
				frame, stdout, exit := p.invokeClone(t, lane, recipe, step)
				switch step {
				case "preview":
					if exit != 0 || !frame.OK || !parityClonePreview(parityOriginalFrameData(stdout), lane.Name, source.Key, fields) {
						t.Fatal("clone preview changed authored context or claimed copied source fields")
					}
				case "confirmation":
					if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "validation_failed" || !parityCloneRefusal(parityOriginalFrameData(stdout), lane.Name, source.Key) {
						t.Fatal("legacy headless clone bypassed explicit confirmation")
					}
				case "read_only":
					if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != parityCommentReadOnlyCode(lane.Name) || !parityCloneRefusal(parityOriginalFrameData(stdout), lane.Name, source.Key) {
						t.Fatal("clone read-only refusal changed classification or invented success")
					}
				case "native":
					identity := parityCloneIdentity(stdout, lane.Name)
					p.evidence.Creations[attempt].Identity = identity
					p.save(t)
					if identity.Key == "" {
						t.Fatal("clone outcome unresolved: no returned identity; never replay")
					}
					clone := p.native(t, identity.Key)
					var observed parityCloneObservedFields
					if json.Unmarshal(clone.Fields, &observed) != nil || observed.Summary != summary || observed.Project != sourceFields.Project || observed.IssueType != sourceFields.IssueType || !slices.Contains(observed.Labels, p.owner.manifest.RunID) || identity.ID != "" && identity.ID != clone.ID {
						t.Fatal("clone cannot be bound to exact native run/source/project/type markers; retain unresolved receipt")
					}
					p.owner.register(t, clone)
					inventory[clone.Key] = clone.ID
					p.evidence.Creations[attempt].Identity, p.evidence.Creations[attempt].Bound = clone, true
					p.save(t)
					if exit != 0 || !frame.OK || !parityCloneAcknowledged(parityOriginalFrameData(stdout), lane.Name, source.Key, clone, fields) || !parityCloneLabels(observed.Labels, labels) || !nativeMetadataMatches(observed.Description, sourceFields.Description, nil) {
						t.Fatal("bound clone lost acknowledgement, identity or copied rich description; cleanup retained")
					}
				}
				fresh := p.native(t, source.Key)
				if p.owner.checkCloneSource(recipe, fresh) != nil || !nativeMetadataMatches(fresh.Fields, source.Fields, nil) {
					t.Fatal("clone operation changed its source fields")
				}
				waitCloneInventory(t, p, inventory)
				p.check(t, lane, "I005.clone."+step)
			}
		})
		if t.Failed() {
			break
		}
	}
}

func TestLiveCloneRecipeGuard(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"valid", "unverified", "foreign", "inactive", "changed", "external", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			r := &liveRun{verified: true, dir: t.TempDir(), manifest: liveManifest{Issues: []ownedIssue{{Key: "KEY-1", ID: "1"}, {Key: "KEY-3", ID: "3", Deleted: true}}}}
			recipe := parityCloneRecipe{source: "KEY-1", sourceID: "1", path: filepath.Join(r.dir, "overrides.json"), payload: []byte(`{"fields":{"summary":"Owned fixture"}}`)}
			if err := os.WriteFile(recipe.path, recipe.payload, 0o600); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "unverified":
				r.verified = false
			case "foreign":
				recipe.source = "KEY-777"
			case "inactive":
				recipe.source = "KEY-3"
			case "changed":
				if err := os.WriteFile(recipe.path, []byte(`{"fields":{"summary":"Changed"}}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "external":
				recipe.path = filepath.Join(t.TempDir(), "not-owned.json")
			case "symlink":
				link := filepath.Join(r.dir, "link.json")
				if err := os.Symlink(recipe.path, link); err != nil {
					if runtime.GOOS == "windows" || os.IsPermission(err) {
						t.Skipf("native symlink creation is unavailable: %v", err)
					}
					t.Fatal(err)
				}
				recipe.path = link
			}
			if err := r.checkCloneRecipe(recipe); (err == nil) != (scenario == "valid") {
				t.Fatal("clone recipe guard admitted an unverified, unowned, indirect or changed input")
			}
		})
	}
}

func TestParityCloneReceiptGuards(t *testing.T) {
	t.Parallel()
	clone := liveIssue{ID: "102", Key: "PAIR-2", Self: "https://example.invalid/rest/api/3/issue/102"}
	fields := map[string]any{"summary": "Clone", "labels": []string{"owned"}}
	for _, sample := range []struct{ lane, valid string }{
		{"before", `{"dry_run":false,"issue":{"key":"PAIR-1"},"result":{"id":"102","key":"PAIR-2","self":"https://example.invalid/rest/api/3/issue/102"},"payload":{"fields":{"summary":"Clone","labels":["owned"]}}}`},
		{"after", `{"dry_run":false,"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"applied":true,"clone":{"id":"102","key":"PAIR-2","self":"https://example.invalid/rest/api/3/issue/102"},"receipt":{"id":"102","key":"PAIR-2","self":"https://example.invalid/rest/api/3/issue/102"}}}]}`},
	} {
		t.Run(sample.lane, func(t *testing.T) {
			if !parityCloneAcknowledged([]byte(sample.valid), sample.lane, "PAIR-1", clone, fields) {
				t.Fatal("valid independent clone acknowledgement fixture refused")
			}
			for name, invalid := range map[string][]byte{
				"foreign-source":  bytes.ReplaceAll([]byte(sample.valid), []byte("PAIR-1"), []byte("PAIR-3")),
				"foreign-clone":   bytes.ReplaceAll([]byte(sample.valid), []byte("PAIR-2"), []byte("PAIR-3")),
				"missing-preview": bytes.ReplaceAll([]byte(sample.valid), []byte(`"dry_run":false,`), nil),
				"preview":         bytes.ReplaceAll([]byte(sample.valid), []byte(`"dry_run":false`), []byte(`"dry_run":true`)),
				"duplicate":       bytes.ReplaceAll([]byte(sample.valid), []byte(`"dry_run":false`), []byte(`"dry_run":true,"dry_run":false`)),
				"trailing":        append([]byte(sample.valid), []byte(`{}`)...),
			} {
				t.Run(name, func(t *testing.T) {
					if parityCloneAcknowledged(invalid, sample.lane, "PAIR-1", clone, fields) {
						t.Fatal("clone oracle accepted changed, missing or ambiguous native facts")
					}
				})
			}
			if sample.lane == "after" {
				for name, invalid := range map[string][]byte{
					"not-applied":     bytes.ReplaceAll([]byte(sample.valid), []byte(`"applied":true`), []byte(`"applied":false`)),
					"failed-row":      bytes.ReplaceAll([]byte(sample.valid), []byte(`"ok":true`), []byte(`"ok":false`)),
					"missing-receipt": bytes.ReplaceAll([]byte(sample.valid), []byte(`"receipt":`), []byte(`"missing_receipt":`)),
				} {
					t.Run(name, func(t *testing.T) {
						if parityCloneAcknowledged(invalid, sample.lane, "PAIR-1", clone, fields) {
							t.Fatal("clone oracle fabricated an acknowledged row or native receipt")
						}
					})
				}
			}
		})
	}
}

func TestParityActualBinaryCloneReadOnly(t *testing.T) {
	lanes := []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}}
	if lanes[0].binary == "" || lanes[1].binary == "" {
		t.Skip("requires both selected actual binaries")
	}
	for _, lane := range lanes {
		if err := parityFingerprint(lane.binary, lane.digest); err != nil {
			t.Fatal(err)
		}
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range lanes {
		t.Run(lane.name, func(t *testing.T) {
			var gets, posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts.Add(1)
					w.WriteHeader(http.StatusForbidden)
					return
				}
				user, token, ok := r.BasicAuth()
				if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issue/PAIR-1" || !ok || user != "fixture@example.invalid" || token != "synthetic-fixture-token" {
					t.Error("clone refusal fixture received an unexpected authenticated read")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				gets.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"101","key":"PAIR-1","fields":{"summary":"Source","project":{"id":"10","key":"PAIR"},"issuetype":{"id":"20","name":"Task"},"labels":["owned"],"description":null}}`))
			}))
			defer server.Close()
			home := t.TempDir()
			config := parityCachePublicConfig(t, home, lane.name, server.URL)
			args := []string{"issue", "clone", "PAIR-1"}
			if lane.name == "before" {
				args = append(args, "--force")
			}
			before := parityLocalTree(t, home)
			began := time.Now().UTC().Truncate(time.Second)
			result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config}, []string{"JIRA_READ_ONLY=true"}, args...)
			evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
			parityAuthoringData(t, result.Stdout, lane.name, "issue.clone", 3)
			var frame liveFrame
			if result.Exit != 3 || gets.Load() != 1 || posts.Load() != 0 || json.Unmarshal(result.Stdout, &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != parityCommentReadOnlyCode(lane.name) || !parityCloneRefusal(parityOriginalFrameData(result.Stdout), lane.name, "PAIR-1") {
				t.Fatalf("clone read-only guard entered a write or lost its exact refusal: exit%d GET%d POST%d stdout%s stderr%s", result.Exit, gets.Load(), posts.Load(), result.Stdout, result.Stderr)
			}
			if lane.name == "after" {
				// The authenticated source read remains useful completion history.
				path := parityOpenHistory(t, home, lane.name, server.URL, "PAIR-1", began)
				parityOpenOnlyHistory(t, home, path, before)
			} else if !reflect.DeepEqual(before, parityLocalTree(t, home)) {
				t.Fatal("legacy clone refusal changed owned public files")
			}
			if err := parityFingerprint(lane.binary, lane.digest); err != nil {
				t.Fatal(err)
			}
			check := "I005.clone.read_only_no_post"
			evidence.Checks[check] = append(evidence.Checks[check], lane.name)
		})
	}
	saveParityLocal(t, "clone-refusal-fixture", evidence)
}

func TestParityCloneRefusalRejectsInventedWrites(t *testing.T) {
	t.Parallel()
	if !parityCloneRefusal([]byte(`null`), "before", "PAIR-1") || parityCloneRefusal([]byte(`{}`), "before", "PAIR-1") {
		t.Fatal("legacy clone refusal changed its null data boundary")
	}
	for name, raw := range map[string]string{
		"sparse":  `{"dry_run":false,"results":[{"issue":{"key":"PAIR-1"},"ok":false}]}`,
		"foreign": `{"dry_run":false,"results":[{"issue":{"key":"PAIR-2"},"ok":false,"error":{"code":"read_only_blocked"}}]}`,
		"applied": `{"dry_run":false,"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"applied":true,"clone":{"id":"102","key":"PAIR-2"}}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if parityCloneRefusal([]byte(raw), "after", "PAIR-1") {
				t.Fatal("clone refusal oracle accepted missing attribution or an acknowledged clone")
			}
		})
	}
}

func TestLiveCloneSourceIdentityAndLabelGuards(t *testing.T) {
	t.Parallel()
	r := &liveRun{manifest: liveManifest{Issues: []ownedIssue{{Key: "KEY-1", ID: "1"}}}}
	recipe := parityCloneRecipe{source: "KEY-1", sourceID: "1"}
	if err := r.checkCloneSource(recipe, liveIssue{Key: "KEY-1", ID: "1"}); err != nil {
		t.Fatal("bound native source refused")
	}
	for name, observed := range map[string]liveIssue{
		"changed-native-ID": {Key: "KEY-1", ID: "2"},
		"missing-ID":        {Key: "KEY-1"},
		"foreign-key":       {Key: "KEY-2", ID: "1"},
	} {
		t.Run(name, func(t *testing.T) {
			if r.checkCloneSource(recipe, observed) == nil {
				t.Fatal("clone admitted an issue that lost its bound native identity")
			}
		})
	}
	if !parityCloneLabels([]string{"clone", "run"}, []string{"run", "clone"}) {
		t.Fatal("native unordered label membership refused")
	}
	for _, labels := range [][]string{{"run"}, {"run", "clone", "extra"}, {"run", "run"}} {
		if parityCloneLabels(labels, []string{"run", "clone"}) {
			t.Fatal("clone labels lost or gained authored membership")
		}
	}
}
