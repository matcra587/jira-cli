package root

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adapters"
	"github.com/matcra587/jira-cli/internal/cli"
	metacache "github.com/matcra587/jira-cli/internal/cli/cache"
	jqlcmd "github.com/matcra587/jira-cli/internal/cli/jql"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

type cachePrimerClient struct {
	core.JiraClient
	calls int
}

func (c *cachePrimerClient) BaseURL() string { return "https://example.com" }
func (c *cachePrimerClient) DoAPI(ctx context.Context, req core.APIRequest) (*core.APIResponse, error) {
	c.calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	body := `[{"id":"1","name":"Example","futureField":"kept","unknown":null}]`
	switch req.Endpoint {
	case "/rest/api/3/label":
		body = `{"startAt":0,"maxResults":100,"isLast":true,"values":["Example"]}`
	case "/rest/api/3/project/search":
		body = `{"startAt":0,"maxResults":100,"isLast":true,"values":[{"id":"1","name":"Example","key":"EX","futureField":"kept"}]}`
	case "/rest/api/3/priority/search", "/rest/api/3/resolution/search":
		body = `{"startAt":0,"maxResults":100,"isLast":true,"values":` + body + `}`
	case "/rest/api/3/issueLinkType":
		body = `{"issueLinkTypes":[{"id":"1","name":"Example","inward":"inside","outward":"outside","futureField":"kept"}]}`
	}
	return &core.APIResponse{StatusCode: http.StatusOK, Body: []byte(body)}, nil
}

func (c *cachePrimerClient) SearchIssues(context.Context, string, []string, string, int, ...string) (core.IssueSearchRead, error) {
	c.calls++
	return core.IssueSearchRead{Results: &jira.SearchResults{IsLast: jira.NullableOf(true), Issues: jira.NullableOf([]jira.IssueBean{{ID: jira.NullableOf("1"), Key: jira.NullableOf("EX-1"), Fields: jira.NullableOf(jira.IssueFields{Summary: jira.NullableOf("Epic"), Extra: map[string]json.RawMessage{"futureField": json.RawMessage(`"kept"`)}})}})}}, nil
}

func (c *cachePrimerClient) GetBoards(context.Context, string, int, int) (core.BoardRead, error) {
	c.calls++
	return core.BoardRead{Page: &jira.BoardPage{IsLast: jira.NullableOf(true), Values: jira.NullableOf([]jira.Board{{ID: jira.NullableOf(int64(42)), Name: jira.NullableOf("Example board"), Type: jira.NullableOf("scrum")}})}}, nil
}

func (c *cachePrimerClient) GetBoardProjects(context.Context, int64, int, int) (core.BoardProjectRead, error) {
	c.calls++
	return core.BoardProjectRead{Page: &jira.BoardProjectPage{IsLast: jira.NullableOf(true), Values: jira.NullableOf([]jira.BoardProject{{ID: jira.NullableOf("1"), Key: jira.NullableOf("EX")}})}}, nil
}

func TestPublicMetadataPrimersUseConsumerFormatsAndFreshHits(t *testing.T) {
	for _, resource := range metacache.MetadataResources() {
		t.Run(resource.Name, func(t *testing.T) {
			var memory adapters.MemoryCache
			client := &cachePrimerClient{}
			deps := testDeps(t)
			deps.Cache = func(ctx context.Context) (core.Cache, error) { return &memory, nil }
			// A live primer must preserve the ordinary constructor's keyring-only identity.
			deps.BuildPreferences = func() (jqlcmd.BuildPreferences, error) {
				return jqlcmd.BuildPreferences{BaseURL: "https://example.com"}, nil
			}
			clientFactories := 0
			deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) { clientFactories++; return client, nil }
			invoke := func(args ...string) []byte {
				t.Helper()
				var stdout, stderr bytes.Buffer
				program, sink := newProgram(deps, &stdout, &stderr)
				if exit := execute(t.Context(), program, sink, args); exit != 0 {
					t.Fatalf("exit=%d stdout=%s stderr=%s", exit, &stdout, &stderr)
				}
				return stdout.Bytes()
			}
			for _, fresh := range []bool{false, true} {
				var frame struct {
					OK   bool                      `json:"ok"`
					Data envelope.CachePrimeOutput `json:"data"`
				}
				if err := json.Unmarshal(invoke("cache", resource.Name, "--output=json", "--no-input"), &frame); err != nil {
					t.Fatal(err)
				}
				if !frame.OK || frame.Data.Count != 1 || frame.Data.Attempted == fresh || frame.Data.FromCache != fresh || frame.Data.Saved == fresh || !frame.Data.Complete || clientFactories != 1 {
					t.Fatalf("public primer=%+v factories=%d", frame, clientFactories)
				}
			}
			calls := client.calls
			switch resource.Name {
			case "boards":
				var frame struct {
					Data envelope.BoardsListOutput `json:"data"`
				}
				if err := json.Unmarshal(invoke("boards", "list", "--all", "--output=json"), &frame); err != nil {
					t.Fatal(err)
				}
				if !frame.Data.FromCache || frame.Data.Count != 1 || len(frame.Data.Boards[0].ProjectKeys) != 1 || frame.Data.Boards[0].ProjectKeys[0] != "EX" {
					t.Fatal("primed boards lost project enrichment")
				}
			case "linktypes":
				var frame struct {
					Data envelope.IssueLinkTypesOutput `json:"data"`
				}
				if err := json.Unmarshal(invoke("issue", "link", "types", "--output=json"), &frame); err != nil {
					t.Fatal(err)
				}
				if !frame.Data.Cached || len(frame.Data.Types) != 1 || frame.Data.Types[0].Name != "Example" {
					t.Fatal("link consumer rejected primed array")
				}
			}
			if client.calls != calls {
				t.Fatal("existing consumer refetched primed metadata")
			}
		})
	}
}

func TestPublicMetadataPreviewsResolveScopeWithoutCredentialsOrStorageCreation(t *testing.T) {
	for _, resource := range metacache.MetadataResources() {
		t.Run(resource.Name, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			deps := testDeps(t)
			deps.Cache = func(ctx context.Context) (core.Cache, error) {
				t.Fatal("preview opened credential-backed cache")
				return nil, nil
			}
			deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
				t.Fatal("preview constructed Jira client")
				return nil, nil
			}
			deps.Credentials = func() (core.CredentialStore, error) { t.Fatal("preview opened credentials"); return nil, nil }
			preferences := 0
			deps.BuildPreferences = func() (jqlcmd.BuildPreferences, error) {
				preferences++
				return jqlcmd.BuildPreferences{BaseURL: "https://example.com", Account: "person@example.com"}, nil
			}
			var memory adapters.MemoryCache
			deps.CacheFor = func(site, account string) (core.Cache, error) {
				if site != "https://example.com" || account != "person@example.com" {
					t.Fatal("preview scope changed")
				}
				return &memory, nil
			}
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(deps, &stdout, &stderr)
			if exit := execute(t.Context(), program, sink, []string{"cache", resource.Name, "--dry-run", "--force", "--output=json"}); exit != 0 {
				t.Fatalf("exit=%d stdout=%s", exit, &stdout)
			}
			var frame struct {
				Data envelope.CachePrimeOutput `json:"data"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil {
				t.Fatal(err)
			}
			if !frame.Data.DryRun || !frame.Data.WouldFetch || frame.Data.Attempted || frame.Data.Saved || len(memory.Keys()) != 0 || preferences != 1 {
				t.Fatalf("preview=%+v preferences=%d", frame.Data, preferences)
			}
		})
	}
}

func TestPublicMetadataScopeFailuresPrecedeClientsAndOpeningStorage(t *testing.T) {
	for _, mode := range []string{"missing preview account", "invalid site", "cache unavailable"} {
		t.Run(mode, func(t *testing.T) {
			deps := testDeps(t)
			deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
				t.Fatal("scope refusal constructed client")
				return nil, nil
			}
			deps.CacheFor = func(string, string) (core.Cache, error) { t.Fatal("bad local scope opened cache"); return nil, nil }
			deps.BuildPreferences = func() (jqlcmd.BuildPreferences, error) {
				site := "https://example.com"
				if mode == "invalid site" {
					site = "invalid"
				}
				return jqlcmd.BuildPreferences{BaseURL: site}, nil
			}
			cause := errors.New("cache unavailable")
			deps.Cache = func(ctx context.Context) (core.Cache, error) {
				if mode != "cache unavailable" {
					t.Fatal("bad scope opened live cache")
				}
				return nil, cause
			}
			args := []string{"cache", "fields", "--output=json"}
			if mode == "missing preview account" {
				args = append(args, "--dry-run")
			}
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(deps, &stdout, &stderr)
			exit := execute(t.Context(), program, sink, args)
			want := cli.ExitValidation
			code := cli.CodeConfigInvalid
			if mode == "cache unavailable" {
				want = cli.ExitValidation
				code = cli.CodeCacheIO
			}
			var frame struct {
				Errors []envelope.Error `json:"errors"`
			}
			if json.Unmarshal(stdout.Bytes(), &frame) != nil || exit != want || len(frame.Errors) != 1 || frame.Errors[0].Code != code {
				t.Fatalf("scope refusal=%d stdout=%s", exit, &stdout)
			}
		})
	}
}

func TestPublicMetadataFailureTextDoesNotClaimCacheReuseOrCollection(t *testing.T) {
	for _, preview := range []bool{false, true} {
		name, message := "client failure", "could not fetch fields metadata"
		if preview {
			name, message = "preview scope failure", "could not inspect fields cache"
		}
		t.Run(name, func(t *testing.T) {
			deps := testDeps(t)
			var store adapters.MemoryCache
			deps.Cache = func(ctx context.Context) (core.Cache, error) { return &store, nil }
			deps.BuildPreferences = func() (jqlcmd.BuildPreferences, error) {
				return jqlcmd.BuildPreferences{BaseURL: "https://example.com"}, nil
			}
			deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
				if preview {
					t.Fatal("preview constructed Jira client")
				}
				return nil, errors.New("client unavailable")
			}
			args := []string{"cache", "fields", "--output=human"}
			if preview {
				args = append(args, "--dry-run")
			}
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(deps, &stdout, &stderr)
			if exit := execute(t.Context(), program, sink, args); exit == 0 {
				t.Fatal("failure reported success")
			}
			text := strings.Join(strings.Fields(stdout.String()+stderr.String()), " ")
			if !strings.Contains(text, message) || strings.Contains(text, "would reuse") || strings.Contains(text, "fetched 0") {
				t.Fatalf("misleading failure text: %s", text)
			}
		})
	}
}

func TestPublicMetadataCappedCacheRetainsResumeAndActionableWarning(t *testing.T) {
	deps := testDeps(t)
	var store adapters.MemoryCache
	if err := store.Set("v1:metadata:labels", []byte(`{"version":1,"items":["kept"],"count":1,"pages":100,"complete":false,"truncated_reason":"max_pages","next_start":1}`)); err != nil {
		t.Fatal(err)
	}
	deps.Cache = func(ctx context.Context) (core.Cache, error) { return &store, nil }
	deps.BuildPreferences = func() (jqlcmd.BuildPreferences, error) {
		return jqlcmd.BuildPreferences{BaseURL: "https://example.com"}, nil
	}
	deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
		t.Fatal("bounded reuse constructed client")
		return nil, nil
	}
	var stdout, stderr bytes.Buffer
	program, sink := newProgram(deps, &stdout, &stderr)
	if exit := execute(t.Context(), program, sink, []string{"cache", "labels", "--output=json"}); exit != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", exit, &stdout, &stderr)
	}
	var frame struct {
		OK       bool                      `json:"ok"`
		Data     envelope.CachePrimeOutput `json:"data"`
		Warnings []envelope.Warning        `json:"warnings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil {
		t.Fatal(err)
	}
	if !frame.OK || !frame.Data.FromCache || frame.Data.Complete || frame.Data.NextStart == nil || *frame.Data.NextStart != 1 || len(frame.Warnings) != 1 || frame.Warnings[0].Type != "cache_truncated" || !frame.Warnings[0].Lossy || !strings.Contains(frame.Warnings[0].Message, "--force --unbounded") {
		t.Fatalf("capped cache lost resume or actionable warning: %+v", frame)
	}
}
