//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/envelope"
)

type nativeCommentInput struct {
	key, id string
	digest  [sha256.Size]byte
}

func (r *liveRun) commentInput(t *testing.T, key, id string, document []byte) string {
	t.Helper()
	issue := r.ownedLiveIssue(key)
	if !r.verified || issue == nil || r.ownedLiveComment(issue, id) == nil {
		t.Fatal("comment input requires a registered active owned comment")
	}
	path := r.nativeBody(t, document)
	if r.commentInputs == nil {
		r.commentInputs = make(map[string]nativeCommentInput)
	}
	r.commentInputs[path] = nativeCommentInput{key: key, id: id, digest: sha256.Sum256(document)}
	return path
}

// Editing and deletion address one registered active comment on one active
// owned issue. Native input is bound to that pair and its authored bytes.
func (r *liveRun) checkCommentMutation(keys, args []string) error {
	if !r.verified || len(keys) != 1 || len(args) < 5 || args[0] != "issue" || args[1] != "comment" || (args[2] != "edit" && args[2] != "delete") || keys[0] != args[3] {
		return errors.New("comment mutation requires one explicit owned resource")
	}
	issue := r.ownedLiveIssue(args[3])
	if issue == nil || !liveKey.MatchString(args[3]) || r.ownedLiveComment(issue, args[4]) == nil {
		return errors.New("comment mutation targets an inactive or foreign comment")
	}
	seen := map[string]bool{}
	for i := 5; i < len(args); i++ {
		flag, value, inline := strings.Cut(args[i], "=")
		if seen[flag] {
			return errors.New("comment mutation repeats an option")
		}
		seen[flag] = true
		switch flag {
		case "--dry-run":
			if inline {
				return errors.New("comment mutation changes an authored preview flag")
			}
		case "--force":
			if args[2] != "delete" || inline {
				return errors.New("comment mutation contains an unauthored force flag")
			}
		case "--notify":
			if args[2] != "edit" || !inline || value != "false" {
				return errors.New("comment mutation changes its authored notification option")
			}
		case "--clear-visibility":
			if args[2] != "edit" || inline {
				return errors.New("comment mutation changes its authored visibility option")
			}
		case "--json-input", "--visibility-role":
			if args[2] != "edit" {
				return errors.New("comment deletion cannot carry an edit body or visibility")
			}
			if !inline {
				i++
				if i == len(args) {
					return errors.New("comment mutation omits an option value")
				}
				value = args[i]
			}
			if flag == "--visibility-role" {
				if r.commentRole == "" || value != r.commentRole {
					return errors.New("comment mutation names an unverified role")
				}
				continue
			}
			input, ok := r.commentInputs[value]
			if !ok || input.key != args[3] || input.id != args[4] {
				return errors.New("comment mutation uses an unauthored or differently targeted body")
			}
			data, err := os.ReadFile(value)
			if err != nil || sha256.Sum256(data) != input.digest {
				return errors.New("comment mutation body changed after registration")
			}
		default:
			return errors.New("comment mutation contains an unauthored option or indirect target")
		}
	}
	if args[2] == "edit" && !seen["--json-input"] {
		return errors.New("comment edit requires its registered native body")
	}
	if seen["--clear-visibility"] && seen["--visibility-role"] {
		return errors.New("comment edit combines contradictory visibility options")
	}
	return nil
}

type storedOwnedComment struct {
	ID         string          `json:"id"`
	Body       json.RawMessage `json:"body"`
	Visibility *struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"visibility"`
}

func rawOwnedComment(t *testing.T, r *liveRun, key, id string) storedOwnedComment {
	t.Helper()
	issue := r.ownedLiveIssue(key)
	if issue == nil || r.ownedLiveComment(issue, id) == nil {
		t.Fatal("comment comparison requires an active owned comment")
	}
	read := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key+"/comment/"+id)
	var comment storedOwnedComment
	if json.Unmarshal(read.Response.JSON, &comment) != nil || comment.ID != id || len(comment.Body) == 0 {
		t.Fatal("raw comment read lost its identity or stored body")
	}
	return comment
}

func requireStoredComment(t *testing.T, r *liveRun, key, id string, body []byte, role string) {
	t.Helper()
	stored := rawOwnedComment(t, r, key, id)
	equal, err := adf.StructuralEqual(body, stored.Body)
	if err != nil || !equal {
		t.Fatal("stored comment changed its complete authored structure")
	}
	if role == "" {
		if stored.Visibility != nil {
			t.Fatal("stored comment acquired an unauthored visibility restriction")
		}
	} else if stored.Visibility == nil || stored.Visibility.Type != "role" || stored.Visibility.Value != role {
		t.Fatal("stored comment lost its authored role restriction")
	}
	listed := r.success(t, "issue", "comment", "list", key, "--all")
	if err := verifyStoredADFComments(map[string]json.RawMessage{id: body}, listed); err != nil {
		t.Fatal(err)
	}
}

// Discover a role the server says includes the current account. No role,
// membership or pre-existing issue is changed by this metadata read.
func currentCommentRole(t *testing.T, r *liveRun) string {
	t.Helper()
	identity := rawRead(t, r, "api", "get", "/rest/api/3/myself")
	var account struct {
		ID string `json:"accountId"`
	}
	if json.Unmarshal(identity.Response.JSON, &account) != nil || account.ID == "" {
		t.Fatal("current-account metadata lacks a stable identity")
	}
	read := rawRead(t, r, "api", "get", "/rest/api/3/project/"+r.manifest.Project+"/role")
	var roles map[string]string
	if json.Unmarshal(read.Response.JSON, &roles) != nil {
		t.Fatal("project role metadata is malformed")
	}
	for name, endpoint := range roles {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Scheme+"://"+parsed.Host != r.manifest.BaseURL || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !strings.HasPrefix(parsed.Path, "/rest/api/3/project/") || !strings.Contains(parsed.Path, "/role/") {
			t.Fatal("project role metadata escaped the verified site")
		}
		roleID := parsed.Path[strings.LastIndex(parsed.Path, "/")+1:]
		numericID, err := strconv.ParseUint(roleID, 10, 64)
		if err != nil || numericID == 0 {
			t.Fatal("project role metadata lacks a numeric identity")
		}
		// Rebind the metadata's role identity to this run's project. An
		// upstream URL never selects another project for the membership read.
		role := rawRead(t, r, "api", "get", "/rest/api/3/project/"+r.manifest.Project+"/role/"+roleID)
		var detail struct {
			Name        string `json:"name"`
			CurrentUser bool   `json:"currentUserRole"`
			Actors      []struct {
				User struct {
					AccountID string `json:"accountId"`
				} `json:"actorUser"`
			} `json:"actors"`
		}
		if json.Unmarshal(role.Response.JSON, &detail) != nil || detail.Name != name {
			t.Fatal("project role metadata lost its identity")
		}
		if detail.CurrentUser {
			return name
		}
		for _, actor := range detail.Actors {
			if actor.User.AccountID == account.ID {
				return name
			}
		}
	}
	t.Fatal("live comment restriction coverage requires a server-confirmed current-account role")
	return ""
}

func TestLiveCommentLifecycle(t *testing.T) {
	r := newLiveRun(t)
	r.commentRole = currentCommentRole(t, r)
	key := r.create(t, "comment-lifecycle")
	original := []byte(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"original comment"}]}]}`)
	created := r.mutate(t, []string{key}, "issue", "comment", "add", key, "--json-input", r.nativeBody(t, original))
	id := postedADFCommentID(t, created)
	r.registerComment(t, key, id)
	requireStoredComment(t, r, key, id, original, "")
	updated := []byte(`{"type":"doc","version":1,"content":[{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":"native rich edit <&>"}]}]},{"type":"paragraph","content":[{"type":"text","text":"replacement"}]}]}`)
	path := r.commentInput(t, key, id, updated)
	for _, preview := range []bool{true, false} {
		args := []string{"issue", "comment", "add", key, "--json-input", path}
		var extra []string
		if preview {
			args = append(args, "--dry-run")
		} else {
			extra = []string{"JIRA_READ_ONLY=true"}
		}
		if err := r.checkMutation([]string{key}, args); err != nil {
			t.Fatal(err)
		}
		frame, stdout, exit := r.invoke(t, t.Context(), extra, args...)
		var out struct {
			Data envelope.IssueCommentAddOutput `json:"data"`
		}
		if json.Unmarshal(stdout, &out) != nil || len(out.Data.Results) != 1 || out.Data.Results[0].Issue.Key != key || string(out.Data.Body) != string(updated) {
			t.Fatal("comment add preview/refusal lost its target or native authored body")
		}
		row := out.Data.Results[0]
		if preview {
			if exit != 0 || !frame.OK || !out.Data.DryRun || !row.OK || row.Data == nil || row.Data.Comment != nil {
				t.Fatal("comment add preview invented a server receipt")
			}
		} else if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "read_only_blocked" || row.OK || row.Data != nil {
			t.Fatal("comment add bypassed read-only enforcement or invented a comment")
		}
		requireStoredComment(t, r, key, id, original, "")
	}
	for _, verb := range []string{"edit", "delete"} {
		for _, preview := range []bool{true, false} {
			args := []string{"issue", "comment", verb, key, id}
			if verb == "edit" {
				args = append(args, "--json-input", path, "--notify=false")
			} else {
				args = append(args, "--force")
			}
			var extra []string
			if preview {
				args = append(args, "--dry-run")
			} else {
				extra = []string{"JIRA_READ_ONLY=true"}
			}
			if err := r.checkMutation([]string{key}, args); err != nil {
				t.Fatal(err)
			}
			frame, stdout, exit := r.invoke(t, t.Context(), extra, args...)
			if preview {
				var out struct {
					Data envelope.IssueCommentEditOutput `json:"data"`
				}
				if json.Unmarshal(stdout, &out) != nil || exit != 0 || !frame.OK || !out.Data.DryRun || out.Data.Issue.Key != key || out.Data.CommentID != id || out.Data.Comment != nil {
					t.Fatal("comment preview lost its target or invented a server receipt")
				}
			} else if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "read_only_blocked" {
				t.Fatal("comment mutation bypassed read-only enforcement")
			}
			requireStoredComment(t, r, key, id, original, "")
		}
	}
	refusalArgs := []string{"issue", "comment", "delete", key, id}
	if err := r.checkMutation([]string{key}, refusalArgs); err != nil {
		t.Fatal(err)
	}
	refused, _, exit := r.invoke(t, t.Context(), nil, refusalArgs...)
	if exit != 3 || refused.OK || len(refused.Errors) != 1 || refused.Errors[0].Code != "flag_required" {
		t.Fatal("headless comment deletion did not require explicit confirmation")
	}
	requireStoredComment(t, r, key, id, original, "")
	for index, step := range []struct {
		options []string
		role    string
	}{
		{[]string{"--visibility-role", r.commentRole}, r.commentRole},
		{nil, r.commentRole},
		{[]string{"--clear-visibility"}, ""},
	} {
		body := updated
		if index > 0 {
			body = []byte(strings.Replace(string(updated), "replacement", "replacement "+strconv.Itoa(index), 1))
			path = r.commentInput(t, key, id, body)
		}
		args := append([]string{"issue", "comment", "edit", key, id, "--json-input", path, "--notify=false"}, step.options...)
		if err := r.checkMutation([]string{key}, args); err != nil {
			t.Fatal(err)
		}
		frame, stdout, exit := r.invoke(t, t.Context(), nil, args...)
		var out struct {
			Data envelope.IssueCommentEditOutput `json:"data"`
		}
		if json.Unmarshal(stdout, &out) != nil || exit != 0 || !frame.OK || out.Data.DryRun || out.Data.Notify || out.Data.CommentID != id || out.Data.Issue.Key != key || out.Data.Comment == nil || out.Data.Comment.ID != id || string(out.Data.Body) != string(body) {
			t.Fatal("comment edit lost its target, native authored body, notification option or stored receipt")
		}
		requireStoredComment(t, r, key, id, body, step.role)
	}
	deleteArgs := []string{"issue", "comment", "delete", key, id, "--force"}
	if err := r.checkMutation([]string{key}, deleteArgs); err != nil {
		t.Fatal(err)
	}
	deleted, stdout, exit := r.invoke(t, t.Context(), nil, deleteArgs...)
	var deletion struct {
		Data envelope.IssueCommentDeleteOutput `json:"data"`
	}
	if json.Unmarshal(stdout, &deletion) != nil || exit != 0 || !deleted.OK || deletion.Data.DryRun || deletion.Data.Issue.Key != key || deletion.Data.CommentID != id {
		t.Fatal("owned comment deletion lost its confirmed target receipt")
	}
	missing, _, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "api", "get", "/rest/api/3/issue/"+key+"/comment/"+id)
	if exit != 2 || missing.OK || len(missing.Errors) != 1 || missing.Errors[0].Code != "jira_not_found" {
		t.Fatal("owned comment deletion was not independently confirmed absent")
	}
	listed := r.success(t, "issue", "comment", "list", key, "--all")
	if len(listed.Data.Comments) != 0 {
		t.Fatal("deleted comment remained in the complete domain list")
	}
	r.ownedLiveComment(r.ownedLiveIssue(key), id).Deleted = true
	r.save(t)
}
