//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

type nativeWorklogInput struct {
	keys   []string
	digest [sha256.Size]byte
}

const liveWorklogComment = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Owned worklog acceptance <>&"}]}]}`

// Only this fixed workflow registers worklog input. Positional ownership and
// immutable authored bytes exclude selectors, key expressions and arbitrary
// payload files from the live write boundary.
func (r *liveRun) checkWorklogMutation(keys, args []string) error {
	if !r.verified || len(args) < 5 || args[0] != "worklog" || args[1] != "add" {
		return errors.New("unsupported owned worklog mutation")
	}
	positionals := []string{}
	i := 2
	for i < len(args) && !strings.HasPrefix(args[i], "--") {
		key := args[i]
		if !liveKey.MatchString(key) || r.ownedLiveIssue(key) == nil || slices.Contains(positionals, key) {
			return errors.New("worklog mutation requires distinct active owned positional keys")
		}
		positionals = append(positionals, key)
		i++
	}
	if len(positionals) == 0 || !slices.Equal(positionals, keys) {
		return errors.New("worklog mutation targets differ from their complete ownership declaration")
	}
	path := ""
	seen := map[string]bool{}
	for ; i < len(args); i++ {
		arg := args[i]
		if seen[arg] {
			return errors.New("worklog mutation repeats an option")
		}
		seen[arg] = true
		switch arg {
		case "--json-input":
			if path != "" || i+1 >= len(args) {
				return errors.New("worklog mutation requires one registered input")
			}
			i++
			path = args[i]
		case "--dry-run", "--notify=false", "--parallelism=1":
		default:
			return errors.New("worklog mutation has an unauthored option")
		}
	}
	registered, ok := r.worklogInputs[path]
	if !ok || !slices.Equal(registered.keys, keys) {
		return errors.New("worklog input is not registered for these owned targets")
	}
	data, err := os.ReadFile(path)
	if err != nil || sha256.Sum256(data) != registered.digest {
		return errors.New("worklog input changed after registration")
	}
	return nil
}

func (r *liveRun) worklogInput(t *testing.T, keys []string, started string, legacy bool) string {
	t.Helper()
	if !r.verified || len(keys) == 0 {
		t.Fatal("worklog input requires verified owned targets")
	}
	for _, key := range keys {
		if r.ownedLiveIssue(key) == nil {
			t.Fatal("worklog input includes an inactive or foreign issue")
		}
	}
	data := []byte(`{"timeSpentSeconds":60,"started":"` + started + `","comment":` + liveWorklogComment + `}`)
	if legacy {
		data = []byte(`{"time_spent":"2m","started":"` + started + `","comment_markdown":"Owned Markdown worklog"}`)
	}
	path := filepath.Join(r.dir, "native-worklog-"+output.NewRequestID()+".json")
	if err := xos.AtomicWrite(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if r.worklogInputs == nil {
		r.worklogInputs = make(map[string]nativeWorklogInput)
	}
	r.worklogInputs[path] = nativeWorklogInput{keys: slices.Clone(keys), digest: sha256.Sum256(data)}
	return path
}

func TestLiveWorklogMutationOwnershipGrammar(t *testing.T) {
	t.Parallel()
	r := &liveRun{verified: true, dir: t.TempDir(), manifest: liveManifest{Issues: []ownedIssue{{Key: "KEY-1"}, {Key: "KEY-2"}, {Key: "KEY-3", Deleted: true}}}}
	keys := []string{"KEY-1", "KEY-2"}
	path := r.worklogInput(t, keys, "2026-01-02T12:30:00Z", false)
	valid := []string{"worklog", "add", "KEY-1", "KEY-2", "--json-input", path, "--notify=false", "--parallelism=1"}
	for _, args := range [][]string{valid, append(slices.Clone(valid), "--dry-run")} {
		if err := r.checkMutation(keys, args); err != nil {
			t.Fatalf("authored owned worklog refused: %v", err)
		}
	}
	for _, tc := range []struct{ keys, args []string }{
		{[]string{"KEY-1"}, valid},
		{[]string{"KEY-1"}, []string{"worklog", "add", "KEY-1", "--json-input", path}},
		{[]string{"KEY-2", "KEY-1"}, valid},
		{[]string{"KEY-1:2"}, []string{"worklog", "add", "KEY-1:2", "--json-input", path}},
		{[]string{"KEY-1,KEY-2"}, []string{"worklog", "add", "KEY-1,KEY-2", "--json-input", path}},
		{[]string{"KEY-1", "FOREIGN-1"}, []string{"worklog", "add", "KEY-1", "FOREIGN-1", "--json-input", path}},
		{[]string{"KEY-1", "KEY-3"}, []string{"worklog", "add", "KEY-1", "KEY-3", "--json-input", path}},
		{[]string{"KEY-1", "KEY-1"}, []string{"worklog", "add", "KEY-1", "KEY-1", "--json-input", path}},
		{keys, []string{"worklog", "add", "KEY-1", "KEY-2", "--json-input=unregistered.json"}},
		{keys, []string{"worklog", "list", "KEY-1", "KEY-2", "--json-input", path}},
		{keys, append(slices.Clone(valid), "--time-spent=2m")},
		{keys, append(slices.Clone(valid), "--jql=project=KEY")},
		{keys, append(slices.Clone(valid), "--json-input", path)},
		{keys, []string{"worklog", "add", "KEY-1", "--dry-run", "KEY-2", "--json-input", path}},
	} {
		if err := r.checkMutation(tc.keys, tc.args); err == nil {
			t.Fatalf("unsafe worklog grammar admitted: %v", tc.args)
		}
	}
	r.manifest.Issues[1].Deleted = true
	if err := r.checkMutation(keys, valid); err == nil {
		t.Fatal("registered target deleted after registration was admitted")
	}
	r.manifest.Issues[1].Deleted = false
	if err := os.WriteFile(path, []byte(`{"timeSpentSeconds":120}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.checkMutation(keys, valid); err == nil {
		t.Fatal("changed worklog input admitted")
	}
	data := []byte(`{"timeSpentSeconds":60}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	r.worklogInputs[path] = nativeWorklogInput{keys: keys, digest: sha256.Sum256(data)}
	r.verified = false
	if err := r.checkMutation(keys, valid); err == nil {
		t.Fatal("unverified worklog mutation admitted")
	}
}

func TestLiveWorklogs(t *testing.T) {
	r := newLiveRun(t)
	first := r.create(t, "worklog-first")
	second := r.create(t, "worklog-second")
	keys := []string{first, second}
	started := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	path := r.worklogInput(t, keys, started, false)
	args := []string{"worklog", "add", first, second, "--json-input", path, "--notify=false", "--parallelism=1"}
	if err := r.checkMutation(keys, args); err != nil {
		t.Fatal(err)
	}
	preview := liveWorklogAdd(t, r, keys, append(slices.Clone(args), "--dry-run")...)
	if !preview.DryRun || len(preview.Results) != 2 {
		t.Fatal("worklog preview lost mutation state or targets")
	}
	for _, row := range preview.Results {
		if !row.OK || row.Data.Applied || row.Data.Worklog != nil {
			t.Fatal("worklog preview invented application")
		}
	}
	for _, key := range keys {
		if len(rawOwnedWorklogs(t, r, key)) != 0 {
			t.Fatal("local worklog preview changed Jira")
		}
	}
	blocked, _, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, args...)
	if exit != 3 || blocked.OK || len(blocked.Errors) != 1 || blocked.Errors[0].Code != "read_only_blocked" {
		t.Fatal("worklog add bypassed read-only policy")
	}
	for _, key := range keys {
		if len(rawOwnedWorklogs(t, r, key)) != 0 {
			t.Fatal("read-only worklog refusal changed Jira")
		}
	}
	applied := liveWorklogAdd(t, r, keys, args...)
	if applied.DryRun || applied.Notify || applied.BodyRoute != "adf" || len(applied.Results) != 2 {
		t.Fatal("live worklog receipt lost request facts")
	}
	for i, row := range applied.Results {
		if row.Issue.Key != keys[i] || !row.OK || !row.Data.Applied || row.Data.Worklog == nil || row.Data.ReceiptError != nil {
			t.Fatal("live worklog receipt lost ordered acknowledgement")
		}
		stored := rawOwnedWorklogs(t, r, keys[i])
		if len(stored) != 1 || stored[0].ID != row.Data.Worklog.ID || stored[0].Seconds != 60 {
			t.Fatal("independent raw worklog read disagrees with created identity/effort")
		}
		var want, got any
		if json.Unmarshal([]byte(liveWorklogComment), &want) != nil || json.Unmarshal(stored[0].Comment, &got) != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("independent raw worklog read changed native ADF")
		}
		instant, err := time.Parse("2006-01-02T15:04:05.999-0700", stored[0].Started)
		expected, expectedErr := time.Parse(time.RFC3339, started)
		if err != nil || expectedErr != nil || !instant.Equal(expected) {
			t.Fatal("stored worklog start instant differs from input")
		}
	}
	legacyPath := r.worklogInput(t, []string{first}, started, true)
	legacy := liveWorklogAdd(t, r, []string{first}, "worklog", "add", first, "--json-input", legacyPath, "--notify=false", "--parallelism=1")
	if legacy.BodyRoute != "markdown" || len(legacy.Results) != 1 || !legacy.Results[0].Data.Applied {
		t.Fatal("legacy worklog Markdown route did not apply")
	}
	stored := rawOwnedWorklogs(t, r, first)
	if len(stored) != 2 || stored[1].ID != legacy.Results[0].Data.Worklog.ID || stored[1].Seconds != 120 || !strings.Contains(string(stored[1].Comment), "Owned Markdown worklog") {
		t.Fatal("independent read lost legacy effort/comment")
	}
	page := liveWorklogList(t, r, first, "--limit=1")
	if len(page.Results) != 1 || page.Results[0].Data.Complete || page.Results[0].Data.Pagination.Returned != 1 || page.Results[0].Data.Pagination.Total != 2 {
		t.Fatal("worklog first page misreports completion")
	}
	resumed := liveWorklogList(t, r, first, "--start=1", "--limit=1")
	if !resumed.Results[0].Data.Complete || len(resumed.Results[0].Data.Worklogs) != 1 || resumed.Results[0].Data.Worklogs[0].ID != stored[1].ID {
		t.Fatal("worklog resume skipped the second entry")
	}
	all := liveWorklogList(t, r, first, second, "--all", "--parallelism=1")
	if len(all.Results) != 2 {
		t.Fatal("worklog all lost ordered issues")
	}
	for i, row := range all.Results {
		independent := rawOwnedWorklogs(t, r, keys[i])
		if row.Issue.Key != keys[i] || !row.OK || !row.Data.Complete || row.Data.Truncated || len(row.Data.Worklogs) != len(independent) {
			t.Fatal("worklog drain disagrees with independent raw pages")
		}
		for j, w := range row.Data.Worklogs {
			if w.ID != independent[j].ID || w.Fields.TimeSpentSeconds.Value != independent[j].Seconds {
				t.Fatal("typed worklogs disagree with native identities/effort")
			}
		}
	}
}

func liveWorklogAdd(t *testing.T, r *liveRun, keys []string, args ...string) envelope.WorklogAddOutput {
	t.Helper()
	if err := r.checkMutation(keys, args); err != nil {
		t.Fatal(err)
	}
	frame, raw, exit := r.invoke(t, t.Context(), nil, args...)
	var receipt struct {
		Data envelope.WorklogAddOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(raw, &receipt) != nil {
		t.Fatal("owned worklog write failed; inspect private receipts")
	}
	return receipt.Data
}

func liveWorklogList(t *testing.T, r *liveRun, args ...string) envelope.WorklogListOutput {
	t.Helper()
	frame, raw, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, append([]string{"worklog", "list"}, args...)...)
	var receipt struct {
		Data envelope.WorklogListOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(raw, &receipt) != nil {
		t.Fatal("owned worklog list failed; inspect private receipts")
	}
	return receipt.Data
}

type rawWorklog struct {
	ID      string          `json:"id"`
	Seconds int64           `json:"timeSpentSeconds"`
	Started string          `json:"started"`
	Comment json.RawMessage `json:"comment"`
}

func rawOwnedWorklogs(t *testing.T, r *liveRun, key string) []rawWorklog {
	t.Helper()
	if r.ownedLiveIssue(key) == nil {
		t.Fatal("raw worklog verification requires an active owned issue")
	}
	read := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key+"/worklog", "--query=startAt=0", "--query=maxResults=100")
	var page struct {
		StartAt  int          `json:"startAt"`
		Total    int          `json:"total"`
		Worklogs []rawWorklog `json:"worklogs"`
	}
	if json.Unmarshal(read.Response.JSON, &page) != nil || page.StartAt != 0 || page.Total != len(page.Worklogs) || page.Worklogs == nil {
		t.Fatal("raw worklog verification is incomplete")
	}
	return page.Worklogs
}
