package issue

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
)

// `issue link create --json-input` exists for ONE member of the link body:
// the comment, which the flag surface cannot express. Everything else in a
// link payload is composed by the command, so these tests are mostly about
// what the route REFUSES — a document that names an end or a type is a
// caller believing something about precedence that is not true.

// linkPayloadFile writes a link payload to a temp file and returns its
// path.
func linkPayloadFile(t *testing.T, payload string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "link.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	return path
}

// linkCreateOptsWithPayload is linkCreateOpts plus the ADF route.
func linkCreateOptsWithPayload(typeInput, path string) linkCreateOptions {
	opts := linkCreateOpts(typeInput)
	opts.jsonInput = path
	return opts
}

const linkCommentADF = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Blocked on the migration"}]}]}`

// TestLinkCreateJSONInputCarriesTheComment is the route's whole purpose:
// a comment reaches the link request, in the same call, carrying the ADF
// bytes the caller wrote.
func TestLinkCreateJSONInputCarriesTheComment(t *testing.T) {
	fake := &fakeLinkWriteClient{types: corpusLinkTypes}
	path := linkPayloadFile(t, `{"comment":{"body":`+linkCommentADF+`}}`)

	if _, err := runLinkCreate(t.Context(), linkWriteDeps(fake),
		invocation(false, "KEY-123"), linkCreateOptsWithPayload("blocks", path)); err != nil {
		t.Fatalf("runLinkCreate: %v", err)
	}

	if len(fake.links) != 1 {
		t.Fatalf("made %d links, want one", len(fake.links))
	}
	got := fake.links[0]
	if got.Comment == nil {
		t.Fatal("the link request carried no comment; --json-input exists for exactly that member")
	}
	// Compared as decoded JSON rather than bytes: what must survive is the
	// document, not the file's whitespace.
	var want, have any
	if err := json.Unmarshal([]byte(linkCommentADF), &want); err != nil {
		t.Fatalf("decode want: %v", err)
	}
	if err := json.Unmarshal(got.Comment.Body, &have); err != nil {
		t.Fatalf("decode comment body: %v", err)
	}
	if !reflect.DeepEqual(want, have) {
		t.Errorf("comment body = %s, want %s", got.Comment.Body, linkCommentADF)
	}

	// The link itself is composed exactly as it is without a payload: the
	// document must not disturb the direction mapping.
	if got.InwardIssue.Key != "KEY-123" || got.OutwardIssue.Key != "KEY-456" {
		t.Errorf("ends = %s/%s, want KEY-123/KEY-456 — a comment must not move them",
			got.InwardIssue.Key, got.OutwardIssue.Key)
	}
	if got.Type.Name != "Blocks" {
		t.Errorf("type = %q, want Blocks", got.Type.Name)
	}
}

// TestLinkCreateJSONInputRefusesOwnedMembers pins the refusal for every
// member the command composes itself.
//
// Refused rather than overridden, because a caller who wrote
// `"type": "Relates"` into the document while passing `--type blocks`
// believes one of them wins, and there is no answer to that which is not a
// surprise to somebody.
func TestLinkCreateJSONInputRefusesOwnedMembers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		member  string
	}{
		{"the inward end", `{"inwardIssue":{"key":"KEY-999"},"comment":{"body":{}}}`, "inwardIssue"},
		{"the outward end", `{"outwardIssue":{"key":"KEY-999"},"comment":{"body":{}}}`, "outwardIssue"},
		{"the type", `{"type":{"name":"Relates"},"comment":{"body":{}}}`, "type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeLinkWriteClient{types: corpusLinkTypes}

			_, err := runLinkCreate(t.Context(), linkWriteDeps(fake),
				invocation(false, "KEY-123"), linkCreateOptsWithPayload("blocks", linkPayloadFile(t, tc.payload)))
			if err == nil {
				t.Fatalf("a document naming %q must be refused", tc.member)
			}
			if !strings.Contains(err.Error(), tc.member) {
				t.Errorf("refusal %q does not name the offending member", err)
			}
			if len(fake.links) != 0 {
				t.Errorf("made %d links despite refusing the input", len(fake.links))
			}
		})
	}
}

// TestLinkCreateJSONInputRefusesADocumentThatSetsNothing pins that an
// unusable document is a caller error rather than a silent no-op.
//
// They meant something by passing the file. Ignoring it would let a
// misspelled member — `comments`, say — look like a successful run that
// posted no comment.
func TestLinkCreateJSONInputRefusesADocumentThatSetsNothing(t *testing.T) {
	fake := &fakeLinkWriteClient{types: corpusLinkTypes}

	_, err := runLinkCreate(t.Context(), linkWriteDeps(fake),
		invocation(false, "KEY-123"), linkCreateOptsWithPayload("blocks", linkPayloadFile(t, `{"comments":{"body":{}}}`)))
	if err == nil {
		t.Fatal("a document setting nothing this command uses must be refused")
	}
	if !strings.Contains(err.Error(), "comment") {
		t.Errorf("refusal %q does not say what the document is for", err)
	}
}

// TestLinkCreateJSONInputRefusesACommentWithoutABody pins the one shape
// Jira itself rejects, caught here so it fails as the input it is rather
// than as a 400 about a payload the caller half-wrote.
func TestLinkCreateJSONInputRefusesACommentWithoutABody(t *testing.T) {
	fake := &fakeLinkWriteClient{types: corpusLinkTypes}

	_, err := runLinkCreate(t.Context(), linkWriteDeps(fake),
		invocation(false, "KEY-123"), linkCreateOptsWithPayload("blocks", linkPayloadFile(t, `{"comment":{}}`)))
	if err == nil {
		t.Fatal("a comment with no body must be refused")
	}
	if !strings.Contains(err.Error(), "body") {
		t.Errorf("refusal %q does not name the missing member", err)
	}
	if len(fake.links) != 0 {
		t.Error("nothing must be written when the input is refused")
	}
}

// TestLinkCreateJSONInputMalformedIsACallerError pins that a broken
// document names the flag that carried it.
func TestLinkCreateJSONInputMalformedIsACallerError(t *testing.T) {
	fake := &fakeLinkWriteClient{types: corpusLinkTypes}

	_, err := runLinkCreate(t.Context(), linkWriteDeps(fake),
		invocation(false, "KEY-123"), linkCreateOptsWithPayload("blocks", linkPayloadFile(t, `{"comment":`)))
	if err == nil {
		t.Fatal("a malformed document must be refused")
	}
	if !strings.Contains(err.Error(), command.FlagJSONInput) {
		t.Errorf("refusal %q does not name --%s", err, command.FlagJSONInput)
	}
}

// TestLinkCreateJSONInputIsCheckedByTheBarePreview pins that --dry-run
// refuses a bad document too.
//
// The preview is where a caller checks their invocation, so refusing the
// input there is the whole point — and reading a FILE keeps the preview
// local, which the second assertion holds it to.
func TestLinkCreateJSONInputIsCheckedByTheBarePreview(t *testing.T) {
	asked := false
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) {
		asked = true
		return nil, errors.New("a bare preview must not build a client")
	}}

	_, err := runLinkCreate(t.Context(), deps, invocation(true, "KEY-123"),
		linkCreateOptsWithPayload("blocks", linkPayloadFile(t, `{"type":{"name":"Relates"}}`)))
	if err == nil {
		t.Fatal("a preview must refuse a document naming a member the command owns")
	}
	if !strings.Contains(err.Error(), "type") {
		t.Errorf("refusal %q does not name the offending member", err)
	}
	if asked {
		t.Error("the bare preview built a client; reading --json-input is a local file read")
	}
}
