package jira

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func checkNullableRefresh[T any](t *testing.T, prior, want T, input string) {
	t.Helper()
	n := NullableOf(prior)
	if err := json.Unmarshal([]byte("null"), &n); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(n, NullableNull[T]()) {
		t.Fatal("null retained an earlier native value", n)
	}
	if err := json.Unmarshal([]byte(input), &n); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(n, NullableOf(want)) {
		t.Fatal("fresh native value retained null or earlier data", n, want)
	}
	encoded, err := MarshalNoEscape(n)
	var compact bytes.Buffer
	if err != nil || json.Compact(&compact, []byte(input)) != nil || !bytes.Equal(encoded, compact.Bytes()) {
		t.Fatal("native refresh changed wire value", string(encoded), input, err)
	}
}

func TestNullableRefreshReplacesNullValueAndEmptyStates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"string zero", func(t *testing.T) { checkNullableRefresh(t, "prior", "", `""`) }},
		{"integer zero", func(t *testing.T) { checkNullableRefresh(t, 7, 0, `0`) }},
		{"boolean false", func(t *testing.T) { checkNullableRefresh(t, true, false, `false`) }},
		{"empty list", func(t *testing.T) { checkNullableRefresh(t, []string{"prior"}, []string{}, `[]`) }},
		{"empty object", func(t *testing.T) { checkNullableRefresh(t, map[string]int{"prior": 1}, map[string]int{}, `{}`) }},
		{"unknown native bytes", func(t *testing.T) {
			input := `{"unknown":[null,false,0,"native <&>\u2028"],"text":"retained"}`
			checkNullableRefresh(t, json.RawMessage(`{"prior":true}`), json.RawMessage(input), input)
		}},
		{"native issue fields", func(t *testing.T) {
			input := `{"assignee":null,"customfield_10099":{"keep":0},"summary":"current"}`
			var want IssueFields
			if err := json.Unmarshal([]byte(input), &want); err != nil {
				t.Fatal(err)
			}
			checkNullableRefresh(t, IssueFields{Summary: NullableOf("prior")}, want, input)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); tc.run(t) })
	}
}

func TestNullableDecodeFailurePreservesReceiverAndAliases(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"absent", func(t *testing.T) {
			var n Nullable[int]
			err := json.Unmarshal([]byte(`"wrong type"`), &n)
			var cause *json.UnmarshalTypeError
			if !errors.As(err, &cause) || n != (Nullable[int]{}) {
				t.Fatal("failed native read changed absent state or lost cause", n, err)
			}
		}},
		{"map aliases", func(t *testing.T) {
			prior := map[string]int{"keep": 1}
			n := NullableOf(prior)
			err := json.Unmarshal([]byte(`{"keep":7,"later":"bad"}`), &n)
			if err == nil || !reflect.DeepEqual(n, NullableOf(map[string]int{"keep": 1})) || !reflect.DeepEqual(prior, map[string]int{"keep": 1}) {
				t.Fatal("failed map read changed native preimage", n, prior, err)
			}
		}},
		{"slice aliases", func(t *testing.T) {
			prior := []int{1, 2}
			n := NullableOf(prior)
			err := json.Unmarshal([]byte(`[7,"bad"]`), &n)
			if err == nil || !reflect.DeepEqual(n, NullableOf([]int{1, 2})) || !reflect.DeepEqual(prior, []int{1, 2}) {
				t.Fatal("failed list read changed native preimage", n, prior, err)
			}
		}},
		{"present null", func(t *testing.T) {
			n := NullableNull[int]()
			err := json.Unmarshal([]byte(`"bad"`), &n)
			if err == nil || n != NullableNull[int]() {
				t.Fatal("failed native read changed explicit null", n, err)
			}
		}},
		{"pointer aliases", func(t *testing.T) {
			type nativeValue struct {
				Name  string `json:"name"`
				Count int    `json:"count"`
			}
			prior := &nativeValue{Name: "prior", Count: 1}
			n := NullableOf(prior)
			err := json.Unmarshal([]byte(`{"name":"changed","count":"bad"}`), &n)
			if err == nil || n.Value != prior || *prior != (nativeValue{Name: "prior", Count: 1}) {
				t.Fatal("failed pointer read changed native preimage", n, prior, err)
			}
			if err := json.Unmarshal([]byte(`{"name":"current","count":2}`), &n); err != nil {
				t.Fatal(err)
			}
			if n.Value == prior || *prior != (nativeValue{Name: "prior", Count: 1}) || *n.Value != (nativeValue{Name: "current", Count: 2}) {
				t.Fatal("successful pointer read changed native preimage", n, prior)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); tc.run(t) })
	}
}

func TestNullableSuccessfulRefreshDoesNotMutatePriorAliases(t *testing.T) {
	t.Parallel()
	prior := map[string]int{"prior": 1}
	n := NullableOf(prior)
	if err := json.Unmarshal([]byte(`{"current":2}`), &n); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prior, map[string]int{"prior": 1}) || !reflect.DeepEqual(n.Value, map[string]int{"current": 2}) {
		t.Fatal("native refresh merged or mutated earlier object", prior, n)
	}
}

func TestNullableNativeBodyRefreshPreservesIndependentPreimage(t *testing.T) {
	t.Parallel()
	prior := json.RawMessage(`{"unknownNativeBody":{"retained":"a sufficiently long prior value <&>\u2028"}}`)
	preimage := bytes.Clone(prior)
	n := NullableOf(prior)
	current := []byte(`{"unknown": [null, false, 0], "text":"current <&>\u2028"}`)
	if err := json.Unmarshal(current, &n); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(prior, preimage) || !bytes.Equal(n.Value, current) || !n.Set || n.Null {
		t.Fatal("native body refresh overwrote the independent preimage or changed current bytes", string(prior), string(n.Value))
	}
}

func TestNullableDirectNullDecodeAcceptsJSONWhitespace(t *testing.T) {
	t.Parallel()
	n := NullableOf("prior")
	if err := n.UnmarshalJSON([]byte(" \n null \t")); err != nil || n != NullableNull[string]() {
		t.Fatal("direct native null read differs from JSON decoder", n, err)
	}
	for _, malformed := range []string{"\vnull", "\fnull", "\u00a0null", "null\u00a0"} {
		n := NullableOf("prior")
		if err := n.UnmarshalJSON([]byte(malformed)); err == nil || n != NullableOf("prior") {
			t.Fatal("invalid JSON whitespace changed native state", n, err)
		}
	}
}
