//go:build live

package live

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	boardcmd "github.com/matcra587/jira-cli/internal/cli/boards"
	metacache "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// metadataCLIReader exercises collector parsing against exact native response
// bytes through the authorized CLI transport. It establishes collector/API
// compatibility, not acceptance of a public cache primer or its persistence.
type metadataCLIReader struct {
	core.JiraClient
	t *testing.T
	r *liveRun
}

func (c metadataCLIReader) DoAPI(ctx context.Context, request core.APIRequest) (*core.APIResponse, error) {
	c.t.Helper()
	args := []string{"api", strings.ToLower(request.Method), request.Endpoint, "--max-response-bytes=" + strconv.FormatInt(request.MaxResponseBytes, 10)}
	for _, key := range slices.Sorted(maps.Keys(request.Query)) {
		for _, value := range request.Query[key] {
			args = append(args, "--query="+key+"="+value)
		}
	}
	if request.Body != nil {
		path := filepath.Join(c.t.TempDir(), "metadata-query.json")
		if err := os.WriteFile(path, request.Body, 0o600); err != nil {
			return nil, err
		}
		args = append(args, "--input="+path)
	}
	frame, stdout, exit := c.r.invoke(c.t, ctx, []string{"JIRA_READ_ONLY=true"}, args...)
	var decoded struct {
		Data envelope.APIOutput `json:"data"`
	}
	if err := json.Unmarshal(stdout, &decoded); err != nil {
		return nil, err
	}
	if exit != 0 || !frame.OK || !decoded.Data.Attempted || decoded.Data.Response == nil {
		return nil, errors.New("native metadata read failed; inspect local live receipts")
	}
	response := decoded.Data.Response
	body, err := base64.StdEncoding.DecodeString(response.BodyBase64)
	if err != nil {
		return nil, err
	}
	return &core.APIResponse{StatusCode: response.StatusCode, Body: body, Truncated: response.Truncated}, nil
}

func (c metadataCLIReader) SearchIssues(ctx context.Context, query string, fields []string, token string, size int, expand ...string) (core.IssueSearchRead, error) {
	body, err := jira.MarshalNoEscape(struct {
		JQL           string   `json:"jql"`
		Fields        []string `json:"fields"`
		NextPageToken string   `json:"nextPageToken,omitempty"`
		MaxResults    int      `json:"maxResults"`
	}{JQL: query, Fields: fields, NextPageToken: token, MaxResults: size})
	if err != nil {
		return core.IssueSearchRead{}, err
	}
	response, err := c.DoAPI(ctx, core.APIRequest{Method: http.MethodPost, Endpoint: "/rest/api/3/search/jql", Body: body, MaxResponseBytes: core.DefaultMaxAPIResponseBytes})
	if err != nil {
		return core.IssueSearchRead{}, err
	}
	var page jira.SearchResults
	if err := json.Unmarshal(response.Body, &page); err != nil {
		return core.IssueSearchRead{}, err
	}
	return core.IssueSearchRead{Results: &page}, nil
}

func (c metadataCLIReader) GetBoards(ctx context.Context, name string, start, size int) (core.BoardRead, error) {
	query := url.Values{"startAt": {strconv.Itoa(start)}, "maxResults": {strconv.Itoa(size)}}
	if name != "" {
		query.Set("name", name)
	}
	response, err := c.DoAPI(ctx, core.APIRequest{Method: http.MethodGet, Endpoint: "/rest/agile/1.0/board", Query: query, MaxResponseBytes: core.DefaultMaxAPIResponseBytes})
	if err != nil {
		return core.BoardRead{}, err
	}
	var page jira.BoardPage
	if err := json.Unmarshal(response.Body, &page); err != nil {
		return core.BoardRead{}, err
	}
	return core.BoardRead{Page: &page}, nil
}

func (c metadataCLIReader) GetBoardProjects(ctx context.Context, id int64, start, size int) (core.BoardProjectRead, error) {
	response, err := c.DoAPI(ctx, core.APIRequest{
		Method: http.MethodGet, Endpoint: "/rest/agile/1.0/board/" + strconv.FormatInt(id, 10) + "/project",
		Query: url.Values{"startAt": {strconv.Itoa(start)}, "maxResults": {strconv.Itoa(size)}}, MaxResponseBytes: core.DefaultMaxAPIResponseBytes,
	})
	if err != nil {
		return core.BoardProjectRead{}, err
	}
	var page jira.BoardProjectPage
	if err := json.Unmarshal(response.Body, &page); err != nil {
		return core.BoardProjectRead{}, err
	}
	return core.BoardProjectRead{Page: &page}, nil
}

func TestLiveCacheMetadataCollectors(t *testing.T) {
	r := newLiveRun(t)
	cacheDir := t.TempDir()
	identityFrame, identityRaw, identityExit := r.invoke(t, t.Context(), nil, "auth", "status")
	var identity struct {
		Data envelope.AuthStatusOutput `json:"data"`
	}
	if identityExit != 0 || !identityFrame.OK || json.Unmarshal(identityRaw, &identity) != nil || strings.TrimSpace(identity.Data.Email) == "" {
		t.Fatal("verified CLI identity cannot select the owned metadata cache")
	}
	liveEnv := []string{"JIRA_READ_ONLY=true", "XDG_CACHE_HOME=" + cacheDir}
	localEnv := append(slices.Clone(liveEnv), "JIRA_EMAIL="+identity.Data.Email, "JIRA_API_TOKEN=", "JIRA_KEYRING=false", "DBUS_SESSION_BUS_ADDRESS=/dev/null")
	complete := func(t *testing.T, value metacache.MetadataValue, resource string) {
		t.Helper()
		before := metadataCacheFiles(t, cacheDir)
		for kind, expected := range nativeMetadataCompletionValues(t, value, resource) {
			raw, exit := r.invokeRaw(t, t.Context(), localEnv, "--@complete="+kind)
			if exit != 0 {
				t.Fatalf("credential-disabled native completion failed: %s", kind)
			}
			var actual []string
			for line := range strings.Lines(string(raw)) {
				line = strings.TrimSuffix(line, "\n")
				candidate, _, _ := strings.Cut(line, "\t")
				if candidate == "" || cli.Text(candidate) != candidate {
					t.Fatalf("native completion emitted an unsafe candidate: %s", kind)
				}
				actual = append(actual, candidate)
			}
			slices.Sort(actual)
			slices.Sort(expected)
			if !slices.Equal(actual, expected) {
				t.Fatalf("native completion differs from persisted producer identities: %s got=%d want=%d", kind, len(actual), len(expected))
			}
		}
		if !maps.Equal(before, metadataCacheFiles(t, cacheDir)) {
			t.Fatal("native completion changed cache storage")
		}
	}
	inspect := func(t *testing.T, resources ...string) envelope.CacheInspectOutput {
		t.Helper()
		frame, raw, exit := r.invoke(t, t.Context(), localEnv, append([]string{"cache", "inspect"}, resources...)...)
		var decoded struct {
			Data envelope.CacheInspectOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(raw, &decoded) != nil || !decoded.Data.Complete || decoded.Data.Email != identity.Data.Email || decoded.Data.Count != len(decoded.Data.Entries) {
			t.Fatal("credential-free native cache inspection lost scope or observations; inspect local receipts")
		}
		return decoded.Data
	}
	reader := metadataCLIReader{t: t, r: r}
	for _, resource := range metacache.MetadataResources() {
		t.Run(resource.Name, func(t *testing.T) {
			reader.t = t
			value, err := metacache.FetchMetadata(t.Context(), reader, func(ctx context.Context, unbounded bool) (metacache.MetadataValue, error) {
				return boardcmd.FetchMetadata(ctx, reader, unbounded)
			}, resource.Name, false)
			if err != nil || !value.Complete || value.Pages < 1 || value.TruncatedReason != "" || value.NextStart != nil || value.NextPageToken != "" {
				t.Fatalf("native metadata collector failed for %s: %v", resource.Name, err)
			}
			if resource.Name == "boards" {
				var snapshot struct {
					Boards []envelope.BoardRow `json:"boards"`
				}
				if err := json.Unmarshal(value.Payload, &snapshot); err != nil || len(snapshot.Boards) != value.Count {
					t.Fatal("native board metadata payload cannot be inspected")
				}
				id := requiredEnv(t, "JIRA_LIVE_BOARD_ID")
				if !slices.ContainsFunc(snapshot.Boards, func(board envelope.BoardRow) bool {
					return strconv.FormatInt(board.ID, 10) == id && board.ProjectsComplete && slices.Contains(board.ProjectKeys, r.manifest.Project)
				}) {
					t.Fatal("designated native board/project relation is missing")
				}
			} else if value.Count != len(value.Items) || value.Items == nil {
				t.Fatal("native metadata count differs from retained array")
			}
			// These are actual public commands on the freshly built binary. Each
			// workflow owns only this temporary cache, with native Jira reads under
			// read-only policy. Offline steps explicitly disable authentication.
			prime := func(env []string, flags ...string) envelope.CachePrimeOutput {
				t.Helper()
				frame, raw, exit := r.invoke(t, t.Context(), env, append([]string{"cache", resource.Name}, flags...)...)
				var decoded struct {
					Data envelope.CachePrimeOutput `json:"data"`
				}
				if exit != 0 || !frame.OK || json.Unmarshal(raw, &decoded) != nil {
					t.Fatal("native public cache primer failed; inspect local receipts")
				}
				return decoded.Data
			}
			cold := prime(localEnv, "--dry-run")
			if !cold.DryRun || !cold.WouldFetch || cold.Attempted || cold.Saved || cold.FromCache || cold.Count != 0 {
				t.Fatal("cold native primer preview fabricated collection or storage")
			}
			primed := prime(liveEnv)
			if !primed.Attempted || !primed.Saved || primed.FromCache || !primed.Complete || primed.Count != value.Count || primed.TTLMinutes != int(resource.DefaultTTL.Minutes()) {
				t.Fatal("native public primer failed to persist the complete selected resource")
			}
			reused := prime(liveEnv)
			if reused.Attempted || reused.Saved || !reused.FromCache || !reflect.DeepEqual(reused.CacheMetadata, primed.CacheMetadata) {
				t.Fatal("native primer did not reuse its exact persisted snapshot")
			}
			preview := prime(localEnv, "--dry-run", "--force")
			if !preview.DryRun || !preview.WouldFetch || preview.Attempted || preview.Saved || !preview.FromCache || !reflect.DeepEqual(preview.CacheMetadata, primed.CacheMetadata) {
				t.Fatal("credential-free native preview changed the persisted snapshot")
			}
			forced := prime(liveEnv, "--force")
			if !forced.Attempted || !forced.Saved || forced.FromCache || !forced.Complete {
				t.Fatal("native forced primer did not fetch and replace metadata")
			}
			inspected := inspect(t, resource.Name)
			if inspected.Resource != resource.Name || inspected.Count != 1 || len(inspected.Resources) != 1 || inspected.KnownPayloadBytes <= 0 || inspected.Unclassified != 0 {
				t.Fatal("native inspection omitted the persisted producer entry")
			}
			row := inspected.Resources[0]
			if row.Resource != resource.Name || row.CacheState != "fresh" || row.TTLMinutes == nil || *row.TTLMinutes != int(resource.DefaultTTL.Minutes()) || row.Count != forced.Count || row.Pages != forced.Pages || row.Complete != forced.Complete || row.WrittenAt == "" {
				t.Fatal("native inspection disagrees with the actual producer snapshot")
			}
			complete(t, forced.CacheMetadata, resource.Name)
			if resource.Name == "boards" {
				frame, raw, exit := r.invoke(t, t.Context(), liveEnv, "boards", "list", "--all")
				var consumer struct {
					Data envelope.BoardsListOutput `json:"data"`
				}
				if exit != 0 || !frame.OK || json.Unmarshal(raw, &consumer) != nil || !consumer.Data.FromCache || consumer.Data.Count != forced.Count {
					t.Fatal("native board consumer rejected primed metadata")
				}
			}
			if resource.Name == "linktypes" {
				frame, raw, exit := r.invoke(t, t.Context(), liveEnv, "issue", "link", "types")
				var consumer struct {
					Data envelope.IssueLinkTypesOutput `json:"data"`
				}
				if exit != 0 || !frame.OK || json.Unmarshal(raw, &consumer) != nil || !consumer.Data.Cached || len(consumer.Data.Types) != forced.Count {
					t.Fatal("native link consumer rejected primed metadata")
				}
			}
			clearFrame, clearRaw, clearExit := r.invoke(t, t.Context(), localEnv, "cache", "clear", resource.Name, "--force")
			var cleared struct {
				Data envelope.CacheClearOutput `json:"data"`
			}
			if clearExit != 0 || !clearFrame.OK || json.Unmarshal(clearRaw, &cleared) != nil || cleared.Data.Removed != 1 || cleared.Data.WouldRemove != 1 {
				t.Fatal("native local clear failed to classify the actual primer key")
			}
			after := prime(localEnv, "--dry-run")
			if !after.WouldFetch || after.FromCache || after.Count != 0 || after.Attempted || after.Saved {
				t.Fatal("native clear left the selected primer entry reusable")
			}
			inspected = inspect(t, resource.Name)
			if inspected.Count != 0 || len(inspected.Resources) != 1 || inspected.Resources[0].CacheState != "missing" || inspected.Resources[0].Count != 0 || inspected.Resources[0].Complete {
				t.Fatal("native inspection fabricated a cleared producer snapshot")
			}
			complete(t, metacache.MetadataValue{Items: []json.RawMessage{}}, resource.Name)
			if resource.Name == "projects" && !slices.ContainsFunc(value.Items, func(raw json.RawMessage) bool {
				var project struct {
					Key string `json:"key"`
				}
				return json.Unmarshal(raw, &project) == nil && project.Key == r.manifest.Project
			}) {
				t.Fatal("designated project absent from native metadata")
			}
		})
	}
	refresh := func(env []string, args ...string) envelope.CacheRefreshOutput {
		t.Helper()
		frame, raw, exit := r.invoke(t, t.Context(), env, append([]string{"cache", "refresh"}, args...)...)
		var decoded struct {
			Data envelope.CacheRefreshOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(raw, &decoded) != nil {
			t.Fatal("native cache refresh failed; inspect local receipts")
		}
		return decoded.Data
	}
	primed := refresh(liveEnv)
	if primed.Parallelism != 1 || len(primed.Results) != 10 {
		t.Fatal("native refresh omitted default resources or changed its worker default")
	}
	for i, resource := range metacache.MetadataResources() {
		row := primed.Results[i]
		if row.Resource != resource.Name || !row.OK || row.Err != nil || row.Data == nil || !row.Data.Attempted || !row.Data.Saved || !row.Data.Complete || row.Data.FromCache || row.Data.TTLMinutes != int(resource.DefaultTTL.Minutes()) {
			t.Fatal("native refresh lost ordered collection/storage facts")
		}
		frame, raw, exit := r.invoke(t, t.Context(), liveEnv, "cache", resource.Name)
		var independent struct {
			Data envelope.CachePrimeOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(raw, &independent) != nil || !independent.Data.FromCache || independent.Data.Attempted || !reflect.DeepEqual(independent.Data.CacheMetadata, row.Data.CacheMetadata) {
			t.Fatal("independent primer read rejected refresh storage or changed native values")
		}
	}
	reused := refresh(liveEnv, "--parallelism=3")
	if len(reused.Results) != len(primed.Results) {
		t.Fatal("native refresh reuse omitted resources")
	}
	for i, row := range reused.Results {
		if !row.OK || row.Data == nil || !row.Data.FromCache || row.Data.Attempted || row.Data.Saved || !reflect.DeepEqual(row.Data.CacheMetadata, primed.Results[i].Data.CacheMetadata) {
			t.Fatal("native refresh did not reuse persisted metadata")
		}
	}
	preview := refresh(localEnv, "fields", "labels", "fields", "--dry-run", "--force", "-p", "2")
	if !preview.DryRun || preview.Parallelism != 2 || len(preview.Results) != 2 {
		t.Fatal("native offline refresh lost selection or worker flag")
	}
	for i, name := range []string{"fields", "labels"} {
		row := preview.Results[i]
		if row.Resource != name || !row.OK || row.Data == nil || !row.Data.FromCache || !row.Data.WouldFetch || row.Data.Attempted || row.Data.Saved {
			t.Fatal("native offline refresh fabricated collection/storage")
		}
	}
	forced := refresh(liveEnv, "fields", "labels", "--force", "--ttl-minutes=7", "--unbounded", "--parallelism=2")
	if len(forced.Results) != 2 {
		t.Fatal("native forced refresh omitted selected resources")
	}
	for _, row := range forced.Results {
		if !row.OK || row.Data == nil || !row.Data.Attempted || !row.Data.Saved || row.Data.FromCache || !row.Data.Unbounded || row.Data.TTLMinutes != 7 {
			t.Fatal("native forced refresh ignored freshness or collection controls")
		}
	}
	t.Run("inspection", func(t *testing.T) {
		out := inspect(t)
		if out.Resource != "all" || out.Count != 10 || len(out.Resources) != 12 || out.Unclassified != 0 || out.KnownPayloadBytes <= 0 {
			t.Fatal("whole-scope native inspection omitted persisted metadata or resource summaries")
		}
		for _, row := range out.Resources {
			if row.Resource == "transitions" {
				if row.CacheState != "missing" || row.Count != 0 || row.Complete || row.TTLMinutes == nil || *row.TTLMinutes != 5 || row.IssueKey != "" {
					t.Fatal("native inspection fabricated a workflow observation")
				}
				continue
			}
			if row.Resource == "issuekeys" {
				if row.CacheState != "missing" || row.TTLMinutes != nil {
					t.Fatal("native inspection fabricated issue history or an expiry policy")
				}
			} else if row.CacheState != "fresh" || !row.Complete || row.TTLMinutes == nil || row.WrittenAt == "" {
				t.Fatal("native inspection rejected complete cached producer data")
			}
		}
	})
	t.Run("issue history", func(t *testing.T) {
		readHistory := func(want []string) {
			t.Helper()
			frame, raw, exit := r.invoke(t, t.Context(), localEnv, "cache", "issuekeys")
			var decoded struct {
				Data envelope.CacheIssueKeysOutput `json:"data"`
			}
			if exit != 0 || !frame.OK || json.Unmarshal(raw, &decoded) != nil || !slices.Equal(decoded.Data.IssueKeys, want) || decoded.Data.Count != len(want) {
				t.Fatal("native persisted issue history differs from typed observations; inspect local receipts")
			}
		}
		readHistory([]string{})
		readKey := requiredEnv(t, "JIRA_LIVE_READ_KEY")
		frame, _, exit := r.invoke(t, t.Context(), liveEnv, "issue", "view", readKey, "--fields=summary")
		if exit != 0 || !frame.OK {
			t.Fatal("native read fixture view failed")
		}
		readHistory([]string{readKey})
		frame, raw, exit := r.invoke(t, t.Context(), liveEnv, "issue", "list", "--project="+r.manifest.Project, "--limit=2")
		var listed struct {
			Data envelope.IssueListOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(raw, &listed) != nil || len(listed.Data.Issues) != 2 {
			t.Fatal("native history search did not return two actual identities")
		}
		want := []string{}
		for _, issue := range listed.Data.Issues {
			want = append(want, issue.Key)
		}
		if !slices.Contains(want, readKey) {
			want = append(want, readKey)
		}
		readHistory(want)
		out := inspect(t, "issuekeys")
		if out.Count != 1 || len(out.Resources) != 1 || out.Resources[0].Count != len(want) || out.Resources[0].CacheState != "present" || !out.Resources[0].Complete || out.Resources[0].TTLMinutes != nil {
			t.Fatal("native inspection disagrees with credential-free persisted issue history")
		}
		refresh(localEnv, "fields", "labels", "--dry-run")
		refresh(liveEnv, "fields", "labels")
		frame, _, exit = r.invoke(t, t.Context(), localEnv, "issue", "edit", readKey, "--summary=Local history preview", "--dry-run")
		if exit != 0 || !frame.OK {
			t.Fatal("credential-free issue preview failed")
		}
		readHistory(want)
		for _, preview := range []bool{true, false} {
			args := []string{"cache", "clear", "issuekeys", "--force"}
			if preview {
				args = append(args, "--dry-run")
			}
			frame, raw, exit = r.invoke(t, t.Context(), localEnv, args...)
			var cleared struct {
				Data envelope.CacheClearOutput `json:"data"`
			}
			if exit != 0 || !frame.OK || json.Unmarshal(raw, &cleared) != nil || cleared.Data.WouldRemove != 1 || preview && cleared.Data.Removed != 0 || !preview && cleared.Data.Removed != 1 {
				t.Fatal("native issue-history clear lost its actual inventory or acknowledgement")
			}
			if preview {
				readHistory(want)
			} else {
				readHistory([]string{})
			}
		}
		frame, raw, exit = r.invoke(t, t.Context(), localEnv, "cache", "fields", "--dry-run")
		var fieldSnapshot struct {
			Data envelope.CachePrimeOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(raw, &fieldSnapshot) != nil || !fieldSnapshot.Data.FromCache {
			t.Fatal("issue-history clear removed unrelated native metadata")
		}
	})
}

func metadataCacheFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := fs.WalkDir(os.DirFS(dir), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil {
			return err
		}
		files[path] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// nativeMetadataCompletionValues derives candidate identities from the actual
// persisted public-primer result, independently of the application's emitter.
func nativeMetadataCompletionValues(t *testing.T, value metacache.MetadataValue, resource string) map[string][]string {
	t.Helper()
	kind := map[string]string{"labels": "cachelabel", "projects": "cacheproject", "epics": "cacheepic", "fields": "cachefield", "issuetypes": "cacheissuetype", "linktypes": "cachelinktype", "statuses": "cachestatus", "priorities": "cachepriority", "resolutions": "cacheresolution"}[resource]
	if resource == "boards" {
		expected := map[string][]string{"cacheboard": nil, "cacheboardid": nil}
		if len(value.Payload) == 0 {
			return expected
		}
		var payload struct {
			Boards []envelope.BoardRow `json:"boards"`
		}
		if err := json.Unmarshal(value.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		for _, board := range payload.Boards {
			expected["cacheboardid"] = append(expected["cacheboardid"], strconv.FormatInt(board.ID, 10))
			matches := 0
			for _, other := range payload.Boards {
				if strings.EqualFold(strings.TrimSpace(board.Name), strings.TrimSpace(other.Name)) {
					matches++
				}
			}
			if matches == 1 && cli.Text(board.Name) == board.Name {
				expected["cacheboard"] = append(expected["cacheboard"], board.Name)
			}
		}
		return expected
	}
	if kind == "" {
		t.Fatalf("native completion has no declared resource: %s", resource)
	}
	expected := map[string][]string{kind: nil}
	seen := map[string]bool{}
	for _, raw := range value.Items {
		var candidate string
		if resource == "labels" {
			if err := json.Unmarshal(raw, &candidate); err != nil {
				t.Fatal(err)
			}
		} else {
			var item struct {
				ID   string `json:"id"`
				Key  string `json:"key"`
				Name string `json:"name"`
			}
			if err := json.Unmarshal(raw, &item); err != nil {
				t.Fatal(err)
			}
			switch resource {
			case "fields":
				candidate = item.ID
			case "projects", "epics":
				candidate = item.Key
			default:
				candidate = item.Name
			}
		}
		if candidate != "" && cli.Text(candidate) == candidate && !seen[candidate] {
			expected[kind] = append(expected[kind], candidate)
			seen[candidate] = true
		}
	}
	return expected
}
