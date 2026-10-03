//go:build live

package live

import (
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

// Exact positional keys and a small authored option set keep range expansion,
// selectors and payload inputs outside the live mutation authorization boundary.
// Both the destination and every child must be active fixtures from this run.
func (r *liveRun) checkEpicMutation(keys, args []string) error {
	if !r.verified || len(args) < 3 || args[0] != "epic" || (args[1] != "add" && args[1] != "remove") {
		return errors.New("unsupported owned epic mutation")
	}
	positionals := []string{}
	optionsStarted := false
	for _, arg := range args[2:] {
		if strings.HasPrefix(arg, "--") {
			optionsStarted = true
			switch arg {
			case "--verify", "--validate-remote", "--dry-run", "--notify=false", "--parallelism=1":
			default:
				return errors.New("live epic mutation has an unauthored option")
			}
			continue
		}
		if optionsStarted || !liveKey.MatchString(arg) || r.ownedLiveIssue(arg) == nil || slices.Contains(positionals, arg) {
			return errors.New("live epic mutation requires distinct active owned positional keys")
		}
		positionals = append(positionals, arg)
	}
	if len(positionals) == 0 || (args[1] == "add" && len(positionals) < 2) || !slices.Equal(positionals, keys) {
		return errors.New("live epic mutation targets differ from the complete ownership declaration")
	}
	return nil
}

func TestLiveEpicMutationOwnershipGrammar(t *testing.T) {
	r := &liveRun{verified: true, manifest: liveManifest{Issues: []ownedIssue{{Key: "KEY-1"}, {Key: "KEY-2"}, {Key: "KEY-100"}}}}
	valid := []string{"epic", "add", "KEY-1", "KEY-2", "KEY-100", "--verify", "--notify=false", "--parallelism=1"}
	keys := []string{"KEY-1", "KEY-2", "KEY-100"}
	if err := r.checkMutation(keys, valid); err != nil {
		t.Fatalf("owned membership refused: %v", err)
	}
	for _, tc := range []struct{ keys, args []string }{
		{[]string{"KEY-1", "FOREIGN-1"}, []string{"epic", "add", "KEY-1", "FOREIGN-1"}},
		{[]string{"KEY-1:2", "KEY-100"}, []string{"epic", "add", "KEY-1:2", "KEY-100"}},
		{[]string{"KEY-1,KEY-2", "KEY-100"}, []string{"epic", "add", "KEY-1,KEY-2", "KEY-100"}},
		{[]string{"KEY-1", "KEY-1"}, []string{"epic", "add", "KEY-1", "KEY-1"}},
		{[]string{"KEY-1"}, []string{"epic", "remove", "KEY-1", "--jql=project=KEY"}},
		{[]string{"KEY-1"}, []string{"epic", "remove", "KEY-1", "--json-input=payload.json"}},
		{[]string{"KEY-1", "KEY-2"}, []string{"epic", "remove", "KEY-1", "--verify", "KEY-2"}},
		{[]string{"10001"}, []string{"epic", "remove", "10001"}},
		{[]string{"KEY-1"}, []string{"epic", "delete", "KEY-1"}},
	} {
		if err := r.checkMutation(tc.keys, tc.args); err == nil {
			t.Fatalf("unsafe membership grammar admitted: %v", tc.args)
		}
	}
	if err := r.checkMutation([]string{"KEY-1"}, valid); err == nil {
		t.Fatal("parent ownership omitted")
	}
	r.manifest.Issues[2].Deleted = true
	if err := r.checkMutation(keys, valid); err == nil {
		t.Fatal("deleted parent admitted")
	}
	r.manifest.Issues[2].Deleted = false
	r.verified = false
	if err := r.checkMutation(keys, valid); err == nil {
		t.Fatal("unverified membership admitted")
	}
}

func TestLiveEpicMembership(t *testing.T) {
	r := newLiveRun(t)
	epic := createLiveEpic(t, r, "epic-membership-parent")
	first := r.create(t, "epic-membership-first")
	second := r.create(t, "epic-membership-second")
	children := []string{first, second}
	all := append(slices.Clone(children), epic)
	add := append([]string{"epic", "add"}, all...)
	add = append(add, "--verify", "--notify=false", "--parallelism=1")
	if err := r.checkMutation(all, add); err != nil {
		t.Fatal(err)
	}
	for _, extra := range [][]string{{"--dry-run"}, {"--dry-run", "--validate-remote"}} {
		_, raw, exit := r.invoke(t, t.Context(), nil, append(slices.Clone(add), extra...)...)
		var frame struct {
			Data envelope.EpicMembershipOutput `json:"data"`
		}
		if exit != 0 || json.Unmarshal(raw, &frame) != nil || !frame.Data.DryRun || len(frame.Data.Results) != 2 {
			t.Fatal("live assignment preview failed")
		}
		for _, row := range frame.Data.Results {
			if !row.OK || row.Data.Applied || row.Data.RemoteValidated != (len(extra) == 2) {
				t.Fatal("preview invented a write or lost remote checks")
			}
		}
		for _, child := range children {
			assertRawOwnedParent(t, r, child, "")
		}
	}
	out := liveMembership(t, r, all, add...)
	checkLiveMembershipReceipt(t, out, children, epic, true)
	for _, child := range children {
		assertRawOwnedParent(t, r, child, epic)
	}
	remove := append([]string{"epic", "remove"}, children...)
	remove = append(remove, "--verify", "--notify=false", "--parallelism=1")
	if err := r.checkMutation(children, remove); err != nil {
		t.Fatal(err)
	}
	_, _, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, remove...)
	if exit != 3 {
		t.Fatal("read-only epic removal was not refused")
	}
	for _, child := range children {
		assertRawOwnedParent(t, r, child, epic)
	}
	out = liveMembership(t, r, children, remove...)
	checkLiveMembershipReceipt(t, out, children, "", true)
	for _, child := range children {
		assertRawOwnedParent(t, r, child, "")
	}
	out = liveMembership(t, r, children, remove...)
	checkLiveMembershipReceipt(t, out, children, "", false)
}

func liveMembership(t *testing.T, r *liveRun, keys []string, args ...string) envelope.EpicMembershipOutput {
	t.Helper()
	if err := r.checkMutation(keys, args); err != nil {
		t.Fatal(err)
	}
	frame, raw, exit := r.invoke(t, t.Context(), nil, args...)
	var receipt struct {
		Data envelope.EpicMembershipOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(raw, &receipt) != nil {
		t.Fatal("owned membership mutation failed; inspect private receipts")
	}
	return receipt.Data
}

func checkLiveMembershipReceipt(t *testing.T, out envelope.EpicMembershipOutput, children []string, parent string, applied bool) {
	t.Helper()
	if out.DryRun || out.Notify || out.Epic != parent || len(out.Results) != len(children) {
		t.Fatal("live membership receipt lost request facts")
	}
	for i, row := range out.Results {
		if row.Issue.Key != children[i] || !row.OK || row.Data == nil || row.Data.Applied != applied || row.Data.VerifyFailed || !row.Data.RemoteValidated || row.Data.Parent == nil || row.Data.Parent.Key != parent || row.Data.AlreadyUnparented == applied {
			t.Fatal("live membership receipt lost ordered acknowledged/readback outcomes")
		}
	}
}

func assertRawOwnedParent(t *testing.T, r *liveRun, child, parent string) {
	t.Helper()
	raw := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(child), "--query=fields=parent,summary")
	var observed struct {
		Fields *struct {
			Parent *struct {
				Key string `json:"key"`
			} `json:"parent"`
		} `json:"fields"`
	}
	if json.Unmarshal(raw.Response.JSON, &observed) != nil || observed.Fields == nil {
		t.Fatal("independent parent read lacks fields")
	}
	key := ""
	if observed.Fields.Parent != nil {
		key = observed.Fields.Parent.Key
	}
	if key != parent {
		t.Fatal("independent raw parent read disagrees with the owned relation")
	}
}
