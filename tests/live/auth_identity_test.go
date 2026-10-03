//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// TestLiveAuthIdentity compares the complete current account with an
// independent raw endpoint read. It creates and changes no Jira resources.
func TestLiveAuthIdentity(t *testing.T) {
	r := newLiveRun(t)
	raw := rawRead(t, r, "api", "get", "/rest/api/3/myself")
	decode := func(data []byte) any {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	expected := decode(raw.Response.JSON)
	for _, path := range [][]string{{"auth", "whoami"}, {"me"}} {
		frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, path...)
		if exit != 0 || !frame.OK {
			t.Fatalf("identity read failed: exit=%d", exit)
		}
		var out struct {
			Data struct {
				BaseURL string          `json:"base_url"`
				User    json.RawMessage `json:"user"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout, &out); err != nil {
			t.Fatal(err)
		}
		if out.Data.BaseURL != r.manifest.BaseURL || !reflect.DeepEqual(decode(out.Data.User), expected) {
			t.Fatal("domain identity differs from the independent native account")
		}
	}
}
