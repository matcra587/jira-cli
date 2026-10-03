package jira

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestCommentKeysMatchStructTags fences the hand-maintained pairing in
// comment.go: the declared-key list must exactly mirror the struct's
// JSON tags, or raw capture would either duplicate a declared field or
// drop an undeclared one — the same fence fields_test.go keeps for
// IssueFields.
func TestCommentKeysMatchStructTags(t *testing.T) {
	var tags []string
	for field := range reflect.TypeFor[Comment]().Fields() {
		tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if tag == "-" {
			continue
		}
		tags = append(tags, tag)
	}
	slices.Sort(tags)

	keys := slices.Clone(commentKeys)
	slices.Sort(keys)
	if !slices.Equal(tags, keys) {
		t.Errorf("commentKeys = %q, struct tags = %q", keys, tags)
	}
}

// TestCommentUnmodeledFieldSurvivesRoundTrip pins the raw-capture
// guarantee on the comment model: a field this package does not
// declare (visibility, in this fixture) must come back byte-equivalent
// after an unmarshal/marshal round trip.
func TestCommentUnmodeledFieldSurvivesRoundTrip(t *testing.T) {
	doc := `{"id":"10001","body":{"type":"doc"},"visibility":{"type":"role","value":"Administrators"}}`

	var c Comment
	if err := json.Unmarshal([]byte(doc), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var want, got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(doc), &want); err != nil {
		t.Fatalf("decode original: %v", err)
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode round-tripped: %v", err)
	}
	if string(got["visibility"]) != string(want["visibility"]) {
		t.Errorf("unmodeled visibility = %s, want byte-equivalent %s", got["visibility"], want["visibility"])
	}
}

// TestCommentBodyPreservesADFBytes pins that a comment's ADF body
// survives the wire tier byte-for-byte — the same HTML-escaping trap
// IssueFields.Description guards against (marshalNoEscape).
//
// Calls MarshalJSON directly rather than the package-level json.Marshal:
// encoding/json re-escapes a nested Marshaler's OWN output when a
// top-level Marshal call processes it (fields.go's marshalNoEscape doc
// comment: "escaping only ever goes forward"), so a top-level
// json.Marshal(c) here would re-introduce the very escapes this method
// exists to avoid — that re-escaping happens further up the real output
// pipeline's own non-escaping encoder, not inside this test.
func TestCommentBodyPreservesADFBytes(t *testing.T) {
	doc := `{"id":"1","body":{"type":"doc","content":[{"type":"text","text":"a<b>&c"}]}}`

	var c Comment
	if err := json.Unmarshal([]byte(doc), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := c.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `a<b>&c`) {
		t.Errorf("marshal = %s, want the ADF text unescaped", out)
	}
}

// TestUpdateCommentRequestVisibilityIsThreeStates is why this type
// exists at all. A PUT REPLACES the comment, so an absent visibility
// means "leave the current restriction alone" — which makes removing one
// a third state that a `*CommentVisibility` with omitempty cannot spell.
//
// The three payloads have to be genuinely different on the wire, because
// the two that look alike (nil and cleared) are the two whose confusion
// leaks a restricted comment or silently keeps a restriction the caller
// asked to drop.
func TestUpdateCommentRequestVisibilityIsThreeStates(t *testing.T) {
	body := json.RawMessage(`{"type":"doc","version":1,"content":[]}`)
	tests := []struct {
		name string
		req  UpdateCommentRequest
		want string
	}{
		{
			name: "absent leaves the current restriction alone",
			req:  UpdateCommentRequest{Body: body},
			want: `{"body":{"type":"doc","version":1,"content":[]}}`,
		},
		{
			name: "an object applies a restriction",
			req:  UpdateCommentRequest{Body: body, Visibility: &CommentVisibility{Type: "group", Value: "developers"}},
			want: `{"body":{"type":"doc","version":1,"content":[]},"visibility":{"type":"group","value":"developers"}}`,
		},
		{
			name: "an explicit null removes it",
			req:  UpdateCommentRequest{Body: body, ClearVisibility: true},
			want: `{"body":{"type":"doc","version":1,"content":[]},"visibility":null}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.req.MarshalJSON()
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if got := strings.TrimSpace(string(out)); got != tt.want {
				t.Errorf("marshal = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestUpdateCommentRequestPreservesADFBytes is AddCommentRequest's own
// guard applied to the edit direction: the document Jira stores must be
// the one the caller composed, or --dry-run previewed something else.
func TestUpdateCommentRequestPreservesADFBytes(t *testing.T) {
	req := UpdateCommentRequest{Body: json.RawMessage(`{"type":"doc","content":[{"type":"text","text":"a<b>&c"}]}`)}
	out, err := req.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `a<b>&c`) {
		t.Errorf("marshal = %s, want the ADF text unescaped", out)
	}
}
