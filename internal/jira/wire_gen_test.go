package jira

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// tagsOf returns the JSON tag names declared on v's struct fields, skipping
// the "-" (Extra) field — the same extraction TestIssueFieldsKeysMatchStructTags
// (fields_test.go) uses for the hand-curated model.
func tagsOf(v any) []string {
	var tags []string
	for field := range reflect.TypeOf(v).Fields() {
		tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if tag == "-" {
			continue
		}
		tags = append(tags, tag)
	}
	return tags
}

// TestWireGenKeysMatchStructTags extends the hand-maintained-pairing fence
// IssueFields already has to every gen-wire type in wire_gen.go: the
// package-level `xxxKeys` slice each type's MarshalJSON/UnmarshalJSON pass
// to marshalMergeExtra/unmarshalKeepExtra must exactly mirror the struct's
// own JSON tags, or raw capture either duplicates a declared field on
// remarshal or lets an undeclared one leak into Extra. This is a distinct
// check from spec/drift_test.go's TestWireModelMatchesPlatformSpec, which
// only compares the Keys slice against the vendored spec — never against
// the struct tags actually driving encoding/json — so the two can drift
// independently on a hand-edit.
func TestWireGenKeysMatchStructTags(t *testing.T) {
	tests := []struct {
		name string
		typ  any
		keys []string
	}{
		{"issue bean", IssueBean{}, issueBeanKeys},
		{"status details", StatusDetails{}, statusDetailsKeys},
		{"status category", StatusCategory{}, statusCategoryKeys},
		{"scope", Scope{}, scopeKeys},
		{"project details", ProjectDetails{}, projectDetailsKeys},
		{"updated project category", UpdatedProjectCategory{}, updatedProjectCategoryKeys},
		{"avatar urls bean", AvatarUrlsBean{}, avatarUrlsBeanKeys},
		{"user details", UserDetails{}, userDetailsKeys},
		{"issue type details", IssueTypeDetails{}, issueTypeDetailsKeys},
		{"priority", Priority{}, priorityKeys},
		{"resolution", Resolution{}, resolutionKeys},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags := tagsOf(tt.typ)
			slices.Sort(tags)
			keys := slices.Clone(tt.keys)
			slices.Sort(keys)
			if !slices.Equal(tags, keys) {
				t.Errorf("%s: keys = %q, struct tags = %q", tt.name, keys, tags)
			}
		})
	}
}

// TestWireGenRoundTrip pins construct/marshal/unmarshal round-trip behavior
// for the generated wire types whose Marshal/UnmarshalJSON were previously
// unexercised: realistic payloads with a known value, an unmodeled field
// (Extra capture), and — per type — the nested-Nullable or odd-tag-name
// case that type actually carries. Every case also asserts whole-document
// byte-equivalence per key, so a mis-keyed field silently rerouted through
// Extra (rather than the typed model) would still be caught by the direct
// field assertions below even though the raw bytes alone would round-trip
// clean.
func TestWireGenRoundTrip(t *testing.T) {
	issueBean := &IssueBean{}
	scope := &Scope{}
	project := &ProjectDetails{}
	category := &UpdatedProjectCategory{}
	avatars := &AvatarUrlsBean{}
	issueType := &IssueTypeDetails{}
	priority := &Priority{}
	resolution := &Resolution{}

	tests := []struct {
		name  string
		value interface {
			json.Marshaler
			json.Unmarshaler
		}
		doc string
		// nested lists top-level keys whose value is itself a known
		// (typed, not Extra) nested struct: encoding/json marshals a
		// struct's fields in declaration order, not the input document's
		// order, so these legitimately reorder on round-trip and are
		// excluded from the byte-exact per-key check below — the check
		// funcs assert their decoded values field-by-field instead.
		nested []string
		check  func(t *testing.T)
	}{
		{
			name:  "issue bean",
			value: issueBean,
			doc: `{"id":"10001","key":"ROCK-1","self":"https://x.atlassian.net/rest/api/3/issue/10001",` +
				`"fields":{"summary":"Fix bug","assignee":null,"customfield_10099":{"deep":{"flag":true}}},` +
				`"futureTopLevelField":["a",1,null]}`,
			nested: []string{"fields"},
			check: func(t *testing.T) {
				if issueBean.ID.Value != "10001" || issueBean.Key.Value != "ROCK-1" {
					t.Errorf("id/key = %q/%q, want 10001/ROCK-1", issueBean.ID.Value, issueBean.Key.Value)
				}
				if !issueBean.Fields.Set || issueBean.Fields.Null {
					t.Fatalf("fields = %+v, want present", issueBean.Fields)
				}
				if issueBean.Fields.Value.Summary.Value != "Fix bug" {
					t.Errorf("fields.summary = %q, want %q", issueBean.Fields.Value.Summary.Value, "Fix bug")
				}
				if !issueBean.Fields.Value.Assignee.Set || !issueBean.Fields.Value.Assignee.Null {
					t.Errorf("fields.assignee = %+v, want explicit null", issueBean.Fields.Value.Assignee)
				}
				if _, ok := issueBean.Fields.Value.Extra["customfield_10099"]; !ok {
					t.Error("nested fields.customfield_10099 did not survive into the inner Extra")
				}
				if _, ok := issueBean.Extra["futureTopLevelField"]; !ok {
					t.Error("top-level futureTopLevelField did not survive into the outer Extra")
				}
				if _, ok := issueBean.Extra["fields"]; ok {
					t.Error("declared field 'fields' leaked into the outer Extra")
				}
			},
		},
		{
			name:   "scope",
			value:  scope,
			doc:    `{"type":"PROJECT","project":{"id":"10000","key":"ROCK"},"futureField":true}`,
			nested: []string{"project"},
			check: func(t *testing.T) {
				if scope.Type.Value != "PROJECT" {
					t.Errorf("type = %q, want PROJECT", scope.Type.Value)
				}
				if scope.Project.Value.ID.Value != "10000" || scope.Project.Value.Key.Value != "ROCK" {
					t.Errorf("project = %+v, want id=10000 key=ROCK", scope.Project.Value)
				}
				if _, ok := scope.Extra["futureField"]; !ok {
					t.Error("futureField did not survive into Extra")
				}
			},
		},
		{
			name:  "project details",
			value: project,
			doc: `{"id":"10000","key":"ROCK","name":"Rocket","avatarUrls":{"48x48":"https://x/avatar48.png"},` +
				`"simplified":false,"futureField":"x"}`,
			nested: []string{"avatarUrls"},
			check: func(t *testing.T) {
				if project.Name.Value != "Rocket" {
					t.Errorf("name = %q, want Rocket", project.Name.Value)
				}
				if !project.Simplified.Set || project.Simplified.Value {
					t.Errorf("simplified = %+v, want explicit false", project.Simplified)
				}
				if project.AvatarURLs.Value.Field48x48.Value != "https://x/avatar48.png" {
					t.Errorf("avatarUrls.48x48 = %q, want the icon url", project.AvatarURLs.Value.Field48x48.Value)
				}
				if _, ok := project.Extra["futureField"]; !ok {
					t.Error("futureField did not survive into Extra")
				}
			},
		},
		{
			name:  "updated project category",
			value: category,
			doc:   `{"id":"1","name":"cat","description":"d","futureField":1}`,
			check: func(t *testing.T) {
				if category.Name.Value != "cat" || category.Description.Value != "d" {
					t.Errorf("name/description = %q/%q, want cat/d", category.Name.Value, category.Description.Value)
				}
				if _, ok := category.Extra["futureField"]; !ok {
					t.Error("futureField did not survive into Extra")
				}
			},
		},
		{
			name:  "avatar urls bean",
			value: avatars,
			doc:   `{"16x16":"a","24x24":"b","32x32":"c","48x48":"d","futureField":"z"}`,
			check: func(t *testing.T) {
				got := [4]string{avatars.Field16x16.Value, avatars.Field24x24.Value, avatars.Field32x32.Value, avatars.Field48x48.Value}
				want := [4]string{"a", "b", "c", "d"}
				if got != want {
					t.Errorf("avatar sizes = %v, want %v", got, want)
				}
				if _, ok := avatars.Extra["futureField"]; !ok {
					t.Error("futureField did not survive into Extra")
				}
			},
		},
		{
			name:  "issue type details",
			value: issueType,
			doc: `{"id":"10001","name":"Bug","subtask":false,` +
				`"scope":{"type":"PROJECT","project":{"id":"10000","key":"ROCK"}},"futureField":true}`,
			nested: []string{"scope"},
			check: func(t *testing.T) {
				if issueType.Name.Value != "Bug" {
					t.Errorf("name = %q, want Bug", issueType.Name.Value)
				}
				if !issueType.Subtask.Set || issueType.Subtask.Value {
					t.Errorf("subtask = %+v, want explicit false", issueType.Subtask)
				}
				if issueType.Scope.Value.Type.Value != "PROJECT" {
					t.Errorf("scope.type = %q, want PROJECT", issueType.Scope.Value.Type.Value)
				}
				if issueType.Scope.Value.Project.Value.Key.Value != "ROCK" {
					t.Errorf("scope.project.key = %q, want ROCK (3 levels deep)", issueType.Scope.Value.Project.Value.Key.Value)
				}
				if _, ok := issueType.Extra["futureField"]; !ok {
					t.Error("futureField did not survive into Extra")
				}
			},
		},
		{
			name:  "priority",
			value: priority,
			doc:   `{"id":"3","name":"Medium","iconUrl":"https://x/icon.png","futureField":42}`,
			check: func(t *testing.T) {
				if priority.Name.Value != "Medium" || priority.IconURL.Value != "https://x/icon.png" {
					t.Errorf("name/iconUrl = %q/%q, want Medium/https://x/icon.png", priority.Name.Value, priority.IconURL.Value)
				}
				if _, ok := priority.Extra["futureField"]; !ok {
					t.Error("futureField did not survive into Extra")
				}
			},
		},
		{
			name:  "resolution",
			value: resolution,
			doc:   `{"id":"1","name":"Fixed","futureField":null}`,
			check: func(t *testing.T) {
				if resolution.Name.Value != "Fixed" {
					t.Errorf("name = %q, want Fixed", resolution.Name.Value)
				}
				v, ok := resolution.Extra["futureField"]
				if !ok {
					t.Fatal("futureField did not survive into Extra")
				}
				if string(v) != "null" {
					t.Errorf("futureField = %s, want literal null preserved verbatim", v)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(tt.doc), tt.value); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if tt.check != nil {
				tt.check(t)
			}

			out, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var want, got map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tt.doc), &want); err != nil {
				t.Fatalf("decode original: %v", err)
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("decode round-tripped: %v", err)
			}
			if len(got) != len(want) {
				t.Errorf("round-tripped %d top-level keys, want %d\n got=%s\nwant=%s", len(got), len(want), out, tt.doc)
			}
			for k, v := range want {
				if slices.Contains(tt.nested, k) {
					continue
				}
				if string(got[k]) != string(v) {
					t.Errorf("field %q = %s, want byte-equivalent %s", k, got[k], v)
				}
			}
		})
	}
}
