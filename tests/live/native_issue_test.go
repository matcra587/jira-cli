//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/output"
)

type nativeIssueInput struct {
	key       string
	parent    string
	digest    [sha256.Size]byte
	preflight bool
}

// Only fixed property, owned-parent, custom-date and missing-field preflight workflows register issue input.
// Exact arguments, active ownership and a byte digest prevent a registered
// file from becoming a route to selectors, fan-out or another issue.
func (r *liveRun) checkNativeIssueMutation(keys, args []string) error {
	if len(args) > 0 && args[len(args)-1] == "--dry-run" {
		args = args[:len(args)-1]
	}
	preflight := len(args) == 6 && args[5] == "--validate-remote"
	if preflight {
		args = args[:5]
	}
	if !r.verified || len(keys) != 1 || len(args) != 5 || args[0] != "issue" || args[1] != "edit" || args[2] != keys[0] || args[3] != "--json-input" || r.ownedLiveIssue(keys[0]) == nil {
		return errors.New("native live issue edit requires exactly one active owned target and registered input")
	}
	registered, ok := r.issueInputs[args[4]]
	if !ok || registered.key != keys[0] {
		return errors.New("native live issue input is not registered for this owned target")
	}
	if registered.preflight != preflight {
		return errors.New("native preflight input requires its explicit metadata check")
	}
	if registered.parent != "" && r.ownedLiveIssue(registered.parent) == nil {
		return errors.New("native live parent input requires an active owned parent")
	}
	data, err := os.ReadFile(args[4])
	if err != nil || sha256.Sum256(data) != registered.digest {
		return errors.New("native live issue input changed after registration")
	}
	return nil
}

// nativeMissingFieldInput registers one fixed refusal probe. The mandatory
// metadata check prevents it becoming an arbitrary live issue payload route.
func (r *liveRun) nativeMissingFieldInput(t *testing.T, key string) string {
	t.Helper()
	if !r.verified || r.ownedLiveIssue(key) == nil {
		t.Fatal("native preflight input requires a verified active owned issue")
	}
	data := []byte(`{"fields":{"jira_cli_preflight_missing_fixture":false}}`)
	path := filepath.Join(r.dir, "missing-field-"+output.NewRequestID()+".json")
	if err := xos.AtomicWrite(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if r.issueInputs == nil {
		r.issueInputs = make(map[string]nativeIssueInput)
	}
	r.issueInputs[path] = nativeIssueInput{key: key, digest: sha256.Sum256(data), preflight: true}
	return path
}

const livePropertyValue = `{"enabled":false,"empty":[],"nullable":null,"markup":"<>&","count":0}`

// nativeCloneDateInput binds one fixed scalar date to an active owned source.
// The caller selects the field from fresh native date-picker metadata. This
// constructor cannot author relationships, selectors or arbitrary payloads.
func (r *liveRun) nativeCloneDateInput(t *testing.T, key, fieldID string) string {
	t.Helper()
	if !r.verified || r.ownedLiveIssue(key) == nil {
		t.Fatal("native clone date input requires a verified active owned source")
	}
	suffix, found := strings.CutPrefix(fieldID, "customfield_")
	if !found || suffix == "" {
		t.Fatal("native clone date input requires a custom field identifier")
	}
	for _, digit := range suffix {
		if digit < '0' || digit > '9' {
			t.Fatal("native clone date field identifier must have a decimal suffix")
		}
	}
	data, err := json.Marshal(map[string]any{"fields": map[string]string{fieldID: "2026-01-03"}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.dir, "clone-source-date-"+output.NewRequestID()+".json")
	if err := xos.AtomicWrite(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if r.issueInputs == nil {
		r.issueInputs = make(map[string]nativeIssueInput)
	}
	r.issueInputs[path] = nativeIssueInput{key: key, digest: sha256.Sum256(data)}
	return path
}

func (r *liveRun) nativePropertyInput(t *testing.T, key string, emptyContainers bool) (string, string) {
	t.Helper()
	if !r.verified || r.ownedLiveIssue(key) == nil {
		t.Fatal("native property input requires a verified active owned issue")
	}
	property := "cli.acceptance." + output.NewRequestID()
	data := []byte(`{"properties":[{"key":"` + property + `","value":` + livePropertyValue + `}]}`)
	if emptyContainers {
		data = []byte(`{"fields":{},"update":{},"properties":[{"key":"` + property + `","value":` + livePropertyValue + `}]}`)
	}
	path := filepath.Join(r.dir, "native-property-"+output.NewRequestID()+".json")
	if err := xos.AtomicWrite(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if r.issueInputs == nil {
		r.issueInputs = make(map[string]nativeIssueInput)
	}
	r.issueInputs[path] = nativeIssueInput{key: key, digest: sha256.Sum256(data)}
	return property, path
}

func TestLiveNativeIssueProperties(t *testing.T) {
	r := newLiveRun(t)
	key := r.create(t, "native-property-edit")
	for _, emptyContainers := range []bool{false, true} {
		property, path := r.nativePropertyInput(t, key, emptyContainers)
		args := []string{"issue", "edit", key, "--json-input", path}
		preview := r.mutate(t, []string{key}, append(args, "--dry-run")...)
		if !preview.Data.DryRun {
			t.Fatal("native property preview lost dry-run state")
		}
		endpoint := "/rest/api/3/issue/" + key + "/properties/" + property
		assertAbsentProperty(t, r, endpoint)
		if err := r.checkMutation([]string{key}, args); err != nil {
			t.Fatal(err)
		}
		blocked, _, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, args...)
		if exit != 3 || blocked.OK || len(blocked.Errors) != 1 || blocked.Errors[0].Code != "read_only_blocked" {
			t.Fatal("native property edit bypassed read-only enforcement")
		}
		assertAbsentProperty(t, r, endpoint)
		applied, stdout, exit := r.invoke(t, t.Context(), nil, args...)
		if !emptyContainers {
			if exit != 3 || applied.OK || len(applied.Errors) != 1 || applied.Errors[0].Code != "jira_bad_request" || len(applied.Data.Results) != 1 || applied.Data.Results[0].OK {
				t.Fatal("native property-only server refusal lost its typed outcome")
			}
			var receipt struct {
				Data struct {
					Payload json.RawMessage `json:"payload"`
				} `json:"data"`
			}
			input, err := os.ReadFile(path)
			if err != nil || json.Unmarshal(stdout, &receipt) != nil || string(receipt.Data.Payload) != string(input) {
				t.Fatal("native property-only server refusal lost its unchanged request")
			}
			assertAbsentProperty(t, r, endpoint)
			continue
		}
		if exit != 0 || !applied.OK {
			t.Fatal("native property edit with explicit empty containers failed; inspect local receipts")
		}
		if applied.Data.DryRun || len(applied.Data.Results) != 1 || !applied.Data.Results[0].OK || applied.Data.Results[0].Issue.Key != key {
			t.Fatal("native property edit has no successful owned-target receipt")
		}
		stored := rawRead(t, r, "api", "get", endpoint)
		var got struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		}
		var want any
		if json.Unmarshal(stored.Response.JSON, &got) != nil || json.Unmarshal([]byte(livePropertyValue), &want) != nil || got.Key != property || !reflect.DeepEqual(got.Value, want) {
			t.Fatal("independent Jira property read lost native null, empty, false, zero or markup values")
		}
	}
}

func assertAbsentProperty(t *testing.T, r *liveRun, endpoint string) {
	t.Helper()
	frame, _, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "api", "get", endpoint)
	if exit != 2 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "jira_not_found" {
		t.Fatal("native property preview or refused edit changed Jira state")
	}
}
