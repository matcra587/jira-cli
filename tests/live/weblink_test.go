//go:build live

package live

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

type ownedWeblink struct {
	ID       string `json:"id"`
	GlobalID string `json:"global_id,omitempty"`
	Deleted  bool   `json:"deleted"`
}

type (
	weblinkRecipe     struct{ URL, Title, Summary, Relationship, GlobalID string }
	ownedWeblinkInput struct {
		keys   []string
		recipe weblinkRecipe
		path   string
		digest [sha256.Size]byte
	}
)

// Each add is bound to explicit active owned issues and the exact authored
// link. Deletion additionally requires a registered resource on that issue.
func (r *liveRun) checkWeblinkMutation(keys, args []string) error {
	if !r.verified || len(keys) == 0 || len(args) < 4 || args[0] != "issue" || args[1] != "weblink" {
		return errors.New("remote-link mutation requires verified explicit owned targets")
	}
	if args[2] == "delete" {
		if len(keys) != 1 || (len(args) != 5 && len(args) != 6) || args[3] != keys[0] || len(args) == 6 && args[5] != "--dry-run" {
			return errors.New("remote-link deletion contains unauthored arguments")
		}
		issue := r.ownedLiveIssue(args[3])
		if issue != nil {
			for _, link := range issue.Weblinks {
				if link.ID == args[4] && !link.Deleted {
					return nil
				}
			}
		}
		return errors.New("remote-link deletion targets an inactive or foreign resource")
	}
	if args[2] != "add" {
		return errors.New("unsupported remote-link mutation grammar")
	}
	i := 3
	seenKeys := map[string]bool{}
	for i < len(args) && !strings.HasPrefix(args[i], "--") {
		if seenKeys[args[i]] || !liveKey.MatchString(args[i]) || r.ownedLiveIssue(args[i]) == nil {
			return errors.New("remote-link mutation targets an inactive, repeated or foreign issue")
		}
		seenKeys[args[i]] = true
		i++
	}
	if !slices.Equal(args[3:i], keys) {
		return errors.New("remote-link targets differ from their ownership declaration")
	}
	var recipe weblinkRecipe
	var nativePath string
	seen := map[string]bool{}
	for ; i < len(args); i++ {
		flag, value, hasValue := strings.Cut(args[i], "=")
		if seen[flag] {
			return errors.New("remote-link mutation repeats an option")
		}
		seen[flag] = true
		switch flag {
		case "--json-input":
			if !hasValue || value == "" || value == "-" {
				return errors.New("native remote-link mutation requires its registered file")
			}
			nativePath = value
		case "--dry-run":
			if hasValue {
				return errors.New("remote-link mutation changes an authored boolean")
			}
		case "--parallelism":
			if !hasValue || (value != "1" && value != "2") {
				return errors.New("remote-link mutation changes its authored worker bound")
			}
		case "--url", "--title", "--summary", "--relationship", "--global-id":
			if !hasValue {
				i++
				if i == len(args) {
					return errors.New("remote-link mutation omits an option value")
				}
				value = args[i]
			}
			switch flag {
			case "--url":
				recipe.URL = value
			case "--title":
				recipe.Title = value
			case "--summary":
				recipe.Summary = value
			case "--relationship":
				recipe.Relationship = value
			case "--global-id":
				recipe.GlobalID = value
			}
		default:
			return errors.New("remote-link mutation contains an unauthored option")
		}
	}
	if nativePath != "" {
		for _, flag := range []string{"--url", "--title", "--summary", "--relationship", "--global-id"} {
			if seen[flag] {
				return errors.New("native remote-link mutation contains an unauthored flag merge")
			}
		}
		for _, input := range r.weblinkInputs {
			if input.path == nativePath && slices.Equal(input.keys, keys) {
				data, err := os.ReadFile(nativePath)
				if err == nil && sha256.Sum256(data) == input.digest {
					return nil
				}
				return errors.New("native remote-link input changed after registration")
			}
		}
		return errors.New("native remote-link input is not registered for these owned targets")
	}
	for _, input := range r.weblinkInputs {
		if input.path == "" && slices.Equal(input.keys, keys) && input.recipe == recipe && seen["--url"] {
			return nil
		}
	}
	return errors.New("remote-link mutation differs from its registered authored request")
}

func (r *liveRun) allowWeblink(keys []string, recipe weblinkRecipe) {
	r.weblinkInputs = append(r.weblinkInputs, ownedWeblinkInput{keys: slices.Clone(keys), recipe: recipe})
}

func weblinkArgs(keys []string, recipe weblinkRecipe) []string {
	args := append([]string{"issue", "weblink", "add"}, keys...)
	args = append(args, "--url="+recipe.URL, "--parallelism=2")
	for _, option := range []struct{ flag, value string }{{"title", recipe.Title}, {"summary", recipe.Summary}, {"relationship", recipe.Relationship}, {"global-id", recipe.GlobalID}} {
		if option.value != "" {
			args = append(args, "--"+option.flag+"="+option.value)
		}
	}
	return args
}

type storedWeblink struct {
	ID           int64                                `json:"id"`
	Self         string                               `json:"self"`
	GlobalID     string                               `json:"globalId"`
	Relationship string                               `json:"relationship"`
	Object       struct{ URL, Title, Summary string } `json:"object"`
	Application  struct{ Name string }                `json:"application"`
}

func (link storedWeblink) matches(recipe weblinkRecipe) bool {
	title := recipe.Title
	if title == "" {
		title = recipe.URL
	}
	return link.Object.URL == recipe.URL && link.Object.Title == title && link.Object.Summary == recipe.Summary && link.GlobalID == recipe.GlobalID && link.Relationship == recipe.Relationship
}

func rawOwnedWeblinks(t *testing.T, r *liveRun, key string) []storedWeblink {
	t.Helper()
	if r.ownedLiveIssue(key) == nil {
		t.Fatal("remote-link comparison requires an active owned issue")
	}
	read := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key+"/remotelink")
	var links []storedWeblink
	if json.Unmarshal(read.Response.JSON, &links) != nil || links == nil {
		t.Fatal("raw remote-link read did not publish a complete native array")
	}
	seen := map[int64]bool{}
	for _, link := range links {
		if link.ID <= 0 || seen[link.ID] || link.Object.URL == "" {
			t.Fatal("raw remote-link read has missing or repeated identity or target")
		}
		seen[link.ID] = true
	}
	return links
}

func compareOwnedWeblinks(t *testing.T, r *liveRun, key string) []storedWeblink {
	t.Helper()
	native := rawOwnedWeblinks(t, r, key)
	frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "issue", "weblink", "list", key)
	var domain struct {
		Data envelope.IssueWeblinkListOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &domain) != nil || len(domain.Data.Results) != 1 || domain.Data.Results[0].Issue.Key != key || !domain.Data.Results[0].OK || domain.Data.Results[0].Data == nil || len(domain.Data.Results[0].Data.Weblinks) != len(native) {
		t.Fatal("domain remote-link list differs from its complete native observation")
	}
	for i, link := range native {
		got := domain.Data.Results[0].Data.Weblinks[i]
		if got.ID != strconv.FormatInt(link.ID, 10) || got.URL != link.Object.URL || got.Title != link.Object.Title || got.Summary != link.Object.Summary || got.GlobalID != link.GlobalID || got.Relationship != link.Relationship || got.Application != link.Application.Name {
			t.Fatal("domain remote-link identity, authored fields or order differ from native storage")
		}
	}
	return native
}

func (r *liveRun) registerWeblink(t *testing.T, key string, ref envelope.WeblinkRef, recipe weblinkRecipe) {
	t.Helper()
	issue := r.ownedLiveIssue(key)
	id, err := strconv.ParseInt(ref.ID, 10, 64)
	if issue == nil || err != nil || id <= 0 {
		t.Fatal("cannot register a remote-link receipt without owned parent and positive identity")
	}
	read := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key+"/remotelink/"+ref.ID)
	var stored storedWeblink
	if json.Unmarshal(read.Response.JSON, &stored) != nil || stored.ID != id {
		t.Fatal("acknowledged remote link has no independently observed owned resource")
	}
	registered := false
	for _, link := range issue.Weblinks {
		if link.ID == ref.ID {
			if link.Deleted || link.GlobalID != recipe.GlobalID {
				t.Fatal("remote-link replacement changed registered ownership")
			}
			registered = true
			break
		}
	}
	if !registered {
		issue.Weblinks = append(issue.Weblinks, ownedWeblink{ID: ref.ID, GlobalID: recipe.GlobalID})
		r.save(t)
	}
	if stored.Self != ref.Self || !stored.matches(recipe) {
		t.Fatal("acknowledged remote link differs from its independently stored resource")
	}
}

func (r *liveRun) finishOwnedWeblinks(t *testing.T) {
	for i := range r.manifest.Issues {
		issue := &r.manifest.Issues[i]
		for j := range issue.Weblinks {
			link := &issue.Weblinks[j]
			if link.Deleted {
				continue
			}
			frame, _, exit := r.invoke(t, context.Background(), []string{"JIRA_READ_ONLY=true"}, "api", "get", "/rest/api/3/issue/"+issue.Key+"/remotelink/"+link.ID)
			if exit != 2 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "jira_not_found" {
				t.Error("registered remote link remains unclosed after owned-parent cleanup")
				continue
			}
			link.Deleted = true
			r.save(t)
		}
	}
}

func TestLiveWeblinks(t *testing.T) {
	r := newLiveRun(t)
	keys := []string{r.create(t, "remote-link lifecycle first"), r.create(t, "remote-link lifecycle second")}
	for _, key := range keys {
		if len(compareOwnedWeblinks(t, r, key)) != 0 {
			t.Fatal("fresh owned issue already has remote links")
		}
	}
	initial := weblinkRecipe{URL: "https://example.com/runbook?x=1&y=2", Title: "Runbook <&> \u2028", Summary: "Authored summary <&>", Relationship: "is documented by", GlobalID: "system=acceptance&id=" + r.manifest.RunID}
	r.allowWeblink(keys, initial)
	args := weblinkArgs(keys, initial)
	for _, preview := range []bool{true, false} {
		invoke := slices.Clone(args)
		extra := []string{"JIRA_READ_ONLY=true"}
		if preview {
			invoke = append(invoke, "--dry-run")
			extra = nil
		}
		if err := r.checkMutation(keys, invoke); err != nil {
			t.Fatal(err)
		}
		frame, stdout, exit := r.invoke(t, t.Context(), extra, invoke...)
		var receipt struct {
			Data envelope.IssueWeblinkAddOutput `json:"data"`
		}
		if json.Unmarshal(stdout, &receipt) != nil || receipt.Data.URL != initial.URL || receipt.Data.Title != initial.Title || receipt.Data.GlobalID != initial.GlobalID || len(receipt.Data.Results) != len(keys) {
			t.Fatal("remote-link preview/refusal lost its authored request")
		}
		if preview {
			if exit != 0 || !frame.OK || !receipt.Data.DryRun {
				t.Fatal("remote-link preview did not complete locally")
			}
		} else if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "read_only_blocked" {
			t.Fatal("remote-link add bypassed read-only policy")
		}
		for i, key := range keys {
			row := receipt.Data.Results[i]
			if row.Issue.Key != key || row.OK != preview || preview && (row.Data == nil || row.Data.Created != nil || row.Data.Weblink != nil) || !preview && row.Data != nil {
				t.Fatal("remote-link preview/refusal fabricated completion or identity")
			}
			if len(compareOwnedWeblinks(t, r, key)) != 0 {
				t.Fatal("remote-link preview/refusal changed native storage")
			}
		}
	}
	ids := map[string]string{}
	for step, recipe := range []weblinkRecipe{initial, {URL: "https://example.com/revised", Title: "Revised", GlobalID: initial.GlobalID}} {
		r.allowWeblink(keys, recipe)
		args := weblinkArgs(keys, recipe)
		if err := r.checkMutation(keys, args); err != nil {
			t.Fatal(err)
		}
		frame, stdout, exit := r.invoke(t, t.Context(), nil, args...)
		var receipt struct {
			Data envelope.IssueWeblinkAddOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(stdout, &receipt) != nil || len(receipt.Data.Results) != len(keys) {
			t.Fatal("remote-link fan-out failed or lost ordered outcomes")
		}
		for i, key := range keys {
			row := receipt.Data.Results[i]
			if row.Issue.Key != key || !row.OK || row.Data == nil || row.Data.Weblink == nil || row.Data.Created == nil || *row.Data.Created != (step == 0) || step == 1 && row.Data.Weblink.ID != ids[key] {
				t.Fatal("remote-link create/replace receipt lost identity or status distinction")
			}
			ids[key] = row.Data.Weblink.ID
			r.registerWeblink(t, key, *row.Data.Weblink, recipe)
			stored := compareOwnedWeblinks(t, r, key)
			if len(stored) != 1 || !stored[0].matches(recipe) {
				t.Fatal("global ID replacement duplicated the link or retained omitted fields")
			}
		}
	}
	for _, request := range []struct {
		key    string
		recipe weblinkRecipe
	}{{keys[0], weblinkRecipe{URL: "https://example.com/duplicate", Title: "Duplicate"}}, {keys[0], weblinkRecipe{URL: "https://example.com/duplicate", Title: "Duplicate"}}, {keys[1], weblinkRecipe{URL: "https://example.com/untitled"}}} {
		r.allowWeblink([]string{request.key}, request.recipe)
		args := weblinkArgs([]string{request.key}, request.recipe)
		if err := r.checkMutation([]string{request.key}, args); err != nil {
			t.Fatal(err)
		}
		frame, stdout, exit := r.invoke(t, t.Context(), nil, args...)
		var receipt struct {
			Data envelope.IssueWeblinkAddOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(stdout, &receipt) != nil || len(receipt.Data.Results) != 1 || receipt.Data.Results[0].Data == nil || receipt.Data.Results[0].Data.Created == nil || !*receipt.Data.Results[0].Data.Created || receipt.Data.Results[0].Data.Weblink == nil {
			t.Fatal("add without global ID did not report a new usable link")
		}
		r.registerWeblink(t, request.key, *receipt.Data.Results[0].Data.Weblink, request.recipe)
	}
	for i, key := range keys {
		stored := compareOwnedWeblinks(t, r, key)
		if len(stored) != 3-i {
			t.Fatal("remote-link duplicates or omitted-title storage lost resources")
		}
		issue := r.ownedLiveIssue(key)
		if len(issue.Weblinks) != len(stored) {
			t.Fatal("every native remote link must be independently registered")
		}
		for j := range issue.Weblinks {
			link := &issue.Weblinks[j]
			for _, preview := range []bool{true, false} {
				args := []string{"issue", "weblink", "delete", key, link.ID}
				extra := []string{"JIRA_READ_ONLY=true"}
				if preview {
					args = append(args, "--dry-run")
					extra = nil
				}
				if err := r.checkMutation([]string{key}, args); err != nil {
					t.Fatal(err)
				}
				frame, stdout, exit := r.invoke(t, t.Context(), extra, args...)
				var receipt struct {
					Data *envelope.IssueWeblinkDeleteOutput `json:"data"`
				}
				if json.Unmarshal(stdout, &receipt) != nil || preview && (exit != 0 || !frame.OK || receipt.Data == nil || !receipt.Data.DryRun || receipt.Data.Issue.Key != key || receipt.Data.WeblinkID != link.ID) || !preview && (exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "read_only_blocked") {
					t.Fatal("remote-link delete preview/refusal lost target or bypassed policy")
				}
				if !slices.Equal(compareOwnedWeblinks(t, r, key), stored) {
					t.Fatal("remote-link delete preview/refusal changed the stored resource set")
				}
			}
			args := []string{"issue", "weblink", "delete", key, link.ID}
			if err := r.checkMutation([]string{key}, args); err != nil {
				t.Fatal(err)
			}
			deleted, stdout, exit := r.invoke(t, t.Context(), nil, args...)
			var receipt struct {
				Data envelope.IssueWeblinkDeleteOutput `json:"data"`
			}
			if exit != 0 || !deleted.OK || json.Unmarshal(stdout, &receipt) != nil || receipt.Data.DryRun || receipt.Data.Issue.Key != key || receipt.Data.WeblinkID != link.ID {
				t.Fatal("remote-link deletion lost its acknowledged issue and resource identity")
			}
			frame, _, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "api", "get", "/rest/api/3/issue/"+key+"/remotelink/"+link.ID)
			if exit != 2 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "jira_not_found" {
				t.Fatal("deleted remote-link resource still exists before parent cleanup")
			}
			link.Deleted = true
			r.save(t)
			remaining := compareOwnedWeblinks(t, r, key)
			want := slices.DeleteFunc(slices.Clone(stored), func(row storedWeblink) bool { return strconv.FormatInt(row.ID, 10) == link.ID })
			if !slices.Equal(remaining, want) {
				t.Fatal("remote-link deletion removed or changed a different resource")
			}
			stored = remaining
		}
		if len(stored) != 0 {
			t.Fatal("owned issue retains remote links after exact registered deletions")
		}
	}
}
