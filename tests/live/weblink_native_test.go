//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func nativeWeblinkArgs(keys []string, path string) []string {
	args := append([]string{"issue", "weblink", "add"}, keys...)
	return append(args, "--json-input="+path, "--parallelism=2")
}

// nativeWeblinkInput only authors this workflow's fixed rich link. Registration
// binds its immutable bytes and exact targets, without granting issue ownership.
func (r *liveRun) nativeWeblinkInput(t *testing.T, keys []string, globalID string, resolved bool) (string, []byte, weblinkRecipe) {
	t.Helper()
	if !r.verified || len(keys) == 0 {
		t.Fatal("native remote-link input requires verified owned targets")
	}
	for _, key := range keys {
		if r.ownedLiveIssue(key) == nil {
			t.Fatal("native remote-link input requires active owned targets")
		}
	}
	recipe := weblinkRecipe{URL: "https://example.com/native-runbook", Title: "Native runbook <&>", Summary: "Authored native summary", Relationship: "is documented by", GlobalID: globalID}
	if !resolved {
		recipe.Title = "Revised native runbook"
	}
	data, err := json.Marshal(map[string]any{
		"globalId": globalID, "relationship": recipe.Relationship,
		"application": map[string]string{"name": "Acceptance runbook", "type": "com.example.runbook"},
		"object": map[string]any{
			"url": recipe.URL, "title": recipe.Title, "summary": recipe.Summary,
			"icon":   map[string]string{"url16x16": "https://example.com/runbook.png", "title": recipe.Title},
			"status": map[string]any{"resolved": resolved, "icon": map[string]string{"url16x16": "https://example.com/status.png", "title": "Authored status", "link": "https://example.com/status"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.dir, "native-weblink-"+output.NewRequestID()+".json")
	if err := xos.AtomicWrite(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	r.weblinkInputs = append(r.weblinkInputs, ownedWeblinkInput{keys: slices.Clone(keys), path: path, digest: sha256.Sum256(data)})
	return path, data, recipe
}

func assertNativeWeblinkPayload(t *testing.T, got, want []byte) {
	t.Helper()
	var observed, authored map[string]any
	if json.Unmarshal(got, &observed) != nil || json.Unmarshal(want, &authored) != nil {
		t.Fatal("native remote-link comparison requires complete JSON objects")
	}
	for key, value := range authored {
		if !reflect.DeepEqual(observed[key], value) {
			t.Fatalf("native remote-link member %s differs from authored data", key)
		}
	}
}

func rawNativeWeblinkList(t *testing.T, r *liveRun, key string) []byte {
	t.Helper()
	if r.ownedLiveIssue(key) == nil {
		t.Fatal("native remote-link comparison requires an active owned issue")
	}
	read := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key+"/remotelink")
	var links []json.RawMessage
	if json.Unmarshal(read.Response.JSON, &links) != nil || links == nil {
		t.Fatal("native remote-link list is not a complete array")
	}
	return read.Response.JSON
}

func TestLiveNativeWeblinks(t *testing.T) {
	r := newLiveRun(t)
	keys := []string{r.create(t, "native remote-link first"), r.create(t, "native remote-link second")}
	ids := map[string]string{}
	for step, resolved := range []bool{true, false} {
		path, data, recipe := r.nativeWeblinkInput(t, keys, "system=acceptance&id="+r.manifest.RunID, resolved)
		args := nativeWeblinkArgs(keys, path)
		for _, preview := range []bool{true, false} {
			before := map[string][]byte{}
			for _, key := range keys {
				before[key] = rawNativeWeblinkList(t, r, key)
			}
			invoke := slices.Clone(args)
			extra := []string{"JIRA_READ_ONLY=true"}
			if preview {
				invoke = append(invoke, "--dry-run")
				extra = nil
			}
			if err := r.checkMutation(keys, invoke); err != nil {
				t.Fatal(err)
			}
			frame, stdout, exit := r.invoke(t, t.Context(), extra, invoke...)
			var receipt struct {
				Data envelope.IssueWeblinkAddOutput `json:"data"`
			}
			if json.Unmarshal(stdout, &receipt) != nil || len(receipt.Data.Results) != len(keys) {
				t.Fatal("native remote-link preview/refusal lost ordered targets")
			}
			assertNativeWeblinkPayload(t, receipt.Data.Payload, data)
			if preview && (exit != 0 || !frame.OK || !receipt.Data.DryRun) || !preview && (exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "read_only_blocked") {
				t.Fatal("native remote-link preview/refusal bypassed policy")
			}
			for i, key := range keys {
				row := receipt.Data.Results[i]
				if row.Issue.Key != key || row.OK != preview || preview && (row.Data == nil || row.Data.Created != nil || row.Data.Weblink != nil) || !preview && row.Data != nil {
					t.Fatal("native remote-link preview/refusal fabricated a write receipt")
				}
				var previous, current any
				if json.Unmarshal(before[key], &previous) != nil || json.Unmarshal(rawNativeWeblinkList(t, r, key), &current) != nil || !reflect.DeepEqual(previous, current) {
					t.Fatal("native remote-link preview/refusal changed stored rich metadata")
				}
			}
		}
		if err := r.checkMutation(keys, args); err != nil {
			t.Fatal(err)
		}
		frame, stdout, exit := r.invoke(t, t.Context(), nil, args...)
		var receipt struct {
			Data envelope.IssueWeblinkAddOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(stdout, &receipt) != nil || len(receipt.Data.Results) != len(keys) {
			t.Fatal("native remote-link fan-out lost acknowledged results")
		}
		assertNativeWeblinkPayload(t, receipt.Data.Payload, data)
		for i, key := range keys {
			row := receipt.Data.Results[i]
			if row.Issue.Key != key || !row.OK || row.Data == nil || row.Data.Weblink == nil || row.Data.Created == nil || *row.Data.Created != (step == 0) || step == 1 && row.Data.Weblink.ID != ids[key] {
				t.Fatal("native remote-link replacement lost stable identity or created status")
			}
			ids[key] = row.Data.Weblink.ID
			r.registerWeblink(t, key, *row.Data.Weblink, recipe)
			stored := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key+"/remotelink/"+row.Data.Weblink.ID)
			assertNativeWeblinkPayload(t, stored.Response.JSON, data)
			var links []json.RawMessage
			if json.Unmarshal(rawNativeWeblinkList(t, r, key), &links) != nil || len(links) != 1 {
				t.Fatal("native global ID replacement duplicated the owned link")
			}
			assertNativeWeblinkPayload(t, links[0], data)
			compareOwnedWeblinks(t, r, key)
		}
	}
}
