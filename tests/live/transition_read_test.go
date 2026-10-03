//go:build live

package live

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// compareNativeTransitions uses untyped, number-preserving JSON so the native
// oracle is independent of the generated wire models used by the CLI.
func compareNativeTransitions(actual, expected json.RawMessage) error {
	decode := func(raw json.RawMessage) (any, error) {
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		var value map[string]any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		var trailing json.RawMessage
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("native workflow contains trailing data")
		}
		if _, ok := value["transitions"].([]any); !ok {
			return nil, fmt.Errorf("native workflow has no explicit transitions array")
		}
		return value, nil
	}
	want, err := decode(expected)
	if err != nil {
		return err
	}
	got, err := decode(actual)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("published workflow differs from independent native metadata")
	}
	return nil
}

func TestLiveTransitionDiscovery(t *testing.T) {
	r := newLiveRun(t)
	key := requiredEnv(t, "JIRA_LIVE_READ_KEY")
	if !strings.HasPrefix(key, r.manifest.Project+"-") {
		t.Fatal("workflow read fixture must belong to the dedicated project")
	}
	native := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(key)+"/transitions", "--query=expand=transitions.fields")
	frame, _, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "issue", "transition", "list", key)
	if exit != 0 || !frame.OK || len(frame.Data.Results) != 1 || !frame.Data.Results[0].OK || frame.Data.Results[0].Issue.Key != key {
		t.Fatal("read-only transition discovery lost its requested issue")
	}
	if err := compareNativeTransitions(frame.Data.Results[0].Data, native.Response.JSON); err != nil {
		t.Fatal(err)
	}
}

func TestNativeTransitionComparisonRequiresCompleteMetadata(t *testing.T) {
	t.Parallel()
	const expected = `{"future":900719925474099312345,"transitions":[{"id":"31","hasScreen":false,"fields":{"summary":{"required":true,"schema":{"type":"string"},"allowedValues":[],"defaultValue":null}}}]}`
	if err := compareNativeTransitions([]byte(expected), []byte(expected)); err != nil {
		t.Fatal(err)
	}
	for name, actual := range map[string]string{
		"screen":            strings.Replace(expected, `"hasScreen":false`, `"hasScreen":true`, 1),
		"required":          strings.Replace(expected, `"required":true`, `"required":false`, 1),
		"identity":          strings.Replace(expected, `"id":"31"`, `"id":"11"`, 1),
		"null":              strings.Replace(expected, `,"defaultValue":null`, "", 1),
		"precision":         strings.Replace(expected, `900719925474099312345`, `900719925474099300000`, 1),
		"array":             `{"transitions":null}`,
		"trailing-document": expected + `{}`,
		"trailing-garbage":  expected + `broken`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if compareNativeTransitions([]byte(actual), []byte(expected)) == nil {
				t.Fatal("native transition comparator accepted changed or incomplete metadata")
			}
		})
	}
}
