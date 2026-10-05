package jira

import (
	"encoding/json"
	"testing"
)

func TestRemoteLinkNativeRequestPreservesAuthoredValues(t *testing.T) {
	t.Parallel()
	input := `{"object":{"url":"https:\/\/example.com/runbook","title":"\u0052unbook","summary":null,"icon":{"url16x16":"https://example.com/icon","future":{"text":"\u003c\/","id":900719925474099312345}},"status":{"resolved":false,"icon":{},"future":0},"future":{"text":"\u2028","value":null}},"application":{"name":"Example","type":"com.example","future":false},"globalId":"","relationship":null,"future":{"id":900719925474099312345,"text":"\u2029"}}`
	var request RemoteLinkRequest
	if err := json.Unmarshal([]byte(input), &request); err != nil {
		t.Fatal(err)
	}
	got, err := MarshalNoEscape(request)
	if err != nil {
		t.Fatal(err)
	}
	var wantFields, gotFields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(input), &wantFields); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got, &gotFields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"object", "application", "globalId", "relationship", "future"} {
		t.Run(name, func(t *testing.T) {
			if string(gotFields[name]) != string(wantFields[name]) {
				t.Fatalf("authored %s changed: got %s, want %s", name, gotFields[name], wantFields[name])
			}
		})
	}
}

func TestRemoteLinkNativeRequestReuseIsAtomic(t *testing.T) {
	t.Parallel()
	var request RemoteLinkRequest
	first := []byte(`{"object":{"url":"https://example.com","title":"First"},"application":{"name":"Example"},"future":false}`)
	if err := json.Unmarshal(first, &request); err != nil {
		t.Fatal(err)
	}
	before, err := request.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"object":{"URL":"https://example.com","title":"Invalid"}}`), &request); err == nil {
		t.Fatal("ambiguous native decode unexpectedly succeeded")
	}
	after, err := request.MarshalJSON()
	if err != nil || string(after) != string(before) {
		t.Fatal("failed native decode altered the previous request")
	}
	if err := json.Unmarshal([]byte(`{"object":{"url":"https://example.com","title":"Second"}}`), &request); err != nil {
		t.Fatal(err)
	}
	if len(request) != 1 || request["application"] != nil || request["future"] != nil {
		t.Fatal("reused native decode retained stale request members")
	}
}
