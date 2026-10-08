//go:build live

package live

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"reflect"
	"slices"
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
	namespace := "XDG_CACHE_HOME=" + filepath.Join(r.dir, "transition-cache")
	frame, _, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true", namespace}, "issue", "transition", "list", key)
	if exit != 0 || !frame.OK || len(frame.Data.Results) != 1 || !frame.Data.Results[0].OK || frame.Data.Results[0].Issue.Key != key {
		t.Fatal("read-only transition discovery lost its requested issue")
	}
	if err := compareNativeTransitions(frame.Data.Results[0].Data, native.Response.JSON); err != nil {
		t.Fatal(err)
	}
	want, err := nativeEligibleTransitionIDs(native.Response.JSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("live workflow fixture has no eligible identity to prove completion")
	}
	// Reuse only this run's derived cache. Disabled credentials and unreachable
	// proxies make completion prove it can use the observation without Jira.
	offline := offlineTransitionEnvironment(namespace, r.account)
	for _, kind := range []string{"cachetransition", "transitionissuekey"} {
		stdout, exit := r.invokeRaw(t, t.Context(), offline, "--@complete="+kind, "--", key)
		var got []string
		for _, line := range strings.Split(strings.TrimSuffix(string(stdout), "\n"), "\n") {
			id, _, _ := strings.Cut(line, "\t")
			got = append(got, id)
		}
		if exit != 0 || !slices.Equal(got, want) {
			t.Fatalf("offline live workflow hints differ for %s; inspect local receipts", kind)
		}
	}
}

// nativeEligibleTransitionIDs is independent of the CLI wire/cache models and
// completion projection. It compares identities, not incidental display text.
func nativeEligibleTransitionIDs(raw json.RawMessage) ([]string, error) {
	var native struct {
		Transitions []map[string]json.RawMessage `json:"transitions"`
	}
	if err := json.Unmarshal(raw, &native); err != nil {
		return nil, err
	}
	if native.Transitions == nil {
		return nil, errors.New("native workflow is not an explicit array")
	}
	ids := []string{}
	for _, transition := range native.Transitions {
		if available, ok := transition["isAvailable"]; ok && string(available) != "true" {
			continue
		}
		var id string
		if err := json.Unmarshal(transition["id"], &id); err != nil || id == "" {
			return nil, errors.New("native workflow identity is absent or malformed")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func TestNativeEligibleTransitionIDsKeepAvailableIdentitiesOnly(t *testing.T) {
	t.Parallel()
	ids, err := nativeEligibleTransitionIDs([]byte(`{"transitions":[{"id":"31"},{"id":"52","isAvailable":false},{"id":"78","isAvailable":true},{"id":"91","isAvailable":null}]}`))
	if err != nil || !slices.Equal(ids, []string{"31", "78"}) {
		t.Fatalf("independent identities=%q err=%v", ids, err)
	}
	for _, raw := range []string{`{}`, `{"transitions":null}`, `{"transitions":[{}]}`, `{"transitions":[{"id":31}]}`} {
		if _, err := nativeEligibleTransitionIDs([]byte(raw)); err == nil {
			t.Fatal("invalid native identity accepted")
		}
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

// offlineTransitionEnvironment retains only public cache scope while disabling
// authentication and routing any accidental network request to a closed port.
func offlineTransitionEnvironment(namespace, account string) []string {
	return []string{
		namespace, "JIRA_READ_ONLY=true", "JIRA_EMAIL=" + account,
		"JIRA_KEYRING=false", "JIRA_API_TOKEN=",
		"HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1",
		"ALL_PROXY=http://127.0.0.1:1", "NO_PROXY=",
		"http_proxy=http://127.0.0.1:1", "https_proxy=http://127.0.0.1:1",
		"all_proxy=http://127.0.0.1:1", "no_proxy=",
	}
}
