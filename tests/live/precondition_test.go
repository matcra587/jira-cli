//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	xos "github.com/gechr/x/os"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	wire "github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

type preconditionObservation struct {
	Resource      string               `json:"resource"`
	Flag          string               `json:"flag"`
	Status        int                  `json:"status"`
	Enforced      bool                 `json:"enforced"`
	SourceHeaders []envelope.APIValues `json:"source_headers"`
}

// This is protocol discovery, not optimistic-concurrency acceptance. It records
// whether each endpoint refuses a stale request or applies it. Passing means
// independently read state agrees with the single-attempt HTTP receipt.
func TestLiveHTTPPreconditionDiscovery(t *testing.T) {
	r := newLiveRun(t)
	key := r.create(t, "http-precondition-discovery")
	document := func(text string) []byte {
		return []byte(fmt.Sprintf(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":%q}]}]}`, text))
	}
	original := document("original snapshot")
	posted := r.mutate(t, []string{key}, "issue", "comment", "add", key, "--json-input", r.nativeBody(t, original))
	id := postedADFCommentID(t, posted)
	r.registerComment(t, key, id)
	var observations []preconditionObservation
	for _, resource := range []string{"description", "comment"} {
		for _, condition := range []struct{ flag, value string }{
			{"--if-match", `"unmatchable-` + output.NewRequestID() + `"`},
			{"--if-unmodified-since", "Thu, 01 Jan 1970 00:00:00 GMT"},
		} {
			input := func(body []byte) (string, string) {
				if resource == "description" {
					return r.rawDescriptionInput(t, key, body)
				}
				return r.rawCommentInput(t, key, id, body)
			}
			endpoint, path := input(original)
			baseline, frame, exit := rawMutation(t, r, key, nil, "api", "put", endpoint, "--input", path, "--force")
			status := 204
			if resource == "comment" {
				status = 200
			}
			requireRawSuccess(t, baseline, frame, exit, status)
			assertStoredPreconditionBody(t, r, key, id, resource, original)
			snapshot := rawRead(t, r, "api", "get", endpoint)
			currentBody := document("intervening update " + resource + " " + condition.flag)
			endpoint, path = input(currentBody)
			updated, frame, exit := rawMutation(t, r, key, nil, "api", "put", endpoint, "--input", path, "--force")
			requireRawSuccess(t, updated, frame, exit, status)
			assertStoredPreconditionBody(t, r, key, id, resource, currentBody)
			endpoint, path = input(original)
			r.bindRawPrecondition(t, path, condition.flag, condition.value)
			attempt, frame, exit := rawMutation(t, r, key, nil, "api", "put", endpoint, "--input", path, condition.flag, condition.value, "--force")
			if !attempt.Attempted || attempt.Response == nil || attempt.Response.Truncated || attempt.Response.Attempts != 1 {
				t.Fatal("precondition probe has no complete single-attempt HTTP receipt")
			}
			if condition.flag == "--if-match" && attempt.Request.IfMatch != condition.value || condition.flag == "--if-unmodified-since" && attempt.Request.IfUnmodifiedSince != condition.value {
				t.Fatal("precondition probe lost its registered request header")
			}
			enforced := attempt.Response.StatusCode == 412
			if enforced {
				if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "jira_precondition_failed" {
					t.Fatal("precondition refusal lost its typed outcome")
				}
				assertStoredPreconditionBody(t, r, key, id, resource, currentBody)
			} else {
				requireRawSuccess(t, attempt, frame, exit, status)
				assertStoredPreconditionBody(t, r, key, id, resource, original)
			}
			observations = append(observations, preconditionObservation{Resource: resource, Flag: condition.flag, Status: attempt.Response.StatusCode, Enforced: enforced, SourceHeaders: snapshot.Response.Headers})
			t.Logf("%s %s: HTTP %d, stale write rejected=%t", resource, condition.flag, attempt.Response.StatusCode, enforced)
		}
	}
	data, err := json.MarshalIndent(observations, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := xos.AtomicWrite(filepath.Join(r.dir, "precondition-observations.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (r *liveRun) rawDescriptionInput(t *testing.T, key string, document []byte) (string, string) {
	t.Helper()
	if !r.verified || r.ownedLiveIssue(key) == nil {
		t.Fatal("description probe requires a verified active owned issue")
	}
	if err := adf.Validate(document); err != nil {
		t.Fatal("description probe does not conform to the stable ADF schema")
	}
	var payload struct {
		Fields struct {
			Description json.RawMessage `json:"description"`
		} `json:"fields"`
	}
	payload.Fields.Description = document
	data, err := wire.MarshalNoEscape(payload)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "/rest/api/3/issue/" + key
	path := filepath.Join(r.dir, "raw-description-"+output.NewRequestID()+".json")
	if err := xos.AtomicWrite(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if r.rawInputs == nil {
		r.rawInputs = make(map[string]rawInput)
	}
	r.rawInputs[path] = rawInput{method: "PUT", endpoint: endpoint, digest: sha256.Sum256(data), issueDescription: true}
	return endpoint, path
}

func (r *liveRun) bindRawPrecondition(t *testing.T, path, flag, value string) {
	t.Helper()
	registered, exists := r.rawInputs[path]
	if !r.verified || !exists || registered.method != "PUT" {
		t.Fatal("precondition requires a registered owned PUT input")
	}
	request := core.APIRequest{Method: registered.method, Endpoint: registered.endpoint}
	switch flag {
	case "--if-match":
		request.IfMatch = value
	case "--if-unmodified-since":
		request.IfUnmodifiedSince = value
	default:
		t.Fatal("precondition probe flag is unsupported")
	}
	if value == "" || adapterjira.ValidateAPIRequest(request) != nil {
		t.Fatal("precondition probe value is invalid")
	}
	registered.conditionFlag, registered.conditionValue = flag, value
	r.rawInputs[path] = registered
}

func assertStoredPreconditionBody(t *testing.T, r *liveRun, key, id, resource string, expected []byte) {
	t.Helper()
	if resource == "comment" {
		frame := r.success(t, "issue", "comment", "list", key, "--all")
		if err := verifyStoredADFComments(map[string]json.RawMessage{id: expected}, frame); err != nil {
			t.Fatal(err)
		}
		return
	}
	frame := r.success(t, "issue", "view", key)
	var fields struct {
		Description json.RawMessage `json:"description"`
	}
	if err := json.Unmarshal(frame.Data.Issue.Fields, &fields); err != nil {
		t.Fatal(err)
	}
	equal, err := adf.StructuralEqual(expected, fields.Description)
	if err != nil || !equal {
		t.Fatal("stored description differs from the independently expected native document")
	}
}
