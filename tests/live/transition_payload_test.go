//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/output"
)

type nativeTransitionInput struct {
	key    string
	digest [sha256.Size]byte
}

func (r *liveRun) checkNativeTransitionMutation(keys, args []string) error {
	if !r.verified || len(keys) != 1 || len(args) != 5 || args[0] != "issue" || args[1] != "transition" || args[2] != keys[0] || args[3] != "--json-input" || r.ownedLiveIssue(keys[0]) == nil {
		return errors.New("native transition requires one active owned target and registered input")
	}
	registered, exists := r.transitionInputs[args[4]]
	if !exists || registered.key != keys[0] {
		return errors.New("native transition input is not bound to this owned target")
	}
	data, err := os.ReadFile(args[4])
	if err != nil || sha256.Sum256(data) != registered.digest {
		return errors.New("native transition input changed after registration")
	}
	return nil
}

// nativeTransitionInput authors only fixed requests for a freshly discovered
// decimal transition on one owned issue. It cannot author relationship fields,
// selectors, arbitrary operations or additional issue targets.
func (r *liveRun) nativeTransitionInput(t *testing.T, key, id, recipe string) (string, string) {
	t.Helper()
	if !r.verified || r.ownedLiveIssue(key) == nil || id == "" || strings.IndexFunc(id, func(c rune) bool { return c < '0' || c > '9' }) >= 0 {
		t.Fatal("native transition input requires one active owned issue and a discovered decimal ID")
	}
	payload := map[string]any{"transition": map[string]string{"id": id}, "fields": map[string]any{}, "update": map[string]any{}}
	property := ""
	switch recipe {
	case "empty":
		payload["historyMetadata"] = map[string]string{"type": "cli.acceptance", "description": r.manifest.RunID}
	case "property":
		property = "cli.acceptance." + output.NewRequestID()
		payload["properties"] = []any{map[string]any{"key": property, "value": json.RawMessage(livePropertyValue)}}
		payload["historyMetadata"] = map[string]string{"type": "cli.acceptance", "description": r.manifest.RunID}
	case "field":
		payload["fields"] = map[string]string{"summary": r.manifest.RunID + " refused transition field"}
	case "comment":
		payload["comment"] = map[string]any{"type": "doc", "version": 1, "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]string{"type": "text", "text": r.manifest.RunID + " native transition comment"}}}}}
	default:
		t.Fatal("unknown fixed native transition recipe")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.dir, "transition-"+output.NewRequestID()+".json")
	if err := xos.AtomicWrite(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if r.transitionInputs == nil {
		r.transitionInputs = make(map[string]nativeTransitionInput)
	}
	r.transitionInputs[path] = nativeTransitionInput{key: key, digest: sha256.Sum256(data)}
	return property, path
}

func TestLiveTransitionNativePayload(t *testing.T) {
	r := newLiveRun(t)
	key := r.create(t, "native transition payload")
	discover := func() (string, string, string) {
		t.Helper()
		current := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(key), "--query=fields=status")
		var issue struct {
			Fields struct{ Status struct{ ID string } }
		}
		if json.Unmarshal(current.Response.JSON, &issue) != nil || issue.Fields.Status.ID == "" {
			t.Fatal("native transition fixture has no independently read current status")
		}
		read := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(key)+"/transitions", "--query=expand=transitions.fields")
		var page struct {
			Transitions []struct {
				ID          string
				HasScreen   *bool `json:"hasScreen"`
				IsAvailable *bool `json:"isAvailable"`
				To          struct{ ID string }
			}
		}
		if json.Unmarshal(read.Response.JSON, &page) != nil || len(page.Transitions) == 0 {
			t.Fatal("native transition payload fixture has no independently discovered workflow")
		}
		for _, transition := range page.Transitions {
			if transition.HasScreen != nil && !*transition.HasScreen && (transition.IsAvailable == nil || *transition.IsAvailable) && transition.ID != "" && transition.To.ID != "" && transition.To.ID != issue.Fields.Status.ID {
				return transition.ID, transition.To.ID, issue.Fields.Status.ID
			}
		}
		t.Fatal("native screenless transition acceptance requires fresh screenless metadata")
		return "", "", ""
	}
	id, status, before := discover()
	for _, recipe := range []string{"field", "property"} {
		_, refusal := r.nativeTransitionInput(t, key, id, recipe)
		if err := r.checkMutation([]string{key}, []string{"issue", "transition", key, "--json-input", refusal}); err != nil {
			t.Fatal(err)
		}
		refused, _, exit := r.invoke(t, t.Context(), nil, "issue", "transition", key, "--json-input", refusal)
		if exit != 3 || refused.OK || len(refused.Data.Results) != 1 || refused.Data.Results[0].OK || len(refused.Errors) == 0 || refused.Errors[0].Code != "transition_screen_required" {
			t.Fatal("screenless native field request was accepted")
		}
		var refusedData struct{ Attempted, Applied bool }
		if json.Unmarshal(refused.Data.Results[0].Data, &refusedData) == nil && (refusedData.Attempted || refusedData.Applied) {
			t.Fatal("screenless native field refusal attempted a write")
		}
	}
	unchanged := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(key), "--query=fields=status")
	var refusedIssue struct {
		Fields struct{ Status struct{ ID string } }
	}
	if json.Unmarshal(unchanged.Response.JSON, &refusedIssue) != nil || refusedIssue.Fields.Status.ID != before {
		t.Fatal("native field/property refusals changed independently stored status")
	}
	_, path := r.nativeTransitionInput(t, key, id, "empty")
	local := r.success(t, "issue", "transition", key, "--json-input", path, "--dry-run")
	if !local.Data.DryRun || len(local.Data.Results) != 1 || !local.Data.Results[0].OK {
		t.Fatal("native transition local preview lost its target")
	}
	preview := r.success(t, "issue", "transition", key, "--json-input", path, "--dry-run", "--validate-remote")
	if !preview.Data.DryRun || len(preview.Data.Results) != 1 || !preview.Data.Results[0].OK {
		t.Fatal("native transition remote preview lost its target")
	}
	moved := r.mutate(t, []string{key}, "issue", "transition", key, "--json-input", path)
	input, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range []liveFrame{preview, moved} {
		var prepared struct{ Payload json.RawMessage }
		if len(frame.Data.Results) != 1 || json.Unmarshal(frame.Data.Results[0].Data, &prepared) != nil || string(prepared.Payload) != string(input) {
			t.Fatal("native transition preview/write changed the exact authored request")
		}
	}
	assertMove := func(frame liveFrame, status string) {
		t.Helper()
		var receipt struct {
			Attempted, Applied bool
			Payload            json.RawMessage
		}
		if len(frame.Data.Results) != 1 || !frame.Data.Results[0].OK || json.Unmarshal(frame.Data.Results[0].Data, &receipt) != nil || !receipt.Attempted || !receipt.Applied || len(receipt.Payload) == 0 {
			t.Fatal("native transition lost its prepared request or acknowledgement")
		}
		stored := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(key), "--query=fields=status")
		var issue struct {
			Fields struct{ Status struct{ ID string } }
		}
		if json.Unmarshal(stored.Response.JSON, &issue) != nil || issue.Fields.Status.ID != status {
			t.Fatal("native transition acknowledgement differs from independently stored status")
		}
	}
	assertMove(moved, status)
	changelog := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(key)+"/changelog")
	var history struct {
		Values []struct {
			HistoryMetadata struct{ Type, Description string } `json:"historyMetadata"`
			Items           []struct{ Field, To string }
		}
	}
	if json.Unmarshal(changelog.Response.JSON, &history) != nil {
		t.Fatal("native transition history could not be decoded")
	}
	found := false
	for _, entry := range history.Values {
		for _, item := range entry.Items {
			if item.Field == "status" && item.To == status && entry.HistoryMetadata.Type == "cli.acceptance" && entry.HistoryMetadata.Description == r.manifest.RunID {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("native transition did not preserve its independently stored history metadata")
	}
	id, status, _ = discover()
	_, commentPath := r.nativeTransitionInput(t, key, id, "comment")
	commentMove := r.mutate(t, []string{key}, "issue", "transition", key, "--json-input", commentPath)
	assertMove(commentMove, status)
	var commentData struct {
		CommentID     string `json:"comment_id"`
		CommentPosted string `json:"comment_posted"`
	}
	if json.Unmarshal(commentMove.Data.Results[0].Data, &commentData) != nil || commentData.CommentID == "" || commentData.CommentPosted != "separate" {
		t.Fatal("native convenience comment did not identify its separate resource")
	}
	r.registerComment(t, key, commentData.CommentID)
	comment := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(key)+"/comment/"+url.PathEscape(commentData.CommentID))
	var posted struct{ Body json.RawMessage }
	if json.Unmarshal(comment.Response.JSON, &posted) != nil || adfText(t, posted.Body) != r.manifest.RunID+" native transition comment" {
		t.Fatal("native transition convenience comment differs from independently stored body")
	}
}

func TestLiveNativeTransitionGuardBindsInputAndOwnedTarget(t *testing.T) {
	t.Parallel()
	r := &liveRun{verified: true, manifest: liveManifest{Issues: []ownedIssue{{Key: "KEY-1", ID: "10001"}, {Key: "KEY-2", ID: "10002"}}}}
	data := []byte(`{"transition":{"id":"31"},"fields":{},"update":{}}`)
	path := filepath.Join(t.TempDir(), "transition.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	r.transitionInputs = map[string]nativeTransitionInput{path: {key: "KEY-1", digest: sha256.Sum256(data)}}
	valid := []string{"issue", "transition", "KEY-1", "--json-input", path}
	if err := r.checkMutation([]string{"KEY-1"}, valid); err != nil {
		t.Fatalf("registered owned transition payload refused: %v", err)
	}
	for name, args := range map[string][]string{
		"another owned target": {"issue", "transition", "KEY-2", "--json-input", path},
		"unregistered input":   {"issue", "transition", "KEY-1", "--json-input", path + "-other"},
		"extra target":         {"issue", "transition", "KEY-1", "KEY-2", "--json-input", path},
		"selector":             append(append([]string{}, valid...), "--jql=project=KEY"),
		"target override":      append(append([]string{}, valid...), "--transition=41"),
	} {
		t.Run(name, func(t *testing.T) {
			if r.checkMutation([]string{"KEY-1"}, args) == nil {
				t.Fatal("registered transition input opened another mutation route")
			}
		})
	}
	if err := os.WriteFile(path, []byte(`{"transition":{"id":"41"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if r.checkMutation([]string{"KEY-1"}, valid) == nil {
		t.Fatal("modified transition input retained live write authorization")
	}
}
