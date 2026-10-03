//go:build live

package live

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/envelope"
	wire "github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

type rawInput struct {
	method           string
	endpoint         string
	digest           [sha256.Size]byte
	issueDescription bool
	conditionFlag    string
	conditionValue   string
}

// checkRawMutation admits authored comments and registered description probes.
// Exact route and
// argument matching prevents selectors, cascading deletes, extra payloads and
// alternate targets from entering the live harness through this fallback.
func (r *liveRun) checkRawMutation(keys, args []string) error {
	if len(keys) != 1 {
		return errors.New("raw live mutations require exactly one declared owned issue")
	}
	owned := r.ownedLiveIssue(keys[0])
	if owned == nil {
		return errors.New("raw live mutation target is not an active owned issue")
	}
	method, endpoint := strings.ToUpper(args[1]), args[2]
	issuePath := "/rest/api/3/issue/" + owned.Key
	commentPath := issuePath + "/comment"
	comment := r.ownedLiveComment(owned, strings.TrimPrefix(endpoint, commentPath+"/"))
	valid := (method == "POST" && endpoint == commentPath) ||
		(method == "PUT" && comment != nil && endpoint == commentPath+"/"+comment.ID) ||
		(method == "DELETE" && comment != nil && endpoint == commentPath+"/"+comment.ID)
	if !valid && (method != "PUT" || endpoint != issuePath) {
		return errors.New("raw live route does not address an allowed owned resource")
	}
	if args[len(args)-1] == "--dry-run" {
		args = args[:len(args)-1]
	}
	if method == "DELETE" {
		if len(args) != 4 || args[3] != "--force" {
			return errors.New("raw live deletion requires an exact route and no extra options")
		}
		return nil
	}
	if (len(args) != 6 && len(args) != 8) || args[3] != "--input" || args[len(args)-1] != "--force" {
		return errors.New("raw live writes require one registered input and explicit force")
	}
	registered, ok := r.rawInputs[args[4]]
	if !ok || registered.method != method || registered.endpoint != endpoint {
		return errors.New("raw live input is not registered for this method and owned route")
	}
	if !valid && !registered.issueDescription {
		return errors.New("raw issue writes require a registered description-only input")
	}
	if registered.conditionFlag == "" {
		if len(args) != 6 {
			return errors.New("raw live input does not register an HTTP precondition")
		}
	} else if (registered.conditionFlag != "--if-match" && registered.conditionFlag != "--if-unmodified-since") || registered.conditionValue == "" || len(args) != 8 || args[5] != registered.conditionFlag || args[6] != registered.conditionValue {
		return errors.New("raw live precondition differs from its registered flag and value")
	}
	data, err := os.ReadFile(args[4])
	if err != nil || sha256.Sum256(data) != registered.digest {
		return errors.New("raw live input changed after registration")
	}
	return nil
}

func (r *liveRun) ownedLiveIssue(key string) *ownedIssue {
	for i := range r.manifest.Issues {
		owned := &r.manifest.Issues[i]
		if owned.Key == key && !owned.Deleted {
			return owned
		}
	}
	return nil
}

func (r *liveRun) ownedLiveComment(issue *ownedIssue, id string) *ownedComment {
	for i := range issue.Comments {
		owned := &issue.Comments[i]
		if owned.ID == id && !owned.Deleted {
			return owned
		}
	}
	return nil
}

// rawCommentInput wraps only validated ADF. The harness offers no arbitrary
// native issue payload authoring route and binds the exact bytes to a target.
func (r *liveRun) rawCommentInput(t *testing.T, key, id string, document []byte) (string, string) {
	t.Helper()
	owned := r.ownedLiveIssue(key)
	if owned == nil || !r.verified || (id != "" && r.ownedLiveComment(owned, id) == nil) {
		t.Fatal("raw comment input requires a verified owned resource")
	}
	if err := adf.Validate(document); err != nil {
		t.Fatal("raw comment body does not conform to the stable ADF schema")
	}
	method, endpoint := "POST", "/rest/api/3/issue/"+key+"/comment"
	if id != "" {
		method, endpoint = "PUT", endpoint+"/"+id
	}
	data, err := wire.MarshalNoEscape(struct {
		Body json.RawMessage `json:"body"`
	}{document})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.dir, "raw-comment-"+output.NewRequestID()+".json")
	if err := xos.AtomicWrite(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if r.rawInputs == nil {
		r.rawInputs = make(map[string]rawInput)
	}
	r.rawInputs[path] = rawInput{method: method, endpoint: endpoint, digest: sha256.Sum256(data)}
	return endpoint, path
}

func (r *liveRun) registerComment(t *testing.T, key, id string) {
	t.Helper()
	owned := r.ownedLiveIssue(key)
	numeric, err := strconv.ParseUint(id, 10, 64)
	if owned == nil || err != nil || numeric == 0 {
		t.Fatal("raw comment receipt lacks a confirmed owned issue and numeric identity")
	}
	for _, issue := range r.manifest.Issues {
		for _, comment := range issue.Comments {
			if comment.ID == id {
				t.Fatal("comment creation returned an already registered identity")
			}
		}
	}
	owned.Comments = append(owned.Comments, ownedComment{ID: id})
	r.save(t)
}

func rawMutation(t *testing.T, r *liveRun, key string, extraEnv []string, args ...string) (envelope.APIMutationOutput, liveFrame, int) {
	t.Helper()
	if err := r.checkMutation([]string{key}, args); err != nil {
		t.Fatal(err)
	}
	frame, stdout, exit := r.invoke(t, t.Context(), extraEnv, args...)
	var decoded struct {
		Data envelope.APIMutationOutput `json:"data"`
	}
	if err := json.Unmarshal(stdout, &decoded); err != nil {
		t.Fatal("raw mutation did not emit a typed machine receipt")
	}
	return decoded.Data, frame, exit
}

func requireRawSuccess(t *testing.T, out envelope.APIMutationOutput, frame liveFrame, exit, status int) {
	t.Helper()
	if exit != 0 || !frame.OK || out.DryRun || !out.Attempted || out.Response == nil || out.Response.StatusCode != status || out.Response.Truncated || out.Response.Attempts != 1 {
		t.Fatalf("raw write did not return a complete single-attempt HTTP %d receipt; inspect local evidence", status)
	}
}

// The status codes and native body routes follow the vendored platform spec's
// comment operations. Independent reads establish stored state, not receipts.
func TestLiveRawAPIMutations(t *testing.T) {
	r := newLiveRun(t)
	key := r.create(t, "raw-comment-lifecycle")
	document := func(text string) []byte {
		return []byte(fmt.Sprintf(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":%q,"marks":[{"type":"strong"}]},{"type":"status","attrs":{"text":"raw","color":"#a1B2c3"}}]}]}`, text))
	}
	initial := document(r.manifest.RunID + " native <&> \u2028")
	endpoint, path := r.rawCommentInput(t, key, "", initial)
	args := []string{"api", "post", endpoint, "--input", path, "--force"}
	preview, previewFrame, exit := rawMutation(t, r, key, nil, append(args, "--dry-run")...)
	if exit != 0 || !previewFrame.OK || !preview.DryRun || preview.Attempted || preview.Response != nil {
		t.Fatal("raw write preview returned an attempted request or HTTP response")
	}
	before := r.success(t, "issue", "comment", "list", key, "--all")
	if len(before.Data.Comments) != 0 {
		t.Fatal("raw preview changed the fresh fixture's comments")
	}
	blocked, blockedFrame, exit := rawMutation(t, r, key, []string{"JIRA_READ_ONLY=true"}, args...)
	if exit != 3 || blockedFrame.OK || blocked.Response != nil || len(blockedFrame.Errors) != 1 || blockedFrame.Errors[0].Code != "read_only_blocked" {
		t.Fatal("forced raw mutation bypassed read-only enforcement")
	}
	unchanged := r.success(t, "issue", "comment", "list", key, "--all")
	if len(unchanged.Data.Comments) != 0 {
		t.Fatal("read-only raw refusal changed the fresh fixture's comments")
	}
	posted, frame, exit := rawMutation(t, r, key, nil, args...)
	requireRawSuccess(t, posted, frame, exit, 201)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(posted.Response.JSON, &created); err != nil {
		t.Fatal("raw comment creation has no JSON identity receipt")
	}
	r.registerComment(t, key, created.ID)
	input, err := os.ReadFile(path)
	if err != nil || posted.Request.BodyBase64 == nil || *posted.Request.BodyBase64 != base64.StdEncoding.EncodeToString(input) {
		t.Fatal("raw live mutation changed its authored native bytes")
	}
	assertStoredComment(t, r, key, created.ID, initial)

	replacement := document(r.manifest.RunID + " updated <&> \u2028")
	endpoint, path = r.rawCommentInput(t, key, created.ID, replacement)
	updated, frame, exit := rawMutation(t, r, key, nil, "api", "PUT", endpoint, "--input", path, "--force")
	requireRawSuccess(t, updated, frame, exit, 200)
	assertStoredComment(t, r, key, created.ID, replacement)

	deleted, frame, exit := rawMutation(t, r, key, nil, "api", "delete", endpoint, "--force")
	requireRawSuccess(t, deleted, frame, exit, 204)
	if deleted.Response.BodyBase64 != "" || len(deleted.Response.JSON) != 0 {
		t.Fatal("empty deletion response fabricated body bytes or JSON")
	}
	missing, missingJSON, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "api", "get", endpoint)
	if exit != 2 || missing.OK || len(missing.Errors) != 1 || missing.Errors[0].Code != "jira_not_found" {
		t.Fatal("raw comment deletion was not confirmed by an independent not-found read")
	}
	var failed struct {
		Data envelope.APIOutput `json:"data"`
	}
	if json.Unmarshal(missingJSON, &failed) != nil || !failed.Data.Attempted || failed.Data.Response == nil || failed.Data.Response.StatusCode != 404 || failed.Data.Response.Truncated || len(failed.Data.Response.JSON) == 0 {
		t.Fatal("live not-found read discarded its failed HTTP receipt")
	}
	r.ownedLiveComment(r.ownedLiveIssue(key), created.ID).Deleted = true
	r.save(t)
	remaining := r.success(t, "issue", "comment", "list", key, "--all")
	if len(remaining.Data.Comments) != 0 {
		t.Fatal("raw comment lifecycle left a duplicate or undeleted comment")
	}
	// Issue cleanup remains in the shared fresh-context owner, including when
	// an assertion above fails after a write. Comment state stays in its manifest.
}

func assertStoredComment(t *testing.T, r *liveRun, key, id string, expected []byte) {
	t.Helper()
	stored := r.success(t, "issue", "comment", "list", key, "--all")
	if len(stored.Data.Comments) != 1 || stored.Data.Comments[0].ID != id {
		t.Fatal("raw comment was not stored exactly once on its owned issue")
	}
	var want, got any
	if json.Unmarshal(expected, &want) != nil || json.Unmarshal(stored.Data.Comments[0].Body, &got) != nil || !reflect.DeepEqual(want, got) {
		t.Fatal("independently listed native comment differs from the authored ADF")
	}
}
