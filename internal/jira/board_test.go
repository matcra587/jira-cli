package jira

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAgileRawCaptureKeysMatchTags(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		typeOf reflect.Type
		keys   []string
	}{
		{"page", reflect.TypeFor[BoardPage](), agilePageKeys},
		{"board", reflect.TypeFor[Board](), boardKeys},
		{"project", reflect.TypeFor[BoardProject](), boardProjectKeys},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var tags []string
			for field := range fixture.typeOf.Fields() {
				tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
				if tag != "-" {
					tags = append(tags, tag)
				}
			}
			keys := slices.Clone(fixture.keys)
			slices.Sort(tags)
			slices.Sort(keys)
			if !slices.Equal(tags, keys) {
				t.Fatalf("capture keys %v disagree with tags %v", keys, tags)
			}
		})
	}
}

func TestAgilePagePresenceAndRawFidelity(t *testing.T) {
	for _, total := range []string{"", `,"total":null`, `,"total":0`} {
		t.Run(total, func(t *testing.T) {
			doc := `{"isLast":true,"maxResults":50,"startAt":0,"values":[],"future":{"big":9007199254740993,"text":"<tag>\\u2028","states":[null,[],false,0]}` + total + `}`
			var page BoardPage
			if err := json.Unmarshal([]byte(doc), &page); err != nil {
				t.Fatal(err)
			}
			if page.Total.Set != (total != "") || page.Total.Null != (total == `,"total":null`) || !page.Values.Set || page.Values.Null || page.Values.Value == nil || !page.IsLast.Value {
				t.Fatalf("page presence collapsed: %+v", page)
			}
			encoded, err := MarshalNoEscape(page)
			if err != nil {
				t.Fatal(err)
			}
			var before, after map[string]json.RawMessage
			if json.Unmarshal([]byte(doc), &before) != nil || json.Unmarshal(encoded, &after) != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("page changed members or raw bytes: %s", encoded)
			}
		})
	}
	for _, body := range []string{`{}`, `{"values":null}`, `{"values":[]}`} {
		var page BoardPage
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatal(err)
		}
		encoded, err := MarshalNoEscape(page)
		if err != nil || string(encoded) != body {
			t.Fatalf("absent/null/empty values changed: %s %v", encoded, err)
		}
	}
}

func TestAgileRecordExtensionsAndNullableFields(t *testing.T) {
	boardJSON := `{"id":42,"name":"Sprint","type":null,"location":{"projectKey":"KEY"},"future":{"big":9007199254740993,"states":[null,[],false,0]}}`
	projectJSON := `{"id":"10042","key":"KEY","name":null,"avatarUrls":{},"future":{"big":9007199254740993,"states":[null,[],false,0]}}`
	for _, fixture := range []struct {
		name, body string
		target     any
	}{
		{"board", boardJSON, new(Board)}, {"project", projectJSON, new(BoardProject)},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(fixture.body), fixture.target); err != nil {
				t.Fatal(err)
			}
			encoded, err := MarshalNoEscape(fixture.target)
			if err != nil {
				t.Fatal(err)
			}
			var before, after map[string]json.RawMessage
			if json.Unmarshal([]byte(fixture.body), &before) != nil || json.Unmarshal(encoded, &after) != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("record lost members/presence/raw bytes: %s", encoded)
			}
		})
	}
}

func TestAgileDecodingReplacesAndCommitsAtomically(t *testing.T) {
	for _, target := range []any{new(BoardPage), new(Board), new(BoardProject)} {
		if err := json.Unmarshal([]byte(`{"id":42,"name":"Old","total":9,"future":false}`), target); err != nil {
			// A project id has the endpoint's string type.
			if _, project := target.(*BoardProject); !project {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(`{"id":"42","name":"Old","future":false}`), target); err != nil {
				t.Fatal(err)
			}
		}
		if err := json.Unmarshal([]byte(`{}`), target); err != nil {
			t.Fatal(err)
		}
		before, err := MarshalNoEscape(target)
		if err != nil || string(before) != `{}` {
			t.Fatalf("reused value retained data: %s %v", before, err)
		}
		if err := json.Unmarshal([]byte(`{"name":3,"isLast":"wrong","future":0}`), target); err == nil {
			t.Fatal("invalid record/page decoded")
		}
		after, err := MarshalNoEscape(target)
		if err != nil || string(after) != string(before) {
			t.Fatalf("failed decode changed receiver: %s %v", after, err)
		}
	}
}

func TestAgileDeclaredFieldsCannotBeShadowed(t *testing.T) {
	for _, value := range []any{
		Board{ID: NullableOf(int64(42)), Extra: map[string]json.RawMessage{"ID": json.RawMessage(`99`), "NAME": json.RawMessage(`"shadow"`)}},
		BoardProject{Key: NullableOf("KEY"), Extra: map[string]json.RawMessage{"KEY": json.RawMessage(`"OTHER"`), "ID": json.RawMessage(`"shadow"`)}},
		BoardPage{IsLast: NullableOf(true), Extra: map[string]json.RawMessage{"ISLAST": json.RawMessage(`false`), "TOTAL": json.RawMessage(`99`)}},
	} {
		encoded, err := MarshalNoEscape(value)
		if err != nil {
			t.Fatal(err)
		}
		var members map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &members); err != nil {
			t.Fatal(err)
		}
		if len(members) != 1 {
			t.Fatalf("extension shadowed an absent or present declared member: %s", encoded)
		}
	}
}
