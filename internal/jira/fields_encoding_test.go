package jira

import (
	"encoding/json"
	"testing"
)

// Kills encoders that preserve visible text but rewrite raw JSON escapes,
// change nil/omitempty semantics, or accept malformed raw input.
func TestMarshalNoEscape(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		value   any
		want    string
		wantErr bool
	}{
		{name: "literal separators", value: json.RawMessage("{\"text\":\"<>&\u2028\u2029\"}"), want: "{\"text\":\"<>&\u2028\u2029\"}"},
		{name: "raw escape spelling", value: json.RawMessage(`{"text":"\u2028\u2029\u003c\/"}`), want: `{"text":"\u2028\u2029\u003c\/"}`},
		{name: "textual escape", value: `\u2028`, want: `"\\u2028"`},
		{name: "ordinary string", value: "<>&\u2028\u2029", want: "\"<>&\u2028\u2029\""},
		{name: "nil slice", value: []string(nil), want: "null"},
		{name: "nil map", value: map[string]string(nil), want: "null"},
		{name: "ordered keys", value: map[string]int{"z": 2, "a": 1}, want: `{"a":1,"z":2}`},
		{name: "absent field", value: IssueFields{}, want: "{}"},
		{name: "explicit null", value: IssueFields{Description: NullableNull[json.RawMessage]()}, want: `{"description":null}`},
		{name: "invalid raw value", value: json.RawMessage(`{"text":`), wantErr: true},
		{name: "unsupported value", value: make(chan int), wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := MarshalNoEscape(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && string(got) != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
