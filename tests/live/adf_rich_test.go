//go:build live

package live

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/adf"
)

// Native storage is authored independently of either projection or renderer.
// This qualifies reads and terminal projection, not concurrent editor writes.
func TestLiveADFRichRendering(t *testing.T) {
	r := newLiveRun(t)
	key := r.create(t, "native-rich-rendering")
	expected := []byte(`{"type":"doc","version":1,"content":[{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":"Review required"},{"type":"hardBreak"},{"type":"status","attrs":{"text":"Ready","color":"#aa55cc"}}]}]},{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableCell","attrs":{"rowspan":2},"content":[{"type":"paragraph","content":[{"type":"text","text":"left"}]}]},{"type":"tableCell","attrs":{"colspan":2},"content":[{"type":"paragraph","content":[{"type":"text","text":"across"}]}]}]},{"type":"tableRow","content":[{"type":"tableCell","attrs":{},"content":[{"type":"paragraph","content":[{"type":"text","text":"東京"}]}]},{"type":"tableCell","attrs":{},"content":[{"type":"paragraph","content":[{"type":"text","text":"right"}]}]}]}]}]}`)
	if err := adf.Validate(expected); err != nil {
		t.Fatal("invalid independently authored rich fixture", err)
	}
	endpoint, path := r.rawDescriptionInput(t, key, expected)
	updated, frame, exit := rawMutation(t, r, key, nil, "api", "put", endpoint, "--input", path, "--force")
	requireRawSuccess(t, updated, frame, exit, 204)
	assertStoredPreconditionBody(t, r, key, "", "description", expected)
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
		for _, want := range []string{"Warning", "Review required", "Ready", "left", "across", "東京", "right", "│"} {
			if !strings.Contains(text, want) {
				t.Fatalf("rich human read lost %q", want)
			}
		}
		if strings.ContainsAny(text, "\x1b\a") || strings.Contains(text, "[table —") || strings.Contains(text, "[panel —") || strings.Contains(text, "[status —") {
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

// The standard live invoker requires one JSON envelope. Human rendering needs
// its own read-only boundary while retaining the same keyring and evidence policy.
func (r *liveRun) richHumanRead(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.binary, append(args, "--output=human", "--color=never", "--no-input", "--timeout=30s")...)
	cmd.Env = append(os.Environ(), "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "NO_COLOR=1", "TERM=dumb", "JIRA_BASE_URL="+r.manifest.BaseURL, "JIRA_READ_ONLY=true")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r.sequence++
	for suffix, data := range map[string][]byte{"stdout": stdout.Bytes(), "stderr": stderr.Bytes()} {
		if writeErr := os.WriteFile(filepath.Join(r.dir, fmt.Sprintf("%03d-human-%s.txt", r.sequence, suffix)), data, 0o600); writeErr != nil {
			t.Error("save human receipt", writeErr)
		}
	}
	if err != nil {
		t.Fatal("human native read failed", err)
	}
	return stdout.String()
}
