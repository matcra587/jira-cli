package jira

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestSearchResultsKeysMatchStructTags fences the hand-maintained pairing
// in search.go: the declared-key list must exactly mirror the struct's
// JSON tags, or raw capture would either duplicate a declared field or
// drop an undeclared one — the same fence comment_test.go and
// fields_test.go keep for their own types.
func TestSearchResultsKeysMatchStructTags(t *testing.T) {
	var tags []string
	for field := range reflect.TypeFor[SearchResults]().Fields() {
		tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if tag == "-" {
			continue
		}
		tags = append(tags, tag)
	}
	slices.Sort(tags)

	keys := slices.Clone(searchResultsKeys)
	slices.Sort(keys)
	if !slices.Equal(tags, keys) {
		t.Errorf("searchResultsKeys = %q, struct tags = %q", keys, tags)
	}
}

// TestSearchResultsUnmodeledFieldSurvivesRoundTrip pins the raw-capture
// guarantee: warnings (and any other field this package does not
// declare) must come back byte-equivalent after an unmarshal/marshal
// round trip rather than vanish.
func TestSearchResultsUnmodeledFieldSurvivesRoundTrip(t *testing.T) {
	doc := `{"isLast":true,"issues":[],"names":{"summary":"Summary"},"warnings":["result set truncated"]}`

	var r SearchResults
	if err := json.Unmarshal([]byte(doc), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(r)
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
	for _, field := range []string{"names", "warnings"} {
		if string(got[field]) != string(want[field]) {
			t.Errorf("unmodeled %q = %s, want byte-equivalent %s", field, got[field], want[field])
		}
	}
}

// TestSearchResultsNextPageTokenAbsentVsNull pins the two-state
// representation search.go's doc comment argues for: an ABSENT
// nextPageToken and an explicitly NULL one must both decode to "no
// next page" (Value == "" either way) while staying told apart in
// Set/Null, so a caller can tell "no more pages" from "the server
// forgot to say" without this wire type collapsing the distinction
// first.
func TestSearchResultsNextPageTokenAbsentVsNull(t *testing.T) {
	tests := []struct {
		name       string
		doc        string
		wantSet    bool
		wantNull   bool
		wantIsLast bool
	}{
		{
			name:       "token omitted entirely (GET form's documented behavior)",
			doc:        `{"isLast":true,"issues":[]}`,
			wantSet:    false,
			wantIsLast: true,
		},
		{
			name:       "token explicitly null (POST form's documented behavior)",
			doc:        `{"isLast":true,"issues":[],"nextPageToken":null}`,
			wantSet:    true,
			wantNull:   true,
			wantIsLast: true,
		},
		{
			name:       "token present means more pages",
			doc:        `{"isLast":false,"issues":[],"nextPageToken":"abc123"}`,
			wantSet:    true,
			wantNull:   false,
			wantIsLast: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r SearchResults
			if err := json.Unmarshal([]byte(tt.doc), &r); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if r.NextPageToken.Set != tt.wantSet {
				t.Errorf("NextPageToken.Set = %v, want %v", r.NextPageToken.Set, tt.wantSet)
			}
			if r.NextPageToken.Null != tt.wantNull {
				t.Errorf("NextPageToken.Null = %v, want %v", r.NextPageToken.Null, tt.wantNull)
			}
			if r.IsLast.Value != tt.wantIsLast {
				t.Errorf("IsLast = %v, want %v", r.IsLast.Value, tt.wantIsLast)
			}
		})
	}
}

// TestSearchResultsIssuesReuseIssueBean pins that a nested issue keeps
// IssueBean's own byte-fidelity guarantee: an ADF description inside a
// search result must survive unescaped, exactly as it does through
// GetIssue, because this type nests IssueBean rather than duplicating
// its shape.
func TestSearchResultsIssuesReuseIssueBean(t *testing.T) {
	doc := `{"isLast":true,"issues":[{"key":"ABC-1","fields":{"description":{"type":"doc","content":[{"type":"text","text":"a<b>&c"}]}}}]}`

	var r SearchResults
	if err := json.Unmarshal([]byte(doc), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(r.Issues.Value) != 1 || r.Issues.Value[0].Key.Value != "ABC-1" {
		t.Fatalf("issues = %+v, want one issue keyed ABC-1", r.Issues.Value)
	}
	out, err := r.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `a<b>&c`) {
		t.Errorf("marshal = %s, want the nested issue's ADF text unescaped", out)
	}
}

func TestSearchResultsDecodeReplacesReceiverAtomically(t *testing.T) {
	var page SearchResults
	first := []byte(`{"isLast":false,"issues":[{"key":"KEY-1"}],"nextPageToken":"old","future":9007199254740993}`)
	if err := json.Unmarshal(first, &page); err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"isLast":true,"issues":[{"key":false}],"future":null}`), &page); err == nil {
		t.Fatal("wrong key type accepted")
	}
	after, err := json.Marshal(page)
	if err != nil || string(after) != string(before) {
		t.Fatal("failed decode partially changed receiver")
	}
	if err := json.Unmarshal([]byte(`{"issues":[]}`), &page); err != nil {
		t.Fatal(err)
	}
	if page.IsLast.Set || page.NextPageToken.Set || len(page.Extra) != 0 || !page.Issues.Set || page.Issues.Null || page.Issues.Value == nil {
		t.Fatal("receiver retained earlier cursor/completion/extensions")
	}
}

func TestSearchPageRequiresPresentFactsAndIdentities(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		alter func(*SearchResults)
	}{
		{"missing final", func(p *SearchResults) { p.IsLast.Set = false }},
		{"null final", func(p *SearchResults) { p.IsLast.Null = true }},
		{"missing array", func(p *SearchResults) { p.Issues.Set = false }},
		{"null array", func(p *SearchResults) { p.Issues.Null = true }},
		{"nil array", func(p *SearchResults) { p.Issues.Value = nil }},
		{"missing key", func(p *SearchResults) { p.Issues.Value[0].Key.Set = false }},
		{"null key", func(p *SearchResults) { p.Issues.Value[0].Key.Null = true }},
		{"blank key", func(p *SearchResults) { p.Issues.Value[0].Key.Value = " " }},
		{"duplicate key", func(p *SearchResults) { p.Issues.Value = append(p.Issues.Value, p.Issues.Value[0]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := SearchResults{IsLast: NullableOf(true), Issues: NullableOf([]IssueBean{{Key: NullableOf("KEY-1")}})}
			tc.alter(&p)
			if err := p.ValidatePage(2); err == nil {
				t.Fatal("page accepted without explicit facts or unique identity")
			}
		})
	}
	var absent *SearchResults
	if absent.ValidatePage(2) == nil {
		t.Fatal("nil page accepted")
	}
	valid := SearchResults{IsLast: NullableOf(true), Issues: NullableOf([]IssueBean{{Key: NullableOf("KEY-1")}, {Key: NullableOf("KEY-2")}})}
	if valid.ValidatePage(1) == nil || valid.ValidatePage(0) == nil {
		t.Fatal("requested count ignored")
	}
	if valid.ValidatePage(2) != nil {
		t.Fatal("valid key-only page refused")
	}
}
