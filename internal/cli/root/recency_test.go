package root

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/adapters"
	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
	jqlcmd "github.com/matcra587/jira-cli/internal/cli/jql"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type recentIssueClient struct{ core.JiraClient }

func (recentIssueClient) BaseURL() string { return "https://example.com" }

func (recentIssueClient) GetIssue(context.Context, string, []string, ...string) (core.IssueRead, error) {
	return core.IssueRead{Issue: &jira.IssueBean{ID: jira.NullableOf("1"), Key: jira.NullableOf("DEMO-1"), Fields: jira.NullableOf(jira.IssueFields{Summary: jira.NullableOf("Native summary")})}}, nil
}

func (recentIssueClient) AddAttachment(_ context.Context, _ string, paths []string) (core.AttachmentWrite, error) {
	return core.AttachmentWrite{Attachments: []jira.Attachment{{ID: jira.NullableOf("1"), Filename: jira.NullableOf(filepath.Base(paths[0])), Size: jira.NullableOf(int64(4))}}}, nil
}

func TestIssueHistoryNeverTreatsAttachmentPathsAsIssueIdentities(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("DEMO-99", []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	deps := testDeps(t)
	deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) { return recentIssueClient{}, nil }
	var memory adapters.MemoryCache
	deps.Cache = func(ctx context.Context) (core.Cache, error) { return &memory, nil }
	var stdout, stderr bytes.Buffer
	program, sink := newProgram(deps, &stdout, &stderr)
	exit := execute(t.Context(), program, sink, []string{"issue", "attachment", "add", "DEMO-1", "DEMO-99", "--output=json"})
	if exit != 0 {
		t.Fatalf("upload failed: exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	keys, _, _ := cachecmd.RecentIssueKeys(&memory)
	if !slices.Equal(keys, []string{"DEMO-1"}) {
		t.Fatalf("attachment path was recorded as an issue identity: %v", keys)
	}
}

func TestIssueHistoryClientRefusalWithoutReceiptNeverOpensCache(t *testing.T) {
	deps := testDeps(t)
	deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
		return nil, cli.NewCodedError("jira_unauthorized", "client unavailable")
	}
	calls := 0
	deps.Cache = func(ctx context.Context) (core.Cache, error) { calls++; return nil, nil }
	var stdout, stderr bytes.Buffer
	program, sink := newProgram(deps, &stdout, &stderr)
	exit := execute(t.Context(), program, sink, []string{"issue", "view", "DEMO-1", "--output=json"})
	if exit == 0 || calls != 0 {
		t.Fatalf("client refusal recorded an unobserved issue: exit=%d cache=%d stdout=%q stderr=%q", exit, calls, stdout.String(), stderr.String())
	}
}

func TestIssueHistoryCompletionRespectsMixedPositionalGrammar(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("JIRA_BASE_URL", "https://example.com")
	t.Setenv("JIRA_EMAIL", "person@example.com")
	store, err := newScopedDiskCache("https://example.com", "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := cachecmd.RecordIssueKeys(store, []string{"DEMO-1", "DEMO-2"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, kind string
		args       []string
		want       bool
	}{
		{name: "attachment first key", kind: "attachmentissuekey", want: true},
		{name: "attachment file slot", kind: "attachmentissuekey", args: []string{"DEMO-1"}},
		{name: "attachment explicit file", kind: "attachmentissuekey", args: []string{"--file=DEMO-99", "DEMO-1"}, want: true},
		{name: "attachment spaced file", kind: "attachmentissuekey", args: []string{"--file", "DEMO-99", "DEMO-1"}, want: true},
		{name: "transition first key", kind: "transitionissuekey", want: true},
		{name: "transition ambiguous status", kind: "transitionissuekey", args: []string{"DEMO-1"}},
		{name: "transition explicit target", kind: "transitionissuekey", args: []string{"--transition=31", "DEMO-1"}, want: true},
		{name: "transition hidden target alias", kind: "transitionissuekey", args: []string{"--status=31", "DEMO-1"}, want: true},
		{name: "transition selector", kind: "transitionissuekey", args: []string{"--transition=31", "--jql=project = DEMO"}},
		{name: "transition file selector", kind: "transitionissuekey", args: []string{"--from-file=absent"}},
		{name: "transition native body has unknown target", kind: "transitionissuekey", args: []string{"--json-input=absent", "DEMO-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fn := completionKinds[tc.kind]
			if fn == nil {
				t.Fatalf("missing contextual predictor %s", tc.kind)
			}
			keys := fn(t.Context(), "fish", tc.args)
			if tc.want && !slices.Equal(keys, []string{"DEMO-1", "DEMO-2"}) || !tc.want && len(keys) != 0 {
				t.Fatalf("contextual candidates=%v, want issue candidates=%t", keys, tc.want)
			}
		})
	}
}

type recentFailStore struct{ core.Cache }

func (recentFailStore) Set(string, []byte) error { return errors.New("history storage unavailable") }

func TestIssueHistoryReusesOrdinaryCacheAndNeverChangesOperationFailures(t *testing.T) {
	for _, failure := range []string{"none", "constructor", "nil store", "set"} {
		t.Run(failure, func(t *testing.T) {
			deps := testDeps(t)
			deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) { return recentIssueClient{}, nil }
			deps.BuildPreferences = func() (jqlcmd.BuildPreferences, error) {
				t.Fatal("live recording resolved a different public account instead of ordinary cache scope")
				return jqlcmd.BuildPreferences{}, nil
			}
			deps.CacheFor = func(string, string) (core.Cache, error) {
				t.Fatal("live recording opened a separately guessed namespace")
				return nil, nil
			}
			deps.Credentials = func() (core.CredentialStore, error) {
				t.Fatal("recorder bypassed the ordinary cache credential owner")
				return nil, nil
			}
			var memory adapters.MemoryCache
			calls := 0
			deps.Cache = func(ctx context.Context) (core.Cache, error) {
				calls++
				switch failure {
				case "constructor":
					return nil, errors.New("scope unavailable")
				case "nil store":
					return nil, nil
				case "set":
					return recentFailStore{Cache: &memory}, nil
				default:
					return &memory, nil
				}
			}
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(deps, &stdout, &stderr)
			exit := execute(t.Context(), program, sink, []string{"issue", "view", "DEMO-1", "--fields=summary", "--output=json"})
			if exit != 0 || calls != 1 || !bytes.Contains(stdout.Bytes(), []byte(`"key":"DEMO-1"`)) {
				t.Fatalf("read altered by local history: exit=%d calls=%d stdout=%q stderr=%q", exit, calls, stdout.String(), stderr.String())
			}
			keys, _, _ := cachecmd.RecentIssueKeys(&memory)
			if failure == "none" && !slices.Equal(keys, []string{"DEMO-1"}) || failure != "none" && len(keys) != 0 {
				t.Fatalf("history=%v, failure=%s", keys, failure)
			}
		})
	}
}

func TestIssueHistoryRefusedMovePlansNeverOpenCache(t *testing.T) {
	deps := testDeps(t)
	cacheCalls, clientCalls := 0, 0
	deps.Cache = func(ctx context.Context) (core.Cache, error) {
		cacheCalls++
		return nil, nil
	}
	deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
		clientCalls++
		return nil, errors.New("unexpected Jira client")
	}
	var stdout, stderr bytes.Buffer
	program, sink := newProgram(deps, &stdout, &stderr)
	program.Root.SetIn(strings.NewReader(`{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["DEMO-1"]}}}`))
	exit := execute(t.Context(), program, sink, []string{"issue", "move", "DEMO-1", "--json-input=-", "--output=json", "--no-input"})
	if exit != 3 || !bytes.Contains(stdout.Bytes(), []byte(`"flag_required"`)) || !bytes.Contains(stdout.Bytes(), []byte(`"outcome":"planned"`)) {
		t.Fatalf("refused move lost validation or retained plans: exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	if cacheCalls != 0 || clientCalls != 0 {
		t.Fatalf("refused move opened local/remote capabilities: cache=%d client=%d", cacheCalls, clientCalls)
	}
}

type recentMoveClient struct {
	core.JiraClient
	read func(context.Context, string) (core.IssueRead, error)
}

func (recentMoveClient) BaseURL() string { return "https://example.com" }

func (c recentMoveClient) GetIssue(ctx context.Context, key string, _ []string, _ ...string) (core.IssueRead, error) {
	return c.read(ctx, key)
}

func TestIssueHistoryMoveRequiresStartedIdentityRead(t *testing.T) {
	for _, tc := range []struct {
		name               string
		factoryCode        string
		readCode           string
		cancelAfterFactory bool
		cancelAfterRead    bool
		wantExit           int
		wantReadCalls      int
		wantCacheCalls     int
	}{
		{name: "client authentication refusal", factoryCode: "jira_unauthorized", wantExit: cli.ExitAuth},
		{name: "client validation refusal", factoryCode: cli.CodeConfigInvalid, wantExit: cli.ExitValidation},
		{name: "canceled before read", cancelAfterFactory: true, wantExit: cli.ExitCanceled},
		{name: "native not found", readCode: "jira_not_found", wantExit: 2, wantReadCalls: 1, wantCacheCalls: 1},
		{name: "native bad request", readCode: "jira_bad_request", wantExit: cli.ExitValidation, wantReadCalls: 1, wantCacheCalls: 1},
		{name: "native precondition failure", readCode: cli.CodeJiraPreconditionFailed, wantExit: cli.ExitValidation, wantReadCalls: 1, wantCacheCalls: 1},
		{name: "retained cancellation", cancelAfterRead: true, wantExit: cli.ExitCanceled, wantReadCalls: 1, wantCacheCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			clientCalls, readCalls, cacheCalls := 0, 0, 0
			client := recentMoveClient{read: func(context.Context, string) (core.IssueRead, error) {
				readCalls++
				if tc.cancelAfterRead {
					cancel()
					return core.IssueRead{}, context.Canceled
				}
				return core.IssueRead{}, cli.NewCodedError(tc.readCode, "native identity read refused")
			}}
			deps := testDeps(t)
			deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
				clientCalls++
				if tc.factoryCode != "" {
					return nil, cli.NewCodedError(tc.factoryCode, "client unavailable")
				}
				if tc.cancelAfterFactory {
					cancel()
				}
				return client, nil
			}
			var memory adapters.MemoryCache
			deps.Cache = func(ctx context.Context) (core.Cache, error) {
				cacheCalls++
				return &memory, nil
			}
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(deps, &stdout, &stderr)
			program.Root.SetIn(strings.NewReader(`{"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["DEMO-1","DEMO-2"]}}}`))
			exit := execute(ctx, program, sink, []string{"issue", "move", "DEMO-1", "DEMO-2", "--json-input=-", "--force", "--no-input", "--output=json"})
			keys, _, _ := cachecmd.RecentIssueKeys(&memory)
			var wantKeys []string
			if tc.wantCacheCalls != 0 {
				wantKeys = []string{"DEMO-1", "DEMO-2"}
			}
			if exit != tc.wantExit || clientCalls != 1 || readCalls != tc.wantReadCalls || cacheCalls != tc.wantCacheCalls || !slices.Equal(keys, wantKeys) {
				t.Fatalf("move history: exit=%d client=%d read=%d cache=%d keys=%v; want exit=%d client=1 read=%d cache=%d keys=%v; stdout=%q stderr=%q", exit, clientCalls, readCalls, cacheCalls, keys, tc.wantExit, tc.wantReadCalls, tc.wantCacheCalls, wantKeys, stdout.String(), stderr.String())
			}
			if !bytes.Contains(stdout.Bytes(), []byte(`"outcome":"planned"`)) || bytes.Contains(stdout.Bytes(), []byte("ReadStarted")) || bytes.Contains(stdout.Bytes(), []byte("read_started")) {
				t.Fatal("move observation changed retained plans or leaked internal state")
			}
		})
	}
}

type recentInputClient struct {
	core.JiraClient
	core.IssueMetadataClient
	core.IssueCreateProjectClient
	read   func(context.Context, string) (core.IssueEditMetadataRead, error)
	search func(context.Context, string, []string) (core.IssueSearchRead, error)
}

type recentCommentClient struct {
	core.JiraClient
	write func(context.Context) (core.CommentWrite, error)
}

func (recentCommentClient) BaseURL() string { return "https://example.com" }

func (c recentCommentClient) AddComment(ctx context.Context, _ string, _ jira.AddCommentRequest) (core.CommentWrite, error) {
	return c.write(ctx)
}

func TestIssueHistoryOrdinaryNativeValidationRetainsDispatchedFailures(t *testing.T) {
	for _, tc := range []struct {
		name          string
		status        int
		factoryCode   string
		factoryStatus int
		localRefused  bool
		readOnly      bool
		wantClients   int
		wantWrites    int
		wantCache     int
	}{
		{name: "native bad request", status: http.StatusBadRequest, wantClients: 1, wantWrites: 2, wantCache: 1},
		{name: "native precondition failure", status: http.StatusPreconditionFailed, wantClients: 1, wantWrites: 2, wantCache: 1},
		{name: "native conflict", status: http.StatusConflict, wantClients: 1, wantWrites: 2, wantCache: 1},
		{name: "native unprocessable entity", status: http.StatusUnprocessableEntity, wantClients: 1, wantWrites: 2, wantCache: 1},
		{name: "client coded refusal", factoryCode: "jira_bad_request", wantClients: 1},
		{name: "client native refusal without rows", factoryStatus: http.StatusBadRequest, wantClients: 1},
		{name: "local body refusal", localRefused: true},
		{name: "read only refusal", readOnly: true, wantClients: 1, wantWrites: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JIRA_READ_ONLY", fmt.Sprint(tc.readOnly))
			clientCalls, writeCalls, cacheCalls := 0, 0, 0
			client := recentCommentClient{write: func(context.Context) (core.CommentWrite, error) {
				writeCalls++
				if tc.readOnly {
					return core.CommentWrite{}, adapterjira.ErrReadOnly
				}
				return core.CommentWrite{}, fmt.Errorf("comment dispatch: %w", &adapterjira.APIError{StatusCode: tc.status})
			}}
			deps := testDeps(t)
			deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
				clientCalls++
				if tc.factoryCode != "" {
					return nil, cli.NewCodedError(tc.factoryCode, "client unavailable")
				}
				if tc.factoryStatus != 0 {
					return nil, &adapterjira.APIError{StatusCode: tc.factoryStatus}
				}
				return client, nil
			}
			var memory adapters.MemoryCache
			deps.Cache = func(ctx context.Context) (core.Cache, error) {
				cacheCalls++
				return &memory, nil
			}
			body := "Authored comment"
			if tc.localRefused {
				body = ""
			}
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(deps, &stdout, &stderr)
			exit := execute(t.Context(), program, sink, []string{"issue", "comment", "add", "DEMO-1", "DEMO-2", "--markdown=" + body, "--force", "--no-input", "--parallelism=1", "--output=json"})
			keys, _, _ := cachecmd.RecentIssueKeys(&memory)
			var wantKeys []string
			if tc.wantCache != 0 {
				wantKeys = []string{"DEMO-1", "DEMO-2"}
			}
			if exit != cli.ExitValidation || clientCalls != tc.wantClients || writeCalls != tc.wantWrites || cacheCalls != tc.wantCache || !slices.Equal(keys, wantKeys) {
				t.Fatalf("ordinary native history: exit=%d client=%d write=%d cache=%d keys=%v; want exit=3 client=%d write=%d cache=%d keys=%v; stdout=%q stderr=%q", exit, clientCalls, writeCalls, cacheCalls, keys, tc.wantClients, tc.wantWrites, tc.wantCache, wantKeys, stdout.String(), stderr.String())
			}
		})
	}
}

func (recentInputClient) BaseURL() string { return "https://example.com" }

func (c recentInputClient) GetEditIssueFields(ctx context.Context, key string) (core.IssueEditMetadataRead, error) {
	return c.read(ctx, key)
}

func (c recentInputClient) SearchIssues(ctx context.Context, query string, fields []string, _ string, _ int, _ ...string) (core.IssueSearchRead, error) {
	return c.search(ctx, query, fields)
}

func TestIssueHistoryRecordsEstablishedInputDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name            string
		selector        bool
		preCanceled     bool
		cancelAfterRead bool
		factoryRefused  bool
		factoryStatus   int
		metadataRefused bool
		metadataCode    string
		invalidMetadata bool
		localRefused    bool
		renderRefused   bool
		storeRefused    bool
		wantExit        int
		wantClientCalls int
		wantReadCalls   int
		wantSearchCalls int
		wantCacheCalls  int
		wantKeys        []string
	}{
		{name: "positional success", wantClientCalls: 1, wantReadCalls: 2, wantCacheCalls: 1, wantKeys: []string{"DEMO-1", "DEMO-2"}},
		{name: "selector success", selector: true, wantClientCalls: 1, wantReadCalls: 2, wantSearchCalls: 1, wantCacheCalls: 1, wantKeys: []string{"DEMO-1", "DEMO-2"}},
		{name: "retained cancellation", cancelAfterRead: true, wantExit: cli.ExitCanceled, wantClientCalls: 1, wantReadCalls: 1, wantCacheCalls: 1, wantKeys: []string{"DEMO-1", "DEMO-2"}},
		{name: "native refusal", metadataRefused: true, wantExit: 2, wantClientCalls: 1, wantReadCalls: 2, wantCacheCalls: 1, wantKeys: []string{"DEMO-1", "DEMO-2"}},
		{name: "native bad request", metadataCode: "jira_bad_request", wantExit: cli.ExitValidation, wantClientCalls: 1, wantReadCalls: 2, wantCacheCalls: 1, wantKeys: []string{"DEMO-1", "DEMO-2"}},
		{name: "native precondition failure", metadataCode: cli.CodeJiraPreconditionFailed, wantExit: cli.ExitValidation, wantClientCalls: 1, wantReadCalls: 2, wantCacheCalls: 1, wantKeys: []string{"DEMO-1", "DEMO-2"}},
		{name: "native protocol failure", invalidMetadata: true, wantExit: cli.ExitServer, wantClientCalls: 1, wantReadCalls: 2, wantCacheCalls: 1, wantKeys: []string{"DEMO-1", "DEMO-2"}},
		{name: "client setup refusal", factoryRefused: true, wantExit: cli.ExitAuth, wantClientCalls: 1},
		{name: "client native refusal before metadata", factoryStatus: http.StatusBadRequest, wantExit: cli.ExitValidation, wantClientCalls: 1},
		{name: "selector client setup refusal", selector: true, factoryRefused: true, wantExit: cli.ExitAuth, wantClientCalls: 1},
		{name: "pre-canceled", preCanceled: true, wantExit: cli.ExitCanceled},
		{name: "local flag refusal", localRefused: true, wantExit: cli.ExitValidation},
		{name: "render failure after observed read", renderRefused: true, wantExit: cli.ExitValidation, wantClientCalls: 1, wantReadCalls: 2, wantCacheCalls: 1, wantKeys: []string{"DEMO-1", "DEMO-2"}},
		{name: "disposable storage failure", storeRefused: true, wantClientCalls: 1, wantReadCalls: 2, wantCacheCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.preCanceled {
				cancel()
			}
			clientCalls, readCalls, searchCalls, cacheCalls := 0, 0, 0, 0
			client := recentInputClient{
				read: func(context.Context, string) (core.IssueEditMetadataRead, error) {
					readCalls++
					if tc.cancelAfterRead {
						cancel()
					}
					if tc.metadataRefused {
						return core.IssueEditMetadataRead{}, cli.NewCodedError("jira_not_found", "issue does not exist")
					}
					if tc.metadataCode != "" {
						return core.IssueEditMetadataRead{}, cli.NewCodedError(tc.metadataCode, "native metadata read refused")
					}
					if tc.invalidMetadata {
						return core.IssueEditMetadataRead{Native: json.RawMessage(`{"fields":null}`)}, nil
					}
					return core.IssueEditMetadataRead{Native: json.RawMessage(`{"fields":{}}`)}, nil
				},
				search: func(_ context.Context, query string, fields []string) (core.IssueSearchRead, error) {
					searchCalls++
					if query != "project = DEMO" || !slices.Equal(fields, []string{"key"}) {
						t.Fatalf("selector changed its authored query or key-only request: query=%q fields=%v", query, fields)
					}
					return core.IssueSearchRead{Results: &jira.SearchResults{IsLast: jira.NullableOf(true), Issues: jira.NullableOf([]jira.IssueBean{{Key: jira.NullableOf("DEMO-1")}, {Key: jira.NullableOf("DEMO-2")}})}}, nil
				},
			}
			deps := testDeps(t)
			deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
				clientCalls++
				if tc.factoryRefused {
					return nil, cli.NewCodedError("jira_unauthorized", "client unavailable")
				}
				if tc.factoryStatus != 0 {
					return nil, &adapterjira.APIError{StatusCode: tc.factoryStatus}
				}
				return client, nil
			}
			var memory adapters.MemoryCache
			deps.Cache = func(ctx context.Context) (core.Cache, error) {
				cacheCalls++
				if tc.storeRefused {
					return recentFailStore{Cache: &memory}, nil
				}
				return &memory, nil
			}
			args := []string{"issue", "edit", "DEMO-1", "DEMO-2"}
			if tc.selector {
				args = []string{"issue", "edit", "--jql=project = DEMO"}
			}
			args = append(args, "--generate-json", "--parallelism=1", "--output=json")
			if tc.localRefused {
				args = append(args, "--summary=")
			}
			if tc.renderRefused {
				args = append(args, `--jq=error("filter refused")`)
			}
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(deps, &stdout, &stderr)
			var runErr error
			if tc.localRefused {
				leaf, _, err := program.Root.Find([]string{"issue", "edit"})
				if err != nil {
					t.Fatal(err)
				}
				run := leaf.RunE
				leaf.RunE = func(c *cobra.Command, args []string) error {
					runErr = run(c, args)
					return runErr
				}
			}
			exit := execute(ctx, program, sink, args)
			keys, _, _ := cachecmd.RecentIssueKeys(&memory)
			if exit != tc.wantExit || clientCalls != tc.wantClientCalls || readCalls != tc.wantReadCalls || searchCalls != tc.wantSearchCalls || cacheCalls != tc.wantCacheCalls || !slices.Equal(keys, tc.wantKeys) {
				t.Fatalf("discovery history: exit=%d client=%d read=%d search=%d cache=%d keys=%v; want exit=%d client=%d read=%d search=%d cache=%d keys=%v; stdout=%q stderr=%q", exit, clientCalls, readCalls, searchCalls, cacheCalls, keys, tc.wantExit, tc.wantClientCalls, tc.wantReadCalls, tc.wantSearchCalls, tc.wantCacheCalls, tc.wantKeys, stdout.String(), stderr.String())
			}
			if bytes.Contains(stdout.Bytes(), []byte("ReadStarted")) || bytes.Contains(stdout.Bytes(), []byte("read_started")) {
				t.Fatal("internal observation state leaked into machine output")
			}
			if tc.localRefused && cli.MapError(runErr).Code != cli.CodeFlagNotApplicable {
				t.Fatal("local refusal did not establish the authored-input refusal")
			}
		})
	}
}
