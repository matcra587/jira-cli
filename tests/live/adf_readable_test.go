//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

// Native storage is authored independently of either projection or renderer.
// This qualifies reads and terminal projection, not concurrent editor writes.
func TestLiveADFReadableRendering(t *testing.T) {
	r := newLiveRun(t)
	identity := rawRead(t, r, "api", "get", "/rest/api/3/myself")
	var account struct {
		AccountID   string `json:"accountId"`
		DisplayName string `json:"displayName"`
	}
	if err := json.Unmarshal(identity.Response.JSON, &account); err != nil || account.AccountID == "" || account.DisplayName == "" {
		t.Fatal("independent account identity unavailable", err)
	}
	key := r.create(t, "native-readable-rendering")
	expected := []byte(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"mention","attrs":{"id":"account-test","text":"@Alex","accessLevel":""}},{"type":"text","text":" "},{"type":"emoji","attrs":{"shortName":":smile:","text":"🙂"}}]},{"type":"expand","attrs":{"title":"Details"},"content":[{"type":"paragraph","content":[{"type":"text","text":"alpha"}]},{"type":"nestedExpand","attrs":{"title":"More"},"content":[{"type":"paragraph","content":[{"type":"text","text":"omega"}]}]}]},{"type":"layoutSection","content":[{"type":"layoutColumn","attrs":{"width":50},"content":[{"type":"paragraph","content":[{"type":"text","text":"left"}]}]},{"type":"layoutColumn","attrs":{"width":50},"content":[{"type":"paragraph","content":[{"type":"text","text":"right"}]}]}]},{"type":"taskList","attrs":{"localId":"tasks"},"content":[{"type":"taskItem","attrs":{"localId":"todo","state":"TODO"},"content":[{"type":"text","text":"pending"}]},{"type":"taskItem","attrs":{"localId":"done","state":"DONE"},"content":[{"type":"text","text":"completed"}]}]},{"type":"decisionList","attrs":{"localId":"decisions"},"content":[{"type":"decisionItem","attrs":{"localId":"choice","state":"DECIDED"},"content":[{"type":"text","text":"chosen"}]}]}]}`)
	// Jira resolves mention labels from the referenced public account. Author
	// that known label and its observed accessLevel default before storage.
	quotedID, err := json.Marshal(account.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	expected = bytes.Replace(expected, []byte(`"account-test"`), quotedID, 1)
	mentionLabel := "@" + account.DisplayName
	quotedLabel, err := json.Marshal(mentionLabel)
	if err != nil {
		t.Fatal(err)
	}
	expected = bytes.Replace(expected, []byte(`"@Alex"`), quotedLabel, 1)

	if err := adf.Validate(expected); err != nil {
		t.Fatal("invalid independently authored rich fixture", err)
	}
	endpoint, path := r.rawDescriptionInput(t, key, expected)
	updated, frame, exit := rawMutation(t, r, key, nil, "api", "put", endpoint, "--input", path, "--force")
	requireRawSuccess(t, updated, frame, exit, 204)
	native := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=description")
	var issue struct {
		Fields struct{ Description json.RawMessage }
	}
	if json.Unmarshal(native.Response.JSON, &issue) != nil {
		t.Fatal("raw native description is unavailable")
	}
	nativeEqual, nativeErr := adf.StructuralEqual(expected, issue.Fields.Description)
	if nativeErr != nil || !nativeEqual {
		t.Fatal("raw native description differs from independently authored expectation")
	}
	assertStoredPreconditionBody(t, r, key, "", "description", expected)

	posted := r.mutate(t, []string{key}, "issue", "comment", "add", key, "--json-input", r.nativeBody(t, expected))
	id := postedADFCommentID(t, posted)
	r.registerComment(t, key, id)
	stored := rawOwnedComment(t, r, key, id)
	equal, err := adf.StructuralEqual(expected, stored.Body)
	if err != nil || !equal {
		t.Fatal("native rich comment changed in storage")
	}
	input := r.nativeBody(t, stored.Body)
	for _, args := range [][]string{
		{"adf", "render", "--input", input},
		{"issue", "view", key, "--comments=0"},
		{"issue", "comment", "list", key, "--all"},
	} {
		text := r.richHumanRead(t, args...)
		for _, want := range []string{mentionLabel, "🙂", "Details", "alpha", "More", "omega", "Column 1 (50%)", "left", "Column 2 (50%)", "right", "[ ] pending", "[x] completed", "Decision (DECIDED)", "chosen"} {
			if !strings.Contains(text, want) {
				t.Fatalf("rich human read lost %q", want)
			}
		}
		if strings.ContainsAny(text, "\x1b\a") || strings.Contains(text, "[expand —") || strings.Contains(text, "[taskList —") || strings.Contains(text, "[mention —") {
			t.Fatal("human read emitted controls or deferred a supported rich node")
		}
	}
	// Rendering cannot rewrite either its input or the native remote source.
	local, err := os.ReadFile(input)
	if err != nil || string(local) != string(stored.Body) {
		t.Fatal("human rendering rewrote native input")
	}
	assertStoredPreconditionBody(t, r, key, id, "description", expected)
	retained := rawOwnedComment(t, r, key, id)
	equal, err = adf.StructuralEqual(expected, retained.Body)
	if err != nil || !equal {
		t.Fatal("human rendering altered native comment storage")
	}
}
