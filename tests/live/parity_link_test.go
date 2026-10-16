//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

type parityLinkAttempt struct {
	Lane        string   `json:"lane"`
	Source      string   `json:"key"`
	Target      string   `json:"target"`
	TypeID      string   `json:"type_id"`
	ObservedIDs []string `json:"observed_ids,omitempty"`
	NativeID    string   `json:"observed_id,omitempty"`
	Bound       bool     `json:"bound"`
}

func (p *parityRun) validateLinkMutation(lane parityLane, keys, args []string) error {
	if !slices.Contains(p.evidence.Lanes, lane) || len(keys) != 2 || keys[0] == keys[1] || p.owner.ownedLiveIssue(keys[0]) == nil || p.owner.ownedLiveIssue(keys[1]) == nil {
		return fmt.Errorf("link mutation requires an admitted lane and two distinct active owned ends")
	}
	if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
		return err
	}
	if lane.Name == "before" {
		if len(args) < 3 || args[0] != "issue" || args[1] != "link" {
			return fmt.Errorf("legacy link mutation requires exact grammar")
		}
		if args[2] == "delete" {
			if len(args) < 5 || len(args) > 6 || args[3] != keys[0] {
				return fmt.Errorf("legacy link deletion requires its exact owned source and ID")
			}
			translated := []string{"issue", "link", "delete", args[4]}
			if len(args) == 6 {
				switch args[5] {
				case "--dry-run":
					translated = append(translated, args[5])
				case "--force":
				default:
					return fmt.Errorf("legacy link deletion has an unauthored option")
				}
			}
			args = translated
		} else {
			if args[2] != keys[0] {
				return fmt.Errorf("legacy bare link creation requires its exact owned source")
			}
			for i := 3; i < len(args); i++ {
				flag, _, inline := strings.Cut(args[i], "=")
				switch flag {
				case "--dry-run", "--parallelism":
				case "--to", "--type":
					if !inline {
						i++
						if i == len(args) {
							return fmt.Errorf("legacy link option lacks a value")
						}
					}
				default:
					return fmt.Errorf("legacy link creation has an unauthored option")
				}
			}
			args = append([]string{"issue", "link", "create"}, args[2:]...)
		}
	} else if lane.Name != "after" {
		return fmt.Errorf("unsupported link lane")
	}
	return p.owner.checkLinkMutation(keys, args)
}

func (p *parityRun) invokeLink(t *testing.T, lane parityLane, keys, extra []string, args ...string) (liveFrame, []byte, int) {
	t.Helper()
	if err := p.validateLinkMutation(lane, keys, args); err != nil {
		t.Fatal(err)
	}
	return p.invoke(t, lane, extra, args...)
}

func parityLinkCreateData(data []byte, lane, source, target, input string, typ storedLinkType, preview, resolved, refused bool) bool {
	var expected map[string]any
	switch lane {
	case "before":
		if refused {
			return parityAuthoringJSON(data, []byte(`null`))
		}
		expected = map[string]any{"inward_issue": map[string]any{"key": source}, "outward_issue": map[string]any{"key": target}, "type": input, "dry_run": preview}
		if resolved {
			expected["preview"] = map[string]any{"inward_issue_sentence": source + " " + typ.Outward + " " + target, "outward_issue_sentence": target + " " + typ.Inward + " " + source}
		}
	case "after":
		row := map[string]any{"issue": map[string]any{"key": source}, "ok": !refused}
		if refused {
			row["error"] = map[string]any{"code": "read_only_blocked", "message": "read-only mode: write blocked: POST /rest/api/3/issueLink", "hint": "Read-only mode is on, so writes are refused before they reach Jira — clear it to allow this command to mutate."}
		} else {
			row["data"] = map[string]any{}
		}
		expected = map[string]any{"dry_run": preview, "to": map[string]any{"key": target}, "type_input": input, "type_resolved": resolved, "results": []any{row}}
		if resolved {
			expected["type"] = map[string]any{"id": typ.ID, "name": typ.Name, "inward": typ.Inward, "outward": typ.Outward}
			expected["relation"] = typ.Outward
		}
	default:
		return false
	}
	raw, err := json.Marshal(expected)
	return err == nil && parityAuthoringJSON(data, raw)
}

func parityLinkDeleteData(data []byte, lane, source, id string, preview bool) bool {
	expected := map[string]any{"link_id": id, "dry_run": preview}
	switch lane {
	case "before":
		expected["issue"] = map[string]any{"key": source}
		if !preview {
			expected["deleted"] = true
		}
	case "after":
	default:
		return false
	}
	raw, err := json.Marshal(expected)
	return err == nil && parityAuthoringJSON(data, raw)
}

func TestLiveParityLinks(t *testing.T) {
	p := newParityRun(t)
	var typ storedLinkType
	for _, candidate := range p.owner.loadLinkTypes(t) {
		if candidate.Inward != candidate.Outward && p.owner.linkTypes[candidate.Name].ID == candidate.ID && p.owner.linkTypes[candidate.Outward].ID == candidate.ID {
			typ = candidate
			break
		}
	}
	if typ.ID == "" {
		t.Fatal("link comparison requires an unambiguous asymmetric native type")
	}
	// Both ends are dedicated owned fixtures. Reverse their logical roles for
	// the second lane after independently confirming the first link is absent.
	keys := []string{p.create(t, p.evidence.Lanes[0]), p.create(t, p.evidence.Lanes[1])}
	for i, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			source, target := keys[i], keys[1-i]
			ends := []string{source, target}
			original := []liveIssue{p.native(t, source), p.native(t, target)}
			empty := func() {
				t.Helper()
				for _, key := range ends {
					if len(rawIssueLinks(t, p.owner, key)) != 0 {
						t.Fatal("owned pair retains an unexpected relationship; refuse dependent writes")
					}
				}
			}
			empty()
			args := []string{"issue", "link", "create", source, "--to", target, "--type", typ.Name, "--parallelism=1"}
			if lane.Name == "before" {
				args = append([]string{"issue", "link"}, args[3:]...)
			}
			frame, stdout, exit := p.invokeLink(t, lane, ends, nil, append(slices.Clone(args), "--dry-run")...)
			if exit != 0 || !frame.OK || !parityLinkCreateData(parityOriginalFrameData(stdout), lane.Name, source, target, typ.Name, typ, true, false, false) {
				t.Fatal("link preview lost authored ends or invented a resolved relationship")
			}
			empty()
			p.check(t, lane, "I014.link.preview")
			frame, stdout, exit = p.invokeLink(t, lane, ends, []string{"JIRA_READ_ONLY=true"}, args...)
			if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != parityCommentReadOnlyCode(lane.Name) || !parityLinkCreateData(parityOriginalFrameData(stdout), lane.Name, source, target, typ.Name, typ, false, true, true) {
				t.Fatal("link refusal changed classification or invented an acknowledged relationship")
			}
			empty()
			p.check(t, lane, "I014.link.read_only")

			attempt := len(p.evidence.Links)
			p.evidence.Links = append(p.evidence.Links, parityLinkAttempt{Lane: lane.Name, Source: source, Target: target, TypeID: typ.ID})
			p.save(t)
			frame, stdout, exit = p.invokeLink(t, lane, ends, nil, args...)
			stored := rawIssueLinks(t, p.owner, source)
			for _, link := range stored {
				p.evidence.Links[attempt].ObservedIDs = append(p.evidence.Links[attempt].ObservedIDs, link.ID)
			}
			p.save(t)
			if len(stored) != 1 || stored[0].Outward == nil || stored[0].Inward != nil || stored[0].Outward.Key != target || stored[0].Type != typ {
				t.Fatal("link outcome cannot be uniquely bound to its authored direction; never replay")
			}
			id := p.owner.registerPairLink(t, source, target, typ, false)
			p.evidence.Links[attempt].NativeID, p.evidence.Links[attempt].Bound = id, true
			p.save(t)
			if exit != 0 || !frame.OK || !parityLinkCreateData(parityOriginalFrameData(stdout), lane.Name, source, target, typ.Name, typ, false, true, false) {
				t.Fatal("link creation has no exact acknowledgement; owned cleanup retained, never replay")
			}
			mirror := rawIssueLinks(t, p.owner, target)
			if len(mirror) != 1 || mirror[0].ID != id || mirror[0].Inward == nil || mirror[0].Outward != nil || mirror[0].Inward.Key != source || mirror[0].Type != typ {
				t.Fatal("native target does not report the exact inverse relationship")
			}
			p.check(t, lane, "I014.link.native_direction")
			unchanged := func() {
				t.Helper()
				if !reflect.DeepEqual(rawIssueLinks(t, p.owner, source), stored) || !reflect.DeepEqual(rawIssueLinks(t, p.owner, target), mirror) {
					t.Fatal("link preview or refusal changed an endpoint before the next step")
				}
			}

			deletion := []string{"issue", "link", "delete", id}
			if lane.Name == "before" {
				deletion = []string{"issue", "link", "delete", source, id}
			}
			frame, stdout, exit = p.invokeLink(t, lane, ends, nil, append(slices.Clone(deletion), "--dry-run")...)
			if exit != 0 || !frame.OK || !parityLinkDeleteData(parityOriginalFrameData(stdout), lane.Name, source, id, true) {
				t.Fatal("link deletion preview lost its exact target")
			}
			unchanged()
			p.check(t, lane, "I015.link.preview")
			if lane.Name == "before" {
				frame, stdout, exit = p.invokeLink(t, lane, ends, nil, deletion...)
				if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "validation_failed" || !parityAuthoringJSON(parityOriginalFrameData(stdout), []byte(`null`)) {
					t.Fatal("legacy link confirmation refusal changed or invented an effect")
				}
				unchanged()
				p.check(t, lane, "I015.link.legacy_confirmation")
				deletion = append(deletion, "--force")
			}
			frame, stdout, exit = p.invokeLink(t, lane, ends, []string{"JIRA_READ_ONLY=true"}, deletion...)
			if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != parityCommentReadOnlyCode(lane.Name) || !parityAuthoringJSON(parityOriginalFrameData(stdout), []byte(`null`)) {
				t.Fatal("link deletion refusal changed the relationship or its receipt")
			}
			unchanged()
			p.check(t, lane, "I015.link.read_only")
			frame, stdout, exit = p.invokeLink(t, lane, ends, nil, deletion...)
			if exit != 0 || !frame.OK || !parityLinkDeleteData(parityOriginalFrameData(stdout), lane.Name, source, id, false) {
				t.Fatal("link deletion lacks exact acknowledgement; never replay")
			}
			missing, _, code := p.invoke(t, p.evidence.Lanes[1], []string{"JIRA_READ_ONLY=true"}, "api", "get", "/rest/api/3/issueLink/"+id)
			if code != 2 || missing.OK || len(missing.Errors) != 1 || missing.Errors[0].Code != "jira_not_found" {
				t.Fatal("link deletion lacks independent native absence")
			}
			for j := range p.owner.manifest.Links {
				if p.owner.manifest.Links[j].ID == id {
					p.owner.manifest.Links[j].Deleted = true
				}
			}
			p.owner.save(t)
			empty()
			for j, key := range ends {
				if !nativeMetadataMatches(p.native(t, key).Fields, original[j].Fields, nil) {
					t.Fatal("link lifecycle changed unrelated native issue fields")
				}
			}
			p.check(t, lane, "I015.link.native_delete")
		})
		if t.Failed() {
			break
		}
	}
}

func TestParityLinkMutationAdmission(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "binary")
	const marker = "independent link admission fixture"
	if err := os.WriteFile(binary, []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	typ := storedLinkType{ID: "7", Name: "Blocks", Inward: "is blocked by", Outward: "blocks"}
	before := parityLane{Name: "before", Binary: binary, Digest: fmt.Sprintf("%x", sha256.Sum256([]byte(marker)))}
	after := before
	after.Name = "after"
	p := &parityRun{owner: &liveRun{verified: true, manifest: liveManifest{Issues: []ownedIssue{{ID: "1", Key: "PAIR-1"}, {ID: "2", Key: "PAIR-2"}}, Links: []ownedLink{{ID: "41", Source: "PAIR-1", Target: "PAIR-2"}}}, linkTypes: map[string]storedLinkType{"Blocks": typ}}, evidence: parityEvidence{Lanes: []parityLane{before, after}}}
	keys := []string{"PAIR-1", "PAIR-2"}
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			add := []string{"issue", "link", "create", "PAIR-1", "--to", "PAIR-2", "--type", "Blocks", "--parallelism=1"}
			del := []string{"issue", "link", "delete", "41"}
			if lane.Name == "before" {
				add = append([]string{"issue", "link"}, add[3:]...)
				del = []string{"issue", "link", "delete", "PAIR-1", "41", "--force"}
			}
			for _, args := range [][]string{add, append(slices.Clone(add), "--dry-run"), del} {
				if err := p.validateLinkMutation(lane, keys, args); err != nil {
					t.Fatal(err)
				}
			}
			for _, tc := range []struct {
				name       string
				keys, args []string
			}{
				{"foreign-end", []string{"PAIR-1", "PAIR-3"}, add},
				{"reversed-admission", []string{"PAIR-2", "PAIR-1"}, del},
				{"same-end", []string{"PAIR-1", "PAIR-1"}, add},
				{"selector", keys, append(slices.Clone(add), "--jql=project=PAIR")},
				{"unregistered-type", keys, slices.Replace(slices.Clone(add), len(add)-2, len(add)-1, "Other")},
				{"unknown-delete-option", keys, append(slices.Clone(del), "--cascade")},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if p.validateLinkMutation(lane, tc.keys, tc.args) == nil {
						t.Fatal("link admission accepted a foreign resource or changed recipe")
					}
				})
			}
		})
	}
}

func TestParityActualBinaryLinkPreviews(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		if lane.binary == "" {
			t.Skip("requires both selected actual binaries")
		}
		for _, mode := range []string{"json", "compact"} {
			for _, scenario := range []string{"create", "create-parallel", "delete"} {
				t.Run(lane.name+"/"+mode+"/"+scenario, func(t *testing.T) {
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusForbidden) }))
					defer server.Close()
					home := t.TempDir()
					config := parityCachePublicConfig(t, home, lane.name, server.URL)
					before := parityLocalTree(t, home)
					input, id := "Blocks <&> café", "I014"
					args := []string{"issue", "link", "create", "PAIR-1", "--to", "PAIR-2", "--type", input, "--dry-run"}
					if lane.name == "before" {
						args = append([]string{"issue", "link"}, args[3:]...)
					}
					if scenario == "create-parallel" {
						args = append(args, "--parallelism=2")
					}
					if scenario == "delete" {
						id = "I015"
						args = []string{"issue", "link", "delete", "41", "--dry-run"}
						if lane.name == "before" {
							args = []string{"issue", "link", "delete", "PAIR-1", "41", "--dry-run"}
						}
					}
					if err := parityFingerprint(lane.binary, lane.digest); err != nil {
						t.Fatal(err)
					}
					result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_KEYRING=false"}, args...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load() != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
						t.Fatalf("link local entry or effects changed: exit%d HTTP%d stdout%s stderr%s", result.Exit, calls.Load(), result.Stdout, result.Stderr)
					}
					raw := result.Stdout
					if mode == "json" {
						parityAuthoringData(t, result.Stdout, lane.name, parityExpectedOperation(lane.name, args, 0), 0)
						raw = parityOriginalFrameData(raw)
						var frame struct{ Warnings json.RawMessage }
						want := []byte(`[]`)
						if lane.name == "after" && scenario != "delete" {
							want, _ = json.Marshal([]any{map[string]any{"type": "link_type_unresolved", "lossy": false, "message": fmt.Sprintf("--type is left as %q in this preview: resolving it reads the site's link types, which --dry-run never does. A live run resolves it; add --validate-remote to resolve it here.", input)}})
						}
						if json.Unmarshal(result.Stdout, &frame) != nil || !parityAuthoringJSON(frame.Warnings, want) {
							t.Fatal("link preview lost exact unresolved provenance or invented a warning")
						}
					}
					ok := parityLinkCreateData(raw, lane.name, "PAIR-1", "PAIR-2", input, storedLinkType{}, true, false, false)
					if scenario == "delete" {
						ok = parityLinkDeleteData(raw, lane.name, "PAIR-1", "41", true)
					}
					if !ok {
						t.Fatalf("link preview differs from its complete independent mapping: %s", raw)
					}
					if err := parityFingerprint(lane.binary, lane.digest); err != nil {
						t.Fatal(err)
					}
					evidence.Checks[id+".link.local_"+scenario+"."+mode] = append(evidence.Checks[id+".link.local_"+scenario+"."+mode], lane.name)
				})
			}
		}
	}
	saveParityLocal(t, "links", evidence)
}

func TestParityLinkOperationMapping(t *testing.T) {
	for _, exit := range []int{0, 3} {
		if got := parityExpectedOperation("before", []string{"issue", "link", "PAIR-1", "--to=PAIR-2", "--type=Blocks"}, exit); got != "issue.link" {
			t.Fatalf("legacy bare link attributed to %q", got)
		}
	}
}

func TestParityLinkReceiptGuards(t *testing.T) {
	typ := storedLinkType{ID: "7", Name: "Blocks", Inward: "is blocked by", Outward: "blocks"}
	for _, tc := range []struct {
		name, lane, operation      string
		preview, resolved, refused bool
		raw                        string
	}{
		{"before-preview", "before", "create", true, false, false, `{"inward_issue":{"key":"PAIR-1"},"outward_issue":{"key":"PAIR-2"},"type":"Blocks","dry_run":true}`},
		{"before-live", "before", "create", false, true, false, `{"inward_issue":{"key":"PAIR-1"},"outward_issue":{"key":"PAIR-2"},"type":"Blocks","dry_run":false,"preview":{"inward_issue_sentence":"PAIR-1 blocks PAIR-2","outward_issue_sentence":"PAIR-2 is blocked by PAIR-1"}}`},
		{"before-refused", "before", "create", false, true, true, `null`},
		{"after-preview", "after", "create", true, false, false, `{"dry_run":true,"to":{"key":"PAIR-2"},"type_input":"Blocks","type_resolved":false,"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{}}]}`},
		{"after-live", "after", "create", false, true, false, `{"dry_run":false,"to":{"key":"PAIR-2"},"type_input":"Blocks","type_resolved":true,"type":{"id":"7","name":"Blocks","inward":"is blocked by","outward":"blocks"},"relation":"blocks","results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{}}]}`},
		{"after-refused", "after", "create", false, true, true, `{"dry_run":false,"to":{"key":"PAIR-2"},"type_input":"Blocks","type_resolved":true,"type":{"id":"7","name":"Blocks","inward":"is blocked by","outward":"blocks"},"relation":"blocks","results":[{"issue":{"key":"PAIR-1"},"ok":false,"error":{"code":"read_only_blocked","message":"read-only mode: write blocked: POST /rest/api/3/issueLink","hint":"Read-only mode is on, so writes are refused before they reach Jira — clear it to allow this command to mutate."}}]}`},
		{"before-delete-preview", "before", "delete", true, false, false, `{"issue":{"key":"PAIR-1"},"link_id":"41","dry_run":true}`},
		{"before-delete-live", "before", "delete", false, false, false, `{"issue":{"key":"PAIR-1"},"link_id":"41","dry_run":false,"deleted":true}`},
		{"after-delete-preview", "after", "delete", true, false, false, `{"link_id":"41","dry_run":true}`},
		{"after-delete-live", "after", "delete", false, false, false, `{"link_id":"41","dry_run":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check := func(raw []byte) bool {
				if tc.operation == "delete" {
					return parityLinkDeleteData(raw, tc.lane, "PAIR-1", "41", tc.preview)
				}
				return parityLinkCreateData(raw, tc.lane, "PAIR-1", "PAIR-2", "Blocks", typ, tc.preview, tc.resolved, tc.refused)
			}
			if !check([]byte(tc.raw)) {
				t.Fatal("independent complete link receipt refused")
			}
			if tc.raw == "null" {
				if check([]byte(`{}`)) {
					t.Fatal("refusal invented retained data")
				}
				return
			}
			for name, replacement := range map[string][2]string{
				"foreign-source":  {`PAIR-1`, `PAIR-3`},
				"foreign-target":  {`PAIR-2`, `PAIR-3`},
				"foreign-id":      {`"41"`, `"42"`},
				"missing-preview": {fmt.Sprintf(`"dry_run":%t`, tc.preview), `"unrelated":true`},
				"null-preview":    {fmt.Sprintf(`"dry_run":%t`, tc.preview), `"dry_run":null`},
				"invented-effect": {`{`, `{"invented_effect":true,`},
			} {
				if !strings.Contains(tc.raw, replacement[0]) {
					continue
				}
				t.Run(name, func(t *testing.T) {
					mutant := strings.Replace(tc.raw, replacement[0], replacement[1], 1)
					if !json.Valid([]byte(mutant)) {
						t.Fatal("negative fixture must remain executable JSON")
					}
					if check([]byte(mutant)) {
						t.Fatal("link receipt admitted changed ownership, sparse facts or invented effects")
					}
				})
			}
		})
	}
}
