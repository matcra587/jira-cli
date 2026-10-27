//go:build live

package live

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

type ownedLink struct {
	ID      string `json:"id"`
	Source  string `json:"source"`
	Target  string `json:"target"`
	Deleted bool   `json:"deleted"`
}

type storedLinkType struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Inward  string `json:"inward"`
	Outward string `json:"outward"`
}

type nativeLinkInput struct {
	sources        []string
	target, phrase string
	digest         [sha256.Size]byte
}

func (r *liveRun) loadLinkTypes(t *testing.T) []storedLinkType {
	t.Helper()
	read := rawRead(t, r, "api", "get", "/rest/api/3/issueLinkType")
	var native struct {
		Types []storedLinkType `json:"issueLinkTypes"`
	}
	if json.Unmarshal(read.Response.JSON, &native) != nil || len(native.Types) == 0 {
		t.Fatal("link type metadata is absent")
	}
	_, stdout, exit := r.invoke(t, t.Context(), nil, "issue", "link", "types", "--refresh")
	var domain struct {
		Data envelope.IssueLinkTypesOutput `json:"data"`
	}
	if exit != 0 || json.Unmarshal(stdout, &domain) != nil || len(domain.Data.Types) != len(native.Types) {
		t.Fatal("native and domain link type counts differ")
	}
	r.linkTypes = make(map[string]storedLinkType)
	ambiguous := make(map[string]bool)
	for i, typ := range native.Types {
		if typ.ID == "" || typ.Name == "" || typ.Inward == "" || typ.Outward == "" {
			t.Fatal("link type metadata has no usable identity or direction")
		}
		got := domain.Data.Types[i]
		if got.ID != typ.ID || got.Name != typ.Name || got.Inward != typ.Inward || got.Outward != typ.Outward {
			t.Fatal("domain link type projection differs from native metadata")
		}
		for _, phrase := range []string{typ.Name, typ.Inward, typ.Outward} {
			if previous, ok := r.linkTypes[phrase]; ok && previous.ID != typ.ID {
				delete(r.linkTypes, phrase)
				ambiguous[phrase] = true
			}
			if !ambiguous[phrase] {
				r.linkTypes[phrase] = typ
			}
		}
	}
	return native.Types
}

// Both link ends must be active run-owned issues. Global link deletion also
// requires a registered resource observed between those same two issues.
func (r *liveRun) checkLinkMutation(keys, args []string) error {
	if !r.verified || len(args) < 4 || args[0] != "issue" || args[1] != "link" {
		return errors.New("link mutation requires verified explicit owned targets")
	}
	if args[2] == "delete" {
		if len(keys) != 2 || (len(args) != 4 && len(args) != 5) || len(args) == 5 && args[4] != "--dry-run" {
			return errors.New("link deletion contains unauthored arguments")
		}
		for _, link := range r.manifest.Links {
			if link.ID == args[3] && !link.Deleted && link.Source == keys[0] && link.Target == keys[1] && r.ownedLiveIssue(link.Source) != nil && r.ownedLiveIssue(link.Target) != nil {
				return nil
			}
		}
		return errors.New("link deletion targets an inactive or foreign resource")
	}
	if args[2] != "create" {
		return errors.New("unsupported link mutation grammar")
	}
	i := 3
	for i < len(args) && !strings.HasPrefix(args[i], "--") {
		i++
	}
	sources := args[3:i]
	if len(sources) == 0 {
		return errors.New("link creation requires explicit sources")
	}
	seen := make(map[string]bool)
	var target, phrase, path string
	for ; i < len(args); i++ {
		flag, value, inline := strings.Cut(args[i], "=")
		if seen[flag] {
			return errors.New("link mutation repeats an option")
		}
		seen[flag] = true
		switch flag {
		case "--dry-run", "--validate-remote", "--refresh":
			if inline {
				return errors.New("link mutation changes an authored boolean option")
			}
		case "--parallelism":
			if !inline || (value != "1" && value != "2") {
				return errors.New("link mutation changes its authored concurrency bound")
			}
		case "--to", "--type", "--json-input":
			if !inline {
				i++
				if i == len(args) {
					return errors.New("link mutation omits an option value")
				}
				value = args[i]
			}
			switch flag {
			case "--to":
				target = value
			case "--type":
				phrase = value
			case "--json-input":
				path = value
			}
		default:
			return errors.New("link mutation contains an unauthored option or indirect target")
		}
	}
	if !slices.Equal(keys, append(slices.Clone(sources), target)) || r.ownedLiveIssue(target) == nil || r.linkTypes[phrase].ID == "" {
		return errors.New("link mutation changes its declared ends or verified type")
	}
	unique := make(map[string]bool)
	for _, source := range sources {
		if source == target || unique[source] || r.ownedLiveIssue(source) == nil {
			return errors.New("link mutation targets a foreign, inactive or repeated source")
		}
		unique[source] = true
	}
	if path != "" {
		input, ok := r.linkInputs[path]
		if !ok || !slices.Equal(input.sources, sources) || input.target != target || input.phrase != phrase {
			return errors.New("link mutation uses an unauthored or differently targeted payload")
		}
		data, err := os.ReadFile(path)
		if err != nil || sha256.Sum256(data) != input.digest {
			return errors.New("link mutation payload changed after registration")
		}
	}
	return nil
}

func (r *liveRun) linkInput(t *testing.T, sources []string, target, phrase string, body []byte) string {
	t.Helper()
	if err := adf.Validate(body); err != nil {
		t.Fatal(err)
	}
	if err := r.checkLinkMutation(append(slices.Clone(sources), target), append(append([]string{"issue", "link", "create"}, sources...), "--to", target, "--type", phrase)); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(struct {
		Comment struct {
			Body json.RawMessage `json:"body"`
		} `json:"comment"`
	}{Comment: struct {
		Body json.RawMessage `json:"body"`
	}{Body: body}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.dir, "link-"+output.NewRequestID()+".json")
	if err := xos.AtomicWrite(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if r.linkInputs == nil {
		r.linkInputs = make(map[string]nativeLinkInput)
	}
	r.linkInputs[path] = nativeLinkInput{sources: slices.Clone(sources), target: target, phrase: phrase, digest: sha256.Sum256(payload)}
	return path
}

type storedIssueLink struct {
	ID     string         `json:"id"`
	Type   storedLinkType `json:"type"`
	Inward *struct {
		Key string `json:"key"`
	} `json:"inwardIssue"`
	Outward *struct {
		Key string `json:"key"`
	} `json:"outwardIssue"`
}

func rawIssueLinks(t *testing.T, r *liveRun, key string) []storedIssueLink {
	t.Helper()
	if r.ownedLiveIssue(key) == nil {
		t.Fatal("link read requires an active owned issue")
	}
	read := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key, "--query", "fields=issuelinks,summary")
	var native struct {
		Key    string `json:"key"`
		Fields *struct {
			Links []storedIssueLink `json:"issuelinks"`
		} `json:"fields"`
	}
	if json.Unmarshal(read.Response.JSON, &native) != nil || native.Key != key || native.Fields == nil || native.Fields.Links == nil {
		t.Fatal("native link read lost its identity or explicit complete array")
	}
	return native.Fields.Links
}

func compareOwnedLinks(t *testing.T, r *liveRun, keys []string) {
	t.Helper()
	_, stdout, exit := r.invoke(t, t.Context(), nil, append([]string{"issue", "link", "list"}, keys...)...)
	var domain struct {
		Data envelope.IssueLinkListOutput `json:"data"`
	}
	if exit != 0 || json.Unmarshal(stdout, &domain) != nil || len(domain.Data.Results) != len(keys) {
		t.Fatal("owned link list failed or lost target rows")
	}
	for i, key := range keys {
		native := rawIssueLinks(t, r, key)
		row := domain.Data.Results[i]
		if !row.OK || row.Issue.Key != key || row.Data == nil || !row.Data.Links.Set || row.Data.Links.Null || len(row.Data.Links.Value) != len(native) {
			t.Fatal("native and domain link identities or counts differ")
		}
		seen := make(map[string]bool)
		for j, link := range native {
			if link.ID == "" || seen[link.ID] || (link.Inward == nil) == (link.Outward == nil) {
				t.Fatal("native link has a missing/repeated identity or ambiguous direction")
			}
			seen[link.ID] = true
			var far, phrase, direction string
			if link.Outward != nil {
				far, phrase, direction = link.Outward.Key, link.Type.Outward, "outward"
			} else {
				far, phrase, direction = link.Inward.Key, link.Type.Inward, "inward"
			}
			got := row.Data.Links.Value[j]
			if r.ownedLiveIssue(far) == nil || got.ID != link.ID || got.Issue.Key != far || got.Relation != phrase || got.Direction != direction || got.Type.ID != link.Type.ID || got.Type.Name != link.Type.Name || got.Type.Inward != link.Type.Inward || got.Type.Outward != link.Type.Outward {
				t.Fatal("domain link projection changed the native identity, far end, type or direction")
			}
		}
	}
}

func (r *liveRun) registerPairLink(t *testing.T, source, target string, typ storedLinkType, reverse bool) string {
	t.Helper()
	var selected string
	for _, link := range rawIssueLinks(t, r, source) {
		far := link.Outward
		if reverse {
			far = link.Inward
		}
		if far != nil && far.Key == target && link.Type.ID == typ.ID {
			if selected != "" {
				t.Fatal("owned relationship has duplicate resource identities")
			}
			selected = link.ID
		}
	}
	if selected == "" {
		t.Fatal("acknowledged link is absent from independent native storage")
	}
	read := rawRead(t, r, "api", "get", "/rest/api/3/issueLink/"+selected)
	var resource storedIssueLink
	if json.Unmarshal(read.Response.JSON, &resource) != nil || resource.ID != selected || resource.Type.ID != typ.ID || resource.Inward == nil || resource.Outward == nil {
		t.Fatal("native link resource lost its exact identity or endpoints")
	}
	inward, outward := source, target
	if reverse {
		inward, outward = target, source
	}
	if resource.Inward.Key != inward || resource.Outward.Key != outward {
		t.Fatal("stored link reverses its authored sentence")
	}
	for _, link := range r.manifest.Links {
		if link.ID == selected {
			t.Fatal("link identity is already registered")
		}
	}
	r.manifest.Links = append(r.manifest.Links, ownedLink{ID: selected, Source: source, Target: target})
	r.save(t)
	return selected
}

func (r *liveRun) finishOwnedLinks(t *testing.T) {
	t.Helper()
	for i := range r.manifest.Links {
		link := &r.manifest.Links[i]
		if link.Deleted {
			continue
		}
		frame, _, exit := r.invoke(t, context.Background(), []string{"JIRA_READ_ONLY=true"}, "api", "get", "/rest/api/3/issueLink/"+link.ID)
		if exit != 2 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "jira_not_found" {
			t.Errorf("registered link cleanup was not independently confirmed absent")
			continue
		}
		link.Deleted = true
	}
	if err := r.writeManifest(); err != nil {
		t.Errorf("save link cleanup progress: %v", err)
	}
}

func TestLiveIssueLinks(t *testing.T) {
	r := newLiveRun(t)
	types := r.loadLinkTypes(t)
	var typ storedLinkType
	for _, candidate := range types {
		if candidate.Inward != candidate.Outward && r.linkTypes[candidate.Inward].ID == candidate.ID && r.linkTypes[candidate.Outward].ID == candidate.ID {
			typ = candidate
			break
		}
	}
	if typ.ID == "" {
		t.Fatal("direction coverage requires one unambiguous asymmetric site link type")
	}
	keys := []string{r.create(t, "link-source"), r.create(t, "link-second"), r.create(t, "link-target")}
	compareOwnedLinks(t, r, keys)
	body := []byte(`{"type":"doc","version":1,"content":[{"type":"panel","attrs":{"panelType":"info"},"content":[{"type":"paragraph","content":[{"type":"text","text":"native link comment <&>"}]}]}]}`)
	path := r.linkInput(t, keys[:2], keys[2], typ.Outward, body)
	args := []string{"issue", "link", "create", keys[0], keys[1], "--to", keys[2], "--type", typ.Outward, "--json-input", path, "--parallelism=2", "--refresh"}
	for _, preview := range []string{"local", "resolved", "read-only"} {
		invocation := slices.Clone(args)
		var env []string
		if preview == "read-only" {
			env = []string{"JIRA_READ_ONLY=true"}
		} else {
			invocation = append(invocation, "--dry-run")
			if preview == "resolved" {
				invocation = append(invocation, "--validate-remote")
			}
		}
		if err := r.checkMutation(keys, invocation); err != nil {
			t.Fatal(err)
		}
		frame, stdout, exit := r.invoke(t, t.Context(), env, invocation...)
		var out struct {
			Data envelope.IssueLinkCreateOutput `json:"data"`
		}
		if json.Unmarshal(stdout, &out) != nil || out.Data.To.Key != keys[2] || len(out.Data.Results) != 2 {
			t.Fatal("link preview/refusal lost its targets")
		}
		if preview == "read-only" {
			if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "read_only_blocked" {
				t.Fatal("link creation bypassed read-only enforcement")
			}
		} else if exit != 0 || !frame.OK || !out.Data.DryRun || out.Data.TypeResolved != (preview == "resolved") {
			t.Fatal("link preview misstated its resolution or performed a write")
		}
		compareOwnedLinks(t, r, keys)
		for _, key := range keys {
			if err := verifyStoredADFComments(map[string]json.RawMessage{}, r.success(t, "issue", "comment", "list", key, "--all")); err != nil {
				t.Fatal(err)
			}
		}
	}
	created := r.mutate(t, keys, args...)
	if len(created.Data.Results) != 2 {
		t.Fatal("link creation lost per-source acknowledgements")
	}
	for i, result := range created.Data.Results {
		if !result.OK || result.Issue.Key != keys[i] || strings.TrimSpace(string(result.Data)) != "{}" {
			t.Fatal("link creation changed target ordering, acknowledgement or empty server receipt")
		}
	}
	for _, source := range keys[:2] {
		r.registerPairLink(t, source, keys[2], typ, false)
	}
	compareOwnedLinks(t, r, keys)
	comments := make(map[string]map[string]json.RawMessage)
	total := 0
	for _, key := range keys {
		read := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key+"/comment")
		var native struct {
			Total    int `json:"total"`
			Comments []struct {
				ID   string          `json:"id"`
				Body json.RawMessage `json:"body"`
			} `json:"comments"`
		}
		if json.Unmarshal(read.Response.JSON, &native) != nil || native.Comments == nil || native.Total != len(native.Comments) {
			t.Fatal("native link-comment read is incomplete")
		}
		comments[key] = make(map[string]json.RawMessage)
		for _, comment := range native.Comments {
			equal, err := adf.StructuralEqual(body, comment.Body)
			if err != nil || !equal || comment.ID == "" {
				t.Fatal("compound link comment lost its complete native structure")
			}
			r.registerComment(t, key, comment.ID)
			comments[key][comment.ID] = body
			total++
		}
		if err := verifyStoredADFComments(comments[key], r.success(t, "issue", "comment", "list", key, "--all")); err != nil {
			t.Fatal(err)
		}
	}
	if total != 2 {
		t.Fatal("compound fan-out did not store exactly one native comment per acknowledged link")
	}
	reversed := r.mutate(t, keys[:2], "issue", "link", "create", keys[0], "--to", keys[1], "--type", typ.Inward, "--refresh")
	if len(reversed.Data.Results) != 1 || !reversed.Data.Results[0].OK || reversed.Data.Results[0].Issue.Key != keys[0] || strings.TrimSpace(string(reversed.Data.Results[0].Data)) != "{}" {
		t.Fatal("inward link creation lost its source acknowledgement")
	}
	r.registerPairLink(t, keys[0], keys[1], typ, true)
	compareOwnedLinks(t, r, keys)
	for _, key := range keys {
		if err := verifyStoredADFComments(comments[key], r.success(t, "issue", "comment", "list", key, "--all")); err != nil {
			t.Fatal(err)
		}
	}
	for i := range r.manifest.Links {
		link := r.manifest.Links[i]
		declared := []string{link.Source, link.Target}
		for _, preview := range []bool{true, false} {
			invocation := []string{"issue", "link", "delete", link.ID}
			var env []string
			if preview {
				invocation = append(invocation, "--dry-run")
			} else {
				env = []string{"JIRA_READ_ONLY=true"}
			}
			if err := r.checkMutation(declared, invocation); err != nil {
				t.Fatal(err)
			}
			frame, _, exit := r.invoke(t, t.Context(), env, invocation...)
			if preview {
				if exit != 0 || !frame.OK || !frame.Data.DryRun {
					t.Fatal("link delete preview failed")
				}
			} else if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "read_only_blocked" {
				t.Fatal("link deletion bypassed read-only enforcement")
			}
			compareOwnedLinks(t, r, keys)
		}
		r.mutate(t, declared, "issue", "link", "delete", link.ID)
		missing, _, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "api", "get", "/rest/api/3/issueLink/"+link.ID)
		if exit != 2 || missing.OK || len(missing.Errors) != 1 || missing.Errors[0].Code != "jira_not_found" {
			t.Fatal("explicit link deletion was not independently confirmed absent")
		}
		r.manifest.Links[i].Deleted = true
		r.save(t)
		compareOwnedLinks(t, r, keys)
	}
	for _, key := range keys {
		if links := rawIssueLinks(t, r, key); len(links) != 0 {
			t.Fatal("deleted relationship remains on an owned issue")
		}
		if err := verifyStoredADFComments(comments[key], r.success(t, "issue", "comment", "list", key, "--all")); err != nil {
			t.Fatal(err)
		}
	}
}
