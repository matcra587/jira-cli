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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

type parityWeblinkAttempt struct {
	Lane       string        `json:"lane"`
	Key        string        `json:"key"`
	Recipe     weblinkRecipe `json:"recipe"`
	ReturnedID string        `json:"returned_id,omitempty"`
	NativeID   string        `json:"observed_id,omitempty"`
	Bound      bool          `json:"bound"`
}

func (p *parityRun) validateWeblinkMutation(lane parityLane, key string, args []string) error {
	if !slices.Contains(p.evidence.Lanes, lane) || p.owner.ownedLiveIssue(key) == nil {
		return fmt.Errorf("web-link mutation requires an admitted lane and active owned parent")
	}
	if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
		return err
	}
	if lane.Name == "before" {
		if len(args) < 4 || args[0] != "issue" || args[1] != "weblink" || args[2] != key {
			return fmt.Errorf("legacy web-link mutation requires exact bare owned grammar")
		}
		for i := 3; i < len(args); i++ {
			flag, _, inline := strings.Cut(args[i], "=")
			switch flag {
			case "--dry-run":
			case "--url", "--title", "--parallelism":
				if !inline {
					i++
					if i == len(args) {
						return fmt.Errorf("legacy web-link option lacks a value")
					}
				}
			default:
				return fmt.Errorf("legacy web-link mutation has an unauthored option")
			}
		}
		args = append([]string{"issue", "weblink", "add"}, args[2:]...)
	} else if lane.Name != "after" {
		return fmt.Errorf("unsupported web-link lane")
	}
	return p.owner.checkWeblinkMutation([]string{key}, args)
}

func (p *parityRun) invokeWeblink(t *testing.T, lane parityLane, key string, extra []string, args ...string) (liveFrame, []byte, int) {
	t.Helper()
	if err := p.validateWeblinkMutation(lane, key, args); err != nil {
		t.Fatal(err)
	}
	return p.invoke(t, lane, extra, args...)
}

func parityWeblinkAddData(data []byte, lane, key string, recipe weblinkRecipe, preview bool, native *storedWeblink) bool {
	var want map[string]any
	switch lane {
	case "before":
		want = map[string]any{"issue": map[string]any{"key": key}, "url": recipe.URL, "title": recipe.Title, "url_remote_checked": false, "dry_run": preview}
	case "after":
		entry := map[string]any{}
		if !preview {
			if native == nil || native.ID <= 0 {
				return false
			}
			entry["created"] = true
			ref := map[string]any{"id": strconv.FormatInt(native.ID, 10)}
			if native.Self != "" {
				ref["self"] = native.Self
			}
			entry["weblink"] = ref
		}
		want = map[string]any{"dry_run": preview, "url": recipe.URL, "title": recipe.Title, "payload": map[string]any{"object": map[string]any{"url": recipe.URL, "title": recipe.Title}}, "results": []any{map[string]any{"issue": map[string]any{"key": key}, "ok": true, "data": entry}}}
	default:
		return false
	}
	raw, err := json.Marshal(want)
	return err == nil && parityAuthoringJSON(data, raw)
}

func parityWeblinkRefusedData(data []byte, lane, key string, recipe weblinkRecipe) bool {
	if lane == "before" {
		return parityAuthoringJSON(data, []byte(`null`))
	}
	if lane != "after" {
		return false
	}
	expected := map[string]any{"dry_run": false, "url": recipe.URL, "title": recipe.Title, "payload": map[string]any{"object": map[string]any{"url": recipe.URL, "title": recipe.Title}}, "results": []any{map[string]any{"issue": map[string]any{"key": key}, "ok": false, "error": map[string]any{"code": "read_only_blocked", "message": "read-only mode: write blocked: POST /rest/api/3/issue/" + key + "/remotelink", "hint": "Read-only mode is on, so writes are refused before they reach Jira — clear it to allow this command to mutate."}}}}
	raw, err := json.Marshal(expected)
	return err == nil && parityAuthoringJSON(data, raw)
}

func parityWeblinkDeleteData(data []byte, key, id string, preview bool) bool {
	raw, err := json.Marshal(map[string]any{"issue": map[string]any{"key": key}, "dry_run": preview, "weblink_id": id})
	return err == nil && parityAuthoringJSON(data, raw)
}

func TestLiveParityWeblinks(t *testing.T) {
	p := newParityRun(t)
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			key := p.create(t, lane)
			original := p.native(t, key)
			recipe := weblinkRecipe{URL: "https://example.invalid/runbook?x=1&y=2#section", Title: "Runbook <&> café"}
			p.owner.allowWeblink([]string{key}, recipe)
			args := weblinkArgs([]string{key}, recipe)
			if lane.Name == "before" {
				args = append([]string{"issue", "weblink"}, args[3:]...)
			}
			empty := func() {
				t.Helper()
				if len(rawOwnedWeblinks(t, p.owner, key)) != 0 {
					t.Fatal("fresh owned web-link inventory changed before an admitted write")
				}
			}
			empty()
			frame, stdout, exit := p.invokeWeblink(t, lane, key, nil, append(slices.Clone(args), "--dry-run")...)
			data := parityAuthoringData(t, stdout, lane.Name, parityExpectedOperation(lane.Name, args, exit), exit)
			raw, err := json.Marshal(data)
			if exit != 0 || !frame.OK || err != nil || !parityWeblinkAddData(raw, lane.Name, key, recipe, true, nil) {
				t.Fatal("paired web-link preview lost authored request or invented completion")
			}
			empty()
			p.check(t, lane, "I029.weblink.preview")
			frame, stdout, exit = p.invokeWeblink(t, lane, key, []string{"JIRA_READ_ONLY=true"}, args...)
			code := parityCommentReadOnlyCode(lane.Name)
			if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != code || !parityWeblinkRefusedData(parityOriginalFrameData(stdout), lane.Name, key, recipe) {
				t.Fatal("paired web-link mutation bypassed read-only refusal")
			}
			empty()
			p.check(t, lane, "I029.weblink.read_only")

			attempt := len(p.evidence.Weblinks)
			p.evidence.Weblinks = append(p.evidence.Weblinks, parityWeblinkAttempt{Lane: lane.Name, Key: key, Recipe: recipe})
			p.save(t)
			frame, stdout, exit = p.invokeWeblink(t, lane, key, nil, args...)
			var received struct {
				Data struct {
					Results []struct {
						Data *struct {
							Weblink *struct {
								ID string `json:"id"`
							} `json:"weblink"`
						} `json:"data"`
					} `json:"results"`
				} `json:"data"`
			}
			if json.Unmarshal(stdout, &received) == nil && len(received.Data.Results) == 1 && received.Data.Results[0].Data != nil && received.Data.Results[0].Data.Weblink != nil {
				p.evidence.Weblinks[attempt].ReturnedID = received.Data.Results[0].Data.Weblink.ID
			}
			p.save(t)
			stored := rawOwnedWeblinks(t, p.owner, key)
			if len(stored) != 1 || !stored[0].matches(recipe) {
				t.Fatal("web-link write has no unique independently bound native effect; never replay")
			}
			id := strconv.FormatInt(stored[0].ID, 10)
			p.evidence.Weblinks[attempt].NativeID = id
			p.save(t)
			p.owner.registerWeblink(t, key, envelope.WeblinkRef{ID: id, Self: stored[0].Self}, recipe)
			p.evidence.Weblinks[attempt].Bound = true
			p.save(t)
			data = parityAuthoringData(t, stdout, lane.Name, parityExpectedOperation(lane.Name, args, exit), exit)
			raw, err = json.Marshal(data)
			if exit != 0 || !frame.OK || err != nil || !parityWeblinkAddData(raw, lane.Name, key, recipe, false, &stored[0]) || lane.Name == "after" && p.evidence.Weblinks[attempt].ReturnedID != id {
				t.Fatal("paired web-link add lost native outcome or identity; owned cleanup retained")
			}
			if !nativeMetadataMatches(p.native(t, key).Fields, original.Fields, nil) {
				t.Fatal("web-link add changed unrelated owned issue fields")
			}
			p.check(t, lane, "I029.weblink.native_create")

			// New list/delete operations use the current lane for both independently
			// bound fixtures. Legacy add never pretends to publish these capabilities.
			after := p.evidence.Lanes[1]
			frame, stdout, exit = p.invoke(t, after, []string{"JIRA_READ_ONLY=true"}, "issue", "weblink", "list", key)
			expectedList, err := json.Marshal(map[string]any{"results": []any{map[string]any{"issue": map[string]any{"key": key}, "ok": true, "data": map[string]any{"weblinks": []any{map[string]any{"id": id, "url": recipe.URL, "title": recipe.Title}}}}}})
			if exit != 0 || !frame.OK || err != nil || !parityAuthoringJSON(parityOriginalFrameData(stdout), expectedList) {
				t.Fatal("current web-link list differs from the independent complete native projection")
			}

			p.check(t, after, "N009.weblink.list_"+lane.Name+"_fixture")

			deleteArgs := []string{"issue", "weblink", "delete", key, id}
			for _, preview := range []bool{true, false} {
				selected := slices.Clone(deleteArgs)
				extra := []string{"JIRA_READ_ONLY=true"}
				if preview {
					selected = append(selected, "--dry-run")
					extra = nil
				}
				frame, stdout, exit = p.invokeWeblink(t, after, key, extra, selected...)
				if preview {
					if exit != 0 || !frame.OK || !parityWeblinkDeleteData(parityOriginalFrameData(stdout), key, id, true) {
						t.Fatal("current web-link delete preview lost its exact owned target")
					}
				} else if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "read_only_blocked" || !parityAuthoringJSON(parityOriginalFrameData(stdout), []byte(`null`)) {
					t.Fatal("current web-link delete bypassed read-only policy or invented a receipt")
				}

				if !slices.Equal(stored, rawOwnedWeblinks(t, p.owner, key)) {
					t.Fatal("web-link deletion preview/refusal changed native inventory")
				}
				p.check(t, after, fmt.Sprintf("N010.weblink.delete_preview_%t_%s_fixture", preview, lane.Name))
			}
			frame, stdout, exit = p.invokeWeblink(t, after, key, nil, deleteArgs...)
			if exit != 0 || !frame.OK || !parityWeblinkDeleteData(parityOriginalFrameData(stdout), key, id, false) {
				t.Fatal("owned web-link deletion has no exact acknowledged target; never replay")
			}

			missing, _, missingExit := p.invoke(t, after, []string{"JIRA_READ_ONLY=true"}, "api", "get", "/rest/api/3/issue/"+key+"/remotelink/"+id)
			if missingExit != 2 || missing.OK || len(missing.Errors) != 1 || missing.Errors[0].Code != "jira_not_found" {
				t.Fatal("web-link deletion lacks independent native absence")
			}
			p.owner.ownedLiveIssue(key).Weblinks[0].Deleted = true
			p.owner.save(t)
			if len(rawOwnedWeblinks(t, p.owner, key)) != 0 || !nativeMetadataMatches(p.native(t, key).Fields, original.Fields, nil) {
				t.Fatal("web-link deletion retained a resource or changed unrelated fields")
			}
			p.check(t, after, "N010.weblink.native_delete_"+lane.Name+"_fixture")
		})
	}
}

func TestParityWeblinkMutationAdmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binary")
	const marker = "independent web-link admission fixture"
	if err := os.WriteFile(path, []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	lane := parityLane{Name: "before", Binary: path, Digest: fmt.Sprintf("%x", sha256.Sum256([]byte(marker)))}
	p := &parityRun{owner: &liveRun{verified: true, manifest: liveManifest{Issues: []ownedIssue{{Key: "PAIR-1", ID: "1"}}}}, evidence: parityEvidence{Lanes: []parityLane{lane}}}
	recipe := weblinkRecipe{URL: "https://example.invalid", Title: "Example"}
	p.owner.allowWeblink([]string{"PAIR-1"}, recipe)
	args := []string{"issue", "weblink", "PAIR-1", "--url=" + recipe.URL, "--title=" + recipe.Title, "--parallelism=2"}
	if err := p.validateWeblinkMutation(lane, "PAIR-1", args); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, key string
		args      []string
	}{
		{"foreign-target", "PAIR-2", args},
		{"changed-target", "PAIR-1", []string{"issue", "weblink", "PAIR-2", "--url=" + recipe.URL}},
		{"changed-url", "PAIR-1", []string{"issue", "weblink", "PAIR-1", "--url=https://other.invalid", "--title=Example"}},
		{"new-only-option", "PAIR-1", append(slices.Clone(args), "--global-id=foreign")},
		{"new-only-grammar", "PAIR-1", append([]string{"issue", "weblink", "add"}, args[2:]...)},
		{"extra-key", "PAIR-1", append(args[:3:3], append([]string{"PAIR-2"}, args[3:]...)...)},
		{"missing-value", "PAIR-1", []string{"issue", "weblink", "PAIR-1", "--url"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if p.validateWeblinkMutation(lane, tc.key, tc.args) == nil {
				t.Fatal("paired web-link admission accepted an unregistered route or request")
			}
		})
	}
}

func TestParityWeblinkOperationAttribution(t *testing.T) {
	for _, exit := range []int{0, 3} {
		if got := parityExpectedOperation("before", []string{"issue", "weblink", "PAIR-1", "--url=https://example.invalid"}, exit); got != "issue.weblink" {
			t.Fatalf("legacy bare web-link operation is %q, want issue.weblink", got)
		}
	}
	for _, tc := range []struct {
		args []string
		op   string
	}{
		{[]string{"issue", "weblink", "add", "PAIR-1"}, "issue.weblink.add"},
		{[]string{"issue", "weblink", "delete", "PAIR-1", "1"}, "issue.weblink.delete"},
		{[]string{"issue", "weblink-other", "PAIR-1"}, "issue.weblink-other"},
	} {
		if got := parityExpectedOperation("before", tc.args, 0); got != tc.op {
			t.Fatalf("web-link attribution matched a different spelling: %q", got)
		}
	}
}

func TestParityWeblinkAddDataGuards(t *testing.T) {
	recipe := weblinkRecipe{URL: "https://example.invalid", Title: "Example"}
	native := &storedWeblink{ID: 41, Self: "https://jira.invalid/native/41"}
	const before = `{"issue":{"key":"PAIR-1"},"url":"https://example.invalid","title":"Example","url_remote_checked":false,"dry_run":false}`
	const after = `{"dry_run":false,"url":"https://example.invalid","title":"Example","payload":{"object":{"url":"https://example.invalid","title":"Example"}},"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"created":true,"weblink":{"id":"41","self":"https://jira.invalid/native/41"}}}]}`
	for _, lane := range []string{"before", "after"} {
		raw := before
		if lane == "after" {
			raw = after
		}
		t.Run(lane, func(t *testing.T) {
			if !parityWeblinkAddData([]byte(raw), lane, "PAIR-1", recipe, false, native) {
				t.Fatal("independent literal web-link receipt refused")
			}
			for name, replacement := range map[string][2]string{
				"target":  {`"PAIR-1"`, `"PAIR-2"`},
				"url":     {`"https://example.invalid"`, `"https://other.invalid"`},
				"title":   {`"Example"`, `"Changed"`},
				"preview": {`"dry_run":false`, `"dry_run":true`},
			} {
				t.Run(name, func(t *testing.T) {
					if parityWeblinkAddData([]byte(strings.Replace(raw, replacement[0], replacement[1], 1)), lane, "PAIR-1", recipe, false, native) {
						t.Fatal("web-link oracle accepted changed request or outcome")
					}
				})
			}
		})
	}
	for name, replacement := range map[string][2]string{
		"missing-id":  {`"id":"41",`, ``},
		"foreign-id":  {`"id":"41"`, `"id":"42"`},
		"failed-key":  {`"ok":true`, `"ok":false`},
		"replacement": {`"created":true`, `"created":false`},
		"missing-row": {`[{"issue":{"key":"PAIR-1"},"ok":true,"data":{"created":true,"weblink":{"id":"41","self":"https://jira.invalid/native/41"}}}]`, `[]`},
	} {
		t.Run(name, func(t *testing.T) {
			if parityWeblinkAddData([]byte(strings.Replace(after, replacement[0], replacement[1], 1)), "after", "PAIR-1", recipe, false, native) {
				t.Fatal("web-link oracle accepted missing or foreign native outcome")
			}
		})
	}
}

func TestParityActualBinaryWeblinkPreviews(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		if lane.binary == "" {
			t.Skip("requires both selected actual binaries")
		}
		for _, mode := range []string{"json", "compact"} {
			for _, scenario := range []string{"explicit-title", "omitted-title", "empty-title", "read-only"} {
				t.Run(lane.name+"/"+mode+"/"+scenario, func(t *testing.T) {
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusForbidden) }))
					defer server.Close()
					home := t.TempDir()
					config := parityCachePublicConfig(t, home, lane.name, server.URL)
					tree := parityLocalTree(t, home)
					recipe := weblinkRecipe{URL: "https://example.invalid/runbook?x=1&y=2#section", Title: "Runbook <&> café"}
					args := []string{"issue", "weblink", "PAIR-1", "--url=" + recipe.URL, "--parallelism=2", "--dry-run"}
					if lane.name == "after" {
						args = append([]string{"issue", "weblink", "add"}, args[2:]...)
					}
					if scenario == "explicit-title" || scenario == "read-only" {
						args = append(args, "--title="+recipe.Title)
					} else {
						recipe.Title = ""
						if scenario == "empty-title" {
							args = append(args, "--title=")
						}
						if lane.name == "after" {
							recipe.Title = recipe.URL
						}
					}
					if err := parityFingerprint(lane.binary, lane.digest); err != nil {
						t.Fatal(err)
					}
					extra := []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_KEYRING=false"}
					expectedExit := 0
					if scenario == "read-only" {
						args = slices.DeleteFunc(args, func(arg string) bool { return arg == "--dry-run" })
						extra = []string{"JIRA_READ_ONLY=true"}
						expectedExit = 3
					}
					result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, extra, args...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					if result.Exit != expectedExit || expectedExit == 0 && len(result.Stderr) != 0 || calls.Load() != 0 || !reflect.DeepEqual(tree, parityLocalTree(t, home)) {
						t.Fatalf("web-link local entry/effects changed: exit%d HTTP%d stdout%s stderr%s", result.Exit, calls.Load(), result.Stdout, result.Stderr)
					}
					raw := result.Stdout
					if mode == "json" || expectedExit != 0 {
						parityAuthoringData(t, result.Stdout, lane.name, parityExpectedOperation(lane.name, args, expectedExit), expectedExit)
						raw = parityOriginalFrameData(result.Stdout)
					}
					if expectedExit != 0 {
						var frame struct {
							Errors []struct {
								Code string `json:"code"`
							} `json:"errors"`
						}
						if json.Unmarshal(result.Stdout, &frame) != nil || len(frame.Errors) != 1 || frame.Errors[0].Code != parityCommentReadOnlyCode(lane.name) || !parityWeblinkRefusedData(raw, lane.name, "PAIR-1", recipe) {
							t.Fatalf("native refusal differs from its independent complete receipt: %s", raw)
						}
					} else if !parityWeblinkAddData(raw, lane.name, "PAIR-1", recipe, true, nil) {
						t.Fatalf("native preview differs from its independent delivered layout: %s", raw)
					}

					if err := parityFingerprint(lane.binary, lane.digest); err != nil {
						t.Fatal(err)
					}
					evidence.Checks["I029.weblink.local_"+scenario+"."+mode] = append(evidence.Checks["I029.weblink.local_"+scenario+"."+mode], lane.name)
				})
			}
		}
	}
	saveParityLocal(t, "weblink", evidence)
}

func TestParityWeblinkRefusalAndDeleteGuards(t *testing.T) {
	recipe := weblinkRecipe{URL: "https://example.invalid", Title: "Example"}
	const refused = `{"dry_run":false,"payload":{"object":{"url":"https://example.invalid","title":"Example"}},"url":"https://example.invalid","title":"Example","results":[{"issue":{"key":"PAIR-1"},"ok":false,"error":{"code":"read_only_blocked","message":"read-only mode: write blocked: POST /rest/api/3/issue/PAIR-1/remotelink","hint":"Read-only mode is on, so writes are refused before they reach Jira — clear it to allow this command to mutate."}}]}`
	if !parityWeblinkRefusedData([]byte(`null`), "before", "PAIR-1", recipe) || !parityWeblinkRefusedData([]byte(refused), "after", "PAIR-1", recipe) {
		t.Fatal("independent refusal fixtures were rejected")
	}
	for name, replacement := range map[string][2]string{
		"foreign-target":    {`"PAIR-1"`, `"PAIR-2"`},
		"success-row":       {`"ok":false`, `"ok":true`},
		"invented-identity": {`"ok":false,`, `"ok":false,"data":{"created":true,"weblink":{"id":"41"}},`},
		"lost-payload":      {`"payload":{"object":{"url":"https://example.invalid","title":"Example"}},`, ``},
		"wrong-refusal":     {`"code":"read_only_blocked"`, `"code":"jira_not_found"`},
	} {
		t.Run(name, func(t *testing.T) {
			if parityWeblinkRefusedData([]byte(strings.Replace(refused, replacement[0], replacement[1], 1)), "after", "PAIR-1", recipe) {
				t.Fatal("refusal guard admitted foreign or invented effects")
			}
		})
	}
	for _, raw := range []string{`{}`, `{"results":[]}`, refused} {
		if parityWeblinkRefusedData([]byte(raw), "before", "PAIR-1", recipe) {
			t.Fatal("legacy refusal invented retained data")
		}
	}
	for _, preview := range []bool{true, false} {
		raw := `{"issue":{"key":"PAIR-1"},"dry_run":` + strconv.FormatBool(preview) + `,"weblink_id":"41"}`
		if !parityWeblinkDeleteData([]byte(raw), "PAIR-1", "41", preview) {
			t.Fatal("independent exact delete fixture refused")
		}
		for name, replacement := range map[string][2]string{
			"missing-preview":   {`"dry_run":` + strconv.FormatBool(preview) + `,`, ``},
			"null-preview":      {`"dry_run":` + strconv.FormatBool(preview), `"dry_run":null`},
			"wrong-preview":     {`"dry_run":` + strconv.FormatBool(preview), `"dry_run":` + strconv.FormatBool(!preview)},
			"foreign-target":    {`"PAIR-1"`, `"PAIR-2"`},
			"foreign-id":        {`"41"`, `"42"`},
			"invented-deletion": {`"weblink_id":"41"`, `"weblink_id":"41","deleted":true`},
			"invented-object":   {`"weblink_id":"41"`, `"weblink_id":"41","removed":{"id":"41"}`},
		} {
			t.Run(fmt.Sprintf("delete-preview-%t/%s", preview, name), func(t *testing.T) {
				if parityWeblinkDeleteData([]byte(strings.Replace(raw, replacement[0], replacement[1], 1)), "PAIR-1", "41", preview) {
					t.Fatal("delete guard admitted sparse or invented effects")
				}
			})
		}
	}
}

func TestParityWeblinkFrameDataGuards(t *testing.T) {
	const data = `{"dry_run":false}`
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"complete", `{"data":` + data + `}`, true},
		{"duplicate-root", `{"data":{"wrong":true},"data":` + data + `}`, false},
		{"escaped-duplicate-root", `{"data":{"wrong":true},"da\u0074a":` + data + `}`, false},
		{"duplicate-nested", `{"data":{"dry_run":true,"dry_run":false}}`, false},
		{"trailing-value", `{"data":` + data + `} {}`, false},
		{"missing", `{}`, false},
		{"null", `null`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parityOriginalFrameData([]byte(tc.raw))
			if tc.valid && !parityAuthoringJSON(got, []byte(data)) || !tc.valid && len(got) != 0 {
				t.Fatal("web-link frame admitted duplicate or incomplete original JSON")
			}
		})
	}
}
